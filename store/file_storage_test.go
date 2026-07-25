package store

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupFileStorageTest(t *testing.T) (*FileStorage, string) {
	// Create a temporary directory for the test
	tmpDir := t.TempDir()

	// Create the storage instance
	storage, err := NewFileStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create file storage: %v", err)
	}

	return storage, tmpDir
}

func TestFileStorage_BasicOperations(t *testing.T) {
	storage, tmpDir := setupFileStorageTest(t)
	ctx := context.Background()

	// Test data
	testBucket := "test-bucket"
	testKey := "test/file.txt"
	testData := []byte("Hello, World!")
	testURI := FormatLocationURI("file", testBucket, testKey)

	// Create the bucket directory
	bucketPath := filepath.Join(tmpDir, testBucket)
	if err := os.MkdirAll(bucketPath, 0755); err != nil {
		t.Fatalf("Failed to create bucket directory: %v", err)
	}

	// Put test data
	_, err := storage.Put(ctx, testURI, testData)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Verify file exists
	filePath := filepath.Join(tmpDir, testBucket, testKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("File was not created at expected location: %s", filePath)
	}

	// Get the data
	retrievedData, metadata, err := storage.Get(ctx, testURI)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify data
	if string(retrievedData) != string(testData) {
		t.Errorf("Retrieved data doesn't match: got %q, want %q", retrievedData, testData)
	}

	// Verify metadata
	if metadata.Size != int64(len(testData)) {
		t.Errorf("Wrong metadata size: got %d, want %d", metadata.Size, len(testData))
	}

	if metadata.ETag == "" {
		t.Error("ETag is empty")
	}

	// Test Head
	headMeta, err := storage.Head(ctx, testURI)
	if err != nil {
		t.Fatalf("Head failed: %v", err)
	}

	if headMeta.Size != metadata.Size || headMeta.ETag != metadata.ETag {
		t.Error("Head metadata doesn't match Get metadata")
	}

	// Test Delete
	err = storage.Delete(ctx, testURI)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify file doesn't exist
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("File still exists after Delete")
	}

	// Test Get on non-existent file (should return NotFound)
	_, _, err = storage.Get(ctx, testURI)
	if err == nil || !IsErrorCode(err, ErrCodeNotFound) {
		t.Errorf("Get on deleted file should return NotFound, got: %v", err)
	}
}

func TestFileStorage_GetStream(t *testing.T) {
	storage, _ := setupFileStorageTest(t)
	ctx := context.Background()

	// Test data
	testBucket := "test-bucket"
	testKey := "test/stream.txt"
	testData := []byte("Stream test data")
	testURI := FormatLocationURI("file", testBucket, testKey)

	// Put test data
	_, err := storage.Put(ctx, testURI, testData)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get as streamx
	stream, metadata, err := storage.GetStream(ctx, testURI)
	if err != nil {
		t.Fatalf("GetStream failed: %v", err)
	}
	defer stream.Close()

	// Read the stream
	streamData, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("Failed to read stream: %v", err)
	}

	// Verify data
	if string(streamData) != string(testData) {
		t.Errorf("Stream data doesn't match: got %q, want %q", streamData, testData)
	}

	// Verify metadata
	if metadata.Size != int64(len(testData)) {
		t.Errorf("Wrong metadata size: got %d, want %d", metadata.Size, len(testData))
	}
}

func TestFileStorage_PutStream(t *testing.T) {
	storage, _ := setupFileStorageTest(t)
	ctx := context.Background()

	// Test data
	testBucket := "test-bucket"
	testKey := "test/put-stream.txt"
	testData := "Stream input data"
	testURI := FormatLocationURI("file", testBucket, testKey)

	// Create a reader
	reader := strings.NewReader(testData)

	// Put as stream
	metadata, err := storage.PutStream(ctx, testURI, reader)
	if err != nil {
		t.Fatalf("PutStream failed: %v", err)
	}

	// Verify metadata
	if metadata.Size != int64(len(testData)) {
		t.Errorf("Wrong metadata size: got %d, want %d", metadata.Size, len(testData))
	}

	if metadata.ETag == "" {
		t.Error("ETag is empty")
	}

	// Get the data to verify
	retrievedData, _, err := storage.Get(ctx, testURI)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify data
	if string(retrievedData) != testData {
		t.Errorf("Retrieved data doesn't match: got %q, want %q", retrievedData, testData)
	}
}

