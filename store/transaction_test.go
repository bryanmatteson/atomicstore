package store

import (
	"context"
	"testing"
	"time"

	"github.com/bryanmatteson/atomicstore/codec"
)

func TestStorageTransaction_BasicOperations(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Set up some initial data
	mockStorage.AddTestObject(
		"mock://test-bucket/existing-item",
		[]byte(`{"id":"existing-id","version":1,"name":"Existing","value":42}`),
		"application/json",
		map[string]string{"version": "1"},
	)

	// Create transaction
	tx := NewStorageTransaction(ctx, mockStorage)

	// Test Get on non-existent item
	_, _, err := tx.Get("mock://test-bucket/nonexistent")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Expected NotFound error on Get, got: %v", err)
	}

	// Test Get on existing item
	data, meta, err := tx.Get("mock://test-bucket/existing-item")
	if err != nil {
		t.Fatalf("Failed to get existing item: %v", err)
	}
	if len(data) == 0 {
		t.Error("Get returned empty data")
	}
	if meta.ETag == "" {
		t.Error("Get returned empty ETag in metadata")
	}

	// Test Put
	err = tx.Put("mock://test-bucket/new-item", []byte(`{"name":"New"}`))
	if err != nil {
		t.Fatalf("Failed to put new item: %v", err)
	}

	// Verify item exists in transaction but not in storage
	txData, _, err := tx.Get("mock://test-bucket/new-item")
	if err != nil {
		t.Errorf("Failed to get new item from transaction: %v", err)
	}
	if string(txData) != `{"name":"New"}` {
		t.Errorf("Transaction returned wrong data: %s", string(txData))
	}

	// Item shouldn't exist in storage yet
	if mockStorage.ObjectExists("mock://test-bucket/new-item") {
		t.Error("New item should not exist in storage before commit")
	}

	// Test Update existing item
	err = tx.Put(
		"mock://test-bucket/existing-item",
		[]byte(`{"id":"existing-id","version":2,"name":"Updated","value":99}`),
	)
	if err != nil {
		t.Fatalf("Failed to update existing item: %v", err)
	}

	// Test Delete
	err = tx.Delete("mock://test-bucket/existing-item")
	if err != nil {
		t.Fatalf("Failed to delete item: %v", err)
	}

	// Get should now return "marked for deletion"
	_, _, err = tx.Get("mock://test-bucket/existing-item")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Get after Delete should return NotFound, got: %v", err)
	}

	// Commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	// Verify storage state after commit
	if !mockStorage.ObjectExists("mock://test-bucket/new-item") {
		t.Error("New item should exist in storage after commit")
	}

	if mockStorage.ObjectExists("mock://test-bucket/existing-item") {
		t.Error("Deleted item should not exist in storage after commit")
	}
}

func TestStorageTransaction_Rollback(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Set up initial data
	mockStorage.AddTestObject(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":1,"name":"Test","value":42}`),
		"application/json",
		map[string]string{"version": "1"},
	)

	// Create transaction
	tx := NewStorageTransaction(ctx, mockStorage)

	// Make changes
	err := tx.Put(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":2,"name":"Modified","value":100}`),
	)
	if err != nil {
		t.Fatalf("Failed to update item: %v", err)
	}

	err = tx.Put(
		"mock://test-bucket/new-item",
		[]byte(`{"name":"New"}`),
	)
	if err != nil {
		t.Fatalf("Failed to put new item: %v", err)
	}

	// Rollback transaction
	err = tx.Rollback()
	if err != nil {
		t.Fatalf("Failed to rollback transaction: %v", err)
	}

	// Verify storage state is unchanged
	if mockStorage.ObjectExists("mock://test-bucket/new-item") {
		t.Error("New item should not exist after rollback")
	}

	// Original item should be unchanged
	data, _, err := mockStorage.Get(ctx, "mock://test-bucket/test-item")
	if err != nil {
		t.Fatalf("Failed to get item: %v", err)
	}
	if string(data) != `{"id":"test-id","version":1,"name":"Test","value":42}` {
		t.Errorf("Item was modified despite rollback: %s", string(data))
	}
}

