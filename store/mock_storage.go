package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"
)

// MockObject represents a stored object in the mock storage
type MockObject struct {
	Data       []byte
	Metadata   Metadata
	LastAccess time.Time
}

// MockStorage implements the Storage interface for testing
type MockStorage struct {
	scheme        string
	objects       map[string]*MockObject
	mutex         sync.RWMutex
	failNext      bool
	failCode      string
	failUri       string
	failRetriable bool
}

// NewMockStorage creates a new mock storage instance
func NewMockStorage(scheme string) *MockStorage {
	return &MockStorage{
		scheme:  scheme,
		objects: make(map[string]*MockObject),
	}
}

// URIScheme returns the URI scheme for this storage
func (m *MockStorage) URIScheme() string {
	return m.scheme
}

func (m *MockStorage) HasLinearizableConditions() bool {
	return true
}

// SetFailNext makes the next operation fail with the given error code
func (m *MockStorage) SetFailNext(uri, code string, retriable bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.failUri = uri
	m.failCode = code
	m.failRetriable = retriable
	m.failNext = true
}

// checkFailNext checks if the operation should fail and resets the flag
func (m *MockStorage) checkFailNext(operation, uri string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.failNext && (m.failUri == "" || m.failUri == uri) {
		m.failNext = false
		m.failUri = ""
		return NewStoreError(m.failCode, operation, uri, "Mock storage error", nil, m.failRetriable)
	}

	return nil
}

