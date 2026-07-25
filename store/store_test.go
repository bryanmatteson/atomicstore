package store

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bryanmatteson/atomicstore/codec"
)

func TestStore_NewStore(t *testing.T) {
	// Test with valid options
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	store, err := NewStore[TestEntity](mockStorage, "test-bucket", WithCodec(jsonCodec))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if store == nil {
		t.Fatal("Store is nil")
	}

	// Test with nil storage
	_, err = NewStore[TestEntity](nil, "test-bucket")
	if err == nil {
		t.Fatal("Expected error when creating store with nil storage")
	}
	if !IsErrorCode(err, ErrCodeInvalidOperation) {
		t.Errorf("Expected InvalidOperation error, got: %v", err)
	}

	// Test with empty bucket
	_, err = NewStore[TestEntity](mockStorage, "")
	if err == nil {
		t.Fatal("Expected error when creating store with empty bucket")
	}
	if !IsErrorCode(err, ErrCodeInvalidBucket) {
		t.Errorf("Expected InvalidBucket error, got: %v", err)
	}
}

func TestStore_URI_Conversion(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	// Test with key prefix
	store, err := NewStore[TestEntity](
		mockStorage,
		"test-bucket",
		WithCodec(jsonCodec),
		WithKeyPrefix("prefix/"),
	)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test ToURI
	uri := store.ToURI("testkey")
	expected := "mock://test-bucket/prefix/testkey"
	if uri != expected {
		t.Errorf("ToURI() = %q, want %q", uri, expected)
	}

	// Test FromURI with correct URI
	key, err := store.FromURI(uri)
	if err != nil {
		t.Fatalf("FromURI() failed: %v", err)
	}
	if key != "testkey" {
		t.Errorf("FromURI() = %q, want %q", key, "testkey")
	}

	// Test FromURI with wrong bucket
	_, err = store.FromURI("mock://wrong-bucket/prefix/testkey")
	if err == nil {
		t.Fatal("FromURI() with wrong bucket should fail")
	}

	// Test FromURI with invalid URI
	_, err = store.FromURI("invalid:uri")
	if err == nil {
		t.Fatal("FromURI() with invalid URI should fail")
	}
}

func TestStore_BasicOperations(t *testing.T) {
	testStore, _ := SetupTestStore(t)
	ctx := context.Background()

	// Test Create and Get
	entity := NewTestEntity("test-name", 42)
	entity.Version = 1
	key := "test-key"

	// Put the entity
	_, err := testStore.Put(ctx, key, *entity)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get the entity back
	retrieved, metadata, err := testStore.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify retrieved entity
	if retrieved.Name != entity.Name || retrieved.Value != entity.Value {
		t.Errorf("Retrieved entity doesn't match: got %v, want %v", retrieved, entity)
	}

	// Verify metadata
	if metadata.ETag == "" {
		t.Error("Metadata ETag is empty")
	}
	if metadata.ContentType != "application/json" {
		t.Errorf("Metadata ContentType = %q, want 'application/json'", metadata.ContentType)
	}

	// Test Get with non-existent key
	_, _, err = testStore.Get(ctx, "non-existent")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Expected NotFound for non-existent key, got: %v", err)
	}

	// Test Delete
	err = testStore.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify entity is gone
	_, _, err = testStore.Get(ctx, key)
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Expected NotFound after Delete, got: %v", err)
	}
}

func TestStore_ConditionalOperations(t *testing.T) {
	testStore, _ := SetupTestStore(t)
	ctx := context.Background()

	// Create initial entity
	entity := NewTestEntity("initial", 100)
	entity.Version = 1
	key := "conditional-key"

	// Put the entity
	_, err := testStore.Put(ctx, key, *entity)
	if err != nil {
		t.Fatalf("Initial put failed: %v", err)
	}

	// Get the ETag
	metadata, err := testStore.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head failed: %v", err)
	}
	etag := metadata.ETag

	// Test conditional update with correct ETag
	updatedEntity := NewTestEntity("updated", 200)
	updatedEntity.Version = 2
	_, err = testStore.Put(ctx, key, *updatedEntity, IfMatch(etag))
	if err != nil {
		t.Errorf("Put with correct ETag should succeed: %v", err)
	}

	// Get the new ETag
	metadata, err = testStore.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head after update failed: %v", err)
	}

	// Test conditional update with wrong ETag (should fail)
	failEntity := NewTestEntity("should-fail", 300)
	failEntity.Version = 3
	_, err = testStore.Put(ctx, key, *failEntity, IfMatch(etag)) // Using old ETag
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Put with incorrect ETag should fail with PreconditionFailed, got: %v", err)
	}

	// Test If-None-Match for new key
	newKey := "new-conditional-key"
	newEntity := NewTestEntity("new", 400)
	newEntity.Version = 1
	_, err = testStore.Put(ctx, newKey, *newEntity, IfNoneMatch("*"))
	if err != nil {
		t.Errorf("Put with If-None-Match=* on new key should succeed: %v", err)
	}

	// Test If-None-Match for existing key (should fail)
	_, err = testStore.Put(ctx, key, *failEntity, IfNoneMatch("*"))
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Put with If-None-Match=* on existing key should fail, got: %v", err)
	}

	// Test If-None-Match with specific ETag
	updatedEntity2 := NewTestEntity("updated-again", 500)
	updatedEntity2.Version = 3
	_, err = testStore.Put(ctx, key, *updatedEntity2, IfNoneMatch(etag)) // Old ETag, so should pass
	if err != nil {
		t.Errorf("Put with If-None-Match and non-matching ETag should succeed: %v", err)
	}
}

