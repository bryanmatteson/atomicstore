package store

import (
	"testing"
	"time"
)

func TestTransactionOptions_Functionality(t *testing.T) {
	t.Run("ReadOnly", func(t *testing.T) {
		// Create read-only transaction option
		readOnly := func(tx *TransactionOptions) {
			tx.ReadOnly.Set(true)
		}

		// Apply to options
		opts := &TransactionOptions{}
		TransactionOption(TransactionOptionFunc(readOnly)).applyTransaction(opts)

		if !opts.ReadOnly.Or(false) {
			t.Error("ReadOnly option not applied correctly")
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		// Create timeout transaction option
		timeout := func(tx *TransactionOptions) {
			tx.Timeout.Set(5 * time.Second)
		}

		// Apply to options
		opts := &TransactionOptions{}
		TransactionOption(TransactionOptionFunc(timeout)).applyTransaction(opts)

		if opts.Timeout.Or(0) != 5*time.Second {
			t.Error("Timeout option not applied correctly")
		}
	})

	t.Run("Inheritance", func(t *testing.T) {
		// Create more complex options that inherit behavior
		opts := &TransactionOptions{}

		// Add conditional option
		ifMatch := IfMatch("test-etag")
		ifMatch.applyTransaction(opts)

		if opts.IfMatch.Right() != "test-etag" {
			t.Error("If-Match conditional option not applied correctly to transaction")
		}
	})
}

func TestTransactionOption_Implementation(t *testing.T) {
	// Test the implementation of TransactionOption with different option types
	var opts TransactionOptions

	// Create a test TransactionOption
	testOpt := TransactionOptionFunc(func(opts *TransactionOptions) {
		opts.ReadOnly.Set(true)
		opts.Timeout.Set(10 * time.Second)
	})

	// Apply the option
	testOpt.applyTransaction(&opts)

	// Verify option was applied
	if !opts.ReadOnly.Or(false) {
		t.Error("ReadOnly not applied by TransactionOptionFunc")
	}

	if opts.Timeout.Or(0) != 10*time.Second {
		t.Error("Timeout not applied by TransactionOptionFunc")
	}
}

func TestTransactionOptions_Combined(t *testing.T) {
	// Test combining different types of options
	var opts TransactionOptions

	// Apply ConditionalOptionFunc as TransactionOption
	conOpt := ConditionalOptionFunc(func(cond *ConditionalOptions) {
		cond.ETag.Set("test-etag")
	})
	conOpt.applyTransaction(&opts)

	// Apply MetadataOptionFunc as TransactionOption
	metaOpt := MetadataOptionFunc(func(meta *MetadataOptions) {
		meta.ContentType.Set("application/json")
	})
	metaOpt.applyTransaction(&opts)

	// Verify both options were applied
	if opts.ETag.Or("") != "test-etag" {
		t.Error("ETag not applied correctly")
	}

	if opts.ContentType.Or("") != "application/json" {
		t.Error("ContentType not applied correctly")
	}
}

func TestTransactionOptions_ApplyFunction(t *testing.T) {
	// Test applying multiple options at once
	opts := &TransactionOptions{}

	// Create test options
	options := []TransactionOption{
		TransactionOptionFunc(func(o *TransactionOptions) {
			o.ReadOnly.Set(true)
		}),
		TransactionOptionFunc(func(o *TransactionOptions) {
			o.Timeout.Set(5 * time.Second)
		}),
	}

	// Apply options with the helper function
	applyTransactionOptions(opts, options)

	// Verify all options were applied
	if !opts.ReadOnly.Or(false) {
		t.Error("ReadOnly not applied by applyTransactionOptions")
	}

	if opts.Timeout.Or(0) != 5*time.Second {
		t.Error("Timeout not applied by applyTransactionOptions")
	}
}

func TestTransactionOptionInterfaces(t *testing.T) {
	// Verify that options properly implement the specialized interfaces

	// ConditionalOptionFunc implements TransactionGetOption
	var getOpt TransactionGetOption = IfMatch("test")
	var txOpt TransactionOption = getOpt

	// Verify GetOption and TransactionOption are both satisfied
	opts1 := &OperationOptions{}
	getOpt.applyGet(opts1)

	opts2 := &TransactionOptions{}
	txOpt.applyTransaction(opts2)

	if opts1.IfMatch.Right() != "test" || opts2.IfMatch.Right() != "test" {
		t.Error("Option does not correctly implement both interfaces")
	}

	// MetadataOptionFunc implements TransactionPutOption
	var putOpt TransactionPutOption = WithContentType("text/plain")

	// Verify PutOption and TransactionOption are both satisfied
	opts3 := &OperationOptions{}
	putOpt.applyPut(opts3)

	opts4 := &TransactionOptions{}
	putOpt.applyTransaction(opts4)

	if opts3.ContentType.Or("") != "text/plain" || opts4.ContentType.Or("") != "text/plain" {
		t.Error("Option does not correctly implement both interfaces")
	}
}
