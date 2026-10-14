// The README demo exercises real FileStorage operations in a fresh directory.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bryanmatteson/atomicstore/store"
)

type Account struct {
	store.Entity
	Name string `json:"name"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "atomicstore-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	storage, err := store.NewFileStorage(dir)
	if err != nil {
		return err
	}
	accounts, err := store.NewStore[Account](storage, "accounts", store.WithRegisteredCodec("json"))
	if err != nil {
		return err
	}
	account := accounts.New("ada")
	account.Set(Account{Entity: store.Entity{ID: "ada"}, Name: "Ada"})
	if err := account.Save(ctx); err != nil {
		return err
	}
	fmt.Println("CREATE    accounts/ada       Ada")

	left, right := accounts.New("ada"), accounts.New("ada")
	if err := left.Load(ctx); err != nil {
		return err
	}
	if err := right.Load(ctx); err != nil {
		return err
	}
	fmt.Println("READ      two readers       same ETag")
	leftValue := left.Get()
	leftValue.Name = "Ada Lovelace"
	left.Set(leftValue)
	if err := left.Save(ctx); err != nil {
		return err
	}
	fmt.Println("SAVE      reader one        Ada Lovelace")
	rightValue := right.Get()
	rightValue.Name = "stale overwrite"
	right.Set(rightValue)
	if err := right.Save(ctx); !store.IsErrorCode(err, store.ErrCodePreconditionFailed) {
		return fmt.Errorf("expected stale-write rejection, got %v", err)
	}
	fmt.Println("CONFLICT  reader two        precondition_failed")
	if err := right.Reload(ctx); err != nil {
		return err
	}
	if right.Get().Name != "Ada Lovelace" {
		return fmt.Errorf("winner was overwritten")
	}
	fmt.Printf("RELOAD    accounts/ada       %s\n", right.Get().Name)

	tx := store.NewStorageTransaction(ctx, storage)
	for _, key := range []string{"a", "b"} {
		if err := tx.Put(store.FormatLocationURI("file", "batch", key), []byte(key)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); !store.IsErrorCode(err, store.ErrCodeUnsupported) {
		return fmt.Errorf("expected unsupported batch, got %v", err)
	}
	for _, key := range []string{"a", "b"} {
		if _, _, err := storage.Get(ctx, store.FormatLocationURI("file", "batch", key)); !store.IsErrorCode(err, store.ErrCodeNotFound) {
			return fmt.Errorf("unsupported batch wrote %s", key)
		}
	}
	fmt.Println("BATCH     filesystem        unsupported; zero writes")

	leases, err := store.NewStore[store.LockLease](storage, "leases", store.WithRegisteredCodec("json"))
	if err != nil {
		return err
	}
	worker := store.NewLocker(leases, store.WithLockOwner("worker-1"), store.WithLockTTL(30*time.Second))
	first, err := worker.TryAcquire(ctx, "import")
	if err != nil {
		return err
	}
	fmt.Printf("ACQUIRE   worker-1          fencing token %d\n", first.FencingToken)
	other := store.NewLocker(leases, store.WithLockOwner("worker-2"))
	if _, err := other.TryAcquire(ctx, "import"); !store.IsErrorCode(err, store.ErrCodeLockHeld) {
		return fmt.Errorf("expected held lease, got %v", err)
	}
	fmt.Println("CONTEND   worker-2          lock_held")
	if err := worker.Release(ctx, first); err != nil {
		return err
	}
	second, err := other.TryAcquire(ctx, "import")
	if err != nil {
		return err
	}
	if second.FencingToken <= first.FencingToken {
		return fmt.Errorf("fencing token did not advance")
	}
	fmt.Printf("ACQUIRE   worker-2          fencing token %d\n", second.FencingToken)
	if err := other.Release(ctx, second); err != nil {
		return err
	}
	fmt.Println("VERIFIED  conditional writes, batch guard, lease handoff")
	return nil
}
