package store

import (
	"strings"
	"time"
)

// ConditionalOptions represents options for conditional operations
type ConditionalOptions struct {
	// IfMatch can be either a boolean flag (left) or an etag string (right)
	IfMatch Either[bool, string]
	// IfNoMatch can be either a boolean flag (left) or an etag string (right)
	IfNoMatch Either[bool, string]
	// IfModified can be either a boolean flag (left) or a time (right)
	IfModified Either[bool, time.Time]
	// IfNotModified can be either a boolean flag (left) or a time (right)
	IfNotModified Either[bool, time.Time]
	// VersionID is an optional version identifier
	VersionID Optional[string]
	// ETag stores the etag value for operations
	ETag Optional[string]
}

// MetadataOptions represents options for metadata operations
type MetadataOptions struct {
	// ContentType is an optional content type
	ContentType Optional[string]
	// ContentEncoding is an optional content encoding
	ContentEncoding Optional[string]
	// StorageClass is an optional storage class
	StorageClass Optional[string]
	// Metadata is an optional map of metadata key-value pairs
	Metadata MapField[string, string]
}

// ListOptions represents options for list operations
type ListOptions struct {
	ConditionalOptions
	MetadataOptions
	// Prefix is an optional prefix to filter objects
	Prefix Optional[string]
	// MaxKeys is an optional maximum number of keys to return
	MaxKeys Optional[int]
	// Delimiter is an optional delimiter for grouping keys
	Delimiter Optional[string]
	// StartAfter is an optional key to start listing after
	StartAfter Optional[string]
	// Recursive is an optional flag to list recursively
	Recursive Optional[bool]
	// IncludeMetadata is an optional flag to include metadata in the response
	IncludeMetadata Optional[bool]
}

func (m MetadataOptions) applyMetadata(opts *MetadataOptions) {
	if m.ContentType.IsSet() {
		opts.ContentType.SetDefault(m.ContentType.Get())
	}
	if m.ContentEncoding.IsSet() {
		opts.ContentEncoding.SetDefault(m.ContentEncoding.Get())
	}
	if m.StorageClass.IsSet() {
		opts.StorageClass.SetDefault(m.StorageClass.Get())
	}
	if m.Metadata.IsSet() {
		opts.Metadata.SetDefaultFrom(m.Metadata.Get)
	}
}

func (c ConditionalOptions) applyTransaction(opts *TransactionOptions) {
	// Apply conditional options to transaction options
	c.applyConditional(&opts.ConditionalOptions)
}

func (c ConditionalOptions) applyGet(opts *OperationOptions) {
	// Apply conditional options to get options
	c.applyConditional(&opts.ConditionalOptions)
}
func (c ConditionalOptions) applyPut(opts *OperationOptions) {
	// Apply conditional options to put options
	c.applyConditional(&opts.ConditionalOptions)
	opts.ETag.SetDefaultFrom(c.ETag.Get)
	opts.VersionID.SetDefaultFrom(c.VersionID.Get)
}
func (c ConditionalOptions) applyDelete(opts *OperationOptions) {
	// Apply conditional options to delete options
	c.applyConditional(&opts.ConditionalOptions)
	opts.ETag.SetDefaultFrom(c.ETag.Get)
	opts.VersionID.SetDefaultFrom(c.VersionID.Get)
}

func (c ConditionalOptions) applyConditional(opts *ConditionalOptions) {
	copyEitherBoolString(&opts.IfMatch, &c.IfMatch)
	copyEitherBoolString(&opts.IfNoMatch, &c.IfNoMatch)
	copyEitherBoolTime(&opts.IfModified, &c.IfModified)
	copyEitherBoolTime(&opts.IfNotModified, &c.IfNotModified)
	opts.VersionID.SetDefaultFrom(c.VersionID.Get)
	opts.ETag.SetDefaultFrom(c.ETag.Get)
}

func copyEitherBoolString(dst, src *Either[bool, string]) {
	if dst.IsSet() || !src.IsSet() {
		return
	}
	if src.IsLeft() {
		dst.SetLeft(src.Left())
	} else {
		dst.SetRight(src.Right())
	}
}

func copyEitherBoolTime(dst, src *Either[bool, time.Time]) {
	if dst.IsSet() || !src.IsSet() {
		return
	}
	if src.IsLeft() {
		dst.SetLeft(src.Left())
	} else {
		dst.SetRight(src.Right())
	}
}

