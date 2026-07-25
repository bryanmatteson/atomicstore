package store

import "time"

type lockConfig struct {
	Owner         Optional[string]
	TTL           Optional[time.Duration]
	ClockSkew     Optional[time.Duration]
	KeyPrefix     Optional[string]
	Now           Optional[func() time.Time]
	RetryDelay    Optional[time.Duration]
	MaxRetryDelay Optional[time.Duration]
}

// LockOption configures Locker construction or per-call overrides.
type LockOption interface {
	applyLock(*lockConfig)
}

type lockOptionFunc func(*lockConfig)

func (f lockOptionFunc) applyLock(cfg *lockConfig) {
	f(cfg)
}

func applyLockOptions(cfg *lockConfig, opts []LockOption) {
	for _, opt := range opts {
		opt.applyLock(cfg)
	}
}

// WithLockOwner sets the lock owner identity used on acquire/steal.
func WithLockOwner(owner string) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.Owner.Set(owner)
	})
}

// WithLockTTL sets the lease duration.
func WithLockTTL(ttl time.Duration) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.TTL.Set(ttl)
	})
}

// WithLockClockSkew sets extra grace after a lease has remained unchanged for
// its full duration. The legacy name is retained for API compatibility.
func WithLockClockSkew(skew time.Duration) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.ClockSkew.Set(skew)
	})
}

// WithLockKeyPrefix sets the key prefix for lock objects (default "locks/").
func WithLockKeyPrefix(prefix string) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.KeyPrefix.Set(prefix)
	})
}

// WithLockClock injects a clock for tests.
func WithLockClock(now func() time.Time) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.Now.Set(now)
	})
}

// WithLockRetryDelay sets the initial Acquire backoff.
func WithLockRetryDelay(d time.Duration) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.RetryDelay.Set(d)
	})
}

// WithLockMaxRetryDelay sets the maximum Acquire backoff.
func WithLockMaxRetryDelay(d time.Duration) LockOption {
	return lockOptionFunc(func(cfg *lockConfig) {
		cfg.MaxRetryDelay.Set(d)
	})
}
