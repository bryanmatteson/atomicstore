package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func init() {
	// Register the file URI scheme
	RegisterURIScheme("file", FileStorageFactory)
}

// FileStorage implements the Storage interface using the local filesystem
type FileStorage struct {
	rootPath string
	basePath string
	lockPath string
}

// NewFileStorage creates a new FileStorage
func NewFileStorage(basePath string) (*FileStorage, error) {
	// Make sure the base path exists
	absPath, err := filepath.Abs(basePath)
	if err != nil {
		return nil, NewStoreError(ErrCodeInvalidOperation, "NewFileStorage", "", "Invalid base path", err, false)
	}

	// Create the directory if it doesn't exist
	err = os.MkdirAll(absPath, 0755)
	if err != nil {
		return nil, NewStoreError(ErrCodeInvalidOperation, "NewFileStorage", "", "Failed to create base directory", err, false)
	}

	lockPath := filepath.Join(absPath, ".conditional-store-locks")
	if err := os.MkdirAll(lockPath, 0700); err != nil {
		return nil, NewStoreError(ErrCodeInvalidOperation, "NewFileStorage", "", "Failed to create lock directory", err, false)
	}

	return &FileStorage{
		rootPath: absPath,
		basePath: basePath,
		lockPath: lockPath,
	}, nil
}

// URIScheme returns the URI scheme for this storage
func (f *FileStorage) URIScheme() string {
	return "file"
}

// HasLinearizableConditions reports that FileStorage serializes conditional
// operations through per-key cross-process advisory locks.
func (f *FileStorage) HasLinearizableConditions() bool {
	return true
}

func (f *FileStorage) Get(ctx context.Context, uri string, options ...GetOption) ([]byte, Metadata, error) {
	// Apply options
	opts := &OperationOptions{}
	applyGetOptions(opts, options)

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, Metadata{}, NewInvalidURIError("Get", uri, err)
	}

	// Construct file path
	filePath := f.constructPath(parsedURI.Bucket, parsedURI.Key)

	var data []byte
	var metadata Metadata
	err = f.withFileLock(ctx, filePath, false, func() error {
		var snapshotErr error
		data, metadata, snapshotErr = f.readSnapshot(filePath)
		if snapshotErr != nil {
			return snapshotErr
		}
		return conditionalHelper.ApplyConditionalGet(metadata, &opts.ConditionalOptions)
	})
	if err != nil {
		return nil, metadata, withStoreErrorKey(err, "Get", parsedURI.Key)
	}
	return data, metadata, nil
}

// GetStream returns an object as a stream
func (f *FileStorage) GetStream(ctx context.Context, uri string, options ...GetOption) (io.ReadCloser, Metadata, error) {
	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, Metadata{}, NewInvalidURIError("GetStream", uri, err)
	}

	data, metadata, err := f.Get(ctx, uri, options...)
	if err != nil {
		return nil, metadata, withStoreErrorKey(err, "GetStream", parsedURI.Key)
	}
	return io.NopCloser(bytes.NewReader(data)), metadata, nil
}

// Put stores an object by URI
func (f *FileStorage) Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error) {
	// Apply options
	opts := &OperationOptions{}
	applyPutOptions(opts, options)

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("Put", uri, err)
	}

	// Construct file path
	filePath := f.constructPath(parsedURI.Bucket, parsedURI.Key)

	return f.putReader(ctx, parsedURI.Key, filePath, bytes.NewReader(data), opts, "Put")
}

// PutStream stores a stream by URI
func (f *FileStorage) PutStream(ctx context.Context, uri string, reader io.Reader, options ...PutOption) (Metadata, error) {
	// Apply options
	opts := &OperationOptions{}
	applyPutOptions(opts, options)

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("PutStream", uri, err)
	}

	// Construct file path
	filePath := f.constructPath(parsedURI.Bucket, parsedURI.Key)

	return f.putReader(ctx, parsedURI.Key, filePath, reader, opts, "PutStream")
}