func (c ConditionalOptionFunc) applyTransaction(opts *TransactionOptions) {
	c(&opts.ConditionalOptions)
}

func (o ListOptions) applyList(opts *ListOptions) {
	o.ConditionalOptions.applyConditional(&opts.ConditionalOptions)
	o.MetadataOptions.applyMetadata(&opts.MetadataOptions)

	if o.Prefix.IsSet() {
		opts.Prefix.SetDefault(o.Prefix.Get())
	}
	if o.MaxKeys.IsSet() {
		opts.MaxKeys.SetDefault(o.MaxKeys.Get())
	}
	if o.Delimiter.IsSet() {
		opts.Delimiter.SetDefault(o.Delimiter.Get())
	}
	if o.StartAfter.IsSet() {
		opts.StartAfter.SetDefault(o.StartAfter.Get())
	}
	if o.Recursive.IsSet() {
		opts.Recursive.SetDefault(o.Recursive.Get())
	}
	if o.IncludeMetadata.IsSet() {
		opts.IncludeMetadata.SetDefault(o.IncludeMetadata.Get())
	}
}

// ObjectOptions represents options for object operations
type ObjectOptions struct {
	ConditionalOptions
	MetadataOptions

	Force      Optional[bool]
	Autocommit Optional[bool]
}

func (o ObjectOptions) applyObject(opts *ObjectOptions) {
	opts.ConditionalOptions = o.ConditionalOptions
	opts.MetadataOptions = o.MetadataOptions
	opts.Force = o.Force
	opts.Autocommit = o.Autocommit
}

// OperationOptions represents options for storage operations
type OperationOptions struct {
	ConditionalOptions
	MetadataOptions
}

func (o OperationOptions) applyTransaction(opts *TransactionOptions) {
	o.applyOperation(&opts.OperationOptions)
}

func (o OperationOptions) applyPut(opts *OperationOptions) {
	o.applyOperation(opts)
}
func (o OperationOptions) applyDelete(opts *OperationOptions) {
	o.applyOperation(opts)
}
func (o OperationOptions) applyHead(opts *OperationOptions) {
	o.applyOperation(opts)
}
func (o OperationOptions) applyGet(opts *OperationOptions) {
	o.applyOperation(opts)
}
func (o OperationOptions) applyOperation(opts *OperationOptions) {
	o.ConditionalOptions.applyConditional(&opts.ConditionalOptions)
	o.MetadataOptions.applyMetadata(&opts.MetadataOptions)

	opts.ContentType.SetDefaultFrom(o.ContentType.Get)
	opts.ContentEncoding.SetDefaultFrom(o.ContentEncoding.Get)
	opts.StorageClass.SetDefaultFrom(o.StorageClass.Get)
	opts.Metadata.SetDefaultFrom(o.Metadata.Get)
	opts.VersionID.SetDefaultFrom(o.VersionID.Get)
	opts.ETag.SetDefaultFrom(o.ETag.Get)
}

// ConditionalOption is an option for conditional operations
type ConditionalOption interface {
	applyConditional(opts *ConditionalOptions)
}

// MetadataOption is an option for metadata operations
type MetadataOption interface {
	applyMetadata(opts *MetadataOptions)
}

// ListOption is an option for list operations
type ListOption interface {
	applyList(opts *ListOptions)
}

// ObjectOption is an option for object operations
type ObjectOption interface {
	applyObject(opts *ObjectOptions)
}

// PutOption is an option for put operations
type PutOption interface {
	applyPut(opts *OperationOptions)
}

// GetOption is an option for get operations
type GetOption interface {
	applyGet(opts *OperationOptions)
}

// DeleteOption is an option for delete operations
type DeleteOption interface {
	applyDelete(opts *OperationOptions)
}

// HeadOption is an option for head operations
type HeadOption interface {
	applyHead(opts *OperationOptions)
}

// ConditionalOptionFunc is a function that implements ConditionalOption
type ConditionalOptionFunc func(*ConditionalOptions)

func (f ConditionalOptionFunc) applyConditional(opts *ConditionalOptions) {
	f(opts)
}

// Function that makes a ConditionalOption also a PutOption, GetOption, etc.
func (f ConditionalOptionFunc) applyPut(opts *OperationOptions) {
	f(&opts.ConditionalOptions)
}

