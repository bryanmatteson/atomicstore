package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"
)

type ambiguousWriteStorage struct {
	*MockStorage
	mu      sync.Mutex
	failPut bool
}

func (a *ambiguousWriteStorage) armAmbiguousPut() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failPut = true
}

func (a *ambiguousWriteStorage) Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error) {
	metadata, err := a.MockStorage.Put(ctx, uri, data, options...)
	if err != nil {
		return metadata, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failPut {
		a.failPut = false
		return Metadata{}, NewStoreError(ErrCodeIO, "Put", uri, "response lost after write", nil, false)
	}
	return metadata, nil
}

func setupLockTest(t *testing.T) (*Locker, *Store[LockLease], *MockStorage) {
	t.Helper()
	mock := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	leaseStore, err := NewStore[LockLease](mock, "locks", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	locker := NewLocker(leaseStore,
		WithLockOwner("tester"),
		WithLockTTL(time.Second),
		WithLockClockSkew(0),
		WithLockRetryDelay(5*time.Millisecond),
		WithLockMaxRetryDelay(20*time.Millisecond),
	)
	return locker, leaseStore, mock
}

func TestLockTryAcquireAndRelease(t *testing.T) {
	locker, _, _ := setupLockTest(t)
	ctx := context.Background()

	h, err := locker.TryAcquire(ctx, "job-1")
	AssertNoError(t, err)
	AssertEqual(t, int64(1), h.FencingToken)
	AssertEqual(t, "tester", h.Owner)

	_, err = locker.TryAcquire(ctx, "job-1", WithLockOwner("other"))
	AssertErrorCode(t, err, ErrCodeLockHeld)

	AssertNoError(t, locker.Release(ctx, h))

	h2, err := locker.TryAcquire(ctx, "job-1", WithLockOwner("other"))
	AssertNoError(t, err)
	AssertEqual(t, int64(2), h2.FencingToken)
	AssertEqual(t, "other", h2.Owner)
}

func TestLockLeaseIDPreventsSameOwnerFromBeingMistakenForHolder(t *testing.T) {
	lockerA, leaseStore, _ := setupLockTest(t)
	lockerB := NewLocker(leaseStore,
		WithLockOwner("tester"),
		WithLockTTL(time.Second),
		WithLockClockSkew(0),
	)
	ctx := context.Background()

	first, err := lockerA.TryAcquire(ctx, "same-owner")
	AssertNoError(t, err)
	_, err = lockerB.TryAcquire(ctx, "same-owner")
	AssertErrorCode(t, err, ErrCodeLockHeld)

	AssertNoError(t, lockerA.Release(ctx, first))
	second, err := lockerB.TryAcquire(ctx, "same-owner")
	AssertNoError(t, err)
	AssertEqual(t, first.FencingToken+1, second.FencingToken)
	AssertTrue(t, first.ETag != second.ETag, "each acquisition must have a distinct ETag")

	err = lockerA.Release(ctx, first)
	AssertErrorCode(t, err, ErrCodeFencingTokenStale)
	AssertNoError(t, lockerB.Release(ctx, second))
}

func TestLockStealExpiredIncrementsFencingToken(t *testing.T) {
	var now atomic.Value
	now.Store(time.Unix(1_700_000_000, 0))

	locker, _, _ := setupLockTest(t)
	locker.now = func() time.Time { return now.Load().(time.Time) }
	locker.ttl = 10 * time.Second
	locker.clockSkew = 0

	ctx := context.Background()
	h1, err := locker.TryAcquire(ctx, "expired", WithLockOwner("a"))
	AssertNoError(t, err)
	AssertEqual(t, int64(1), h1.FencingToken)

	now.Store(now.Load().(time.Time).Add(11 * time.Second))

	h2, err := locker.TryAcquire(ctx, "expired", WithLockOwner("b"))
	AssertNoError(t, err)
	AssertEqual(t, int64(2), h2.FencingToken)
	AssertEqual(t, "b", h2.Owner)

	err = locker.Release(ctx, h1)
	AssertErrorCode(t, err, ErrCodeFencingTokenStale)

	AssertNoError(t, locker.Release(ctx, h2))
}

func TestLockRenewAndRejectStale(t *testing.T) {
	var now atomic.Value
	now.Store(time.Unix(1_700_000_000, 0))

	locker, _, _ := setupLockTest(t)
	locker.now = func() time.Time { return now.Load().(time.Time) }
	locker.ttl = 10 * time.Second

	ctx := context.Background()
	h, err := locker.TryAcquire(ctx, "renew")
	AssertNoError(t, err)

	now.Store(now.Load().(time.Time).Add(5 * time.Second))
	AssertNoError(t, locker.Renew(ctx, h))
	AssertTrue(t, h.ExpiresAt.After(now.Load().(time.Time)), "expiry should extend")

	stale := *h
	stale.ETag = "wrong"
	err = locker.Renew(ctx, &stale)
	AssertErrorCode(t, err, ErrCodeFencingTokenStale)
}

func TestLockRenewExpired(t *testing.T) {
	var now atomic.Value
	now.Store(time.Unix(1_700_000_000, 0))

	locker, _, _ := setupLockTest(t)
	locker.now = func() time.Time { return now.Load().(time.Time) }
	locker.ttl = time.Second
	locker.clockSkew = 0

	ctx := context.Background()
	h, err := locker.TryAcquire(ctx, "expire-renew")
	AssertNoError(t, err)

	now.Store(now.Load().(time.Time).Add(2 * time.Second))
	err = locker.Renew(ctx, h)
	AssertErrorCode(t, err, ErrCodeLockExpired)
}

func TestLockAcquireWaitsThenSucceeds(t *testing.T) {
	locker, _, _ := setupLockTest(t)
	ctx := context.Background()

	h1, err := locker.TryAcquire(ctx, "wait")
	AssertNoError(t, err)

	done := make(chan *LockHandle, 1)
	errCh := make(chan error, 1)
	go func() {
		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		h, err := locker.Acquire(ctx2, "wait", WithLockOwner("waiter"))
		if err != nil {
			errCh <- err
			return
		}
		done <- h
	}()

	time.Sleep(30 * time.Millisecond)
	AssertNoError(t, locker.Release(ctx, h1))

	select {
	case h := <-done:
		AssertEqual(t, "waiter", h.Owner)
		AssertNoError(t, locker.Release(ctx, h))
	case err := <-errCh:
		t.Fatalf("Acquire failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Acquire")
	}
}

func TestLockWithLock(t *testing.T) {
	locker, _, _ := setupLockTest(t)
	ctx := context.Background()

	var sawToken int64
	err := locker.WithLock(ctx, "with", func(ctx context.Context, h *LockHandle) error {
		sawToken = h.FencingToken
		held, _, err := locker.IsHeld(ctx, "with")
		AssertNoError(t, err)
		AssertTrue(t, held, "should be held inside WithLock")
		return nil
	})
	AssertNoError(t, err)
	AssertEqual(t, int64(1), sawToken)

	held, _, err := locker.IsHeld(ctx, "with")
	AssertNoError(t, err)
	AssertTrue(t, !held, "should be released after WithLock")
}

func TestLockConcurrentAcquireSingleWinner(t *testing.T) {
	locker, _, _ := setupLockTest(t)
	ctx := context.Background()

	const n = 20
	var wins int32
	var mu sync.Mutex
	var holders []*LockHandle
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			h, err := locker.TryAcquire(ctx, "race", WithLockOwner(fmt.Sprintf("owner-%d", i)))
			if err == nil {
				atomic.AddInt32(&wins, 1)
				mu.Lock()
				holders = append(holders, h)
				mu.Unlock()
			} else if !IsErrorCode(err, ErrCodeLockHeld) {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	AssertEqual(t, int32(1), wins)
	for _, h := range holders {
		AssertNoError(t, locker.Release(ctx, h))
	}
}

func TestIfNotExistsCreateIfAbsent(t *testing.T) {
	store, mock := SetupTestStore(t)
	ctx := context.Background()

	entity := *NewTestEntity("a", 1)
	entity.ID = "k1"
	_, err := store.Put(ctx, "k1", entity, IfNotExists())
	AssertNoError(t, err)

	entity.Name = "b"
	_, err = store.Put(ctx, "k1", entity, IfNotExists())
	AssertErrorCode(t, err, ErrCodePreconditionFailed)

	_ = mock
}

func TestFileStorageSequentialLocking(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStorage(dir)
	AssertNoError(t, err)

	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)

	leaseStore, err := NewStore[LockLease](fs, "bucket", WithCodec(jsonCodec))
	AssertNoError(t, err)

	locker := NewLocker(leaseStore, WithLockOwner("file-a"), WithLockTTL(time.Minute), WithLockClockSkew(0))
	ctx := context.Background()

	h, err := locker.TryAcquire(ctx, "file-lock")
	AssertNoError(t, err)

	lockerB := NewLocker(leaseStore, WithLockOwner("file-b"), WithLockTTL(time.Minute), WithLockClockSkew(0))
	_, err = lockerB.TryAcquire(ctx, "file-lock")
	AssertErrorCode(t, err, ErrCodeLockHeld)

	AssertNoError(t, locker.Release(ctx, h))
}

func TestFileStorageConditionalPutHasSingleCASWinner(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStorage(dir)
	AssertNoError(t, err)
	ctx := context.Background()
	uri := FormatLocationURI("file", "bucket", "cas")

	meta, err := fs.Put(ctx, uri, []byte("initial"))
	AssertNoError(t, err)

	const contenders = 64
	start := make(chan struct{})
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := 0; i < contenders; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			_, putErr := fs.Put(ctx, uri, []byte(fmt.Sprintf("winner-%d", i)), IfMatch(meta.ETag))
			if putErr == nil {
				winners.Add(1)
			} else if !IsErrorCode(putErr, ErrCodePreconditionFailed) {
				t.Errorf("unexpected put error: %v", putErr)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	AssertEqual(t, int32(1), winners.Load())
}

func TestFileStorageReleasedLeaseHasSingleWinnerAndMonotonicToken(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStorage(dir)
	AssertNoError(t, err)
	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)
	leaseStore, err := NewStore[LockLease](fs, "bucket", WithCodec(jsonCodec))
	AssertNoError(t, err)

	ctx := context.Background()
	firstLocker := NewLocker(leaseStore, WithLockOwner("first"), WithLockTTL(time.Minute), WithLockClockSkew(0))
	first, err := firstLocker.TryAcquire(ctx, "released-race")
	AssertNoError(t, err)
	AssertNoError(t, firstLocker.Release(ctx, first))

	const contenders = 64
	start := make(chan struct{})
	var winners atomic.Int32
	var winningToken atomic.Int64
	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := 0; i < contenders; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			locker := NewLocker(leaseStore, WithLockOwner(fmt.Sprintf("owner-%d", i)), WithLockTTL(time.Minute), WithLockClockSkew(0))
			handle, acquireErr := locker.TryAcquire(ctx, "released-race")
			if acquireErr == nil {
				winners.Add(1)
				winningToken.Store(handle.FencingToken)
			} else if !IsErrorCode(acquireErr, ErrCodeLockHeld) {
				t.Errorf("unexpected acquire error: %v", acquireErr)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	AssertEqual(t, int32(1), winners.Load())
	AssertEqual(t, int64(2), winningToken.Load())
}

func TestFileStorageExpiredLeaseHasSingleWinnerAfterStableObservation(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStorage(dir)
	AssertNoError(t, err)
	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)
	leaseStore, err := NewStore[LockLease](fs, "bucket", WithCodec(jsonCodec))
	AssertNoError(t, err)

	var now atomic.Value
	now.Store(time.Unix(1_700_000_000, 0))
	clock := func() time.Time { return now.Load().(time.Time) }
	ctx := context.Background()
	first := NewLocker(
		leaseStore,
		WithLockOwner("first"),
		WithLockTTL(time.Second),
		WithLockClockSkew(0),
		WithLockClock(clock),
	)
	_, err = first.TryAcquire(ctx, "expired-race")
	AssertNoError(t, err)

	const contenders = 64
	lockers := make([]*Locker, contenders)
	for i := range lockers {
		lockers[i] = NewLocker(
			leaseStore,
			WithLockOwner(fmt.Sprintf("owner-%d", i)),
			WithLockTTL(time.Second),
			WithLockClockSkew(0),
			WithLockClock(clock),
		)
		_, err = lockers[i].TryAcquire(ctx, "expired-race")
		AssertErrorCode(t, err, ErrCodeLockHeld)
	}

	now.Store(now.Load().(time.Time).Add(2 * time.Second))
	start := make(chan struct{})
	var winners atomic.Int32
	var winningToken atomic.Int64
	var wg sync.WaitGroup
	wg.Add(contenders)
	for _, locker := range lockers {
		go func(locker *Locker) {
			defer wg.Done()
			<-start
			handle, acquireErr := locker.TryAcquire(ctx, "expired-race")
			if acquireErr == nil {
				winners.Add(1)
				winningToken.Store(handle.FencingToken)
			} else if !IsErrorCode(acquireErr, ErrCodeLockHeld) {
				t.Errorf("unexpected acquire error: %v", acquireErr)
			}
		}(locker)
	}
	close(start)
	wg.Wait()
	AssertEqual(t, int32(1), winners.Load())
	AssertEqual(t, int64(2), winningToken.Load())
}

func TestWithLockRenewsLongRunningCallback(t *testing.T) {
	locker, leaseStore, _ := setupLockTest(t)
	locker.ttl = 60 * time.Millisecond
	locker.clockSkew = 0
	other := NewLocker(
		leaseStore,
		WithLockOwner("other"),
		WithLockTTL(60*time.Millisecond),
		WithLockClockSkew(0),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- locker.WithLock(ctx, "auto-renew", func(ctx context.Context, handle *LockHandle) error {
			close(started)
			timer := time.NewTimer(180 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		})
	}()

	<-started
	time.Sleep(100 * time.Millisecond)
	_, err := other.TryAcquire(ctx, "auto-renew")
	AssertErrorCode(t, err, ErrCodeLockHeld)
	AssertNoError(t, <-done)
}

func TestLockerRecoversAmbiguousSuccessfulWrites(t *testing.T) {
	storage := &ambiguousWriteStorage{MockStorage: NewMockStorage("mock")}
	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)
	leaseStore, err := NewStore[LockLease](storage, "locks", WithCodec(jsonCodec))
	AssertNoError(t, err)
	locker := NewLocker(
		leaseStore,
		WithLockOwner("ambiguous"),
		WithLockTTL(time.Second),
		WithLockClockSkew(0),
	)
	ctx := context.Background()

	storage.armAmbiguousPut()
	handle, err := locker.TryAcquire(ctx, "write-outcome")
	AssertNoError(t, err)
	AssertEqual(t, int64(1), handle.FencingToken)

	storage.armAmbiguousPut()
	AssertNoError(t, locker.Renew(ctx, handle))

	storage.armAmbiguousPut()
	AssertNoError(t, locker.Release(ctx, handle))
	held, _, err := locker.IsHeld(ctx, "write-outcome")
	AssertNoError(t, err)
	AssertTrue(t, !held, "ambiguous release must be recognized as successful")
}

func TestLockerRejectsBackendWithoutLinearizableConditionContract(t *testing.T) {
	mock := NewMockStorage("mock")
	// Embedding only Storage deliberately hides MockStorage's optional
	// LinearizableConditionalStorage capability.
	unmarked := struct{ Storage }{Storage: mock}
	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)
	leaseStore, err := NewStore[LockLease](unmarked, "locks", WithCodec(jsonCodec))
	AssertNoError(t, err)

	locker := NewLocker(leaseStore)
	_, err = locker.TryAcquire(context.Background(), "unsupported")
	AssertErrorCode(t, err, ErrCodeUnsupported)
	AssertTrue(t, !mock.ObjectExists("mock://locks/locks/unsupported"), "unsupported backend must not be mutated")
}

func TestWithLockCancelsCallbackAndReportsRenewalFailure(t *testing.T) {
	locker, _, storage := setupLockTest(t)
	locker.ttl = 60 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan struct{})
	callbackCanceled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- locker.WithLock(ctx, "renew-failure", func(workCtx context.Context, _ *LockHandle) error {
			close(started)
			<-workCtx.Done()
			close(callbackCanceled)
			return workCtx.Err()
		})
	}()
	<-started
	storage.SetFailNext("mock://locks/locks/renew-failure", ErrCodeIO, false)

	select {
	case <-callbackCanceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("callback context was not canceled after renewal failure")
	}
	err := <-done
	AssertErrorCode(t, err, ErrCodeIO)
}

func TestWithLockReportsReleaseFailure(t *testing.T) {
	locker, _, storage := setupLockTest(t)
	locker.ttl = time.Second

	err := locker.WithLock(context.Background(), "release-failure", func(_ context.Context, _ *LockHandle) error {
		storage.SetFailNext("mock://locks/locks/release-failure", ErrCodeIO, false)
		return nil
	})
	AssertErrorCode(t, err, ErrCodeIO)
}
