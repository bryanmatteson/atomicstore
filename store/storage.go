package store

import (
	"context"
	"io"
)

// Storage defines the interface for storage providers
type Storage interface {
	// URIScheme returns the URI scheme for this storage provider
	URIScheme() string

	// Get retrieves an object by URI
	Get(ctx context.Context, uri string, options ...GetOption) ([]byte, Metadata, error)

	// GetStream returns an object as a stream
	GetStream(ctx context.Context, uri string, options ...GetOption) (io.ReadCloser, Metadata, error)

	// Put stores an object by URI
	Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error)

	// PutStream stores a stream by URI
	PutStream(ctx context.Context, uri string, reader io.Reader, options ...PutOption) (Metadata, error)

	// Delete removes an object by URI
	Delete(ctx context.Context, uri string, options ...DeleteOption) error

	// Head retrieves metadata without retrieving the full object
	Head(ctx context.Context, uri string, options ...HeadOption) (Metadata, error)

	// List returns objects matching specified criteria
	List(ctx context.Context, uri string, options ...ListOption) ([]Entry, error)
}

// AtomicBatchOperation is one mutation in an all-or-nothing backend batch.
type AtomicBatchOperation struct {
	Type    string
	URI     string
	Data    []byte
	Options OperationOptions
}

// AtomicBatchStorage is implemented only by backends that can make every
// operation in a batch visible atomically. StorageTransaction fails closed for
// multi-object commits when this capability is unavailable.
type AtomicBatchStorage interface {
	Storage
	ApplyAtomicBatch(ctx context.Context, operations []AtomicBatchOperation) error
}

// LinearizableConditionalStorage declares that create-if-absent, ETag
// compare-and-swap, and conditional delete are linearizable for one key.
type LinearizableConditionalStorage interface {
	Storage
	HasLinearizableConditions() bool
}

// StreamHandler performs operations on streams
type StreamHandler interface {
	// CopyStream copies from source to destination
	CopyStream(ctx context.Context, srcURI, destURI string, options ...CopyOption) (Metadata, error)

	// TransformStream applies a transformation function to a stream
	TransformStream(ctx context.Context, uri string, transform func(io.Reader) io.Reader, options ...PutOption) (Metadata, error)
}

// CopyOption defines options for CopyStream operations
type CopyOption interface {
	applyCopy(*CopyOptions)
}

// CopyOptions contains options for copy operations
type CopyOptions struct {
	// Embed conditional and metadata options
	ConditionalOptions
	MetadataOptions
}

// TransformOption defines options for TransformStream operations
type TransformOption interface {
	applyTransform(*TransformOptions)
}

// TransformOptions contains options for transform operations
type TransformOptions struct {
	// Embed put options
	OperationOptions
}
