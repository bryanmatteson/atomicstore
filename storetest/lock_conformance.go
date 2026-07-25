// Package storetest provides reusable conformance tests for Storage backends.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"
	"github.com/bryanmatteson/atomicstore/store"
)

// StorageFactory must return a separately constructed client connected to the
// same backend namespace on every call. Returning one shared in-memory client
// does not establish distributed-lock conformance.
type StorageFactory func(context.Context) (store.Storage, error)

// DistributedLockConfig identifies an isolated namespace in the backend.
// Conformance retains a small number of released lease records because deleting
// them would invalidate the fencing-token contract.
type DistributedLockConfig struct {
	BackendName string
	Bucket      string
	KeyPrefix   string
	Contenders  int
	TTL         time.Duration
	Grace       time.Duration
	Timeout     time.Duration
}

func (c DistributedLockConfig) normalized(t *testing.T) DistributedLockConfig {
	t.Helper()
	if c.BackendName == "" {
		c.BackendName = "storage"
	}
	if c.Bucket == "" {
		t.Fatal("distributed lock conformance requires a bucket")
	}
	if c.KeyPrefix == "" {
		c.KeyPrefix = "conditional-store-conformance/" + randomSuffix()
	}
	if c.KeyPrefix[len(c.KeyPrefix)-1] != '/' {
		c.KeyPrefix += "/"
	}
	if c.Contenders == 0 {
		c.Contenders = 32
	}
	if c.Contenders < 2 {
		t.Fatal("distributed lock conformance requires at least two contenders")
	}
	if c.TTL == 0 {
		c.TTL = 300 * time.Millisecond
	}
	if c.TTL <= 0 {
		t.Fatal("distributed lock conformance requires a positive TTL")
	}
	if c.Grace < 0 {
		t.Fatal("distributed lock conformance requires non-negative grace")
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	return c
}

// RunDistributedLockConformance verifies the backend properties required by
// Locker. A backend is conformant only when every subtest runs and passes
// against the real deployment topology in which it will be used.
func RunDistributedLockConformance(t *testing.T, factory StorageFactory, config DistributedLockConfig) {
	t.Helper()
	if factory == nil {
		t.Fatal("nil storage factory")
	}
	config = config.normalized(t)
	t.Logf("distributed-lock conformance backend=%s bucket=%s retained-prefix=%s", config.BackendName, config.Bucket, config.KeyPrefix)

	t.Run("declares-linearizable-single-key-conditions", func(t *testing.T) {
		clients := newClients(t, factory, 2, config.Timeout)
		for i, client := range clients {
			capability, ok := client.(store.LinearizableConditionalStorage)
			if !ok || !capability.HasLinearizableConditions() {
				t.Fatalf("client %d does not declare LinearizableConditionalStorage", i)
			}
		}
	})

	t.Run("create-if-absent-admits-exactly-one-winner", func(t *testing.T) {
		clients := newClients(t, factory, config.Contenders, config.Timeout)
		ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
		defer cancel()
		uri := store.FormatLocationURI(clients[0].URIScheme(), config.Bucket, config.KeyPrefix+"raw-create")
		results := race(t, config.Contenders, func(i int) error {
			_, err := clients[i].Put(ctx, uri, []byte(fmt.Sprintf("owner-%d", i)), store.IfNotExists())
			return err
		})
		requireOneConditionalWinner(t, results)
		requireIdenticalReadback(t, ctx, clients, uri)
	})

	t.Run("etag-compare-and-swap-admits-exactly-one-winner", func(t *testing.T) {
		clients := newClients(t, factory, config.Contenders, config.Timeout)
		ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
		defer cancel()
		uri := store.FormatLocationURI(clients[0].URIScheme(), config.Bucket, config.KeyPrefix+"raw-cas")
		meta, err := clients[0].Put(ctx, uri, []byte("initial"), store.IfNotExists())
		if err != nil {
			t.Fatalf("initialize CAS object: %v", err)
		}
		results := race(t, config.Contenders, func(i int) error {
			_, err := clients[i].Put(ctx, uri, []byte(fmt.Sprintf("winner-%d", i)), store.IfMatch(meta.ETag))
			return err
		})
		requireOneConditionalWinner(t, results)
		requireIdenticalReadback(t, ctx, clients, uri)
	})

	t.Run("initial-lease-contention-admits-one-holder", func(t *testing.T) {
		lockers := newLockers(t, factory, config, "initial")
		ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
		defer cancel()
		handles, results := raceAcquire(t, ctx, lockers, "initial-contention")
		winner := requireOneLockWinner(t, handles, results, 1)
		if err := lockers[winner].Release(ctx, handles[winner]); err != nil {
			t.Fatalf("release initial winner: %v", err)
		}
	})

	t.Run("released-lease-contention-has-one-winner-and-next-token", func(t *testing.T) {
		lockers := newLockers(t, factory, config, "released")
		ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
		defer cancel()
		first, err := lockers[0].TryAcquire(ctx, "released-contention")
		if err != nil {
			t.Fatalf("initial acquire: %v", err)
		}
		if err := lockers[0].Release(ctx, first); err != nil {
			t.Fatalf("initial release: %v", err)
		}
		handles, results := raceAcquire(t, ctx, lockers, "released-contention")
		winner := requireOneLockWinner(t, handles, results, first.FencingToken+1)
		if err := lockers[winner].Release(ctx, handles[winner]); err != nil {
			t.Fatalf("release contention winner: %v", err)
		}
	})

	t.Run("fencing-tokens-remain-monotonic-across-release", func(t *testing.T) {
		lockers := newLockers(t, factory, config, "sequence")
		ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
		defer cancel()
		var previous int64
		for i := 0; i < 8; i++ {
			locker := lockers[i%len(lockers)]
			handle, err := locker.TryAcquire(ctx, "token-sequence")
			if err != nil {
				t.Fatalf("acquire %d: %v", i, err)
			}
			if handle.FencingToken != previous+1 {
				t.Fatalf("token %d: got %d, want %d", i, handle.FencingToken, previous+1)
			}
			previous = handle.FencingToken
			if err := locker.Release(ctx, handle); err != nil {
				t.Fatalf("release %d: %v", i, err)
			}
		}
	})

	t.Run("unchanged-expired-lease-has-one-takeover-and-fences-old-holder", func(t *testing.T) {
		runExpiredTakeover(t, factory, config)
	})

	t.Run("automatic-renewal-prevents-takeover", func(t *testing.T) {
		runAutomaticRenewal(t, factory, config)
	})
}

func runExpiredTakeover(t *testing.T, factory StorageFactory, config DistributedLockConfig) {
	t.Helper()
	var clockNanos atomic.Int64
	startTime := time.Unix(1_700_000_000, 0)
	clockNanos.Store(startTime.UnixNano())
	now := func() time.Time { return time.Unix(0, clockNanos.Load()) }

	lockers := newLockersWithClock(t, factory, config, "expired", now)
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	old, err := lockers[0].TryAcquire(ctx, "expired-contention")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}

	// A remote absolute ExpiresAt is not sufficient. Every contender must first
	// observe the unchanged ETag and refuse takeover.
	for i := 1; i < len(lockers); i++ {
		if _, err := lockers[i].TryAcquire(ctx, "expired-contention"); !store.IsErrorCode(err, store.ErrCodeLockHeld) {
			t.Fatalf("contender %d initial observation: got %v, want LockHeld", i, err)
		}
	}
	clockNanos.Store(startTime.Add(config.TTL + config.Grace - time.Nanosecond).UnixNano())
	for i := 1; i < len(lockers); i++ {
		if _, err := lockers[i].TryAcquire(ctx, "expired-contention"); !store.IsErrorCode(err, store.ErrCodeLockHeld) {
			t.Fatalf("contender %d acquired before TTL plus grace: %v", i, err)
		}
	}

	clockNanos.Store(startTime.Add(config.TTL + config.Grace).UnixNano())
	handles, results := raceAcquire(t, ctx, lockers[1:], "expired-contention")
	winner := requireOneLockWinner(t, handles, results, old.FencingToken+1)
	if err := lockers[0].Renew(ctx, old); !isRejectedStaleHolder(err) {
		t.Fatalf("old holder renew was not rejected as stale: %v", err)
	}
	if err := lockers[0].Release(ctx, old); !isRejectedStaleHolder(err) {
		t.Fatalf("old holder release was not rejected as stale: %v", err)
	}
	if err := lockers[winner+1].Release(ctx, handles[winner]); err != nil {
		t.Fatalf("release takeover winner: %v", err)
	}
}

