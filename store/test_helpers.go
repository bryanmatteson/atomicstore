package store

import (
	"context"
	"testing"

	"github.com/bryanmatteson/atomicstore/codec"
)

// SetupTestStore creates a store with mock storage for testing
func SetupTestStore(t *testing.T) (*Store[TestEntity], *MockStorage) {
	t.Helper()

	// Create mock storage
	mockStorage := NewMockStorage("mock")

	// Get JSON codec
	jsonCodec, err := codec.Get("json")
	if err != nil {
		t.Fatalf("Failed to get JSON codec: %v", err)
	}

	// Create store
	testStore, err := NewStore[TestEntity](
		mockStorage,
		"test-bucket",
		WithCodec(jsonCodec),
	)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	return testStore, mockStorage
}

// CreateTestEntity creates and stores a test entity
func CreateTestEntity(
	t *testing.T,
	ctx context.Context,
	s *Store[TestEntity],
	key string,
	name string,
	value int,
) *TestEntity {
	t.Helper()

	entity := NewTestEntity(name, value)
	entity.ID = key
	entity.Version = 1

	_, err := s.Put(ctx, key, *entity)
	if err != nil {
		t.Fatalf("Failed to put test entity: %v", err)
	}

	return entity
}

// AssertNoError asserts that an error is nil
func AssertNoError(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()

	if err != nil {
		if len(msgAndArgs) == 0 {
			t.Fatalf("Unexpected error: %v", err)
		} else {
			msg := msgAndArgs[0].(string)
			args := msgAndArgs[1:]
			t.Fatalf("Unexpected error: "+msg+": %v", append(args, err)...)
		}
	}
}

// AssertError asserts that an error is not nil
func AssertError(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()

	if err == nil {
		if len(msgAndArgs) == 0 {
			t.Fatal("Expected error, got nil")
		} else {
			msg := msgAndArgs[0].(string)
			args := msgAndArgs[1:]
			t.Fatalf("Expected error, got nil: "+msg, args...)
		}
	}
}

// AssertEqual asserts that two values are equal
func AssertEqual[T comparable](t *testing.T, expected, actual T, msgAndArgs ...any) {
	t.Helper()

	if expected != actual {
		if len(msgAndArgs) == 0 {
			t.Fatalf("Expected %v, got %v", expected, actual)
		} else {
			msg := msgAndArgs[0].(string)
			args := msgAndArgs[1:]
			t.Fatalf("Expected %v, got %v: "+msg, append([]any{expected, actual}, args...)...)
		}
	}
}

// AssertErrorCode asserts that an error has the expected error code
func AssertErrorCode(t *testing.T, err error, expectedCode string) {
	t.Helper()

	if err == nil {
		t.Fatalf("Expected error with code %s, got nil", expectedCode)
		return
	}

	if !IsErrorCode(err, expectedCode) {
		t.Fatalf("Expected error with code %s, got %v", expectedCode, err)
	}
}

// AssertTrue asserts that a condition is true
func AssertTrue(t *testing.T, condition bool, msgAndArgs ...any) {
	t.Helper()

	if !condition {
		if len(msgAndArgs) == 0 {
			t.Fatal("Condition is false")
		} else {
			msg := msgAndArgs[0].(string)
			args := msgAndArgs[1:]
			t.Fatalf("Condition is false: "+msg, args...)
		}
	}
}
