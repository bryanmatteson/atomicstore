package store_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"
	"github.com/bryanmatteson/atomicstore/store"
	"github.com/bryanmatteson/atomicstore/storetest"
)

func TestFileStorageDistributedLockConformance(t *testing.T) {
	root := t.TempDir()
	storetest.RunDistributedLockConformance(t, func(context.Context) (store.Storage, error) {
		// A fresh FileStorage instance ensures the test is exercising the
		// filesystem protocol, not shared client-local state.
		return store.NewFileStorage(root)
	}, storetest.DistributedLockConfig{
		BackendName: "FileStorage/local-filesystem",
		Bucket:      "lock-conformance",
		KeyPrefix:   "independent-clients/",
		Contenders:  32,
		TTL:         250 * time.Millisecond,
		Grace:       25 * time.Millisecond,
		Timeout:     20 * time.Second,
	})
}

func TestFileStorageDistributedLockSubprocessConformance(t *testing.T) {
	if os.Getenv("CONDITIONAL_STORE_LOCK_HELPER") == "1" {
		runFileLockProcessHelper(t)
		return
	}

	root := t.TempDir()
	t.Run("initial-create-has-one-process-winner", func(t *testing.T) {
		runFileProcessRace(t, root, "process-create", false, 1)
	})
	t.Run("released-lease-cas-has-one-process-winner", func(t *testing.T) {
		runFileProcessRace(t, root, "process-released", true, 2)
	})
}

func runFileProcessRace(t *testing.T, root, name string, released bool, expectedToken int64) {
	t.Helper()
	const contenders = 8
	const bucket = "process-lock-conformance"
	const prefix = "locks/"

	if released {
		storage, err := store.NewFileStorage(root)
		if err != nil {
			t.Fatalf("file storage: %v", err)
		}
		jsonCodec, err := codec.Get("json")
		if err != nil {
			t.Fatalf("json codec: %v", err)
		}
		leaseStore, err := store.NewStore[store.LockLease](storage, bucket, store.WithCodec(jsonCodec))
		if err != nil {
			t.Fatalf("lease store: %v", err)
		}
		locker := store.NewLocker(leaseStore,
			store.WithLockOwner("parent"),
			store.WithLockKeyPrefix(prefix),
			store.WithLockTTL(time.Minute),
			store.WithLockClockSkew(0),
		)
		handle, err := locker.TryAcquire(context.Background(), name)
		if err != nil {
			t.Fatalf("seed acquire: %v", err)
		}
		if err := locker.Release(context.Background(), handle); err != nil {
			t.Fatalf("seed release: %v", err)
		}
	}

	coordination := filepath.Join(root, "process-coordination", name)
	if err := os.MkdirAll(coordination, 0700); err != nil {
		t.Fatalf("coordination directory: %v", err)
	}
	barrier := filepath.Join(coordination, "start")

	type process struct {
		cmd    *exec.Cmd
		output bytes.Buffer
		ready  string
	}
	processes := make([]process, contenders)
	for i := range processes {
		ready := filepath.Join(coordination, fmt.Sprintf("ready-%d", i))
		cmd := exec.Command(os.Args[0], "-test.run=^TestFileStorageDistributedLockSubprocessConformance$")
		cmd.Env = append(os.Environ(),
			"CONDITIONAL_STORE_LOCK_HELPER=1",
			"CONDITIONAL_STORE_LOCK_ROOT="+root,
			"CONDITIONAL_STORE_LOCK_BUCKET="+bucket,
			"CONDITIONAL_STORE_LOCK_PREFIX="+prefix,
			"CONDITIONAL_STORE_LOCK_NAME="+name,
			fmt.Sprintf("CONDITIONAL_STORE_LOCK_OWNER=process-%d", i),
			"CONDITIONAL_STORE_LOCK_READY="+ready,
			"CONDITIONAL_STORE_LOCK_BARRIER="+barrier,
		)
		cmd.Stdout = &processes[i].output
		cmd.Stderr = &processes[i].output
		processes[i].cmd = cmd
		processes[i].ready = ready
		if err := cmd.Start(); err != nil {
			t.Fatalf("start contender %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for i := range processes {
		for {
			if _, err := os.Stat(processes[i].ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("contender %d did not reach barrier", i)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(barrier, []byte("start"), 0600); err != nil {
		t.Fatalf("release process barrier: %v", err)
	}

	winners := 0
	for i := range processes {
		if err := processes[i].cmd.Wait(); err != nil {
			t.Fatalf("contender %d failed: %v\n%s", i, err, processes[i].output.String())
		}
		output := processes[i].output.String()
		switch {
		case strings.Contains(output, fmt.Sprintf("LOCK_CONFORMANCE_WIN token=%d", expectedToken)):
			winners++
		case strings.Contains(output, "LOCK_CONFORMANCE_HELD"):
		default:
			t.Fatalf("contender %d produced no conformance result:\n%s", i, output)
		}
	}
	if winners != 1 {
		t.Fatalf("cross-process race had %d winners, want exactly 1", winners)
	}
}

func runFileLockProcessHelper(t *testing.T) {
	t.Helper()
	required := func(name string) string {
		t.Helper()
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("missing %s", name)
		}
		return value
	}
	root := required("CONDITIONAL_STORE_LOCK_ROOT")
	bucket := required("CONDITIONAL_STORE_LOCK_BUCKET")
	prefix := required("CONDITIONAL_STORE_LOCK_PREFIX")
	name := required("CONDITIONAL_STORE_LOCK_NAME")
	owner := required("CONDITIONAL_STORE_LOCK_OWNER")
	ready := required("CONDITIONAL_STORE_LOCK_READY")
	barrier := required("CONDITIONAL_STORE_LOCK_BARRIER")

	storage, err := store.NewFileStorage(root)
	if err != nil {
		t.Fatalf("file storage: %v", err)
	}
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("json codec: %v", err)
	}
	leaseStore, err := store.NewStore[store.LockLease](storage, bucket, store.WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("lease store: %v", err)
	}
	locker := store.NewLocker(leaseStore,
		store.WithLockOwner(owner),
		store.WithLockKeyPrefix(prefix),
		store.WithLockTTL(time.Minute),
		store.WithLockClockSkew(0),
	)

	if err := os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatalf("signal ready: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for process barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}

	handle, err := locker.TryAcquire(context.Background(), name)
	switch {
	case err == nil:
		fmt.Printf("LOCK_CONFORMANCE_WIN token=%d\n", handle.FencingToken)
	case store.IsErrorCode(err, store.ErrCodeLockHeld):
		fmt.Println("LOCK_CONFORMANCE_HELD")
	default:
		t.Fatalf("unexpected acquire result: %v", err)
	}
}