func TestStore_ListOperations(t *testing.T) {
	testStore, _ := SetupTestStore(t)
	ctx := context.Background()

	// Create test entities with different prefixes
	prefixes := []string{"a/", "b/", "c/"}
	for _, prefix := range prefixes {
		for i := 1; i <= 3; i++ {
			key := prefix + "item" + strconv.Itoa(i)
			entity := NewTestEntity("test", i*10)
			entity.Version = 1
			_, err := testStore.Put(ctx, key, *entity)
			if err != nil {
				t.Fatalf("Put failed for %s: %v", key, err)
			}
		}
	}

	// Test List all
	entries, err := testStore.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(entries) != 9 { // 3 prefixes × 3 items
		t.Errorf("List() returned %d entries, want 9", len(entries))
	}

	// Test List with prefix
	entries, err = testStore.List(ctx, WithPrefix("a/"))
	if err != nil {
		t.Fatalf("List with prefix failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("List(WithPrefix('a/')) returned %d entries, want 3", len(entries))
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Key, "a/") {
			t.Errorf("Entry key %s doesn't have prefix 'a/'", entry.Key)
		}
	}

	// Test List with recursive option
	entries, err = testStore.List(ctx, WithPrefix(""), WithRecursive(true))
	if err != nil {
		t.Fatalf("List with recursive option failed: %v", err)
	}

	if len(entries) != 9 {
		t.Errorf("List(WithRecursive(true)) returned %d entries, want 9", len(entries))
	}

	// Test List with max keys
	entries, err = testStore.List(ctx, WithMaxKeys(5))
	if err != nil {
		t.Fatalf("List with max keys failed: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("List(WithMaxKeys(5)) returned %d entries, want 5", len(entries))
	}
}

func TestStore_Head(t *testing.T) {
	testStore, _ := SetupTestStore(t)
	ctx := context.Background()

	// Create test entity
	entity := NewTestEntity("test-head", 42)
	entity.Version = 1
	key := "head-test-key"

	// Put the entity
	_, err := testStore.Put(ctx, key, *entity)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get metadata with Head
	metadata, err := testStore.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head failed: %v", err)
	}

	// Verify metadata
	if metadata.ETag == "" {
		t.Error("Metadata ETag is empty")
	}
	if metadata.ContentType != "application/json" {
		t.Errorf("Metadata ContentType = %q, want 'application/json'", metadata.ContentType)
	}

	// Check version in metadata
	versionStr, found := metadata.UserMetadata["version"]
	if !found {
		t.Error("Version not found in metadata")
	} else if versionStr != "1" {
		t.Errorf("Version = %s, want '1'", versionStr)
	}

	// Test Head with non-existent key
	_, err = testStore.Head(ctx, "non-existent")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Expected NotFound for non-existent key, got: %v", err)
	}
}

func TestStore_GetMany(t *testing.T) {
	testStore, _ := SetupTestStore(t)
	ctx := context.Background()

	// Create test entities
	keys := []string{"multi1", "multi2", "multi3"}
	for i, key := range keys {
		entity := NewTestEntity("multi-test", i+1)
		entity.Version = 1
		_, err := testStore.Put(ctx, key, *entity)
		if err != nil {
			t.Fatalf("Put failed for %s: %v", key, err)
		}
	}

	// Add non-existent key
	keys = append(keys, "non-existent")

	// Get multiple entities
	results, errors := testStore.GetMany(ctx, keys)

	// Check results
	if len(results) != 3 {
		t.Errorf("GetMany() returned %d results, want 3", len(results))
	}
	if len(errors) != 1 {
		t.Errorf("GetMany() returned %d errors, want 1", len(errors))
	}

	// Check each result
	for i := 0; i < 3; i++ {
		key := keys[i]
		entity, found := results[key]
		if !found {
			t.Errorf("Expected result for key %s not found", key)
			continue
		}
		if entity.Value != i+1 {
			t.Errorf("Entity value for key %s = %d, want %d", key, entity.Value, i+1)
		}
	}

	// Check error for non-existent key
	err, found := errors["non-existent"]
	if !found {
		t.Error("Expected error for non-existent key not found")
	} else if !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Expected NotFound error for non-existent key, got: %v", err)
	}
}