func isRejectedStaleHolder(err error) bool {
	return store.IsErrorCode(err, store.ErrCodeFencingTokenStale) ||
		store.IsErrorCode(err, store.ErrCodeLockNotHeld) ||
		store.IsErrorCode(err, store.ErrCodeLockExpired)
}

func runAutomaticRenewal(t *testing.T, factory StorageFactory, config DistributedLockConfig) {
	t.Helper()
	clients := newClients(t, factory, 2, config.Timeout)
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("json codec: %v", err)
	}
	stores := make([]*store.Store[store.LockLease], 2)
	for i := range clients {
		stores[i], err = store.NewStore[store.LockLease](clients[i], config.Bucket, store.WithCodec(jsonCodec))
		if err != nil {
			t.Fatalf("lease store %d: %v", i, err)
		}
	}
	keyPrefix := config.KeyPrefix + "renew/"
	holder := store.NewLocker(stores[0],
		store.WithLockOwner("renew-holder"),
		store.WithLockKeyPrefix(keyPrefix),
		store.WithLockTTL(config.TTL),
		store.WithLockClockSkew(config.Grace),
	)
	contender := store.NewLocker(stores[1],
		store.WithLockOwner("renew-contender"),
		store.WithLockKeyPrefix(keyPrefix),
		store.WithLockTTL(config.TTL),
		store.WithLockClockSkew(config.Grace),
	)

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.WithLock(ctx, "auto-renew", func(workCtx context.Context, _ *store.LockHandle) error {
			close(started)
			timer := time.NewTimer(3 * config.TTL)
			defer timer.Stop()
			select {
			case <-workCtx.Done():
				return workCtx.Err()
			case <-timer.C:
				return nil
			}
		})
	}()
	<-started

	deadline := time.Now().Add(2 * config.TTL)
	for time.Now().Before(deadline) {
		if _, err := contender.TryAcquire(ctx, "auto-renew"); !store.IsErrorCode(err, store.ErrCodeLockHeld) {
			t.Fatalf("contender acquired while holder was renewing: %v", err)
		}
		time.Sleep(config.TTL / 5)
	}
	if err := <-done; err != nil {
		t.Fatalf("WithLock: %v", err)
	}
}

