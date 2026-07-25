package store

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestParseURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantURI  StorageURI
		wantErr  bool
		errCheck func(error) bool
	}{
		{
			name: "Valid S3 URI",
			uri:  "s3://my-bucket/path/to/object",
			wantURI: StorageURI{
				Scheme: "s3",
				Bucket: "my-bucket",
				Key:    "path/to/object",
			},
			wantErr: false,
		},
		{
			name: "Valid file URI",
			uri:  "file://local-bucket/path/to/file.txt",
			wantURI: StorageURI{
				Scheme: "file",
				Bucket: "local-bucket",
				Key:    "path/to/file.txt",
			},
			wantErr: false,
		},
		{
			name: "URI with query params",
			uri:  "s3://my-bucket/path/to/object?version=1&acl=private",
			wantURI: StorageURI{
				Scheme:  "s3",
				Bucket:  "my-bucket",
				Key:     "path/to/object",
				Options: map[string][]string{"version": {"1"}, "acl": {"private"}},
			},
			wantErr: false,
		},
		{
			name:    "Missing scheme",
			uri:     "my-bucket/path/to/object",
			wantErr: true,
		},
		{
			name:    "Invalid URI format",
			uri:     "://invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseURI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				// Skip further checks if we expected an error
				return
			}

			if got.Scheme != tt.wantURI.Scheme {
				t.Errorf("ParseURI() scheme = %v, want %v", got.Scheme, tt.wantURI.Scheme)
			}
			if got.Bucket != tt.wantURI.Bucket {
				t.Errorf("ParseURI() bucket = %v, want %v", got.Bucket, tt.wantURI.Bucket)
			}
			if got.Key != tt.wantURI.Key {
				t.Errorf("ParseURI() key = %v, want %v", got.Key, tt.wantURI.Key)
			}

			// Check options if specified in test case
			if tt.wantURI.Options != nil {
				for k, v := range tt.wantURI.Options {
					if !equal(got.Options[k], v) {
						t.Errorf("ParseURI() options[%s] = %v, want %v", k, got.Options[k], v)
					}
				}
			}
		})
	}
}