func TestFileStorage_List(t *testing.T) {
	storage, _ := setupFileStorageTest(t)
	ctx := context.Background()

	// Test data
	testBucket := "list-bucket"
	testItems := []struct {
		key  string
		data string
	}{
		{"file1.txt", "file1 data"},
		{"dir/file2.txt", "file2 data"},
		{"dir/subdir/file3.txt", "file3 data"},
		{"another-dir/file4.txt", "file4 data"},
	}

	// Create test files
	for _, item := range testItems {
		uri := FormatLocationURI("file", testBucket, item.key)
		_, err := storage.Put(ctx, uri, []byte(item.data))
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", item.key, err)
		}
	}

	// Test listing everything - with default delimiter '/', this should return:
	// - file1.txt (file)
	// - dir/ (prefix)
	// - another-dir/ (prefix)
	entries, err := storage.List(ctx, FormatLocationURI("file", testBucket, ""))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	t.Logf("All entries: %+v", entriesAsStrings(entries))

	// Expecting 3 entries: file1.txt, dir/ and another-dir/
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Test listing with prefix
	entries, err = storage.List(ctx, FormatLocationURI("file", testBucket, ""), WithPrefix("dir/"))
	if err != nil {
		t.Fatalf("List with prefix failed: %v", err)
	}

	// Should return "dir/file2.txt" and "dir/subdir/"
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries for dir/ prefix, got %d: %+v",
			len(entries), entriesAsStrings(entries))
	} else {
		// Verify correct entries for dir/ prefix
		var foundFile, foundDir bool
		for _, entry := range entries {
			if entry.Key == "dir/file2.txt" && !entry.IsPrefix {
				foundFile = true
			} else if entry.Key == "dir/subdir/" && entry.IsPrefix {
				foundDir = true
			}
		}
		if !foundFile {
			t.Error("Missing file entry dir/file2.txt")
		}
		if !foundDir {
			t.Error("Missing directory entry dir/subdir/")
		}
	}

	// Test recursive listing
	entries, err = storage.List(ctx, FormatLocationURI("file", testBucket, ""),
		WithPrefix("dir/"), WithRecursive(true))
	if err != nil {
		t.Fatalf("Recursive list failed: %v", err)
	}

	// Should return both files under dir/ (file2.txt and subdir/file3.txt)
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries for recursive dir/ listing, got %d: %+v",
			len(entries), entriesAsStrings(entries))
	} else {
		// Verify correct entries for recursive listing
		var foundFile2, foundFile3 bool
		for _, entry := range entries {
			if entry.Key == "dir/file2.txt" && !entry.IsPrefix {
				foundFile2 = true
			} else if entry.Key == "dir/subdir/file3.txt" && !entry.IsPrefix {
				foundFile3 = true
			}
		}
		if !foundFile2 {
			t.Error("Missing file entry dir/file2.txt in recursive listing")
		}
		if !foundFile3 {
			t.Error("Missing file entry dir/subdir/file3.txt in recursive listing")
		}
	}
}

// Helper function to convert entries to strings for debugging
func entriesAsStrings(entries []Entry) []string {
	result := make([]string, len(entries))
	for i, entry := range entries {
		if entry.IsPrefix {
			result[i] = entry.Key + " (prefix)"
		} else {
			result[i] = entry.Key + " (file)"
		}
	}
	return result
}

