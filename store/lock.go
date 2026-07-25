package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"time"
)

const (
	defaultLockTTL             = 30 * time.Second
	defaultLockSkew            = 1 * time.Second
	defaultAcquireBackoff      = 50 * time.Millisecond
	defaultAcquireMaxBackoff   = 1 * time.Second
	maxAutomaticReleaseTimeout = 5 * time.Second
)

// LockLease is the persisted lease document for a distributed lock. Released
// leases remain in storage so Version stays monotonic across every acquisition.
type LockLease struct {
	Entity
	Owner         string        `json:"owner"`
	LeaseID       string        `json:"lease_id"`
	Held          bool          `json:"held"`
	LeaseDuration time.Duration `json:"lease_duration"`
	Revision      int64         `json:"revision"`
	ExpiresAt     time.Time     `json:"expires_at"`
	AcquiredAt    time.Time     `json:"acquired_at"`
}

// LockHandle is a held lock lease. FencingToken must be checked by protected
// resources before accepting side effects from the holder.
type LockHandle struct {
	Name         string
	Key          string
	Owner        string
	FencingToken int64
	ETag         string
	ExpiresAt    time.Time
	AcquiredAt   time.Time
}

// Locker provides distributed locking on top of a typed Store[LockLease].
type Locker struct {
	store      *Store[LockLease]
	owner      string
	ttl        time.Duration
	clockSkew  time.Duration
	keyPrefix  string
	now        func() time.Time
	retryDelay time.Duration
	maxDelay   time.Duration

	observeMu sync.Mutex
	observed  map[string]lockObservation
}

type lockObservation struct {
	etag      string
	firstSeen time.Time
}

// NewLocker creates a locker backed by the given lease store.
func NewLocker(store *Store[LockLease], opts ...LockOption) *Locker {
	cfg := &lockConfig{}
	applyLockOptions(cfg, opts)

	owner := cfg.Owner.Or("")
	if owner == "" {
		owner = defaultLockOwner()
	}

	return &Locker{
		store:      store,
		owner:      owner,
		ttl:        cfg.TTL.Or(defaultLockTTL),
		clockSkew:  cfg.ClockSkew.Or(defaultLockSkew),
		keyPrefix:  cfg.KeyPrefix.Or("locks/"),
		now:        cfg.Now.Or(time.Now),
		retryDelay: cfg.RetryDelay.Or(defaultAcquireBackoff),
		maxDelay:   cfg.MaxRetryDelay.Or(defaultAcquireMaxBackoff),
		observed:   make(map[string]lockObservation),
	}
}

func defaultLockOwner() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), randomLockID())
}

func randomLockID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (l *Locker) lockKey(name string) string {
	return l.keyPrefix + name
}

