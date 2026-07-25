package store_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bryanmatteson/atomicstore/store"
	"github.com/bryanmatteson/atomicstore/storetest"
)

// TestS3DistributedLockConformance is intentionally opt-in because it performs
// real writes and retains released lease records. A skipped test means the S3
// deployment has NOT been certified; it must not be interpreted as a pass.
func TestS3DistributedLockConformance(t *testing.T) {
	if os.Getenv("CONDITIONAL_STORE_S3_CONFORMANCE") != "1" {
		t.Skip("NOT CERTIFIED: set CONDITIONAL_STORE_S3_CONFORMANCE=1 and CONDITIONAL_STORE_S3_BUCKET to test the real S3 deployment")
	}
	bucket := os.Getenv("CONDITIONAL_STORE_S3_BUCKET")
	if bucket == "" {
		t.Fatal("CONDITIONAL_STORE_S3_BUCKET is required")
	}
	region := os.Getenv("CONDITIONAL_STORE_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	endpoint := os.Getenv("CONDITIONAL_STORE_S3_ENDPOINT")
	contenders := 16
	if raw := os.Getenv("CONDITIONAL_STORE_S3_CONTENDERS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 2 {
			t.Fatalf("invalid CONDITIONAL_STORE_S3_CONTENDERS %q", raw)
		}
		contenders = value
	}
	prefix := os.Getenv("CONDITIONAL_STORE_S3_PREFIX")
	if prefix == "" {
		prefix = fmt.Sprintf("conditional-store-conformance/%d-%d/", time.Now().UnixNano(), os.Getpid())
	}

	factory := func(context.Context) (store.Storage, error) {
		options := []store.S3StorageOption{store.WithS3Region(region)}
		if endpoint != "" {
			options = append(options, store.WithS3Endpoint(endpoint))
		}
		return store.NewS3Storage(options...)
	}
	storetest.RunDistributedLockConformance(t, factory, storetest.DistributedLockConfig{
		BackendName: "S3 endpoint=" + endpoint,
		Bucket:      bucket,
		KeyPrefix:   prefix,
		Contenders:  contenders,
		TTL:         2 * time.Second,
		Grace:       250 * time.Millisecond,
		Timeout:     2 * time.Minute,
	})
}
