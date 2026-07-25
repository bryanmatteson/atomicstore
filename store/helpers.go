package store

import (
	"io"
	"sync/atomic"
)

type safeReadCloser struct {
	Reader io.Reader
	Closer io.Closer
	closed atomic.Bool
}

func (s *safeReadCloser) Read(p []byte) (n int, err error) {
	n, err = s.Reader.Read(p)
	if err != nil && err != io.EOF {
		s.Close()
	}
	return
}

func (s *safeReadCloser) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		return s.Closer.Close()
	}
	return nil
}

var conditionalHelper ConditionalHelper

// ConditionalHelper provides common conditional operation logic
type ConditionalHelper struct{}

// wantsIfNoneMatchStar reports whether options require create-if-absent semantics.
func wantsIfNoneMatchStar(opts *ConditionalOptions) bool {
	if opts.IfNoMatch.IsRight() && opts.IfNoMatch.Right() == "*" {
		return true
	}
	return opts.IfNoMatch.IsLeft() && opts.IfNoMatch.Left()
}

// wantsIfMatchExists reports whether options require the object to exist (If-Match: *).
func wantsIfMatchExists(opts *ConditionalOptions) bool {
	return opts.IfMatch.IsLeft() && opts.IfMatch.Left()
}

// ApplyConditionalGet handles conditional logic for Get operations
func (h ConditionalHelper) ApplyConditionalGet(metadata Metadata, opts *ConditionalOptions) error {
	// Handle If-Match: specific etag
	if opts.IfMatch.IsRight() && metadata.ETag != opts.IfMatch.Right() {
		return NewStoreError(ErrCodePreconditionFailed, "Get", "", "ETag doesn't match", nil, false)
	}

	// Handle If-None-Match
	if opts.IfNoMatch.IsRight() && metadata.ETag == opts.IfNoMatch.Right() {
		return NewStoreError(ErrCodeNotModified, "Get", "", "Not modified", nil, false)
	}

	// Handle If-Modified-Since
	if opts.IfModified.IsRight() && !metadata.LastModified.After(opts.IfModified.Right()) {
		return NewStoreError(ErrCodeNotModified, "Get", "", "Not modified since", nil, false)
	}

	// Handle If-Unmodified-Since
	if opts.IfNotModified.IsRight() && metadata.LastModified.After(opts.IfNotModified.Right()) {
		return NewStoreError(ErrCodePreconditionFailed, "Get", "", "Modified since", nil, false)
	}

	return nil
}

// ApplyConditionalPut handles conditional logic for Put operations
func (h ConditionalHelper) ApplyConditionalPut(exists bool, existingMetadata Metadata, opts *OperationOptions) error {
	if wantsIfNoneMatchStar(&opts.ConditionalOptions) && exists {
		return NewStoreError(ErrCodePreconditionFailed, "Put", "", "Object already exists", nil, false)
	}

	// If-Match: * — object must exist
	if wantsIfMatchExists(&opts.ConditionalOptions) && !exists {
		return NewStoreError(ErrCodePreconditionFailed, "Put", "", "Object does not exist", nil, false)
	}

	// If-Match: etag — object must exist and match
	if opts.IfMatch.IsRight() {
		if !exists {
			return NewStoreError(ErrCodePreconditionFailed, "Put", "", "Object does not exist", nil, false)
		}
		if existingMetadata.ETag != opts.IfMatch.Right() {
			return NewStoreError(ErrCodePreconditionFailed, "Put", "", "ETag doesn't match", nil, false)
		}
	}

	// Specific If-None-Match etag (not *)
	if opts.IfNoMatch.IsRight() && opts.IfNoMatch.Right() != "*" && exists {
		if existingMetadata.ETag == opts.IfNoMatch.Right() {
			return NewStoreError(ErrCodePreconditionFailed, "Put", "", "ETag matches If-None-Match", nil, false)
		}
	}

	return nil
}

// ApplyConditionalDelete handles conditional logic for Delete operations
func (h ConditionalHelper) ApplyConditionalDelete(exists bool, existingMetadata Metadata, opts *ConditionalOptions) error {
	if wantsIfMatchExists(opts) && !exists {
		return NewStoreError(ErrCodePreconditionFailed, "Delete", "", "Object does not exist", nil, false)
	}

	if opts.IfMatch.IsRight() {
		if !exists {
			return NewStoreError(ErrCodePreconditionFailed, "Delete", "", "Object does not exist", nil, false)
		}
		if existingMetadata.ETag != opts.IfMatch.Right() {
			return NewStoreError(ErrCodePreconditionFailed, "Delete", "", "ETag doesn't match", nil, false)
		}
	}

	return nil
}

// withStoreErrorKey clones a StoreError with operation/key filled in.
func withStoreErrorKey(err error, operation, key string) error {
	var se *StoreError
	if !AsStoreError(err, &se) {
		return err
	}
	return NewStoreError(se.Code, operation, key, se.Message, se.Cause, se.Retriable)
}