// TryAcquire attempts to acquire the named lock once.
//
// A contender does not trust another host's absolute wall-clock expiry. It may
// steal only after observing an unchanged lease for that lease's full duration
// plus the configured expiry grace. A newly started contender therefore waits
// one complete lease duration before recovering a crashed holder.
func (l *Locker) TryAcquire(ctx context.Context, name string, opts ...LockOption) (*LockHandle, error) {
	cfg := l.override(opts)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if l.store == nil {
		return nil, NewStoreError(ErrCodeInvalidOperation, "TryAcquire", name, "nil lease store", nil, false)
	}
	capable, ok := l.store.storage.(LinearizableConditionalStorage)
	if !ok || !capable.HasLinearizableConditions() {
		return nil, NewStoreError(
			ErrCodeUnsupported,
			"TryAcquire",
			name,
			"storage backend does not guarantee linearizable conditional operations",
			nil,
			false,
		)
	}

	key := l.lockKey(name)
	if err := validateLockKey(name, key, "TryAcquire"); err != nil {
		return nil, err
	}
	now := cfg.now()
	lease := LockLease{
		Entity:        Entity{ID: name, Version: 1},
		Owner:         cfg.owner,
		LeaseID:       randomLockID(),
		Held:          true,
		LeaseDuration: cfg.ttl,
		Revision:      1,
		ExpiresAt:     now.Add(cfg.ttl),
		AcquiredAt:    now,
	}

	meta, err := l.store.Create(ctx, key, lease)
	if err == nil {
		l.recordObservation(key, meta.ETag, now)
		return handleFromLease(name, key, lease, meta.ETag), nil
	}
	if recovered, recoveredMeta, recoveredErr := l.recoverLeaseWrite(ctx, key, lease); recoveredErr == nil && recovered {
		l.recordObservation(key, recoveredMeta.ETag, now)
		return handleFromLease(name, key, lease, recoveredMeta.ETag), nil
	}
	if !IsErrorCode(err, ErrCodePreconditionFailed) && !IsErrorCode(err, ErrCodeAlreadyExists) {
		return nil, err
	}

	existing, meta, getErr := l.store.Get(ctx, key)
	if getErr != nil {
		if IsErrorCode(getErr, ErrCodeNotFound) {
			// This is possible only when an external actor deletes lease records.
			return nil, NewStoreError(ErrCodeLockHeld, "TryAcquire", name, "lease changed concurrently", getErr, false)
		}
		return nil, getErr
	}
	if existing.LeaseID == lease.LeaseID {
		l.recordObservation(key, meta.ETag, now)
		return handleFromLease(name, key, existing, meta.ETag), nil
	}

	if existing.Held && !l.canTakeOver(key, meta.ETag, existing.LeaseDuration, cfg.ttl, now, cfg.clockSkew) {
		return nil, NewStoreError(ErrCodeLockHeld, "TryAcquire", name, "lock is held", nil, false)
	}

	claimed := LockLease{
		Entity:        Entity{ID: name, Version: existing.Version + 1},
		Owner:         cfg.owner,
		LeaseID:       randomLockID(),
		Held:          true,
		LeaseDuration: cfg.ttl,
		Revision:      existing.Revision + 1,
		ExpiresAt:     now.Add(cfg.ttl),
		AcquiredAt:    now,
	}
	res, err := l.store.Put(ctx, key, claimed, IfMatch(meta.ETag))
	if err != nil {
		if recovered, recoveredMeta, recoveredErr := l.recoverLeaseWrite(ctx, key, claimed); recoveredErr == nil && recovered {
			l.recordObservation(key, recoveredMeta.ETag, now)
			return handleFromLease(name, key, claimed, recoveredMeta.ETag), nil
		}
		if IsErrorCode(err, ErrCodePreconditionFailed) || IsErrorCode(err, ErrCodeNotFound) {
			return nil, NewStoreError(ErrCodeLockHeld, "TryAcquire", name, "lock is held", err, false)
		}
		return nil, err
	}
	l.recordObservation(key, res.Metadata.ETag, now)
	return handleFromLease(name, key, claimed, res.Metadata.ETag), nil
}