// Get retrieves an object
func (m *MockStorage) Get(ctx context.Context, uri string, options ...GetOption) ([]byte, Metadata, error) {
	if err := m.checkFailNext("Get", uri); err != nil {
		return nil, Metadata{}, err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Apply options
	opts := &OperationOptions{}
	applyGetOptions(opts, options)

	// Get the object
	obj, exists := m.objects[uri]
	if !exists {
		return nil, Metadata{}, NewStoreError(ErrCodeNotFound, "Get", uri, "Object not found", nil, false)
	}

	// Update access time
	obj.LastAccess = time.Now()

	// Apply conditional options
	if opts.IfMatch.IsRight() && opts.IfMatch.Right() != obj.Metadata.ETag {
		return nil, obj.Metadata, NewStoreError(ErrCodePreconditionFailed, "Get", uri, "ETag doesn't match", nil, false)
	}

	if opts.IfNoMatch.IsRight() && opts.IfNoMatch.Right() == obj.Metadata.ETag {
		return nil, obj.Metadata, NewStoreError(ErrCodeNotModified, "Get", uri, "Not modified", nil, false)
	}

	if opts.IfModified.IsRight() && !obj.Metadata.LastModified.After(opts.IfModified.Right()) {
		return nil, obj.Metadata, NewStoreError(ErrCodeNotModified, "Get", uri, "Not modified since", nil, false)
	}

	if opts.IfNotModified.IsRight() && obj.Metadata.LastModified.After(opts.IfNotModified.Right()) {
		return nil, obj.Metadata, NewStoreError(ErrCodePreconditionFailed, "Get", uri, "Modified since", nil, false)
	}

	metadata := obj.Metadata
	metadata.UserMetadata = maps.Clone(obj.Metadata.UserMetadata)
	return append([]byte(nil), obj.Data...), metadata, nil
}

// GetStream returns an object as a stream
func (m *MockStorage) GetStream(ctx context.Context, uri string, options ...GetOption) (io.ReadCloser, Metadata, error) {
	if err := m.checkFailNext("GetStream", uri); err != nil {
		return nil, Metadata{}, err
	}

	// Get the data
	data, metadata, err := m.Get(ctx, uri, options...)
	if err != nil {
		return nil, Metadata{}, err
	}

	// Create a reader from the data
	return io.NopCloser(strings.NewReader(string(data))), metadata, nil
}

// Put stores an object
func (m *MockStorage) Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error) {
	if err := m.checkFailNext("Put", uri); err != nil {
		return Metadata{}, err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Apply options
	opts := &OperationOptions{}
	applyPutOptions(opts, options)

	// Get the object for conditional operations
	existingObj, exists := m.objects[uri]
	var existingMetadata Metadata
	if exists {
		existingMetadata = existingObj.Metadata
	}

	if err := conditionalHelper.ApplyConditionalPut(exists, existingMetadata, opts); err != nil {
		return Metadata{}, withStoreErrorKey(err, "Put", uri)
	}

	// Calculate ETag
	hash := md5.Sum(data)
	etag := hex.EncodeToString(hash[:])
	now := time.Now()

	// Create metadata
	metadata := Metadata{
		ETag:         etag,
		LastModified: now,
		Size:         int64(len(data)),
		ContentType:  opts.ContentType.Or("application/octet-stream"),
		UserMetadata: make(map[string]string),
	}

	// Apply metadata options
	if opts.ContentEncoding.IsSet() {
		metadata.ContentEncoding = opts.ContentEncoding.Get()
	}

	if opts.StorageClass.IsSet() {
		metadata.StorageClass = opts.StorageClass.Get()
	}

	if opts.Metadata.IsSet() {
		maps.Copy(metadata.UserMetadata, opts.Metadata.Get())
	}

	// Store the object
	m.objects[uri] = &MockObject{
		Data:       data,
		Metadata:   metadata,
		LastAccess: now,
	}

	return metadata, nil
}

// PutStream stores a stream
func (m *MockStorage) PutStream(ctx context.Context, uri string, reader io.Reader, options ...PutOption) (Metadata, error) {
	if err := m.checkFailNext("PutStream", uri); err != nil {
		return Metadata{}, err
	}

	// Read the stream into a byte slice
	data, err := io.ReadAll(reader)
	if err != nil {
		return Metadata{}, NewStoreError(ErrCodeIO, "PutStream", uri, "Failed to read stream", err, true)
	}

	// Delegate to Put
	return m.Put(ctx, uri, data, options...)
}

// Delete removes an object
func (m *MockStorage) Delete(ctx context.Context, uri string, options ...DeleteOption) error {
	if err := m.checkFailNext("Delete", uri); err != nil {
		return err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Apply options
	opts := &OperationOptions{}
	applyDeleteOptions(opts, options)

	// Check if object exists
	obj, exists := m.objects[uri]
	var existingMetadata Metadata
	if exists {
		existingMetadata = obj.Metadata
	}

	if err := conditionalHelper.ApplyConditionalDelete(exists, existingMetadata, &opts.ConditionalOptions); err != nil {
		return withStoreErrorKey(err, "Delete", uri)
	}

	if !exists {
		return NewStoreError(ErrCodeNotFound, "Delete", uri, "Object not found", nil, false)
	}

	// Delete the object
	delete(m.objects, uri)

	return nil
}

// ApplyAtomicBatch applies all mutations under one mutex and publishes the
// resulting map only after every condition has passed.
func (m *MockStorage) ApplyAtomicBatch(ctx context.Context, operations []AtomicBatchOperation) error {
	for _, operation := range operations {
		if err := m.checkFailNext("AtomicBatch", operation.URI); err != nil {
			return err
		}
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	next := make(map[string]*MockObject, len(m.objects))
	for uri, object := range m.objects {
		next[uri] = &MockObject{
			Data:       append([]byte(nil), object.Data...),
			Metadata:   object.Metadata,
			LastAccess: object.LastAccess,
		}
		next[uri].Metadata.UserMetadata = maps.Clone(object.Metadata.UserMetadata)
	}

	for _, operation := range operations {
		object, exists := next[operation.URI]
		var existingMetadata Metadata
		if exists {
			existingMetadata = object.Metadata
		}

		switch operation.Type {
		case "put":
			if err := conditionalHelper.ApplyConditionalPut(exists, existingMetadata, &operation.Options); err != nil {
				return withStoreErrorKey(err, "AtomicBatch", operation.URI)
			}
			hash := md5.Sum(operation.Data)
			now := time.Now()
			metadata := Metadata{
				ETag:            hex.EncodeToString(hash[:]),
				LastModified:    now,
				Size:            int64(len(operation.Data)),
				ContentType:     operation.Options.ContentType.Or("application/octet-stream"),
				ContentEncoding: operation.Options.ContentEncoding.Or(""),
				StorageClass:    operation.Options.StorageClass.Or(""),
				UserMetadata:    operation.Options.Metadata.Cloned(),
			}
			next[operation.URI] = &MockObject{
				Data:       append([]byte(nil), operation.Data...),
				Metadata:   metadata,
				LastAccess: now,
			}
		case "delete":
			if err := conditionalHelper.ApplyConditionalDelete(exists, existingMetadata, &operation.Options.ConditionalOptions); err != nil {
				return withStoreErrorKey(err, "AtomicBatch", operation.URI)
			}
			if !exists {
				return NewStoreError(ErrCodeNotFound, "AtomicBatch", operation.URI, "Object not found", nil, false)
			}
			delete(next, operation.URI)
		default:
			return NewStoreError(ErrCodeInvalidOperation, "AtomicBatch", operation.URI, "Unknown batch operation", nil, false)
		}
	}

	m.objects = next
	return nil
}

// Head retrieves metadata for an object
func (m *MockStorage) Head(ctx context.Context, uri string, options ...HeadOption) (Metadata, error) {
	if err := m.checkFailNext("Head", uri); err != nil {
		return Metadata{}, err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Apply options
	opts := &OperationOptions{}
	applyHeadOptions(opts, options)

	// Get the object
	obj, exists := m.objects[uri]
	if !exists {
		return Metadata{}, NewStoreError(ErrCodeNotFound, "Head", uri, "Object not found", nil, false)
	}

	// Update access time
	obj.LastAccess = time.Now()

	// Apply conditional options
	if opts.IfMatch.IsRight() && opts.IfMatch.Right() != obj.Metadata.ETag {
		return Metadata{}, NewStoreError(ErrCodePreconditionFailed, "Head", uri, "ETag doesn't match", nil, false)
	}

	if opts.IfNoMatch.IsRight() && opts.IfNoMatch.Right() == obj.Metadata.ETag {
		return Metadata{}, NewStoreError(ErrCodeNotModified, "Head", uri, "Not modified", nil, false)
	}

	metadata := obj.Metadata
	metadata.UserMetadata = maps.Clone(obj.Metadata.UserMetadata)
	return metadata, nil
}

// List returns objects matching specified criteria
func (m *MockStorage) List(ctx context.Context, uri string, options ...ListOption) ([]Entry, error) {
	if err := m.checkFailNext("List", uri); err != nil {
		return nil, err
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, NewInvalidURIError("List", uri, err)
	}

	// Apply options
	opts := &ListOptions{}
	applyListOptions(opts, options)

	// Build base prefix for matching
	basePrefix := fmt.Sprintf("%s://%s/", parsedURI.Scheme, parsedURI.Bucket)

	// Add any prefix from the URI key or options
	prefix := ""
	if parsedURI.Key != "" {
		prefix = parsedURI.Key
		// Only add trailing slash if not already present and not empty
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
	}
	if opts.Prefix.IsSet() {
		prefix += opts.Prefix.Get()
	}

	// Create full match prefix for filtering objects
	matchPrefix := basePrefix + prefix

	// Filter objects by prefix
	var entries []Entry
	var commonPrefixes = make(map[string]bool)

	// Process only if delimiter is explicitly set
	useDelimiter := !opts.Recursive.Or(false) && opts.Delimiter.IsSet()

	for objURI, obj := range m.objects {
		// Skip objects that don't match our prefix
		if !strings.HasPrefix(objURI, matchPrefix) {
			continue
		}

		// Get the key relative to the base prefix (not match prefix)
		relKey := strings.TrimPrefix(objURI, basePrefix)

		// Handle delimiter if specified and we're not in recursive mode
		if useDelimiter {
			delimiter := opts.Delimiter.Get()

			// Check for delimiter after the prefix
			relToPrefix := strings.TrimPrefix(relKey, prefix)
			delimIndex := strings.Index(relToPrefix, delimiter)

			if delimIndex >= 0 {
				// This key contains the delimiter after the prefix
				commonPrefix := prefix + relToPrefix[:delimIndex+len(delimiter)]
				commonPrefixes[commonPrefix] = true
				continue // Skip this object, it will be represented by a prefix
			}
		}

		// Add entry
		entry := Entry{
			Key: relKey,
			Metadata: Metadata{
				ETag:            obj.Metadata.ETag,
				LastModified:    obj.Metadata.LastModified,
				Size:            obj.Metadata.Size,
				ContentType:     obj.Metadata.ContentType,
				ContentEncoding: obj.Metadata.ContentEncoding,
				StorageClass:    obj.Metadata.StorageClass,
				UserMetadata:    obj.Metadata.UserMetadata,
				VersionID:       obj.Metadata.VersionID,
			},
		}

		// Include metadata if requested
		if opts.IncludeMetadata.Or(false) {
			entry.Metadata = obj.Metadata
		}

		entries = append(entries, entry)
	}

	// Add common prefixes as entries
	for commonPrefix := range commonPrefixes {
		entries = append(entries, Entry{
			Key:      commonPrefix,
			IsPrefix: true,
		})
	}

	// Sort entries by key
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})

	// Apply max keys limit
	if opts.MaxKeys.IsSet() && len(entries) > opts.MaxKeys.Get() {
		entries = entries[:opts.MaxKeys.Get()]
	}

	return entries, nil
}

// ObjectExists checks if an object exists in the mock storage
func (m *MockStorage) ObjectExists(uri string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	_, exists := m.objects[uri]
	return exists
}

// AddTestObject adds an object to the mock storage for testing
func (m *MockStorage) AddTestObject(uri string, data []byte, contentType string, metadata map[string]string) {
	hash := md5.Sum(data)
	etag := hex.EncodeToString(hash[:])
	now := time.Now()

	meta := Metadata{
		ETag:         etag,
		LastModified: now,
		Size:         int64(len(data)),
		ContentType:  contentType,
		UserMetadata: metadata,
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.objects[uri] = &MockObject{
		Data:       data,
		Metadata:   meta,
		LastAccess: now,
	}
}

// ClearObjects removes all objects from the mock storage
func (m *MockStorage) ClearObjects() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.objects = make(map[string]*MockObject)
}

// GetObjectCount returns the number of objects in storage
func (m *MockStorage) GetObjectCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.objects)
}

// MockStorageFactory creates a MockStorage from a URI
func MockStorageFactory(ctx context.Context, uri StorageURI) (Storage, error) {
	return NewMockStorage(uri.Scheme), nil
}

func init() {
	// Register the mock URI scheme
	RegisterURIScheme("mock", MockStorageFactory)
}
