package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"

	_ "github.com/bryanmatteson/atomicstore/codec/json"
)

// Store provides a typed interface for entity storage
type Store[T BaseEntity] struct {
	storage   Storage
	bucket    string
	codec     codec.Codec
	keyPrefix string
	metrics   MetricsCollector
	tracer    TracingProvider
	logger    Logger
	retrier   Retrier
}

// NewStore creates a new typed store
func NewStore[T BaseEntity](storage Storage, bucket string, options ...StoreOption) (*Store[T], error) {
	// Create default options
	opts := &StoreOptions{}

	// Set required fields
	opts.Storage.Set(storage)
	opts.Bucket.Set(bucket)

	// Apply provided options
	if err := applyStoreOptions(opts, options); err != nil {
		return nil, err
	}

	// Validate required options
	if opts.Storage.Get() == nil {
		return nil, NewStoreError(ErrCodeInvalidOperation, "NewStore", "", "Storage cannot be nil", nil, false)
	}

	if opts.Bucket.Get() == "" {
		return nil, NewStoreError(ErrCodeInvalidBucket, "NewStore", "", "Bucket cannot be empty", nil, false)
	}

	// Create store with configured options
	return &Store[T]{
		storage:   opts.Storage.Get(),
		bucket:    opts.Bucket.Get(),
		codec:     opts.Codec.Get(),
		keyPrefix: opts.KeyPrefix.Or(""),
		metrics:   opts.Metrics.Or(&NoOpMetrics{}),
		tracer:    opts.Tracer.Or(&NoOpTracer{}),
		logger:    opts.Logger.Or(&NoOpLogger{}),
		retrier:   opts.Retrier.Or(NewDefaultRetrier()),
	}, nil
}

// ToURI converts a key to a URI
func (s *Store[T]) ToURI(key string) string {
	// Apply key prefix if configured
	if s.keyPrefix != "" && !strings.HasPrefix(key, s.keyPrefix) {
		key = s.keyPrefix + key
	}

	// Format the full URI
	return FormatLocationURI(s.storage.URIScheme(), s.bucket, key)
}

// FromURI extracts a key from a URI
func (s *Store[T]) FromURI(uri string) (string, error) {
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return "", err
	}

	// Verify this URI belongs to our bucket
	if parsedURI.Bucket != s.bucket {
		return "", fmt.Errorf("URI belongs to different bucket: %s, expected: %s", parsedURI.Bucket, s.bucket)
	}

	// Strip prefix if present
	key := parsedURI.Key
	if s.keyPrefix != "" && strings.HasPrefix(key, s.keyPrefix) {
		key = strings.TrimPrefix(key, s.keyPrefix)
	}

	return key, nil
}

// Get retrieves an entity by key
func (s *Store[T]) Get(ctx context.Context, key string, options ...GetOption) (T, Metadata, error) {
	span := s.tracer.StartSpan(ctx, "Store.Get")
	defer span.End()

	var result T
	var metadata Metadata

	// Create the full URI
	uri := s.ToURI(key)

	// Execute operation with retry
	err := s.retrier.Do(ctx, func() error {
		// Get raw data from storage
		data, meta, err := s.storage.Get(ctx, uri, options...)
		if err != nil {
			return err
		}

		// Unmarshal data into entity
		err = s.codec.Unmarshal(data, &result)
		if err != nil {
			return NewStoreError(ErrCodeInvalidData, "Get", key, "Failed to unmarshal entity", err, false)
		}

		metadata = meta
		return nil
	})

	if err != nil {
		var zero T
		return zero, Metadata{}, err
	}

	return result, metadata, nil
}

type PutResult struct {
	Metadata Metadata
}

// Put stores an entity by key
func (s *Store[T]) Put(ctx context.Context, key string, entity T, options ...PutOption) (*PutResult, error) {
	// Create a properly contextualized span
	span := s.tracer.StartSpan(ctx, "Store.Put")
	defer span.End()
	span.AddTag("key", key)
	span.AddTag("entity_type", fmt.Sprintf("%T", entity))

	// Log the operation
	s.logger.Log(LogLevelDebug, "Starting Put operation", map[string]interface{}{
		"key":         key,
		"entity_type": fmt.Sprintf("%T", entity),
		"has_options": len(options) > 0,
	})

	// Create the full URI
	uri := s.ToURI(key)

	// Marshal entity to bytes
	data, err := s.codec.Marshal(entity)
	if err != nil {
		marshalErr := NewStoreError(
			ErrCodeInvalidData,
			"Put",
			key,
			fmt.Sprintf("Failed to marshal entity of type %T", entity),
			err,
			false,
		)

		// Log the error
		s.logger.Log(LogLevelError, "Entity marshaling failed", map[string]interface{}{
			"key":         key,
			"entity_type": fmt.Sprintf("%T", entity),
			"error":       marshalErr.Error(),
		})

		// Record metrics
		s.metrics.RecordError("store.put.marshal_error", marshalErr)

		// Add error to span
		span.AddTag("error", marshalErr.Error())

		return nil, marshalErr
	}

	// Add content type option
	allOptions := append([]PutOption{WithContentType(s.codec.ContentType())}, options...)

	ent := entity.GetEntity()
	// Add version metadata if entity supports it
	version := ent.Version
	allOptions = append(allOptions, WithMetadata("version", fmt.Sprintf("%d", version)))
	span.AddTag("entity_version", fmt.Sprintf("%d", version))

	var result PutResult
	// Execute operation with retry
	retryErr := s.retrier.Do(ctx, func() error {
		start := time.Now()

		meta, err := s.storage.Put(ctx, uri, data, allOptions...)

		// Record metrics even on failure
		s.metrics.RecordDuration("store.put.duration", time.Since(start))
		s.metrics.RecordSize("store.put.size", int64(len(data)))

		if err != nil {
			// Add context to error
			s.logger.Log(LogLevelError, "Storage Put failed", map[string]interface{}{
				"key":   key,
				"uri":   uri,
				"error": err.Error(),
				"retry": IsRetriable(err),
			})

			return err
		}

		// Store metadata
		result.Metadata = meta

		// Log success
		s.logger.Log(LogLevelDebug, "Storage Put succeeded", map[string]interface{}{
			"key":  key,
			"uri":  uri,
			"etag": meta.ETag,
			"size": meta.Size,
		})

		return nil
	})

	if retryErr != nil {
		// Add error to span
		span.AddTag("error", retryErr.Error())
		span.AddTag("error_code", GetErrorCode(retryErr))

		// Log final error after all retries
		s.logger.Log(LogLevelError, "All retries failed for Put operation", map[string]interface{}{
			"key":   key,
			"uri":   uri,
			"error": retryErr.Error(),
		})

		return nil, retryErr
	}

	return &result, nil
}