func TestFileStorage_ConditionalOperations(t *testing.T) {
	storage, _ := setupFileStorageTest(t)
	ctx := context.Background()

	// Test data
	testBucket := "conditional-bucket"
	testKey := "conditional-test.txt"
	testData := []byte("Initial data")
	updatedData := []byte("Updated data")
	testURI := FormatLocationURI("file", testBucket, testKey)

	// Put initial data
	_, err := storage.Put(ctx, testURI, testData)
	if err != nil {
		t.Fatalf("Initial put failed: %v", err)
	}

	// Get the ETag
	metadata, err := storage.Head(ctx, testURI)
	if err != nil {
		t.Fatalf("Head failed: %v", err)
	}
	etag := metadata.ETag

	// Test If-Match with correct ETag (should succeed)
	_, err = storage.Put(ctx, testURI, updatedData, IfMatch(etag))
	if err != nil {
		t.Errorf("Put with correct If-Match failed: %v", err)
	}

	// Get the new ETag
	metadata, err = storage.Head(ctx, testURI)
	if err != nil {
		t.Fatalf("Head after update failed: %v", err)
	}
	newEtag := metadata.ETag

	// Test If-Match with wrong ETag (should fail)
	_, err = storage.Put(ctx, testURI, []byte("This should fail"), IfMatch(etag))
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Put with incorrect If-Match should fail with PreconditionFailed, got: %v", err)
	}

	// Test If-None-Match with correct ETag (should return NotModified)
	_, _, err = storage.Get(ctx, testURI, IfNoneMatch(newEtag))
	if err == nil || !IsErrorCode(err, ErrCodeNotModified) {
		t.Errorf("Get with matching If-None-Match should return NotModified, got: %v", err)
	}

	// Test If-None-Match with wrong ETag (should succeed)
	data, _, err := storage.Get(ctx, testURI, IfNoneMatch(etag))
	if err != nil {
		t.Errorf("Get with non-matching If-None-Match should succeed, got error: %v", err)
	} else if string(data) != "Updated data" {
		t.Errorf("Get with non-matching If-None-Match returned wrong data: %s", string(data))
	}

	// Test If-None-Match for new key (should succeed)
	newKey := "new-conditional-key"
	newURI := FormatLocationURI("file", testBucket, newKey)
	_, err = storage.Put(ctx, newURI, []byte("New data"), IfNoneMatch("*"))
	if err != nil {
		t.Errorf("Put with If-None-Match=* on new key should succeed: %v", err)
	}

	// Test If-None-Match=* for existing key (should fail)
	_, err = storage.Put(ctx, testURI, []byte("Should fail"), IfNoneMatch("*"))
	if err == nil || !IsErrorCode(err, ErrCodePreconditionFailed) {
		t.Errorf("Put with If-None-Match=* on existing key should fail with PreconditionFailed, got: %v", err)
	}
}

func TestFileStorage_CleanupEmptyDirs(t *testing.T) {
	storage, tmpDir := setupFileStorageTest(t)
	ctx := context.Background()

	// Create a nested file
	testBucket := "cleanup-bucket"
	testKey := "dir1/dir2/dir3/file.txt"
	testURI := FormatLocationURI("file", testBucket, testKey)

	// Put the file
	_, err := storage.Put(ctx, testURI, []byte("Test data"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Delete the file
	err = storage.Delete(ctx, testURI)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Give the system time to clean up
	time.Sleep(50 * time.Millisecond)

	// Check if parent directories were cleaned up
	dir1Path := filepath.Join(tmpDir, testBucket, "dir1")
	_, err = os.Stat(dir1Path)
	if !os.IsNotExist(err) {
		t.Error("Empty directories were not cleaned up after delete")
	}
}

func TestFileStorageFactory(t *testing.T) {
	ctx := context.Background()

	// Test with explicit path option
	uri := StorageURI{
		Scheme: "file",
		Bucket: "test-bucket",
		Options: url.Values{
			"path": []string{t.TempDir()},
		},
	}

	storage, err := FileStorageFactory(ctx, uri)
	if err != nil {
		t.Fatalf("FileStorageFactory failed: %v", err)
	}

	if storage == nil {
		t.Fatal("Factory returned nil storage")
	}

	// Test without path (should use bucket name)
	uri2 := StorageURI{
		Scheme: "file",
		Bucket: "temp-bucket",
	}
	t.Cleanup(func() {
		// Clean up the temporary directory
		if err := os.RemoveAll(uri2.Bucket); err != nil {
			t.Fatalf("Failed to remove temp directory: %v", err)
		}
	})

	storage, err = FileStorageFactory(ctx, uri2)
	if err != nil {
		t.Fatalf("FileStorageFactory without path failed: %v", err)
	}

	if storage == nil {
		t.Fatal("Factory returned nil storage")
	}
}