// Delete removes an object by URI
func (f *FileStorage) Delete(ctx context.Context, uri string, options ...DeleteOption) error {
	// Apply options
	opts := &OperationOptions{}
	applyDeleteOptions(opts, options)

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return NewInvalidURIError("Delete", uri, err)
	}

	// Construct file path
	filePath := f.constructPath(parsedURI.Bucket, parsedURI.Key)

	err = f.withFileLock(ctx, filePath, true, func() error {
		existingMetadata, snapshotErr := f.getMetadataUnlocked(filePath)
		fileExists := snapshotErr == nil
		if snapshotErr != nil && !IsErrorCode(snapshotErr, ErrCodeNotFound) {
			return snapshotErr
		}
		if conditionErr := conditionalHelper.ApplyConditionalDelete(fileExists, existingMetadata, &opts.ConditionalOptions); conditionErr != nil {
			return conditionErr
		}
		if !fileExists {
			return NewStoreError(ErrCodeNotFound, "Delete", parsedURI.Key, "Object not found", nil, false)
		}
		if removeErr := os.Remove(filePath); removeErr != nil {
			return NewStoreError(ErrCodeIO, "Delete", parsedURI.Key, "Failed to delete file", removeErr, true)
		}
		return f.syncDirectory(filepath.Dir(filePath))
	})
	if err != nil {
		return withStoreErrorKey(err, "Delete", parsedURI.Key)
	}

	// Try to clean up empty directories
	f.cleanupEmptyDirs(filepath.Dir(filePath))

	return nil
}

// Head retrieves metadata without retrieving the full object
func (f *FileStorage) Head(ctx context.Context, uri string, options ...HeadOption) (Metadata, error) {
	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("Head", uri, err)
	}

	// Construct file path
	filePath := f.constructPath(parsedURI.Bucket, parsedURI.Key)

	var metadata Metadata
	err = f.withFileLock(ctx, filePath, false, func() error {
		var metadataErr error
		metadata, metadataErr = f.getMetadataUnlocked(filePath)
		return metadataErr
	})
	return metadata, withStoreErrorKey(err, "Head", parsedURI.Key)
}

// List returns objects matching specified criteria
func (f *FileStorage) List(ctx context.Context, uri string, options ...ListOption) ([]Entry, error) {
	// Apply options
	opts := &ListOptions{}
	applyListOptions(opts, options)

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, NewInvalidURIError("List", uri, err)
	}

	// Construct base directory path
	basePath := filepath.Join(f.rootPath, parsedURI.Bucket)
	prefix := opts.Prefix.Or("")
	delimiter := opts.Delimiter.Or("/")
	recursive := opts.Recursive.Or(false)
	maxKeys := int(opts.MaxKeys.Or(1000))

	// Ensure the bucket directory exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return []Entry{}, nil
	}

	// Convert Windows paths to forward slashes for consistent comparison
	normalizeKey := func(key string) string {
		return strings.ReplaceAll(key, "\\", "/")
	}

	// Create the result entries
	var entries []Entry
	seen := make(map[string]bool)

	// Helper to add entries only if not seen before
	addEntry := func(entry Entry) {
		if !seen[entry.Key] {
			seen[entry.Key] = true
			entries = append(entries, entry)
		}
	}

	// If prefix includes directory components, adjust basePath
	baseSearchPath := basePath
	if prefix != "" {
		// Check if prefix refers to a directory that exists
		prefixPath := filepath.Join(basePath, prefix)
		if fi, err := os.Stat(prefixPath); err == nil && fi.IsDir() {
			baseSearchPath = prefixPath
		} else if dir := filepath.Dir(prefix); dir != "." {
			// If prefix contains directory components, adjust base path
			baseSearchPath = filepath.Join(basePath, dir)
		}
	}

	// Walk the directory
	err = filepath.Walk(baseSearchPath, func(path string, info os.FileInfo, err error) error {
		// Skip base path itself unless we're searching with an empty prefix
		if path == baseSearchPath && baseSearchPath != basePath {
			return nil
		}

		// Handle walk errors
		if err != nil {
			return nil // Continue walking despite errors
		}

		// Get relative path from base
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return nil
		}
		relPath = normalizeKey(relPath)

		// Skip if doesn't match prefix
		if prefix != "" && !strings.HasPrefix(relPath, prefix) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Handle directories
		if info.IsDir() {
			if path == basePath {
				return nil // Skip the bucket root
			}

			dirKey := relPath
			if !strings.HasSuffix(dirKey, "/") {
				dirKey += "/"
			}

			// In non-recursive mode, only add directories at current level
			if !recursive {
				// For direct children of the prefix, add them as prefixes
				relativeToPrefixPath := strings.TrimPrefix(dirKey, prefix)
				parts := strings.Split(strings.Trim(relativeToPrefixPath, "/"), "/")

				if len(parts) == 1 || (len(parts) == 0 && prefix == "") {
					// This is a direct child or the prefix itself
					addEntry(Entry{
						Key:      dirKey,
						IsPrefix: true,
					})
				}

				// Skip deeper directories in non-recursive mode
				if len(parts) > 1 {
					return filepath.SkipDir
				}
			}

			// In recursive mode, just continue to get files
			return nil
		}

		// Handle files
		if strings.HasPrefix(info.Name(), ".conditional-store-stage-") {
			return nil
		}
		if !recursive && delimiter != "" {
			// In non-recursive mode, check if file is in a subdirectory relative to prefix
			relativeToPrefixPath := strings.TrimPrefix(relPath, prefix)
			delimIndex := strings.Index(relativeToPrefixPath, delimiter)

			if delimIndex >= 0 {
				// File is in a subdirectory, add the prefix
				prefixPath := prefix + relativeToPrefixPath[:delimIndex+len(delimiter)]
				addEntry(Entry{
					Key:      prefixPath,
					IsPrefix: true,
				})
				return nil
			}
		}

		// Add file entry
		metadata, err := f.getMetadata(path)
		if err != nil {
			return nil // Skip files with errors
		}

		addEntry(Entry{
			Key:      relPath,
			IsPrefix: false,
			Metadata: metadata,
		})

		return nil
	})

	if err != nil {
		return nil, NewStoreError(ErrCodeIO, "List", parsedURI.Bucket, "Failed to list directory", err, true)
	}

	// Limit results if maxKeys is specified
	if maxKeys > 0 && len(entries) > maxKeys {
		entries = entries[:maxKeys]
	}

	return entries, nil
}

