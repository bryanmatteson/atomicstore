package store

import (
	"testing"
	"time"
)

func TestTransactionOptions_Apply(t *testing.T) {
	// Create base options
	baseOpts := TransactionOptions{}
	baseOpts.ReadOnly.Set(true)
	baseOpts.Timeout.Set(5 * time.Second)

	// Create a target to apply to
	target := TransactionOptions{}

	// Apply the options
	baseOpts.applyTransaction(&target)

	// Check the results
	if !target.ReadOnly.Or(false) {
		t.Error("ReadOnly was not applied correctly")
	}

	if target.Timeout.Or(0) != 5*time.Second {
		t.Errorf("Timeout was not applied correctly, expected 5s, got %v", target.Timeout.Or(0))
	}
}

func TestTransactionOption_Interfaces(t *testing.T) {
	// Create options of each type
	readOnly := ConditionalOptionFunc(func(opts *ConditionalOptions) {
		// Just a dummy implementation
	})

	// Test that each option satisfies the required interfaces
	var txOption TransactionOption = readOnly
	var txGetOption TransactionGetOption = readOnly
	var txDeleteOption TransactionDeleteOption = readOnly

	// Test metadata option for put options
	metadataOpt := MetadataOptionFunc(func(opts *MetadataOptions) {
		// Just a dummy implementation
	})

	var txPutOption TransactionPutOption = metadataOpt

	// Just checking interface satisfaction - if it compiles, it works
	t.Log("Option interfaces are working correctly:", txOption, txGetOption, txPutOption, txDeleteOption)
}

func TestTransactionOptions_MixedOptions(t *testing.T) {
	// Test applying a mix of option types to a transaction
	tx := TransactionOptions{}

	// Apply options
	roOpt := ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfMatch.SetRight("test-etag")
	})

	metaOpt := MetadataOptionFunc(func(opts *MetadataOptions) {
		opts.ContentType.Set("application/json")
	})

	// Apply the conditional option
	roOpt.applyTransaction(&tx)

	// Apply the metadata option
	metaOpt.applyTransaction(&tx)

	// Verify both were applied
	if tx.IfMatch.Right() != "test-etag" {
		t.Error("Conditional option was not applied correctly")
	}

	if tx.ContentType.Or("") != "application/json" {
		t.Error("Metadata option was not applied correctly")
	}
}

// Custom transaction option implementation for testing
type testTransactionOption struct {
	readOnly bool
	timeout  time.Duration
}

func (o testTransactionOption) applyTransaction(opts *TransactionOptions) {
	opts.ReadOnly.Set(o.readOnly)
	opts.Timeout.Set(o.timeout)
}

func TestCustomTransactionOption(t *testing.T) {
	// Create custom option
	customOpt := testTransactionOption{
		readOnly: true,
		timeout:  10 * time.Second,
	}

	// Apply to empty options
	opts := TransactionOptions{}
	customOpt.applyTransaction(&opts)

	// Check results
	if !opts.ReadOnly.Or(false) {
		t.Error("Custom option did not set ReadOnly correctly")
	}

	if opts.Timeout.Or(0) != 10*time.Second {
		t.Errorf("Custom option did not set Timeout correctly, got %v", opts.Timeout.Or(0))
	}

	// Test as part of the interface
	var txOption TransactionOption = customOpt

	// Apply through the interface
	newOpts := TransactionOptions{}
	txOption.applyTransaction(&newOpts)

	if !newOpts.ReadOnly.Or(false) || newOpts.Timeout.Or(0) != 10*time.Second {
		t.Error("Applying option through interface did not work correctly")
	}
}