func TestStorageTransaction_ConditionalOperations(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Set up initial data
	mockStorage.AddTestObject(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":1,"name":"Test","value":42}`),
		"application/json",
		map[string]string{"version": "1"},
	)

	// Get the ETag for conditional operations
	meta, err := mockStorage.Head(ctx, "mock://test-bucket/test-item")
	if err != nil {
		t.Fatalf("Failed to head item: %v", err)
	}
	etag := meta.ETag

	// Create transaction
	tx := NewStorageTransaction(ctx, mockStorage)

	// Test Put with incorrect If-Match (should fail)
	err = tx.Put(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":2,"name":"Modified","value":100}`),
		IfMatch("wrong-etag"),
	)
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Expected PreconditionFailed for Put with wrong ETag, got: %v", err)
	}

	// Test Put with correct If-Match (should succeed)
	err = tx.Put(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":2,"name":"Modified","value":100}`),
		IfMatch(etag),
	)
	if err != nil {
		t.Errorf("Put with correct ETag should succeed, got: %v", err)
	}

	// Test Delete with incorrect If-Match (should fail)
	err = tx.Delete(
		"mock://test-bucket/test-item",
		ConditionalOptionFunc(func(opts *ConditionalOptions) {
			opts.IfMatch.SetRight("wrong-etag")
		}),
	)
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Expected PreconditionFailed for Delete with wrong ETag, got: %v", err)
	}

	// Test If-None-Match="*" on new item (should succeed)
	err = tx.Put(
		"mock://test-bucket/new-item",
		[]byte(`{"id":"new-id","version":1,"name":"New","value":1}`),
		ConditionalOptionFunc(func(opts *ConditionalOptions) {
			opts.IfNoMatch.SetRight("*")
		}),
	)
	if err != nil {
		t.Errorf("Put with If-None-Match=* on new item should succeed, got: %v", err)
	}

	// Test If-None-Match="*" on existing item (should fail)
	err = tx.Put(
		"mock://test-bucket/test-item",
		[]byte(`{"id":"test-id","version":3,"name":"Another","value":200}`),
		ConditionalOptionFunc(func(opts *ConditionalOptions) {
			opts.IfNoMatch.SetRight("*")
		}),
	)
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Expected PreconditionFailed for Put with If-None-Match=* on existing item, got: %v", err)
	}

	// Commit and verify results
	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	// Verify new item exists
	if !mockStorage.ObjectExists("mock://test-bucket/new-item") {
		t.Error("New item should exist after commit")
	}

	// Verify existing item was updated
	data, _, err := mockStorage.Get(ctx, "mock://test-bucket/test-item")
	if err != nil {
		t.Fatalf("Failed to get item: %v", err)
	}
	if string(data) != `{"id":"test-id","version":2,"name":"Modified","value":100}` {
		t.Errorf("Item not updated as expected: %s", string(data))
	}
}

func TestStorageTransaction_ClosedTransactions(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Create transaction
	tx := NewStorageTransaction(ctx, mockStorage)

	// Make a change
	err := tx.Put("mock://test-bucket/test-item", []byte(`{"name":"Test"}`))
	if err != nil {
		t.Fatalf("Failed to put item: %v", err)
	}

	// Commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	// Attempt operations on committed transaction (should fail)
	_, _, err = tx.Get("mock://test-bucket/test-item")
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Get after commit, got: %v", err)
	}

	err = tx.Put("mock://test-bucket/another-item", []byte(`{"name":"Another"}`))
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Put after commit, got: %v", err)
	}

	err = tx.Delete("mock://test-bucket/test-item")
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Delete after commit, got: %v", err)
	}

	// Test rollback after commit (should fail)
	err = tx.Rollback()
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Rollback after commit, got: %v", err)
	}

	// Test with rollback instead of commit
	tx2 := NewStorageTransaction(ctx, mockStorage)
	err = tx2.Put("mock://test-bucket/test-item2", []byte(`{"name":"Test2"}`))
	if err != nil {
		t.Fatalf("Failed to put item in second transaction: %v", err)
	}

	// Rollback transaction
	err = tx2.Rollback()
	if err != nil {
		t.Fatalf("Failed to rollback transaction: %v", err)
	}

	// Attempt operations on rolled back transaction (should fail)
	err = tx2.Put("mock://test-bucket/another-item", []byte(`{"name":"Another"}`))
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Put after rollback, got: %v", err)
	}

	// Test commit after rollback (should fail)
	err = tx2.Commit(ctx)
	if err == nil || !IsErrorCode(err, ErrCodeTransactionClosed) {
		t.Errorf("Expected TransactionClosed error on Commit after rollback, got: %v", err)
	}
}