// Acquire retries TryAcquire until success or context cancellation.
func (l *Locker) Acquire(ctx context.Context, name string, opts ...LockOption) (*LockHandle, error) {
	cfg := l.override(opts)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	delay := cfg.retryDelay

	for {
		handle, err := l.TryAcquire(ctx, name, opts...)
		if err == nil {
			return handle, nil
		}
		if !IsErrorCode(err, ErrCodeLockHeld) {
			return nil, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		next := delay * 2
		if next > cfg.maxDelay {
			next = cfg.maxDelay
		}
		delay = next
	}
}

// Renew extends the lock TTL if the handle still owns the current lease.
func (l *Locker) Renew(ctx context.Context, handle *LockHandle, opts ...LockOption) error {
	if handle == nil {
		return NewStoreError(ErrCodeInvalidOperation, "Renew", "", "nil lock handle", nil, false)
	}
	if err := validateLockKey(handle.Name, handle.Key, "Renew"); err != nil {
		return err
	}
	cfg := l.override(opts)
	if err := cfg.validate(); err != nil {
		return err
	}
	now := cfg.now()

	existing, meta, err := l.store.Get(ctx, handle.Key)
	if err != nil {
		if IsErrorCode(err, ErrCodeNotFound) {
			return NewStoreError(ErrCodeLockNotHeld, "Renew", handle.Name, "lock not found", err, false)
		}
		return err
	}
	if err := l.validateHandle(handle, existing, meta.ETag, now, cfg, "Renew"); err != nil {
		return err
	}

	updated := existing
	updated.Held = true
	updated.LeaseDuration = cfg.ttl
	updated.Revision++
	updated.ExpiresAt = now.Add(cfg.ttl)
	res, err := l.store.Put(ctx, handle.Key, updated, IfMatch(handle.ETag))
	if err != nil {
		if recovered, recoveredMeta, recoveredErr := l.recoverLeaseWrite(ctx, handle.Key, updated); recoveredErr == nil && recovered {
			handle.ETag = recoveredMeta.ETag
			handle.ExpiresAt = updated.ExpiresAt
			l.recordObservation(handle.Key, handle.ETag, now)
			return nil
		}
		if IsErrorCode(err, ErrCodePreconditionFailed) || IsErrorCode(err, ErrCodeNotFound) {
			return NewStoreError(ErrCodeFencingTokenStale, "Renew", handle.Name, "lease changed concurrently", err, false)
		}
		return err
	}

	handle.ETag = res.Metadata.ETag
	handle.ExpiresAt = updated.ExpiresAt
	l.recordObservation(handle.Key, handle.ETag, now)
	return nil
}

// Release marks the lease free while retaining its fencing sequence.
func (l *Locker) Release(ctx context.Context, handle *LockHandle) error {
	if handle == nil {
		return NewStoreError(ErrCodeInvalidOperation, "Release", "", "nil lock handle", nil, false)
	}
	if err := validateLockKey(handle.Name, handle.Key, "Release"); err != nil {
		return err
	}
	now := l.now()
	existing, meta, err := l.store.Get(ctx, handle.Key)
	if err != nil {
		if IsErrorCode(err, ErrCodeNotFound) {
			return NewStoreError(ErrCodeLockNotHeld, "Release", handle.Name, "lock not found", err, false)
		}
		return err
	}
	if existing.Owner != handle.Owner || existing.Version != handle.FencingToken || !existing.Held {
		return NewStoreError(ErrCodeFencingTokenStale, "Release", handle.Name, "no longer the lock holder", nil, false)
	}
	if meta.ETag != handle.ETag {
		return NewStoreError(ErrCodeFencingTokenStale, "Release", handle.Name, "lease etag mismatch", nil, false)
	}

	released := existing
	released.Held = false
	released.Revision++
	released.ExpiresAt = now
	res, err := l.store.Put(ctx, handle.Key, released, IfMatch(handle.ETag))
	if err != nil {
		if recovered, recoveredMeta, recoveredErr := l.recoverLeaseWrite(ctx, handle.Key, released); recoveredErr == nil && recovered {
			l.recordObservation(handle.Key, recoveredMeta.ETag, now)
			return nil
		}
		if IsErrorCode(err, ErrCodePreconditionFailed) || IsErrorCode(err, ErrCodeNotFound) {
			return NewStoreError(ErrCodeFencingTokenStale, "Release", handle.Name, "lease changed concurrently", err, false)
		}
		return err
	}
	l.recordObservation(handle.Key, res.Metadata.ETag, now)
	return nil
}

// IsHeld reports whether the named lock currently has a live holder. As with
// TryAcquire, expiry is established by observing an unchanged lease.
func (l *Locker) IsHeld(ctx context.Context, name string) (bool, *LockHandle, error) {
	key := l.lockKey(name)
	if err := validateLockKey(name, key, "IsHeld"); err != nil {
		return false, nil, err
	}
	lease, meta, err := l.store.Get(ctx, key)
	if err != nil {
		if IsErrorCode(err, ErrCodeNotFound) {
			return false, nil, nil
		}
		return false, nil, err
	}
	handle := handleFromLease(name, key, lease, meta.ETag)
	if !lease.Held || l.canTakeOver(key, meta.ETag, lease.LeaseDuration, l.ttl, l.now(), l.clockSkew) {
		return false, handle, nil
	}
	return true, handle, nil
}

// WithLock acquires and automatically renews the lock. If renewal fails, the
// callback context is canceled and the renewal error is returned.
func (l *Locker) WithLock(ctx context.Context, name string, fn func(context.Context, *LockHandle) error, opts ...LockOption) error {
	if fn == nil {
		return NewStoreError(ErrCodeInvalidOperation, "WithLock", name, "nil lock callback", nil, false)
	}
	cfg := l.override(opts)
	if err := cfg.validate(); err != nil {
		return err
	}
	handle, err := l.Acquire(ctx, name, opts...)
	if err != nil {
		return err
	}

	workCtx, cancel := context.WithCancel(ctx)
	stopRenew := make(chan struct{})
	renewDone := make(chan error, 1)
	go func() {
		renewInterval := cfg.ttl / 3
		if renewInterval <= 0 {
			renewInterval = cfg.ttl
		}
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenew:
				renewDone <- nil
				return
			case <-workCtx.Done():
				renewDone <- workCtx.Err()
				return
			case <-ticker.C:
				if renewErr := l.Renew(workCtx, handle, opts...); renewErr != nil {
					cancel()
					renewDone <- renewErr
					return
				}
			}
		}
	}()

	fnErr := fn(workCtx, handle)
	close(stopRenew)
	renewErr := <-renewDone
	cancel()

	releaseTimeout := cfg.ttl
	if releaseTimeout > maxAutomaticReleaseTimeout {
		releaseTimeout = maxAutomaticReleaseTimeout
	}
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), releaseTimeout)
	releaseErr := l.Release(releaseCtx, handle)
	releaseCancel()

	if errors.Is(renewErr, context.Canceled) && fnErr != nil {
		renewErr = nil
	}
	return errors.Join(fnErr, renewErr, releaseErr)
}

