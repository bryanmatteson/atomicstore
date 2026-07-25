package store

import (
	"context"
	"sync"
	"testing"

	"github.com/bryanmatteson/atomicstore/codec"
)

type blockingPutStorage struct {
	*MockStorage
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingPutStorage) Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return Metadata{}, ctx.Err()
	case <-b.release:
	}
	return b.MockStorage.Put(ctx, uri, data, options...)
}

func TestObject_BasicOperations(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()
	const key = "test-object"

	// Create an object
	obj := store.New(key)

	// Initially, object should not be loaded
	if obj.IsLoaded() {
		t.Error("New object should not be loaded")
	}

	// Set initial data
	entity := NewTestEntity("test-name", 42)
	obj.SetPointer(entity)

	// Save the object
	err = obj.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save object: %v", err)
	}

	// Verify the object exists in storage
	if !mockStorage.ObjectExists(obj.URI()) {
		t.Error("Object not found in storage after Save")
	}

	// Create a new object instance pointing to the same key
	obj2 := store.New(key)

	// Load the object
	err = obj2.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load object: %v", err)
	}

	// Check loaded state
	if !obj2.IsLoaded() {
		t.Error("Object should be marked as loaded after Load()")
	}

	// Verify data
	loadedEntity := obj2.Get()
	if loadedEntity.Name != "test-name" || loadedEntity.Value != 42 {
		t.Errorf("Loaded object has wrong data: name=%s, value=%d",
			loadedEntity.Name, loadedEntity.Value)
	}

	// Modify and update the object
	loadedEntity.Name = "updated-name"
	loadedEntity.Value = 100
	obj2.Set(loadedEntity)

	// Save the changes
	err = obj2.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save updated object: %v", err)
	}

	// Verify original object instance doesn't see the changes
	originalEntity := obj.Get()
	if originalEntity.Name == "updated-name" || originalEntity.Value == 100 {
		t.Error("Original object instance shouldn't see changes without reload")
	}

	// Reload the original object
	err = obj.Reload(ctx)
	if err != nil {
		t.Fatalf("Failed to reload object: %v", err)
	}

	// Now the original should see the changes
	reloadedEntity := obj.Get()
	if reloadedEntity.Name != "updated-name" || reloadedEntity.Value != 100 {
		t.Errorf("Reloaded object has wrong data: name=%s, value=%d",
			reloadedEntity.Name, reloadedEntity.Value)
	}

	// Delete the object
	err = obj.Delete(ctx)
	if err != nil {
		t.Fatalf("Failed to delete object: %v", err)
	}

	// Verify it's gone from storage
	if mockStorage.ObjectExists(obj.URI()) {
		t.Error("Object still exists after Delete")
	}
}

func TestObject_ConditionalOperations(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()
	const key = "test-object"

	// Create and save an object
	obj := store.New(key)
	entity := NewTestEntity("original", 42)
	obj.SetPointer(entity)
	err = obj.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save object: %v", err)
	}

	// Get the metadata
	metadata := obj.GetMetadata()
	if metadata.ETag == "" {
		t.Fatal("ETag is empty")
	}

	// Create a new object instance
	obj2 := store.New(key)
	err = obj2.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load object: %v", err)
	}

	// Modify both objects
	entity1 := obj.Get()
	entity1.Name = "changed-by-obj1"
	obj.Set(entity1)

	entity2 := obj2.Get()
	entity2.Name = "changed-by-obj2"
	obj2.Set(entity2)

	// Save the first object
	err = obj.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save first object: %v", err)
	}

	// Save the second object without an explicit condition. Save should use the
	// ETag captured by Load and reject the stale update.
	err = obj2.Save(ctx)
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Expected PreconditionFailed when saving a stale object, got: %v", err)
	}

	// Save with Force option (should succeed)
	err = obj2.Save(ctx, ObjectOptionFunc(func(opts *ObjectOptions) {
		opts.Force.Set(true)
	}))
	if err != nil {
		t.Errorf("Save with Force option should succeed: %v", err)
	}

	// Create a new object
	objNew := store.New("new-object")
	objNew.SetPointer(NewTestEntity("new", 100))

	// Save with If-None-Match=*
	err = objNew.Save(ctx, ObjectOptionFunc(func(opts *ObjectOptions) {
		opts.IfNoMatch.SetRight("*")
	}))
	if err != nil {
		t.Errorf("Save with If-None-Match=* on new object should succeed: %v", err)
	}

	// Try again (should fail)
	err = objNew.Save(ctx, ObjectOptionFunc(func(opts *ObjectOptions) {
		opts.IfNoMatch.SetRight("*")
	}))
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Expected PreconditionFailed for second save with If-None-Match=*, got: %v", err)
	}
}

