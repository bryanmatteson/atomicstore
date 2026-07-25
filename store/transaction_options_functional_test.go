package store

import (
	"context"
	"testing"
	"time"
)

func TestTransactionOptions_ApplyFunctions(t *testing.T) {
	t.Run("Basic Apply", func(t *testing.T) {
		// Create base transaction options
		opts := &TransactionOptions{}

		// Define option functions
		readOnly := func(o *TransactionOptions) {
			o.ReadOnly.Set(true)
		}

		timeout := func(o *TransactionOptions) {
			o.Timeout.Set(5 * time.Second)
		}

		// Apply the functions
		applyTransactionOptions(opts, []TransactionOption{
			TransactionOptionFunc(readOnly),
			TransactionOptionFunc(timeout),
		})

		// Verify options were applied
		if !opts.ReadOnly.Or(false) {
			t.Error("ReadOnly option not applied")
		}

		if opts.Timeout.Or(0) != 5*time.Second {
			t.Errorf("Timeout option not applied correctly, got %v", opts.Timeout.Or(0))
		}
	})

	t.Run("WithReadOnly", func(t *testing.T) {
		// Test ReadOnly option
		opts := &TransactionOptions{}

		// Define a WithReadOnly option function
		withReadOnly := func() TransactionOption {
			return TransactionOptionFunc(func(o *TransactionOptions) {
				o.ReadOnly.Set(true)
			})
		}

		// Apply the option
		withReadOnly().applyTransaction(opts)

		// Verify
		if !opts.ReadOnly.Or(false) {
			t.Error("WithReadOnly option not applied")
		}
	})

	t.Run("WithTimeout", func(t *testing.T) {
		// Test Timeout option
		opts := &TransactionOptions{}

		// Define a WithTimeout option function
		withTimeout := func(duration time.Duration) TransactionOption {
			return TransactionOptionFunc(func(o *TransactionOptions) {
				o.Timeout.Set(duration)
			})
		}

		// Apply the option
		withTimeout(10 * time.Second).applyTransaction(opts)

		// Verify
		if opts.Timeout.Or(0) != 10*time.Second {
			t.Errorf("WithTimeout option not applied correctly, got %v", opts.Timeout.Or(0))
		}
	})

	t.Run("Option Composition", func(t *testing.T) {
		// Test composing multiple options
		opts := &TransactionOptions{}

		// Define composite option
		compositeOption := func() TransactionOption {
			return TransactionOptionFunc(func(o *TransactionOptions) {
				o.ReadOnly.Set(true)
				o.Timeout.Set(15 * time.Second)
				o.ContentType.Set("application/json")
				o.IfMatch.SetRight("test-etag")
			})
		}

		// Apply the option
		compositeOption().applyTransaction(opts)

		// Verify
		if !opts.ReadOnly.Or(false) {
			t.Error("ReadOnly not set in composite option")
		}

		if opts.Timeout.Or(0) != 15*time.Second {
			t.Errorf("Timeout not set correctly in composite option, got %v", opts.Timeout.Or(0))
		}

		if opts.ContentType.Or("") != "application/json" {
			t.Errorf("ContentType not set correctly in composite option, got %s", opts.ContentType.Or(""))
		}

		if opts.IfMatch.Right() != "test-etag" {
			t.Errorf("IfMatch not set correctly in composite option, got %s", opts.IfMatch.Right())
		}
	})

	t.Run("Option Inheritance", func(t *testing.T) {
		// Test TransactionOption implementations inheriting from other option types
		opts := &TransactionOptions{}

		// Apply ConditionalOptionFunc to TransactionOptions
		condOpt := IfMatch("test-etag")
		condOpt.applyTransaction(opts)

		// Apply MetadataOptionFunc to TransactionOptions
		metaOpt := WithContentType("application/json")
		metaOpt.applyTransaction(opts)

		// Verify options were applied correctly
		if opts.IfMatch.Right() != "test-etag" {
			t.Errorf("Conditional option not applied correctly, got %s", opts.IfMatch.Right())
		}

		if opts.ContentType.Or("") != "application/json" {
			t.Errorf("Metadata option not applied correctly, got %s", opts.ContentType.Or(""))
		}
	})

	t.Run("Option Overriding", func(t *testing.T) {
		// Test overriding existing options
		opts := &TransactionOptions{}

		// Set initial values
		opts.ReadOnly.Set(true)
		opts.Timeout.Set(5 * time.Second)

		// Create options that override
		overrideOpt := TransactionOptionFunc(func(o *TransactionOptions) {
			o.ReadOnly.Set(false)
			o.Timeout.Set(10 * time.Second)
		})

		// Apply override
		overrideOpt.applyTransaction(opts)

		// Verify
		if opts.ReadOnly.Or(true) {
			t.Error("ReadOnly option not overridden")
		}

		if opts.Timeout.Or(0) != 10*time.Second {
			t.Errorf("Timeout option not overridden correctly, got %v", opts.Timeout.Or(0))
		}
	})
}

// Test actual implementation of transaction options with transaction operations
func TestTransactionOptions_WithTransaction(t *testing.T) {
	// Create mock storage with an item
	mockStorage := NewMockStorage("mock")
	mockStorage.AddTestObject(
		"mock://test-bucket/test-key",
		[]byte(`{"id":"test","data":"value"}`),
		"application/json",
		map[string]string{},
	)

	t.Run("ReadOnly Transaction", func(t *testing.T) {
		ctx := context.Background()

		// Create transaction with ReadOnly flag
		tx := NewStorageTransaction(ctx, mockStorage)
		tx.readOnly = true

		// Try to read (should succeed)
		_, _, err := tx.Get("mock://test-bucket/test-key")
		if err != nil {
			t.Errorf("Get on read-only transaction should succeed: %v", err)
		}

		// Try to write (should fail)
		err = tx.Put("mock://test-bucket/new-key", []byte(`{"data":"new"}`))
		if err == nil {
			t.Error("Put on read-only transaction should fail")
		}

		// Try to delete (should fail)
		err = tx.Delete("mock://test-bucket/test-key")
		if err == nil {
			t.Error("Delete on read-only transaction should fail")
		}
	})

	t.Run("Transaction With Timeout", func(t *testing.T) {
		ctx := context.Background()

		// Create transaction with a timeout
		tx := NewStorageTransaction(ctx, mockStorage)
		tx.timeout = 5 * time.Millisecond

		// Sleep longer than the timeout
		time.Sleep(10 * time.Millisecond)

		// Attempting operations should fail with timeout
		err := tx.Put("mock://test-bucket/timeout-key", []byte(`{"data":"timeout"}`))
		if err == nil {
			t.Error("Put should fail after timeout")
		}
	})
}
