package store

import (
	"context"
	"math/rand"
	"time"
)

// RetryFunc is the function type that will be retried
type RetryFunc func() error

// Retrier provides retry functionality with configurable behavior
type Retrier interface {
	// Do executes a function with retries
	Do(ctx context.Context, operation RetryFunc) error

	// WithOptions returns a new Retrier with updated options
	WithOptions(options ...RetryOption) Retrier
}

// RetryOption modifies retry behavior
type RetryOption interface {
	applyRetry(*RetryOptions)
}

// RetryOptions contains configuration for retry behavior
type RetryOptions struct {
	// MaxRetries is the maximum number of retry attempts
	MaxRetries Field[int]

	// InitialInterval is the base delay between retries
	InitialInterval Field[time.Duration]

	// MaxInterval caps the maximum delay between retries
	MaxInterval Field[time.Duration]

	// Multiplier increases the delay with each retry
	Multiplier Field[float64]

	// Jitter adds randomness to delay to avoid thundering herd
	Jitter Field[float64]

	// RetryableErrors determines which errors should be retried
	RetryableErrors Field[func(error) bool]
}

// RetryOptionFunc is a function implementing RetryOption
type RetryOptionFunc func(*RetryOptions)

func (f RetryOptionFunc) applyRetry(opts *RetryOptions) {
	f(opts)
}

// DefaultRetrier implements Retrier with exponential backoff
type DefaultRetrier struct {
	options RetryOptions
	rand    *rand.Rand
}

// NewDefaultRetrier creates a new retrier with default settings
func NewDefaultRetrier() *DefaultRetrier {
	source := rand.NewSource(time.Now().UnixNano())
	return &DefaultRetrier{
		options: RetryOptions{},
		rand:    rand.New(source),
	}
}

// WithOptions returns a new DefaultRetrier with updated options
func (r *DefaultRetrier) WithOptions(options ...RetryOption) Retrier {
	newRetrier := &DefaultRetrier{
		options: r.options,
		rand:    r.rand,
	}

	for _, option := range options {
		option.applyRetry(&newRetrier.options)
	}

	return newRetrier
}

// Do executes the operation with retries
func (r *DefaultRetrier) Do(ctx context.Context, operation RetryFunc) error {
	maxRetries := r.options.MaxRetries.Or(3)
	initial := r.options.InitialInterval.Or(100 * time.Millisecond)
	max := r.options.MaxInterval.Or(30 * time.Second)
	multiplier := r.options.Multiplier.Or(2.0)
	jitter := r.options.Jitter.Or(0.2)

	isRetryable := r.options.RetryableErrors.Or(IsRetriable)

	var err error
	currentInterval := initial

	// First attempt
	if err = operation(); err == nil {
		return nil
	}

	// Return immediately if not retriable
	if !isRetryable(err) {
		return err
	}

	// Start retry loop
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check context before sleeping
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Calculate backoff with jitter
		jitterRange := currentInterval.Seconds() * jitter
		jitterTime := time.Duration(r.rand.Float64() * jitterRange * float64(time.Second))
		sleepTime := currentInterval + jitterTime

		// Sleep before retry
		timer := time.NewTimer(sleepTime)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Continue with retry
		}

		// Try the operation again
		if err = operation(); err == nil {
			return nil
		}

		// If error is not retriable, stop
		if !isRetryable(err) {
			return err
		}

		// Increase interval for next attempt, but cap it
		nextInterval := time.Duration(float64(currentInterval) * multiplier)
		if nextInterval > max {
			currentInterval = max
		} else {
			currentInterval = nextInterval
		}
	}

	// We've exhausted retries, return the last error
	return err
}

// RetryOption implementations

// WithMaxRetries sets the maximum number of retries
func WithMaxRetries(count int) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.MaxRetries.Set(count)
	})
}

// WithInitialBackoff sets the initial backoff duration
func WithInitialBackoff(duration time.Duration) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.InitialInterval.Set(duration)
	})
}

// WithMaxBackoff sets the maximum backoff duration
func WithMaxBackoff(duration time.Duration) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.MaxInterval.Set(duration)
	})
}

// WithBackoffMultiplier sets the multiplier for exponential backoff
func WithBackoffMultiplier(multiplier float64) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.Multiplier.Set(multiplier)
	})
}

// WithBackoffJitter sets the jitter factor for randomizing delays
func WithBackoffJitter(jitter float64) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.Jitter.Set(jitter)
	})
}

// WithRetryableErrors sets a function that determines if an error is retriable
func WithRetryableErrors(isRetriable func(error) bool) RetryOption {
	return RetryOptionFunc(func(opts *RetryOptions) {
		opts.RetryableErrors.Set(isRetriable)
	})
}
