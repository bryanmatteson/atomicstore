package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// StorageURI represents a parsed storage URI
type StorageURI struct {
	Scheme  string     // Protocol (e.g., s3, file)
	Bucket  string     // Bucket or container name
	Key     string     // Object key or path
	Options url.Values // Additional options for the URI
}

// ParseURI parses a URI string into a StorageURI
func ParseURI(uriStr string) (StorageURI, error) {
	// Parse the URL
	parsedURL, err := url.Parse(uriStr)
	if err != nil {
		return StorageURI{}, fmt.Errorf("invalid URI format: %w", err)
	}

	if parsedURL.Scheme == "" {
		return StorageURI{}, fmt.Errorf("missing scheme")
	}

	var bucket, key string
	if parsedURL.Host != "" {
		// Prefer authority/host as bucket (s3://bucket/key, file://bucket/key)
		bucket = parsedURL.Host
		key = strings.TrimPrefix(parsedURL.Path, "/")
	} else {
		// Fallback for path-style URIs without a host
		path := strings.TrimPrefix(parsedURL.Path, "/")
		if idx := strings.Index(path, "/"); idx >= 0 {
			bucket = path[:idx]
			key = path[idx+1:]
		} else {
			bucket = path
			key = ""
		}
	}

	return StorageURI{
		Scheme:  parsedURL.Scheme,
		Bucket:  bucket,
		Key:     key,
		Options: parsedURL.Query(),
	}, nil
}

// String returns the string representation of the URI
func (u StorageURI) String() string {
	// Build the URL
	url := &url.URL{
		Scheme:   u.Scheme,
		Host:     u.Bucket,
		Path:     "/" + u.Key,
		RawQuery: u.Options.Encode(),
	}
	return url.String()
}

// Location returns the URI without query parameters
func (u StorageURI) Location() string {
	return fmt.Sprintf("%s://%s/%s", u.Scheme, u.Bucket, u.Key)
}

// Validate checks if the URI is valid
func (u StorageURI) Validate() error {
	if u.Scheme == "" {
		return fmt.Errorf("missing scheme")
	}
	if u.Bucket == "" {
		return fmt.Errorf("missing bucket")
	}
	// Key may be empty for bucket-level operations such as List.
	return nil
}

// FormatLocationURI creates a URI string from components
func FormatLocationURI(scheme, bucket, key string) string {
	if key == "" {
		return fmt.Sprintf("%s://%s/", scheme, bucket)
	}
	return fmt.Sprintf("%s://%s/%s", scheme, bucket, key)
}

// Storage factory functions map - used to create storage instances from URIs
var (
	storageFactories      = make(map[string]StorageFactory)
	storageFactoriesMutex sync.RWMutex
)

// StorageFactory creates a storage instance from a URI
type StorageFactory func(ctx context.Context, uri StorageURI) (Storage, error)

// RegisterURIScheme registers a factory function for a URI scheme
func RegisterURIScheme(scheme string, factory StorageFactory) {
	storageFactoriesMutex.Lock()
	defer storageFactoriesMutex.Unlock()
	storageFactories[scheme] = factory
}

// GetStorageFactory returns the factory function for a scheme
func GetStorageFactory(scheme string) (StorageFactory, bool) {
	storageFactoriesMutex.RLock()
	defer storageFactoriesMutex.RUnlock()
	factory, found := storageFactories[scheme]
	return factory, found
}

// CreateStorageFromURI creates a storage instance from a URI string
func CreateStorageFromURI(ctx context.Context, uriStr string) (Storage, error) {
	uri, err := ParseURI(uriStr)
	if err != nil {
		return nil, err
	}

	factory, ok := GetStorageFactory(uri.Scheme)
	if !ok {
		return nil, fmt.Errorf("unsupported URI scheme: %s", uri.Scheme)
	}

	return factory(ctx, uri)
}