func TestTypedTransactionView(t *testing.T) {
	// Set up mock storage and store
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

	// Create an entity directly in the store
	entity := NewTestEntity("Original", 100)
	_, err = store.Put(ctx, "test-key", *entity)
	if err != nil {
		t.Fatalf("Failed to put entity in store: %v", err)
	}

	// Start a transaction
	tx, err := store.BeginTransaction(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Get a typed view of the transaction
	typedView := store.WithTransaction(tx)

	// Read the entity through the transaction
	readEntity, err := typedView.Get("test-key")
	if err != nil {
		t.Fatalf("Failed to get entity through transaction: %v", err)
	}

	// Verify we got the right entity
	if readEntity.Name != "Original" || readEntity.Value != 100 {
		t.Errorf("Got wrong entity data: name=%s value=%d", readEntity.Name, readEntity.Value)
	}

	// Modify the entity through the transaction
	readEntity.Name = "Modified"
	readEntity.Value = 200
	err = typedView.Put("test-key", readEntity)
	if err != nil {
		t.Fatalf("Failed to put modified entity: %v", err)
	}

	// Read the modified entity from the transaction
	modifiedEntity, err := typedView.Get("test-key")
	if err != nil {
		t.Fatalf("Failed to get modified entity: %v", err)
	}

	// Verify the modifications are visible within the transaction
	if modifiedEntity.Name != "Modified" || modifiedEntity.Value != 200 {
		t.Errorf("Transaction view didn't show modifications: name=%s value=%d",
			modifiedEntity.Name, modifiedEntity.Value)
	}

	// Verify the original entity is still unchanged in the store
	originalEntity, _, err := store.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("Failed to get original entity: %v", err)
	}
	if originalEntity.Name != "Original" || originalEntity.Value != 100 {
		t.Errorf("Original entity in store was modified: name=%s value=%d",
			originalEntity.Name, originalEntity.Value)
	}

	// Try adding a new entity through the transaction
	newEntity := NewTestEntity("New", 300)
	err = typedView.Put("new-key", *newEntity)
	if err != nil {
		t.Fatalf("Failed to put new entity: %v", err)
	}

	// Test deleting an entity through the transaction
	err = typedView.Delete("test-key")
	if err != nil {
		t.Fatalf("Failed to delete entity through transaction: %v", err)
	}

	// Commit the transaction
	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	// Verify changes are now visible in the store

	// New entity should exist
	newStoredEntity, _, err := store.Get(ctx, "new-key")
	if err != nil {
		t.Fatalf("Failed to get new entity after commit: %v", err)
	}
	if newStoredEntity.Name != "New" || newStoredEntity.Value != 300 {
		t.Errorf("New entity incorrect after commit: name=%s value=%d",
			newStoredEntity.Name, newStoredEntity.Value)
	}

	// Original entity should be deleted
	_, _, err = store.Get(ctx, "test-key")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Deleted entity still exists after commit")
	}
}

