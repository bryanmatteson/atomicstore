package store

import (
	"errors"
	"fmt"
	"strings"
)

// Error codes for store operations
const (
	ErrCodeNotFound           = "NotFound"
	ErrCodeNotModified        = "NotModified"
	ErrCodePreconditionFailed = "PreconditionFailed"
	ErrCodeInvalidData        = "InvalidData"
	ErrCodeInvalidURI         = "InvalidURI"
	ErrCodeInvalidBucket      = "InvalidBucket"
	ErrCodeInvalidOperation   = "InvalidOperation"
	ErrCodeTransactionClosed  = "TransactionClosed"
	ErrCodeIO                 = "IOError"
	ErrCodeTimeout            = "Timeout"
	ErrCodeAccessDenied       = "AccessDenied"
	ErrCodeUnsupported        = "Unsupported"
	ErrCodeExceedsLimit       = "ExceedsLimit"
	ErrCodeAlreadyExists      = "AlreadyExists"
	ErrCodeLockHeld           = "LockHeld"
	ErrCodeLockNotHeld        = "LockNotHeld"
	ErrCodeLockExpired        = "LockExpired"
	ErrCodeFencingTokenStale  = "FencingTokenStale"
)

// StoreError is the error type returned by store operations
type StoreError struct {
	// Code is the error classification code
	Code string

	// Operation is the operation that failed
	Operation string

	// Key is the key that was being operated on
	Key string

	// Message contains error details
	Message string

	// Cause is the underlying error that caused this one
	Cause error

	// Retriable indicates whether the operation can be retried
	Retriable bool
}

// Error implements the error interface
func (e *StoreError) Error() string {
	var sb strings.Builder
	sb.WriteString(e.Code)
	sb.WriteString(": ")

	if e.Operation != "" {
		sb.WriteString(e.Operation)
		sb.WriteString(" ")
	}

	if e.Key != "" {
		sb.WriteString("'")
		sb.WriteString(e.Key)
		sb.WriteString("' ")
	}

	sb.WriteString(e.Message)

	if e.Cause != nil {
		sb.WriteString(": ")
		sb.WriteString(e.Cause.Error())
	}

	return sb.String()
}

// Is implements errors.Is interface for StoreError
func (e *StoreError) Is(target error) bool {
	t, ok := target.(*StoreError)
	if !ok {
		return false
	}

	// Match on code if provided
	if t.Code != "" && t.Code != e.Code {
		return false
	}

	// Match on operation if provided
	if t.Operation != "" && t.Operation != e.Operation {
		return false
	}

	// Match on key if provided
	if t.Key != "" && t.Key != e.Key {
		return false
	}

	return true
}

// Unwrap returns the underlying cause
func (e *StoreError) Unwrap() error {
	return e.Cause
}

// GetCode returns the error code
func (e *StoreError) GetCode() string {
	return e.Code
}

// IsRetriable returns whether the error is retriable
func (e *StoreError) IsRetriable() bool {
	return e.Retriable
}

// NewStoreError creates a new StoreError
func NewStoreError(code, operation, key, message string, cause error, retriable bool) *StoreError {
	return &StoreError{
		Code:      code,
		Operation: operation,
		Key:       key,
		Message:   message,
		Cause:     cause,
		Retriable: retriable,
	}
}

// IsErrorCode checks if an error has a specific error code
func IsErrorCode(err error, code string) bool {
	var storeErr *StoreError
	if AsStoreError(err, &storeErr) {
		return storeErr.Code == code
	}
	return false
}

// IsRetriable determines if an error is retriable
func IsRetriable(err error) bool {
	var storeErr *StoreError
	if AsStoreError(err, &storeErr) {
		return storeErr.Retriable
	}

	// By default, treat unknown errors as not retriable
	return false
}

// AsStoreError attempts to convert an error to a StoreError
func AsStoreError(err error, target **StoreError) bool {
	return As(err, target)
}

// As is a helper for errors.As that works with nil errors
func As(err error, target any) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

// NewInvalidURIError creates an error for invalid URIs
func NewInvalidURIError(operation, uri string, cause error) *StoreError {
	return NewStoreError(
		ErrCodeInvalidURI,
		operation,
		uri,
		"Invalid URI",
		cause,
		false,
	)
}

// Error translation helpers for common storage services

// TranslateAWSError converts AWS SDK errors to StoreErrors
func TranslateAWSError(operation, key string, err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	// Check for specific error types
	switch {
	case strings.Contains(errMsg, "NoSuchKey") || strings.Contains(errMsg, "NotFound"):
		return NewStoreError(ErrCodeNotFound, operation, key, "Object not found", err, false)

	case strings.Contains(errMsg, "PreconditionFailed"):
		return NewStoreError(ErrCodePreconditionFailed, operation, key, "Precondition failed", err, false)

	case strings.Contains(errMsg, "NotModified"):
		return NewStoreError(ErrCodeNotModified, operation, key, "Not modified", err, false)

	case strings.Contains(errMsg, "AccessDenied") || strings.Contains(errMsg, "Forbidden"):
		return NewStoreError(ErrCodeAccessDenied, operation, key, "Access denied", err, false)

	case strings.Contains(errMsg, "Timeout") || strings.Contains(errMsg, "RequestTimeout"):
		return NewStoreError(ErrCodeTimeout, operation, key, "Request timed out", err, true)

	case strings.Contains(errMsg, "SlowDown") || strings.Contains(errMsg, "ThrottlingException"):
		return NewStoreError(ErrCodeIO, operation, key, "Request throttled", err, true)

	default:
		// Generic IO error
		return NewStoreError(ErrCodeIO, operation, key, fmt.Sprintf("Storage error: %s", errMsg), err, true)
	}
}