// Helper to check if string slices are equal
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func TestStorageURI_Validate(t *testing.T) {
	tests := []struct {
		name    string
		uri     StorageURI
		wantErr bool
	}{
		{
			name: "Valid URI",
			uri: StorageURI{
				Scheme: "s3",
				Bucket: "my-bucket",
				Key:    "path/to/object",
			},
			wantErr: false,
		},
		{
			name: "Missing scheme",
			uri: StorageURI{
				Bucket: "my-bucket",
				Key:    "path/to/object",
			},
			wantErr: true,
		},
		{
			name: "Missing bucket",
			uri: StorageURI{
				Scheme: "s3",
				Key:    "path/to/object",
			},
			wantErr: true,
		},
		{
			name: "Missing key",
			uri: StorageURI{
				Scheme: "s3",
				Bucket: "my-bucket",
			},
			wantErr: false, // empty key is valid for bucket-level ops
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.uri.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("StorageURI.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStorageURI_String_And_Location(t *testing.T) {
	tests := []struct {
		name           string
		uri            StorageURI
		wantString     string
		wantLocation   string
		wantWithParams bool
	}{
		{
			name: "Basic URI",
			uri: StorageURI{
				Scheme: "s3",
				Bucket: "my-bucket",
				Key:    "path/to/object",
			},
			wantString:   "s3://my-bucket/path/to/object",
			wantLocation: "s3://my-bucket/path/to/object",
		},
		{
			name: "URI with query parameters",
			uri: StorageURI{
				Scheme:  "file",
				Bucket:  "my-bucket",
				Key:     "path/to/file",
				Options: url.Values{"version": []string{"1"}},
			},
			wantString:     "file://my-bucket/path/to/file?version=1",
			wantLocation:   "file://my-bucket/path/to/file",
			wantWithParams: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.uri.String()
			if got != tt.wantString {
				t.Errorf("StorageURI.String() = %v, want %v", got, tt.wantString)
			}

			location := tt.uri.Location()
			if location != tt.wantLocation {
				t.Errorf("StorageURI.Location() = %v, want %v", location, tt.wantLocation)
			}

			// If we're testing with params, make sure they aren't in Location but are in String
			if tt.wantWithParams {
				for k := range tt.uri.Options {
					if !strings.Contains(got, k) {
						t.Errorf("StorageURI.String() should contain param %v", k)
					}
					if strings.Contains(location, k) {
						t.Errorf("StorageURI.Location() should not contain param %v", k)
					}
				}
			}
		})
	}
}

func TestFormatLocationURI(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
		bucket string
		key    string
		want   string
	}{
		{
			name:   "Basic URI",
			scheme: "s3",
			bucket: "my-bucket",
			key:    "path/to/object",
			want:   "s3://my-bucket/path/to/object",
		},
		{
			name:   "Empty key",
			scheme: "file",
			bucket: "local-bucket",
			key:    "",
			want:   "file://local-bucket/",
		},
		{
			name:   "Key with special chars",
			scheme: "s3",
			bucket: "my-bucket",
			key:    "path/to/file with spaces.txt",
			want:   "s3://my-bucket/path/to/file with spaces.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatLocationURI(tt.scheme, tt.bucket, tt.key)
			if got != tt.want {
				t.Errorf("FormatLocationURI() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegisterURIScheme(t *testing.T) {
	// Clear any existing registration for the test scheme
	storageFactoriesMutex.Lock()
	delete(storageFactories, "test-scheme")
	storageFactoriesMutex.Unlock()

	// Register a mock factory
	mockStorage := &MockStorage{scheme: "test-scheme"}
	RegisterURIScheme("test-scheme", func(ctx context.Context, uri StorageURI) (Storage, error) {
		return mockStorage, nil
	})

	// Verify registration
	factory, found := GetStorageFactory("test-scheme")
	if !found {
		t.Fatal("Factory not registered")
	}

	// Test creating storage
	storage, err := factory(context.Background(), StorageURI{Scheme: "test-scheme"})
	if err != nil {
		t.Fatalf("Error creating storage: %v", err)
	}

	if storage != mockStorage {
		t.Error("Got wrong storage instance")
	}
}

func TestCreateStorageFromURI(t *testing.T) {
	// Register a mock factory
	mockStorage := &MockStorage{scheme: "create-test"}
	RegisterURIScheme("create-test", func(ctx context.Context, uri StorageURI) (Storage, error) {
		return mockStorage, nil
	})

	// Test valid creation
	storage, err := CreateStorageFromURI(context.Background(), "create-test://bucket/key")
	if err != nil {
		t.Fatalf("Error creating storage: %v", err)
	}

	if storage != mockStorage {
		t.Error("Got wrong storage instance")
	}

	// Test with unregistered scheme
	_, err = CreateStorageFromURI(context.Background(), "unknown-scheme://bucket/key")
	if err == nil {
		t.Error("Expected error for unregistered scheme")
	}

	// Test with invalid URI
	_, err = CreateStorageFromURI(context.Background(), "invalid")
	if err == nil {
		t.Error("Expected error for invalid URI")
	}
}

func TestStorageURI_String(t *testing.T) {
	uri := StorageURI{
		Scheme: "s3",
		Bucket: "test-bucket",
		Key:    "path/to/object",
	}
	expected := "s3://test-bucket/path/to/object"

	if uri.String() != expected {
		t.Errorf("String() = %v, want %v", uri.String(), expected)
	}

	// Test with query parameters
	uri.Options = map[string][]string{
		"version": {"1"},
		"acl":     {"public-read"},
	}

	result := uri.String()
	if !strings.Contains(result, "s3://test-bucket/path/to/object?") ||
		!strings.Contains(result, "version=1") ||
		!strings.Contains(result, "acl=public-read") {
		t.Errorf("String() with options = %v, doesn't contain expected parts", result)
	}
}

func TestStorageURI_Location(t *testing.T) {
	uri := StorageURI{
		Scheme:  "s3",
		Bucket:  "test-bucket",
		Key:     "path/to/object",
		Options: map[string][]string{"version": {"1"}},
	}
	expected := "s3://test-bucket/path/to/object"

	if uri.Location() != expected {
		t.Errorf("Location() = %v, want %v", uri.Location(), expected)
	}
}