func (f ConditionalOptionFunc) applyGet(opts *OperationOptions) {
	f(&opts.ConditionalOptions)
}

func (f ConditionalOptionFunc) applyDelete(opts *OperationOptions) {
	f(&opts.ConditionalOptions)
}

func (f ConditionalOptionFunc) applyHead(opts *OperationOptions) {
	f(&opts.ConditionalOptions)
}

func (f ConditionalOptionFunc) applyObject(opts *ObjectOptions) {
	f(&opts.ConditionalOptions)
}

func (f ConditionalOptionFunc) applyList(opts *ListOptions) {
	f(&opts.ConditionalOptions)
}

// MetadataOptionFunc is a function that implements MetadataOption
type MetadataOptionFunc func(*MetadataOptions)

func (f MetadataOptionFunc) applyMetadata(opts *MetadataOptions) {
	f(opts)
}
func (f MetadataOptionFunc) applyTransaction(opts *TransactionOptions) {
	f(&opts.MetadataOptions)
}

// Function that makes a MetadataOption also a PutOption, GetOption, etc.
func (f MetadataOptionFunc) applyPut(opts *OperationOptions) {
	f(&opts.MetadataOptions)
}

func (f MetadataOptionFunc) applyGet(opts *OperationOptions) {
	f(&opts.MetadataOptions)
}

func (f MetadataOptionFunc) applyDelete(opts *OperationOptions) {
	f(&opts.MetadataOptions)
}

func (f MetadataOptionFunc) applyHead(opts *OperationOptions) {
	f(&opts.MetadataOptions)
}

func (f MetadataOptionFunc) applyObject(opts *ObjectOptions) {
	f(&opts.MetadataOptions)
}

func (f MetadataOptionFunc) applyList(opts *ListOptions) {
	f(&opts.MetadataOptions)
}

// ListOptionFunc is a function that implements ListOption
type ListOptionFunc func(*ListOptions)

func (f ListOptionFunc) applyList(opts *ListOptions) {
	f(opts)
}

// ObjectOptionFunc is a function that implements ObjectOption
type ObjectOptionFunc func(*ObjectOptions)

func (f ObjectOptionFunc) applyObject(opts *ObjectOptions) {
	f(opts)
}

type OperationOptionFunc func(*OperationOptions)

func (f OperationOptionFunc) applyPut(opts *OperationOptions) {
	f(opts)
}
func (f OperationOptionFunc) applyGet(opts *OperationOptions) {
	f(opts)
}
func (f OperationOptionFunc) applyDelete(opts *OperationOptions) {
	f(opts)
}
func (f OperationOptionFunc) applyHead(opts *OperationOptions) {
	f(opts)
}
func (f OperationOptionFunc) applyTransaction(opts *TransactionOptions) {
	f(&opts.OperationOptions)
}

// IfMatch creates a conditional option for If-Match header
func IfMatch(etags ...string) ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		// Join multiple etags with commas if provided
		joinedEtags := strings.Join(etags, ",")
		opts.IfMatch.SetRight(joinedEtags)
		// Also set the ETag field
		opts.ETag.Set(joinedEtags)
	})
}

// IfNoneMatch creates a conditional option for If-None-Match header
func IfNoneMatch(etags ...string) ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		// Join multiple etags with commas if provided
		joinedEtags := strings.Join(etags, ",")
		opts.IfNoMatch.SetRight(joinedEtags)
		// Also set the ETag field
		opts.ETag.Set(joinedEtags)
	})
}

// IfExists creates a conditional option that requires the object to exist.
// Backends treat this as If-Match: * (must exist). Prefer IfMatch(etag) for CAS updates.
func IfExists() ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfMatch.SetLeft(true)
	})
}

// IfNotExists creates a conditional option that requires the object to not exist.
// This maps to If-None-Match: * on Put (create-if-absent).
func IfNotExists() ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfNoMatch.SetRight("*")
	})
}

// IfModified creates a conditional option that requires the object to be modified
func IfModified() ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfModified.SetLeft(true)
	})
}

// IfModifiedSince creates a conditional option that requires the object to be modified since the given time
func IfModifiedSince(t time.Time) ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfModified.SetRight(t)
	})
}

// IfNotModified creates a conditional option that requires the object to not be modified
func IfNotModified() ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfNotModified.SetLeft(true)
	})
}