// Add this helper function to extract error codes consistently
func GetErrorCode(err error) string {
	var storeErr *StoreError
	if AsStoreError(err, &storeErr) {
		return storeErr.Code
	}
	return "Unknown"
}

// Create adds a new entity, failing if it already exists
func (s *Store[T]) Create(ctx context.Context, key string, entity T, options ...PutOption) (Metadata, error) {
	span := s.tracer.StartSpan(ctx, "Store.Create")
	defer span.End()

	// Create the full URI
	uri := s.ToURI(key)

	// Marshal entity to bytes
	data, err := s.codec.Marshal(entity)
	if err != nil {
		return Metadata{}, NewStoreError(ErrCodeInvalidData, "Create", key, "Failed to marshal entity", err, false)
	}

	opts := &OperationOptions{}
	applyPutOptions(opts, options)
	opts.ContentType.Set(s.codec.ContentType())
	opts.IfNoMatch.SetRight("*")

	ent := entity.GetEntity()
	version := ent.Version
	opts.Metadata.SetKey("version", strconv.Itoa(int(version)))

	var metadata Metadata

	// Execute operation with retry
	err = s.retrier.Do(ctx, func() error {
		meta, err := s.storage.Put(ctx, uri, data, opts)
		if err != nil {
			return err
		}
		metadata = meta
		return nil
	})

	if err != nil {
		return Metadata{}, err
	}

	return metadata, nil
}

// Delete removes an entity by key
func (s *Store[T]) Delete(ctx context.Context, key string, options ...DeleteOption) error {
	span := s.tracer.StartSpan(ctx, "Store.Delete")
	defer span.End()

	// Create the full URI
	uri := s.ToURI(key)

	// Execute operation with retry
	return s.retrier.Do(ctx, func() error {
		return s.storage.Delete(ctx, uri, options...)
	})
}

// Head retrieves metadata for an entity
func (s *Store[T]) Head(ctx context.Context, key string, options ...HeadOption) (Metadata, error) {
	span := s.tracer.StartSpan(ctx, "Store.Head")
	defer span.End()

	// Create the full URI
	uri := s.ToURI(key)

	var metadata Metadata

	// Execute operation with retry
	err := s.retrier.Do(ctx, func() error {
		meta, err := s.storage.Head(ctx, uri, options...)
		if err != nil {
			return err
		}
		metadata = meta
		return nil
	})

	if err != nil {
		return Metadata{}, err
	}

	return metadata, nil
}

// List returns entities matching the specified criteria
func (s *Store[T]) List(ctx context.Context, options ...ListOption) ([]Entry, error) {
	span := s.tracer.StartSpan(ctx, "Store.List")
	defer span.End()

	// Create URI for listing (no key, just scheme and bucket)
	uri := FormatLocationURI(s.storage.URIScheme(), s.bucket, "")

	opts := &ListOptions{}
	applyListOptions(opts, options)

	if s.keyPrefix != "" {
		opts.Prefix.Set(s.keyPrefix)
	}

	var entries []Entry

	// Execute operation with retry
	err := s.retrier.Do(ctx, func() error {
		results, err := s.storage.List(ctx, uri, opts)
		if err != nil {
			return err
		}
		entries = results
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Strip store's key prefix from keys if needed
	if s.keyPrefix != "" {
		for i := range entries {
			entries[i].Key = strings.TrimPrefix(entries[i].Key, s.keyPrefix)
		}
	}

	return entries, nil
}

// GetMany retrieves multiple entities by keys
func (s *Store[T]) GetMany(ctx context.Context, keys []string, options ...GetOption) (map[string]T, map[string]error) {
	span := s.tracer.StartSpan(ctx, "Store.GetMany")
	defer span.End()

	results := make(map[string]T)
	errors := make(map[string]error)

	// Retrieve each entity
	for _, key := range keys {
		entity, _, err := s.Get(ctx, key, options...)
		if err != nil {
			errors[key] = err
			continue
		}
		results[key] = entity
	}

	return results, errors
}

// BeginTransaction starts a new transaction
func (s *Store[T]) BeginTransaction(ctx context.Context, options ...TransactionOption) (Transaction, error) {
	span := s.tracer.StartSpan(ctx, "Store.BeginTransaction")
	defer span.End()

	// Create a new transaction
	return NewStorageTransaction(ctx, s.storage, options...), nil
}

// WithTransaction returns a typed view for working with transactions
func (s *Store[T]) WithTransaction(tx Transaction) *TypedTransactionView[T] {
	return &TypedTransactionView[T]{
		store: s,
		tx:    tx,
	}
}

// New creates an Object wrapper for an entity
func (s *Store[T]) New(key string) *Object[T] {
	return NewObject(s, s.bucket, key)
}
