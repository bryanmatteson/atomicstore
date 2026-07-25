package store

import "time"

// TransactionOptions defines options for transaction operations
type TransactionOptions struct {
	// ReadOnly determines if the transaction allows writes
	ReadOnly Field[bool]

	// Timeout specifies a timeout for the entire transaction
	Timeout Field[time.Duration]

	// AllowPartial opts into a non-atomic ordered best-effort commit.
	AllowPartial Field[bool]

	// RetryStrategy controls commit-time retries
	RetryStrategy Field[RetryStrategy]

	// TxLogger records transaction operations
	TxLogger Field[TransactionLogger]

	// TxMetrics records transaction metrics
	TxMetrics Field[TransactionMetrics]

	// Embed operation options to inherit conditional and metadata options
	OperationOptions
}

// TransactionOption is the interface for all transaction options
type TransactionOption interface {
	applyTransaction(*TransactionOptions)
}

// TransactionGetOption is a transaction option that can be used in Get operations
type TransactionGetOption interface {
	TransactionOption
	GetOption
}

// TransactionPutOption is a transaction option that can be used in Put operations
type TransactionPutOption interface {
	TransactionOption
	PutOption
}

// TransactionDeleteOption is a transaction option that can be used in Delete operations
type TransactionDeleteOption interface {
	TransactionOption
	DeleteOption
}

// TransactionHeadOption is a transaction option that can be used in Head operations
type TransactionHeadOption interface {
	TransactionOption
	HeadOption
}

// TransactionListOption is a transaction option that can be used in List operations
type TransactionListOption interface {
	TransactionOption
	ListOption
}

// applyTransactionOptions applies a slice of transaction options to the target options
func applyTransactionOptions[T TransactionOption](opts *TransactionOptions, options []T) {
	for _, option := range options {
		option.applyTransaction(opts)
	}
}

// Allow TransactionOptions to be used as a TransactionOption
func (s TransactionOptions) applyTransaction(opts *TransactionOptions) {
	// Apply self to target options
	opts.ReadOnly.SetDefaultFrom(s.ReadOnly.Get)
	opts.Timeout.SetDefaultFrom(s.Timeout.Get)
	opts.AllowPartial.SetDefaultFrom(s.AllowPartial.Get)
	opts.RetryStrategy.SetDefaultFrom(s.RetryStrategy.Get)
	opts.TxLogger.SetDefaultFrom(s.TxLogger.Get)
	opts.TxMetrics.SetDefaultFrom(s.TxMetrics.Get)
	s.applyOperation(&opts.OperationOptions)
}

func (s TransactionOptions) applyOperation(opts *OperationOptions) {
	// Apply operation options to transaction options'
	s.MetadataOptions.applyMetadata(&opts.MetadataOptions)
	s.ConditionalOptions.applyConditional(&opts.ConditionalOptions)
	opts.ETag.SetDefaultFrom(s.ETag.Get)
	opts.IfMatch.SetDefaultFrom(s.IfMatch.Get)
	opts.IfNoMatch.SetDefaultFrom(s.IfNoMatch.Get)
	opts.IfModified.SetDefaultFrom(s.IfModified.Get)
	opts.IfNotModified.SetDefaultFrom(s.IfNotModified.Get)
}

// WithReadOnly creates a transaction option that sets the ReadOnly flag
func WithReadOnly(readOnly bool) TransactionOption {
	return TransactionOptionFunc(func(opts *TransactionOptions) {
		opts.ReadOnly.Set(readOnly)
	})
}

// WithTransactionTimeout creates a transaction option that sets the timeout
func WithTransactionTimeout(timeout time.Duration) TransactionOption {
	return TransactionOptionFunc(func(opts *TransactionOptions) {
		opts.Timeout.Set(timeout)
	})
}

// WithAllowPartialCommit opts into ordered best-effort mutation. It explicitly
// disables all-or-nothing commit semantics.
func WithAllowPartialCommit(allow bool) TransactionOption {
	return TransactionOptionFunc(func(opts *TransactionOptions) {
		opts.AllowPartial.Set(allow)
	})
}

// TransactionOptionFunc is a function that implements TransactionOption
type TransactionOptionFunc func(*TransactionOptions)

func (f TransactionOptionFunc) applyTransaction(opts *TransactionOptions) {
	f(opts)
}

// Apply existing option types to TransactionOptions by making them also implement TransactionOption
// This is already in the options.go file:
// func (f ConditionalOptionFunc) applyTransaction(opts *TransactionOptions) {
//     f.applyConditional(&opts.ConditionalOptions)
// }
// func (f MetadataOptionFunc) applyTransaction(opts *TransactionOptions) {
//     f.applyMetadata(&opts.MetadataOptions)
// }