// Helper methods

func (f *FileStorage) putReader(
	ctx context.Context,
	key string,
	filePath string,
	reader io.Reader,
	opts *OperationOptions,
	operation string,
) (Metadata, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to create directory", err, true)
	}

	staged, err := os.CreateTemp(dir, ".conditional-store-stage-*")
	if err != nil {
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to create staged file", err, true)
	}
	stagedPath := staged.Name()
	cleanupStaged := true
	defer func() {
		if cleanupStaged {
			_ = os.Remove(stagedPath)
		}
	}()

	if err := staged.Chmod(0644); err != nil {
		_ = staged.Close()
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to set staged file permissions", err, true)
	}

	hash := md5.New()
	size, copyErr := io.Copy(io.MultiWriter(staged, hash), reader)
	if copyErr != nil {
		_ = staged.Close()
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to write staged file", copyErr, true)
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to sync staged file", err, true)
	}
	if err := staged.Close(); err != nil {
		return Metadata{}, NewStoreError(ErrCodeIO, operation, key, "Failed to close staged file", err, true)
	}

	metadata := Metadata{
		ETag:         hex.EncodeToString(hash.Sum(nil)),
		Size:         size,
		ContentType:  opts.ContentType.Or("application/octet-stream"),
		UserMetadata: make(map[string]string),
	}
	if opts.Metadata.IsSet() {
		metadata.UserMetadata = opts.Metadata.Cloned()
	}

	err = f.withFileLock(ctx, filePath, true, func() error {
		existingMetadata, snapshotErr := f.getMetadataUnlocked(filePath)
		fileExists := snapshotErr == nil
		if snapshotErr != nil && !IsErrorCode(snapshotErr, ErrCodeNotFound) {
			return snapshotErr
		}
		if conditionErr := conditionalHelper.ApplyConditionalPut(fileExists, existingMetadata, opts); conditionErr != nil {
			return conditionErr
		}

		if wantsIfNoneMatchStar(&opts.ConditionalOptions) {
			if linkErr := os.Link(stagedPath, filePath); linkErr != nil {
				if os.IsExist(linkErr) {
					return NewStoreError(ErrCodePreconditionFailed, operation, key, "Object already exists", linkErr, false)
				}
				return NewStoreError(ErrCodeIO, operation, key, "Failed to publish staged file", linkErr, true)
			}
			if removeErr := os.Remove(stagedPath); removeErr != nil {
				return NewStoreError(ErrCodeIO, operation, key, "Failed to remove staged link", removeErr, true)
			}
			cleanupStaged = false
		} else {
			if renameErr := os.Rename(stagedPath, filePath); renameErr != nil {
				return NewStoreError(ErrCodeIO, operation, key, "Failed to publish staged file", renameErr, true)
			}
			cleanupStaged = false
		}

		if syncErr := f.syncDirectory(dir); syncErr != nil {
			return syncErr
		}
		info, statErr := os.Stat(filePath)
		if statErr != nil {
			return NewStoreError(ErrCodeIO, operation, key, "Failed to stat published file", statErr, true)
		}
		metadata.LastModified = info.ModTime()
		return nil
	})
	if err != nil {
		return Metadata{}, withStoreErrorKey(err, operation, key)
	}
	return metadata, nil
}