func TestObjectSavePreservesConcurrentModification(t *testing.T) {
	storage := &blockingPutStorage{
		MockStorage: NewMockStorage("mock"),
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	jsonCodec, err := codec.Get("json")
	AssertNoError(t, err)
	entityStore, err := NewStore[TestEntity](storage, "test-bucket", WithCodec(jsonCodec))
	AssertNoError(t, err)

	obj := entityStore.New("concurrent-save")
	obj.Set(*NewTestEntity("snapshot", 1))
	done := make(chan error, 1)
	go func() {
		done <- obj.Save(context.Background())
	}()

	<-storage.started
	obj.Set(*NewTestEntity("newer", 2))
	close(storage.release)
	AssertNoError(t, <-done)

	AssertTrue(t, obj.IsModified(), "concurrent change must remain dirty")
	AssertEqual(t, "newer", obj.Get().Name)
	persisted, _, err := entityStore.Get(context.Background(), "concurrent-save")
	AssertNoError(t, err)
	AssertEqual(t, "snapshot", persisted.Name)
}

func TestObject_Exists(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()

	// Test with non-existent object
	obj1 := store.New("nonexistent")
	exists, err := obj1.Exists(ctx)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if exists {
		t.Error("Nonexistent object should return exists=false")
	}

	// Create an object
	obj2 := store.New("test-object")
	obj2.SetPointer(NewTestEntity("test", 42))
	err = obj2.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save object: %v", err)
	}

	// Check exists for the created object
	exists, err = obj2.Exists(ctx)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Error("Existing object should return exists=true")
	}

	// Create another reference to the same object
	obj3 := store.New("test-object")
	exists, err = obj3.Exists(ctx)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Error("Existing object should return exists=true with new reference")
	}

	// The metadata should be loaded
	metadata := obj3.GetMetadata()
	if metadata.ETag == "" {
		t.Error("Metadata ETag should be populated after Exists call")
	}
}

func TestObject_CreateOrUpdate(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()

	// Test CreateOrUpdate on new object
	obj1 := store.New("new-object")
	obj1.SetPointer(NewTestEntity("new", 100))
	err = obj1.CreateOrUpdate(ctx)
	if err != nil {
		t.Fatalf("CreateOrUpdate failed on new object: %v", err)
	}

	// Verify object was created
	exists, err := obj1.Exists(ctx)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Error("Object should exist after CreateOrUpdate")
	}

	// Test CreateOrUpdate on existing object

	obj1.Update(ctx, func(entity *TestEntity) {
		entity.Value = 200
	})
	err = obj1.CreateOrUpdate(ctx)
	if err != nil {
		t.Fatalf("CreateOrUpdate failed on existing object: %v", err)
	}

	// Verify the update happened
	obj2 := store.New("new-object")
	err = obj2.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load object: %v", err)
	}

	if obj2.Get().Value != 200 {
		t.Errorf("Update via CreateOrUpdate didn't work, value = %d", obj2.Get().Value)
	}
}

func TestObject_FetchMetadata(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()

	// Create an object
	obj := store.New("test-object")
	obj.SetPointer(NewTestEntity("test", 42))
	err = obj.Save(ctx)
	if err != nil {
		t.Fatalf("Failed to save object: %v", err)
	}

	// Create new reference
	obj2 := store.New("test-object")

	// FetchMetadata should only get metadata, not the full object
	err = obj2.FetchMetadata(ctx)
	if err != nil {
		t.Fatalf("FetchMetadata failed: %v", err)
	}

	// Metadata should be loaded
	meta := obj2.GetMetadata()
	if meta.ETag == "" {
		t.Error("ETag should be populated after FetchMetadata")
	}

	// Object should not be loaded
	if obj2.IsLoaded() {
		t.Error("Object should not be marked as loaded after FetchMetadata")
	}

	// Entity should be empty
	entity := obj2.Get()
	if entity.ID != "" || entity.Name != "" || entity.Value != 0 {
		t.Error("Entity should be empty after FetchMetadata")
	}
}