func (l *Locker) validateHandle(
	handle *LockHandle,
	existing LockLease,
	etag string,
	now time.Time,
	cfg runtimeLockConfig,
	op string,
) error {
	if existing.Owner != handle.Owner || !existing.Held {
		return NewStoreError(ErrCodeLockNotHeld, op, handle.Name, "owner no longer holds lease", nil, false)
	}
	if existing.Version != handle.FencingToken {
		return NewStoreError(ErrCodeFencingTokenStale, op, handle.Name, "fencing token mismatch", nil, false)
	}
	if etag != handle.ETag {
		return NewStoreError(ErrCodeFencingTokenStale, op, handle.Name, "lease etag mismatch", nil, false)
	}
	if l.canTakeOver(handle.Key, etag, existing.LeaseDuration, cfg.ttl, now, cfg.clockSkew) {
		return NewStoreError(ErrCodeLockExpired, op, handle.Name, "lock expired", nil, false)
	}
	return nil
}

func (l *Locker) canTakeOver(
	key string,
	etag string,
	persistedTTL time.Duration,
	fallbackTTL time.Duration,
	now time.Time,
	grace time.Duration,
) bool {
	ttl := persistedTTL
	if ttl <= 0 {
		ttl = fallbackTTL
	}
	l.observeMu.Lock()
	defer l.observeMu.Unlock()
	observation, ok := l.observed[key]
	if !ok || observation.etag != etag || now.Before(observation.firstSeen) {
		l.observed[key] = lockObservation{etag: etag, firstSeen: now}
		return false
	}
	return !now.Before(observation.firstSeen.Add(ttl + grace))
}

func (l *Locker) recordObservation(key, etag string, now time.Time) {
	l.observeMu.Lock()
	defer l.observeMu.Unlock()
	l.observed[key] = lockObservation{etag: etag, firstSeen: now}
}

func (l *Locker) recoverLeaseWrite(ctx context.Context, key string, expected LockLease) (bool, Metadata, error) {
	current, metadata, err := l.store.Get(ctx, key)
	if err != nil {
		return false, Metadata{}, err
	}
	return sameLeaseState(current, expected), metadata, nil
}

func sameLeaseState(left, right LockLease) bool {
	return left.Entity == right.Entity &&
		left.Owner == right.Owner &&
		left.LeaseID == right.LeaseID &&
		left.Held == right.Held &&
		left.LeaseDuration == right.LeaseDuration &&
		left.Revision == right.Revision &&
		left.ExpiresAt.Equal(right.ExpiresAt) &&
		left.AcquiredAt.Equal(right.AcquiredAt)
}

func handleFromLease(name, key string, lease LockLease, etag string) *LockHandle {
	return &LockHandle{
		Name:         name,
		Key:          key,
		Owner:        lease.Owner,
		FencingToken: lease.Version,
		ETag:         etag,
		ExpiresAt:    lease.ExpiresAt,
		AcquiredAt:   lease.AcquiredAt,
	}
}

type runtimeLockConfig struct {
	owner      string
	ttl        time.Duration
	clockSkew  time.Duration
	now        func() time.Time
	retryDelay time.Duration
	maxDelay   time.Duration
}

func (c runtimeLockConfig) validate() error {
	switch {
	case c.owner == "":
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock owner cannot be empty", nil, false)
	case c.now == nil:
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock clock cannot be nil", nil, false)
	case c.ttl <= 0:
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock TTL must be positive", nil, false)
	case c.clockSkew < 0:
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock expiry grace cannot be negative", nil, false)
	case c.retryDelay <= 0:
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock retry delay must be positive", nil, false)
	case c.maxDelay <= 0:
		return NewStoreError(ErrCodeInvalidOperation, "Locker", "", "lock maximum retry delay must be positive", nil, false)
	}
	return nil
}

func validateLockKey(name, key, operation string) error {
	if name == "" {
		return NewStoreError(ErrCodeInvalidOperation, operation, name, "lock name cannot be empty", nil, false)
	}
	if path.IsAbs(key) || path.Clean(key) != key || key == "." {
		return NewStoreError(ErrCodeInvalidOperation, operation, name, "lock name or prefix contains an unsafe path", nil, false)
	}
	return nil
}

func (l *Locker) override(opts []LockOption) runtimeLockConfig {
	cfg := &lockConfig{}
	applyLockOptions(cfg, opts)
	return runtimeLockConfig{
		owner:      cfg.Owner.Or(l.owner),
		ttl:        cfg.TTL.Or(l.ttl),
		clockSkew:  cfg.ClockSkew.Or(l.clockSkew),
		now:        cfg.Now.Or(l.now),
		retryDelay: cfg.RetryDelay.Or(l.retryDelay),
		maxDelay:   cfg.MaxRetryDelay.Or(l.maxDelay),
	}
}