func (f *FileStorage) withFileLock(ctx context.Context, filePath string, exclusive bool, fn func() error) error {
	sum := sha256.Sum256([]byte(filePath))
	lockFilePath := filepath.Join(f.lockPath, hex.EncodeToString(sum[:])+".lock")
	lockFile, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return NewStoreError(ErrCodeIO, "Lock", filePath, "Failed to open path lock", err, true)
	}
	defer lockFile.Close()

	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		err = syscall.Flock(int(lockFile.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return NewStoreError(ErrCodeIO, "Lock", filePath, "Failed to acquire path lock", err, true)
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func (f *FileStorage) readSnapshot(path string) ([]byte, Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Metadata{}, NewStoreError(ErrCodeNotFound, "Get", path, "File not found", err, false)
		}
		return nil, Metadata{}, NewStoreError(ErrCodeIO, "Get", path, "Failed to open file", err, true)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, Metadata{}, NewStoreError(ErrCodeIO, "Get", path, "Failed to read file", err, true)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, Metadata{}, NewStoreError(ErrCodeIO, "Get", path, "Failed to stat file", err, true)
	}
	metadata := Metadata{
		ETag:         f.calculateETag(data),
		LastModified: info.ModTime(),
		Size:         int64(len(data)),
		ContentType:  "application/octet-stream",
		UserMetadata: make(map[string]string),
	}
	return data, metadata, nil
}

func (f *FileStorage) syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return NewStoreError(ErrCodeIO, "Sync", dir, "Failed to open directory", err, true)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return NewStoreError(ErrCodeIO, "Sync", dir, "Failed to sync directory", err, true)
	}
	return nil
}

// constructPath converts bucket and key to filesystem path
func (f *FileStorage) constructPath(bucket, key string) string {
	return filepath.Join(f.rootPath, bucket, key)
}

// calculateETag generates an MD5 hash from data
func (f *FileStorage) calculateETag(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

// getMetadata retrieves file metadata
func (f *FileStorage) getMetadata(path string) (Metadata, error) {
	var metadata Metadata
	err := f.withFileLock(context.Background(), path, false, func() error {
		var metadataErr error
		metadata, metadataErr = f.getMetadataUnlocked(path)
		return metadataErr
	})
	return metadata, err
}

func (f *FileStorage) getMetadataUnlocked(path string) (Metadata, error) {
	_, metadata, err := f.readSnapshot(path)
	return metadata, err
}

// cleanupEmptyDirs removes empty directories
func (f *FileStorage) cleanupEmptyDirs(dirPath string) {
	// Don't try to remove anything at or above the root path
	if dirPath == f.rootPath || len(dirPath) <= len(f.rootPath) {
		return
	}

	// Read the directory
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}

	// If directory is empty, remove it and check parent
	if len(entries) == 0 {
		os.Remove(dirPath)
		f.cleanupEmptyDirs(filepath.Dir(dirPath))
	}
}

// FileStorageFactory creates a FileStorage from a URI
func FileStorageFactory(ctx context.Context, uri StorageURI) (Storage, error) {
	// Default to current directory if path not provided
	rootPath := "."

	// Check for root path in options
	if path := uri.Options.Get("path"); path != "" {
		rootPath = path
	} else if uri.Bucket != "" {
		// Use the bucket as a subdirectory
		rootPath = uri.Bucket
	}

	return NewFileStorage(rootPath)
}
