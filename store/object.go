package store

import (
	"context"
	"sync"
)

const (
	flagLoaded = 1 << iota
	flagModified
)

// Object represents a typed entity in storage that can be manipulated
type Object[T BaseEntity] struct {
	store  *Store[T]
	bucket string
	key    string
	ioMu   sync.Mutex
	mu     sync.Mutex
	state  ObjectState[T]
}

// ObjectState holds the mutable state for an Object
type ObjectState[T BaseEntity] struct {
	entity   T
	metadata Metadata
	flags    uint32
	revision uint64
}

// NewObject creates a new object for a specific entity
func NewObject[T BaseEntity](store *Store[T], bucket string, key string) *Object[T] {
	return &Object[T]{
		store:  store,
		bucket: bucket,
		key:    key,
	}
}

func (o *Object[T]) getState() ObjectState[T] {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

func (o *Object[T]) updateState(fn func(*ObjectState[T])) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fn(&o.state)
}

// Set updates the entity in the object
func (o *Object[T]) Set(entity T) {
	o.updateState(func(s *ObjectState[T]) {
		s.entity = entity
		s.flags |= flagModified
		s.revision++
	})
}

// Get returns the current entity
func (o *Object[T]) Get() T {
	return o.getState().entity
}

// IsLoaded returns true if the entity has been loaded
func (o *Object[T]) IsLoaded() bool {
	return o.getState().flags&flagLoaded != 0
}

// IsModified returns true if the entity has been modified
func (o *Object[T]) IsModified() bool {
	return o.getState().flags&flagModified != 0
}

// GetMetadata returns the current metadata
func (o *Object[T]) GetMetadata() Metadata {
	return o.getState().metadata
}

// Load retrieves the entity from storage
func (o *Object[T]) Load(ctx context.Context, options ...GetOption) error {
	o.ioMu.Lock()
	defer o.ioMu.Unlock()

	startRevision := o.getState().revision
	entity, metadata, err := o.store.Get(ctx, o.key, options...)
	if err != nil {
		return err
	}

	var changed bool
	o.updateState(func(s *ObjectState[T]) {
		if s.revision != startRevision {
			changed = true
			return
		}
		s.entity = entity
		s.metadata = metadata
		s.flags = flagLoaded
		s.revision++
	})
	if changed {
		return NewStoreError(ErrCodePreconditionFailed, "Load", o.key, "object changed while load was in progress", nil, false)
	}

	return nil
}

// Reload refreshes the object from storage.
func (o *Object[T]) Reload(ctx context.Context, options ...GetOption) error {
	return o.Load(ctx, options...)
}

// FetchMetadata loads only metadata without unmarshaling the entity body.
func (o *Object[T]) FetchMetadata(ctx context.Context, options ...HeadOption) error {
	metadata, err := o.store.Head(ctx, o.key, options...)
	if err != nil {
		return err
	}
	o.updateState(func(s *ObjectState[T]) {
		s.metadata = metadata
	})
	return nil
}

// URI returns the full URI for this object
func (o *Object[T]) URI() string {
	return FormatLocationURI(o.store.storage.URIScheme(), o.bucket, o.key)
}

// Key returns the object key
func (o *Object[T]) Key() string {
	return o.key
}

func (o *Object[T]) SetPointer(entity *T) {
	if entity != nil {
		o.Set(*entity)
	} else {
		var zero T
		o.Set(zero)
	}
}

// Exists checks if the object exists in storage
func (o *Object[T]) Exists(ctx context.Context) (bool, error) {
	metadata, err := o.store.Head(ctx, o.key)

	if err == nil {
		o.updateState(func(s *ObjectState[T]) {
			s.metadata = metadata
		})
		return true, nil
	}

	if IsErrorCode(err, ErrCodeNotFound) {
		return false, nil
	}
	return false, err
}

