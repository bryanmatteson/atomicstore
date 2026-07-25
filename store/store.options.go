package store

import (
	"fmt"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"
)

// StoreOptions defines configurable options for Store
type StoreOptions struct {
	// Storage implementation to use
	Storage Field[Storage]

	// Bucket or container name
	Bucket Field[string]

	// Codec to use for serialization/deserialization
	Codec Field[codec.Codec]

	// Default prefix for all keys
	KeyPrefix Field[string]

	// Default timeout for operations
	Timeout Field[time.Duration]

	// Retry configuration
	Retrier Field[Retrier]

	// Embed observability options
	ObservabilityOptions
}

// StoreOption is the interface for all store options
type StoreOption interface {
	applyStore(*StoreOptions) error
}

// Store option implementations

func WithCodec(codec codec.Codec) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if codec == nil {
			return fmt.Errorf("codec cannot be nil")
		}

		opts.Codec.Set(codec)
		return nil
	})
}

func WithRegisteredCodec(name string, options ...codec.Option) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if name == "" {
			return fmt.Errorf("codec name cannot be empty")
		}

		codec, err := codec.Get(name, options...)
		if err != nil {
			return fmt.Errorf("failed to get codec: %w", err)
		}

		opts.Codec.Set(codec)
		return nil
	})
}

// WithBucket sets the bucket name
func WithBucket(bucket string) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if bucket == "" {
			return NewStoreError(ErrCodeInvalidBucket, "WithBucket", "", "Bucket name cannot be empty", nil, false)
		}

		opts.Bucket.Set(bucket)
		return nil
	})
}

// WithKeyPrefix sets a prefix for all keys
func WithKeyPrefix(prefix string) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		opts.KeyPrefix.Set(prefix)
		return nil
	})
}

// WithTimeout sets the default timeout for operations
func WithTimeout(timeout time.Duration) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}

		opts.Timeout.Set(timeout)
		return nil
	})
}

// WithRetrier sets the retry configuration
func WithRetrier(retrier Retrier) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if retrier == nil {
			return fmt.Errorf("retrier cannot be nil")
		}

		opts.Retrier.Set(retrier)
		return nil
	})
}

// WithStorage sets the storage implementation
func WithStorage(storage Storage) StoreOption {
	return StoreOptionFunc(func(opts *StoreOptions) error {
		if storage == nil {
			return fmt.Errorf("storage cannot be nil")
		}

		opts.Storage.Set(storage)
		return nil
	})
}

// StoreOptionFunc is a function that implements StoreOption
type StoreOptionFunc func(*StoreOptions) error

func (f StoreOptionFunc) applyStore(opts *StoreOptions) error {
	return f(opts)
}

// Helper function to apply store options
func applyStoreOptions(opts *StoreOptions, options []StoreOption) error {
	for _, option := range options {
		if err := option.applyStore(opts); err != nil {
			return err
		}
	}
	return nil
}

// Allow StoreOptions to be used as a StoreOption
func (s StoreOptions) applyStore(opts *StoreOptions) error {
	// Apply self to target options
	opts.Storage.SetDefaultFrom(s.Storage.Get)
	opts.Bucket.SetDefaultFrom(s.Bucket.Get)
	opts.Codec.SetDefaultFrom(s.Codec.Get)
	opts.KeyPrefix.SetDefaultFrom(s.KeyPrefix.Get)
	opts.Timeout.SetDefaultFrom(s.Timeout.Get)
	opts.Retrier.SetDefaultFrom(s.Retrier.Get)
	s.applyObservability(&opts.ObservabilityOptions)
	return nil
}