func TestTransactionOptions(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Test ReadOnly option
	_ = NewStorageTransaction(ctx, mockStorage)

	// Set the ReadOnly flag
	opts := TransactionOptions{}
	opts.ReadOnly.Set(true)
	opts.applyTransaction(&opts)

	if !opts.ReadOnly.Or(false) {
		t.Error("ReadOnly option should be true")
	}

	// Test Timeout option
	timeout := 30 * time.Second
	opts = TransactionOptions{}
	opts.Timeout.Set(timeout)
	opts.applyTransaction(&opts)

	if opts.Timeout.Or(0) != timeout {
		t.Errorf("Timeout option should be %v, got %v", timeout, opts.Timeout.Or(0))
	}

	// Test option combining
	baseOpts := TransactionOptions{}
	baseOpts.ReadOnly.Set(true)
	baseOpts.Timeout.Set(10 * time.Second)

	combinedOpts := TransactionOptions{}
	baseOpts.applyTransaction(&combinedOpts)

	if !combinedOpts.ReadOnly.Or(false) {
		t.Error("Combined ReadOnly option should be true")
	}

	if combinedOpts.Timeout.Or(0) != 10*time.Second {
		t.Errorf("Combined Timeout option should be 10s, got %v", combinedOpts.Timeout.Or(0))
	}
}

// New test in store/transaction_test.go

func TestStorageTransaction_PartialFailureRecovery(t *testing.T) {
	mockStorage := NewMockStorage("mock")
	ctx := context.Background()

	// Add test setup
	mockStorage.AddTestObject(
		"mock://test-bucket/item1",
		[]byte(`{"id":"item1","name":"Item One"}`),
		"application/json",
		nil,
	)
	mockStorage.AddTestObject(
		"mock://test-bucket/item2",
		[]byte(`{"id":"item2","name":"Item Two"}`),
		"application/json",
		nil,
	)

	// Create a transaction
	tx := NewStorageTransaction(ctx, mockStorage)

	// Set up transaction operations
	err := tx.Put("mock://test-bucket/item1", []byte(`{"id":"item1","name":"Updated Item"}`))
	if err != nil {
		t.Fatalf("Failed to add first Put operation: %v", err)
	}

	err = tx.Put("mock://test-bucket/item3", []byte(`{"id":"item3","name":"New Item"}`))
	if err != nil {
		t.Fatalf("Failed to add second Put operation: %v", err)
	}

	err = tx.Delete("mock://test-bucket/item2")
	if err != nil {
		t.Fatalf("Failed to add Delete operation: %v", err)
	}

	// Configure mock to fail on the second Put operation
	mockStorage.SetFailNext("mock://test-bucket/item3", ErrCodeIO, true)

	// Attempt to commit, should fail
	err = tx.Commit(ctx)
	if err == nil {
		t.Fatal("Expected commit to fail due to mock failure, but it succeeded")
	}

	// Verify transaction was properly rolled back - item1 should not be updated
	data, _, err := mockStorage.Get(ctx, "mock://test-bucket/item1")
	if err != nil {
		t.Fatalf("Failed to get item1: %v", err)
	}
	if string(data) != `{"id":"item1","name":"Item One"}` {
		t.Errorf("Item1 was incorrectly updated despite transaction failure: %s", string(data))
	}

	// Verify item2 was not deleted
	_, _, err = mockStorage.Get(ctx, "mock://test-bucket/item2")
	if err != nil {
		t.Errorf("Item2 was incorrectly deleted despite transaction failure: %v", err)
	}

	// Verify item3 was not created
	_, _, err = mockStorage.Get(ctx, "mock://test-bucket/item3")
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Item3 was incorrectly created despite transaction failure")
	}
}

func TestStorageTransactionFailsClosedWithoutAtomicBatchCapability(t *testing.T) {
	fs, err := NewFileStorage(t.TempDir())
	AssertNoError(t, err)
	ctx := context.Background()
	tx := NewStorageTransaction(ctx, fs)
	firstURI := FormatLocationURI("file", "bucket", "first")
	secondURI := FormatLocationURI("file", "bucket", "second")

	AssertNoError(t, tx.Put(firstURI, []byte("first")))
	AssertNoError(t, tx.Put(secondURI, []byte("second")))
	err = tx.Commit(ctx)
	AssertErrorCode(t, err, ErrCodeUnsupported)

	_, err = fs.Head(ctx, firstURI)
	AssertErrorCode(t, err, ErrCodeNotFound)
	_, err = fs.Head(ctx, secondURI)
	AssertErrorCode(t, err, ErrCodeNotFound)
}