// putOptionsFromObject converts object options into put options.
func putOptionsFromObject(opts *ObjectOptions) []PutOption {
	putOpts := OperationOptions{
		ConditionalOptions: opts.ConditionalOptions,
		MetadataOptions:    opts.MetadataOptions,
	}
	return []PutOption{putOpts}
}

// Save writes the entity to storage
func (o *Object[T]) Save(ctx context.Context, options ...ObjectOption) error {
	o.ioMu.Lock()
	defer o.ioMu.Unlock()

	opts := &ObjectOptions{}
	applyObjectOptions(opts, options)

	state := o.getState()
	modified := state.flags&flagModified != 0

	if !modified &&
		!opts.Force.Or(false) &&
		!opts.IfMatch.IsSet() &&
		!opts.IfNoMatch.IsSet() &&
		!opts.IfModified.IsSet() &&
		!opts.IfNotModified.IsSet() {
		return nil
	}
	if !opts.Force.Or(false) && !opts.IfMatch.IsSet() && !opts.IfNoMatch.IsSet() {
		if state.metadata.ETag != "" {
			opts.IfMatch.SetRight(state.metadata.ETag)
		} else {
			opts.IfNoMatch.SetRight("*")
		}
	}

	res, err := o.store.Put(ctx, o.key, state.entity, putOptionsFromObject(opts)...)
	if err != nil {
		return err
	}

	o.updateState(func(s *ObjectState[T]) {
		if res != nil {
			s.metadata = res.Metadata
		}
		s.flags |= flagLoaded
		if s.revision == state.revision {
			s.flags &^= flagModified
		}
	})
	return nil
}

// Delete removes the object from storage
func (o *Object[T]) Delete(ctx context.Context, options ...DeleteOption) error {
	o.ioMu.Lock()
	defer o.ioMu.Unlock()

	startRevision := o.getState().revision
	err := o.store.Delete(ctx, o.key, options...)
	if err != nil {
		return err
	}

	var zero T
	o.updateState(func(s *ObjectState[T]) {
		s.metadata = Metadata{}
		if s.revision == startRevision {
			s.entity = zero
			s.flags = 0
			s.revision++
		} else {
			s.flags &^= flagLoaded
			s.flags |= flagModified
		}
	})

	return nil
}

// CreateOrUpdate creates the object if it doesn't exist or updates it if it does
func (o *Object[T]) CreateOrUpdate(ctx context.Context, options ...ObjectOption) error {
	exists, err := o.Exists(ctx)
	if err != nil {
		return err
	}

	if exists && !o.IsLoaded() {
		if err := o.Load(ctx); err != nil {
			return err
		}
	}

	o.updateState(func(s *ObjectState[T]) {
		s.flags |= flagModified
	})

	configured := &ObjectOptions{}
	applyObjectOptions(configured, options)
	var saveOptions []ObjectOption
	if !exists {
		if !configured.IfNoMatch.IsSet() && !configured.IfMatch.IsSet() {
			saveOptions = append(saveOptions, IfNotExists())
		}
	} else {
		state := o.getState()
		if !configured.IfNoMatch.IsSet() && !configured.IfMatch.IsSet() && state.metadata.ETag != "" {
			saveOptions = append(saveOptions, IfMatch(state.metadata.ETag))
		}
	}
	saveOptions = append(saveOptions, options...)

	return o.Save(ctx, saveOptions...)
}

// Update applies a function to modify the entity and optionally saves it
func (o *Object[T]) Update(ctx context.Context, fn func(*T), options ...ObjectOption) error {
	if !o.IsLoaded() {
		if err := o.Load(ctx); err != nil {
			return err
		}
	}

	opts := &ObjectOptions{}
	applyObjectOptions(opts, options)

	o.mu.Lock()
	fn(&o.state.entity)
	o.state.flags |= flagModified
	o.state.revision++
	o.mu.Unlock()

	if opts.Autocommit.Or(false) {
		return o.Save(ctx, options...)
	}
	return nil
}