// IfNotModifiedSince creates a conditional option that requires the object to not be modified since the given time
func IfNotModifiedSince(t time.Time) ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.IfNotModified.SetRight(t)
	})
}

// WithVersionID creates a conditional option that specifies a version ID
func WithVersionID(versionID string) ConditionalOptionFunc {
	return ConditionalOptionFunc(func(opts *ConditionalOptions) {
		opts.VersionID.Set(versionID)
	})
}

// WithMetadata creates a metadata option that adds a key-value pair
func WithMetadata(key, value string) MetadataOptionFunc {
	return MetadataOptionFunc(func(opts *MetadataOptions) {
		if !opts.Metadata.IsSet() || opts.Metadata.Get() == nil {
			opts.Metadata.Set(make(map[string]string))
		}
		opts.Metadata.Get()[key] = value
	})
}

// WithContentType creates a metadata option that sets the content type
func WithContentType(contentType string) MetadataOptionFunc {
	return MetadataOptionFunc(func(opts *MetadataOptions) {
		opts.ContentType.Set(contentType)
	})
}

// WithContentEncoding creates a metadata option that sets the content encoding
func WithContentEncoding(contentEncoding string) MetadataOptionFunc {
	return MetadataOptionFunc(func(opts *MetadataOptions) {
		opts.ContentEncoding.Set(contentEncoding)
	})
}

// WithStorageClass creates a metadata option that sets the storage class
func WithStorageClass(storageClass string) MetadataOptionFunc {
	return MetadataOptionFunc(func(opts *MetadataOptions) {
		opts.StorageClass.Set(storageClass)
	})
}

// WithPrefix creates a list option that sets the prefix
func WithPrefix(prefix string) ListOptionFunc {
	return ListOptionFunc(func(opts *ListOptions) {
		opts.Prefix.Set(prefix)
	})
}

// WithMaxKeys creates a list option that sets the maximum number of keys
func WithMaxKeys(maxKeys int) ListOptionFunc {
	return ListOptionFunc(func(opts *ListOptions) {
		opts.MaxKeys.Set(maxKeys)
	})
}

// WithDelimiter creates a list option that sets the delimiter
func WithDelimiter(delimiter string) ListOptionFunc {
	return ListOptionFunc(func(opts *ListOptions) {
		opts.Delimiter.Set(delimiter)
	})
}

// WithStartAfter creates a list option that sets the start after key
func WithStartAfter(startAfter string) ListOptionFunc {
	return ListOptionFunc(func(opts *ListOptions) {
		opts.StartAfter.Set(startAfter)
	})
}

// WithRecursive creates a list option that sets the recursive flag
func WithRecursive(recursive bool) ListOptionFunc {
	return ListOptionFunc(func(opts *ListOptions) {
		opts.Recursive.Set(recursive)
	})
}

// Force creates an object option that forces the operation
func Force() ObjectOptionFunc {
	return ObjectOptionFunc(func(opts *ObjectOptions) {
		opts.Force.Set(true)
	})
}

func Autocommit() ObjectOptionFunc {
	return ObjectOptionFunc(func(opts *ObjectOptions) {
		opts.Autocommit.Set(true)
	})
}

// Helper functions to apply options

func applyPutOptions[T PutOption](opts *OperationOptions, options []T) {
	for _, opt := range options {
		opt.applyPut(opts)
	}
}

func applyGetOptions[T GetOption](opts *OperationOptions, options []T) {
	for _, opt := range options {
		opt.applyGet(opts)
	}
}

func applyDeleteOptions[T DeleteOption](opts *OperationOptions, options []T) {
	for _, opt := range options {
		opt.applyDelete(opts)
	}
}

func applyHeadOptions[T HeadOption](opts *OperationOptions, options []T) {
	for _, opt := range options {
		opt.applyHead(opts)
	}
}

func applyListOptions(opts *ListOptions, options []ListOption) {
	for _, opt := range options {
		opt.applyList(opts)
	}
}

func applyObjectOptions(opts *ObjectOptions, options []ObjectOption) {
	for _, opt := range options {
		opt.applyObject(opts)
	}
}

// OperationOptions methods to apply to other option types
func (o OperationOptions) applyList(opts *ListOptions) {
	// Copy conditional options
	opts.ConditionalOptions = o.ConditionalOptions
	// Copy metadata options
	opts.MetadataOptions = o.MetadataOptions
}