func newClients(t *testing.T, factory StorageFactory, count int, timeout time.Duration) []store.Storage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	clients := make([]store.Storage, count)
	for i := range clients {
		var err error
		clients[i], err = factory(ctx)
		if err != nil {
			t.Fatalf("construct independent storage client %d: %v", i, err)
		}
		if clients[i] == nil {
			t.Fatalf("construct independent storage client %d: nil client", i)
		}
	}
	return clients
}

func newLockers(t *testing.T, factory StorageFactory, config DistributedLockConfig, group string) []*store.Locker {
	t.Helper()
	return newLockersWithClock(t, factory, config, group, time.Now)
}

func newLockersWithClock(
	t *testing.T,
	factory StorageFactory,
	config DistributedLockConfig,
	group string,
	now func() time.Time,
) []*store.Locker {
	t.Helper()
	clients := newClients(t, factory, config.Contenders, config.Timeout)
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("json codec: %v", err)
	}
	lockers := make([]*store.Locker, len(clients))
	for i, client := range clients {
		leaseStore, createErr := store.NewStore[store.LockLease](client, config.Bucket, store.WithCodec(jsonCodec))
		if createErr != nil {
			t.Fatalf("lease store %d: %v", i, createErr)
		}
		lockers[i] = store.NewLocker(leaseStore,
			store.WithLockOwner(fmt.Sprintf("%s-owner-%d", group, i)),
			store.WithLockKeyPrefix(config.KeyPrefix+group+"/"),
			store.WithLockTTL(config.TTL),
			store.WithLockClockSkew(config.Grace),
			store.WithLockClock(now),
			store.WithLockRetryDelay(config.TTL/20),
			store.WithLockMaxRetryDelay(config.TTL/5),
		)
	}
	return lockers
}

func race(t *testing.T, count int, fn func(int) error) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, count)
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return results
}

func raceAcquire(
	t *testing.T,
	ctx context.Context,
	lockers []*store.Locker,
	name string,
) ([]*store.LockHandle, []error) {
	t.Helper()
	handles := make([]*store.LockHandle, len(lockers))
	results := race(t, len(lockers), func(i int) error {
		var err error
		handles[i], err = lockers[i].TryAcquire(ctx, name)
		return err
	})
	return handles, results
}

func requireOneConditionalWinner(t *testing.T, results []error) int {
	t.Helper()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner >= 0 {
				t.Fatalf("multiple conditional winners: %d and %d", winner, i)
			}
			winner = i
			continue
		}
		if !store.IsErrorCode(err, store.ErrCodePreconditionFailed) &&
			!store.IsErrorCode(err, store.ErrCodeAlreadyExists) {
			t.Fatalf("contender %d returned non-conditional error: %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("conditional race had no winner")
	}
	return winner
}

func requireOneLockWinner(
	t *testing.T,
	handles []*store.LockHandle,
	results []error,
	expectedToken int64,
) int {
	t.Helper()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner >= 0 {
				t.Fatalf("multiple lock winners: %d and %d", winner, i)
			}
			if handles[i] == nil {
				t.Fatalf("winner %d returned nil handle", i)
			}
			if handles[i].FencingToken != expectedToken {
				t.Fatalf("winner token: got %d, want %d", handles[i].FencingToken, expectedToken)
			}
			winner = i
			continue
		}
		if !store.IsErrorCode(err, store.ErrCodeLockHeld) {
			t.Fatalf("contender %d returned non-contention error: %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("lock race had no winner")
	}
	return winner
}

func requireIdenticalReadback(t *testing.T, ctx context.Context, clients []store.Storage, uri string) {
	t.Helper()
	var expectedData []byte
	var expectedETag string
	for i, client := range clients {
		data, metadata, err := client.Get(ctx, uri)
		if err != nil {
			t.Fatalf("client %d immediate readback: %v", i, err)
		}
		if i == 0 {
			expectedData = data
			expectedETag = metadata.ETag
			if expectedETag == "" {
				t.Fatal("immediate readback returned empty ETag")
			}
			continue
		}
		if string(data) != string(expectedData) || metadata.ETag != expectedETag {
			t.Fatalf(
				"client %d observed inconsistent state: data=%q etag=%q, want data=%q etag=%q",
				i,
				data,
				metadata.ETag,
				expectedData,
				expectedETag,
			)
		}
	}
}

func randomSuffix() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
