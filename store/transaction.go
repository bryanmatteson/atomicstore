package store

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Transaction states
const (
	TxStateActive      = "active"
	TxStatePreparing   = "preparing"
	TxStateCommitting  = "committing"
	TxStateCommitted   = "committed"
	TxStateRollingBack = "rolling-back"
	TxStateRolledBack  = "rolled-back"
	TxStateAborted     = "aborted"
)

// TransactionLog represents a log entry for audit and recovery
type TransactionLog struct {
	TxID      string
	Operation string
	URI       string
	Timestamp time.Time
	Metadata  map[string]string
	Success   bool
	ErrorMsg  string
}

// Transaction extends Transaction with additional capabilities
type Transaction interface {
	// Get retrieves an item from the transaction or underlying storage
	Get(uri string, options ...TransactionGetOption) ([]byte, Metadata, error)

	// Put adds or updates an item in the transaction
	Put(uri string, data []byte, options ...TransactionPutOption) error

	// Delete marks an item for deletion
	Delete(uri string, options ...TransactionDeleteOption) error

	// Head retrieves metadata for an item
	Head(uri string, options ...TransactionHeadOption) (Metadata, error)

	// Commit applies all pending changes to the underlying storage
	Commit(ctx context.Context) error

	// Rollback discards all pending changes
	Rollback() error

	// GetID returns the unique transaction ID
	GetID() string

	// GetState returns the current transaction state
	GetState() string

	// GetStartTime returns when the transaction was created
	GetStartTime() time.Time

	// GetOperations returns all operations in this transaction
	GetOperations() []string

	// Prepare verifies operations can be completed without executing them
	Prepare(ctx context.Context) error

	// AddHook adds a function to be called at a transaction lifecycle event
	AddHook(event string, hook func(Transaction) error)

	// GetLogs returns the transaction logs
	GetLogs() []TransactionLog
}

// StorageTransaction implements the enhanced transaction interface
type StorageTransaction struct {
	ctx           context.Context
	txID          string
	storage       Storage
	operations    map[string]*operation
	opOrder       []string // Maintains operation order
	items         map[string][]byte
	metadata      map[string]Metadata
	state         string
	startTime     time.Time
	lastActivity  time.Time
	timeout       time.Duration
	readOnly      bool
	mutex         sync.RWMutex
	hooks         map[string][]func(Transaction) error
	logs          []TransactionLog
	allowPartial  bool
	retryStrategy RetryStrategy
	txLogger      TransactionLogger
	metrics       TransactionMetrics
	closed        bool
}

// RetryStrategy defines how operations are retried
type RetryStrategy interface {
	// ShouldRetry determines if an operation should be retried
	ShouldRetry(err error, attempt int) bool

	// GetDelay calculates the delay before the next retry
	GetDelay(attempt int) time.Duration
}

// TransactionLogger handles transaction logging
type TransactionLogger interface {
	// LogOperation records an operation in the transaction
	LogOperation(log TransactionLog)

	// GetLogs retrieves logs for a transaction
	GetLogs(txID string) []TransactionLog
}

// TransactionMetrics collects metrics about transactions
type TransactionMetrics interface {
	// RecordOperation records timing for an operation
	RecordOperation(txID, operation string, duration time.Duration)

	// RecordOutcome records transaction outcome
	RecordOutcome(txID, outcome string, duration time.Duration)
}

// DefaultRetryStrategy retries retriable store errors with exponential backoff.
type DefaultRetryStrategy struct{}

func (DefaultRetryStrategy) ShouldRetry(err error, attempt int) bool {
	return attempt < 5 && IsRetriable(err)
}

func (DefaultRetryStrategy) GetDelay(attempt int) time.Duration {
	d := time.Duration(attempt) * 50 * time.Millisecond
	if d > time.Second {
		return time.Second
	}
	return d
}

// NoOpTransactionLogger discards transaction logs.
type NoOpTransactionLogger struct{}

func (NoOpTransactionLogger) LogOperation(log TransactionLog) {}
func (NoOpTransactionLogger) GetLogs(txID string) []TransactionLog {
	return nil
}

// NoOpTransactionMetrics discards transaction metrics.
type NoOpTransactionMetrics struct{}

func (NoOpTransactionMetrics) RecordOperation(txID, operation string, duration time.Duration) {}
func (NoOpTransactionMetrics) RecordOutcome(txID, outcome string, duration time.Duration)     {}

// NewStorageTransaction creates a new storage transaction.
// Kept as the primary constructor name used by Store and tests.
func NewStorageTransaction(ctx context.Context, storage Storage, options ...TransactionOption) *StorageTransaction {
	return NewEnhancedTransaction(ctx, storage, options...)
}

// NewEnhancedTransaction creates a new enhanced transaction
func NewEnhancedTransaction(ctx context.Context, storage Storage, options ...TransactionOption) *StorageTransaction {
	// Generate a unique transaction ID
	txID := generateTransactionID()

	// Apply options
	opts := &TransactionOptions{}
	applyTransactionOptions(opts, options)

	tx := &StorageTransaction{
		ctx:           ctx,
		txID:          txID,
		storage:       storage,
		operations:    make(map[string]*operation),
		opOrder:       make([]string, 0),
		items:         make(map[string][]byte),
		metadata:      make(map[string]Metadata),
		state:         TxStateActive,
		startTime:     time.Now(),
		lastActivity:  time.Now(),
		timeout:       opts.Timeout.Or(5 * time.Minute),
		readOnly:      opts.ReadOnly.Or(false),
		hooks:         make(map[string][]func(Transaction) error),
		logs:          make([]TransactionLog, 0),
		allowPartial:  opts.AllowPartial.Or(false),
		retryStrategy: opts.RetryStrategy.Or(DefaultRetryStrategy{}),
		txLogger:      opts.TxLogger.Or(NoOpTransactionLogger{}),
		metrics:       opts.TxMetrics.Or(NoOpTransactionMetrics{}),
	}

	// Register transaction with coordinator if available
	if coordinator := GetTransactionCoordinator(); coordinator != nil {
		coordinator.RegisterTransaction(tx)
	}

	return tx
}

// Prepare validates the staged operation set without publishing it.
func (tx *StorageTransaction) Prepare(ctx context.Context) error {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()

	// Check transaction state
	if tx.state != TxStateActive {
		return NewStoreError(ErrCodeInvalidOperation, "Prepare", tx.txID,
			fmt.Sprintf("Transaction is in %s state", tx.state), nil, false)
	}

	// Update state
	tx.state = TxStatePreparing
	tx.lastActivity = time.Now()

	startTime := time.Now()
	defer func() {
		tx.metrics.RecordOperation(tx.txID, "prepare", time.Since(startTime))
	}()

	// Run pre-prepare hooks
	if err := tx.runHooks("pre-prepare"); err != nil {
		tx.state = TxStateAborted
		return err
	}

	// Validate each operation
	for uri, op := range tx.operations {
		if err := tx.validateOperation(ctx, uri, op); err != nil {
			tx.logOperation("prepare", uri, false, err.Error())
			tx.state = TxStateAborted
			return err
		}
		tx.logOperation("prepare", uri, true, "")
	}

	// Run post-prepare hooks
	if err := tx.runHooks("post-prepare"); err != nil {
		tx.state = TxStateAborted
		return err
	}

	return nil
}

// Commit publishes one mutation, an explicit best-effort batch, or an atomic
// batch when the backend implements AtomicBatchStorage.
func (tx *StorageTransaction) Commit(ctx context.Context) error {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()

	// Check transaction state
	if tx.state != TxStateActive && tx.state != TxStatePreparing {
		code := ErrCodeInvalidOperation
		if tx.closed || tx.state == TxStateCommitted || tx.state == TxStateRolledBack || tx.state == TxStateAborted {
			code = ErrCodeTransactionClosed
		}
		return NewStoreError(code, "Commit", tx.txID,
			fmt.Sprintf("Transaction is in %s state", tx.state), nil, false)
	}

	// Auto-prepare if not already prepared
	if tx.state == TxStateActive {
		tx.mutex.Unlock() // Temporarily release lock for prepare
		if err := tx.Prepare(ctx); err != nil {
			tx.mutex.Lock() // Re-acquire lock
			return err
		}
		tx.mutex.Lock() // Re-acquire lock
	}

	// Update state
	tx.state = TxStateCommitting
	tx.lastActivity = time.Now()

	startTime := time.Now()
	defer func() {
		tx.metrics.RecordOutcome(tx.txID, "commit", time.Since(startTime))
	}()

	// Run pre-commit hooks
	if err := tx.runHooks("pre-commit"); err != nil {
		// Rollback on hook failure
		tx.mutex.Unlock() // Release lock for rollback
		tx.Rollback()
		tx.mutex.Lock() // Re-acquire lock
		return err
	}

	var commitErr error
	if len(tx.opOrder) > 1 && !tx.allowPartial {
		atomicStorage, ok := tx.storage.(AtomicBatchStorage)
		if !ok {
			commitErr = NewStoreError(
				ErrCodeUnsupported,
				"Commit",
				tx.txID,
				"backend does not support atomic multi-object batches",
				nil,
				false,
			)
		} else {
			batch := make([]AtomicBatchOperation, 0, len(tx.opOrder))
			for _, uri := range tx.opOrder {
				op := tx.operations[uri]
				batch = append(batch, AtomicBatchOperation{
					Type:    op.opType,
					URI:     uri,
					Data:    append([]byte(nil), op.data...),
					Options: op.resolvedOptions,
				})
			}
			commitErr = atomicStorage.ApplyAtomicBatch(ctx, batch)
			if commitErr == nil {
				for _, uri := range tx.opOrder {
					tx.logOperation("commit", uri, true, "")
				}
			}
		}
	} else {
		for _, uri := range tx.opOrder {
			op := tx.operations[uri]
			err := tx.applyOperationWithRetry(ctx, uri, op)
			if err != nil {
				tx.logOperation("commit", uri, false, err.Error())
				commitErr = err
				if !tx.allowPartial {
					break
				}
			} else {
				tx.logOperation("commit", uri, true, "")
			}
		}
	}

	if commitErr != nil && !tx.allowPartial {
		tx.state = TxStateAborted
		tx.runHooks("post-rollback")
		return commitErr
	}

	// Update state
	tx.state = TxStateCommitted
	tx.closed = true

	// Run post-commit hooks
	tx.runHooks("post-commit")

	// Unregister from coordinator
	if coordinator := GetTransactionCoordinator(); coordinator != nil {
		coordinator.UnregisterTransaction(tx.txID)
	}

	return commitErr
}

// Rollback aborts the transaction and rolls back changes
func (tx *StorageTransaction) Rollback() error {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()

	// Check if already committed or rolled back
	if tx.closed || tx.state == TxStateCommitted || tx.state == TxStateRolledBack {
		return NewStoreError(ErrCodeTransactionClosed, "Rollback", tx.txID,
			fmt.Sprintf("Transaction is in %s state", tx.state), nil, false)
	}

	// Update state
	tx.state = TxStateRollingBack
	tx.lastActivity = time.Now()

	startTime := time.Now()
	defer func() {
		tx.metrics.RecordOutcome(tx.txID, "rollback", time.Since(startTime))
	}()

	// Run pre-rollback hooks
	tx.runHooks("pre-rollback")

	// Clear operation state
	tx.operations = make(map[string]*operation)
	tx.opOrder = tx.opOrder[:0]

	// Update state
	tx.state = TxStateRolledBack
	tx.closed = true

	// Run post-rollback hooks
	tx.runHooks("post-rollback")

	// Unregister from coordinator
	if coordinator := GetTransactionCoordinator(); coordinator != nil {
		coordinator.UnregisterTransaction(tx.txID)
	}

	return nil
}

// validateOperation checks if an operation can be performed
func (tx *StorageTransaction) validateOperation(ctx context.Context, uri string, op *operation) error {
	switch op.opType {
	case "put":
		// Check if the item exists and if conditions are met
		_, err := tx.storage.Head(ctx, uri)
		if err != nil {
			if !IsErrorCode(err, ErrCodeNotFound) {
				return err
			}
			// Not found is OK for put operations without If-Match
		} else {
			// Item exists, check conditions
			for _, option := range op.options {
				if putOpts, ok := option.([]PutOption); ok {
					for _, opt := range putOpts {
						_ = opt // conditions validated at commit apply time
					}
				}
			}
		}

	case "delete":
		// Check if the item exists
		_, err := tx.storage.Head(ctx, uri)
		if err != nil {
			return err
		}

		// Check delete conditions
		for _, option := range op.options {
			if deleteOpts, ok := option.([]DeleteOption); ok {
				for _, opt := range deleteOpts {
					_ = opt // conditions validated at commit apply time
				}
			}
		}
	}

	return nil
}

// applyOperationWithRetry applies an operation with retry logic
func (tx *StorageTransaction) applyOperationWithRetry(ctx context.Context, uri string, op *operation) error {
	var lastErr error

	for attempt := 1; attempt <= 5; attempt++ {
		// Apply the operation
		err := tx.applyOperation(ctx, uri, op)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry
		if !tx.retryStrategy.ShouldRetry(err, attempt) {
			break
		}

		// Wait before retrying
		delay := tx.retryStrategy.GetDelay(attempt)
		select {
		case <-time.After(delay):
			// Continue with retry
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastErr
}

// applyOperation applies a single operation
func (tx *StorageTransaction) applyOperation(ctx context.Context, uri string, op *operation) error {
	switch op.opType {
	case "put":
		_, err := tx.storage.Put(ctx, uri, op.data, op.resolvedOptions)
		return err

	case "delete":
		return tx.storage.Delete(ctx, uri, op.resolvedOptions)
	}

	return fmt.Errorf("unknown operation type: %s", op.opType)
}

// logOperation adds an entry to the transaction log
func (tx *StorageTransaction) logOperation(operation, uri string, success bool, errorMsg string) {
	log := TransactionLog{
		TxID:      tx.txID,
		Operation: operation,
		URI:       uri,
		Timestamp: time.Now(),
		Metadata:  make(map[string]string),
		Success:   success,
		ErrorMsg:  errorMsg,
	}

	tx.logs = append(tx.logs, log)
	tx.txLogger.LogOperation(log)
}

// runHooks executes hooks for a specific event
func (tx *StorageTransaction) runHooks(event string) error {
	for _, hook := range tx.hooks[event] {
		if err := hook(tx); err != nil {
			return err
		}
	}
	return nil
}

// GetID returns the transaction ID
func (tx *StorageTransaction) GetID() string {
	tx.mutex.RLock()
	defer tx.mutex.RUnlock()
	return tx.txID
}

// GetState returns the current transaction state
func (tx *StorageTransaction) GetState() string {
	tx.mutex.RLock()
	defer tx.mutex.RUnlock()
	return tx.state
}

// GetStartTime returns when the transaction started
func (tx *StorageTransaction) GetStartTime() time.Time {
	tx.mutex.RLock()
	defer tx.mutex.RUnlock()
	return tx.startTime
}

// GetOperations returns all operations in the transaction
func (tx *StorageTransaction) GetOperations() []string {
	tx.mutex.RLock()
	defer tx.mutex.RUnlock()
	return append([]string{}, tx.opOrder...)
}

// AddHook adds a function to be called at a transaction lifecycle event
func (tx *StorageTransaction) AddHook(event string, hook func(Transaction) error) {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()
	tx.hooks[event] = append(tx.hooks[event], hook)
}

// GetLogs returns the transaction logs
func (tx *StorageTransaction) GetLogs() []TransactionLog {
	tx.mutex.RLock()
	defer tx.mutex.RUnlock()
	return append([]TransactionLog{}, tx.logs...)
}

// lookupForCondition resolves current object existence/metadata for conditional checks.
// Prefers in-transaction state, then underlying storage.
func (tx *StorageTransaction) lookupForCondition(uri string) (bool, Metadata, error) {
	if op, ok := tx.operations[uri]; ok {
		if op.opType == "delete" {
			return false, Metadata{}, nil
		}
		if op.opType == "put" {
			etag := calculateETag(op.data)
			return true, Metadata{ETag: etag, Size: int64(len(op.data))}, nil
		}
	}
	if meta, ok := tx.metadata[uri]; ok {
		return true, meta, nil
	}
	if _, ok := tx.items[uri]; ok {
		return true, Metadata{}, nil
	}
	meta, err := tx.storage.Head(tx.ctx, uri)
	if err != nil {
		if IsErrorCode(err, ErrCodeNotFound) {
			return false, Metadata{}, nil
		}
		return false, Metadata{}, err
	}
	tx.metadata[uri] = meta
	return true, meta, nil
}

// Put overrides the original Put method to maintain operation order
func (tx *StorageTransaction) Put(uri string, data []byte, options ...TransactionPutOption) error {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()

	if err := tx.checkState(); err != nil {
		return err
	}

	// Check transaction state
	if tx.state != TxStateActive {
		return NewStoreError(ErrCodeTransactionClosed, "Put", uri,
			fmt.Sprintf("Transaction is in %s state", tx.state), nil, false)
	}

	// Update last activity
	tx.lastActivity = time.Now()

	// Check if transaction is read-only
	if tx.readOnly {
		return NewStoreError(ErrCodeInvalidOperation, "Put", uri, "Transaction is read-only", nil, false)
	}

	// Apply options
	opts := &OperationOptions{}
	for _, opt := range options {
		opt.applyPut(opts)
	}

	exists, existingMeta, err := tx.lookupForCondition(uri)
	if err != nil {
		return err
	}
	if err := conditionalHelper.ApplyConditionalPut(exists, existingMeta, opts); err != nil {
		return withStoreErrorKey(err, "Put", uri)
	}
	if !opts.IfMatch.IsSet() && !opts.IfNoMatch.IsSet() {
		if prior, ok := tx.operations[uri]; ok {
			opts.ConditionalOptions = prior.resolvedOptions.ConditionalOptions
		} else if exists {
			opts.IfMatch.SetRight(existingMeta.ETag)
		} else {
			opts.IfNoMatch.SetRight("*")
		}
	}

	// Add to operations map and maintain order
	tx.operations[uri] = &operation{
		opType:          "put",
		uri:             uri,
		data:            data,
		options:         []any{options},
		resolvedOptions: *opts,
	}

	// Add to ordered list if not already there
	inOrder := false
	for _, existingUri := range tx.opOrder {
		if existingUri == uri {
			inOrder = true
			break
		}
	}

	if !inOrder {
		tx.opOrder = append(tx.opOrder, uri)
	}

	// Log the operation
	tx.logOperation("prepare-put", uri, true, "")

	return nil
}

// Delete overrides the original Delete method to maintain operation order
func (tx *StorageTransaction) Delete(uri string, options ...TransactionDeleteOption) error {
	tx.mutex.Lock()
	defer tx.mutex.Unlock()

	if err := tx.checkState(); err != nil {
		return err
	}

	// Check transaction state
	if tx.state != TxStateActive {
		return NewStoreError(ErrCodeTransactionClosed, "Delete", uri,
			fmt.Sprintf("Transaction is in %s state", tx.state), nil, false)
	}

	// Update last activity
	tx.lastActivity = time.Now()

	// Check if transaction is read-only
	if tx.readOnly {
		return NewStoreError(ErrCodeInvalidOperation, "Delete", uri, "Transaction is read-only", nil, false)
	}

	// Apply options
	opts := &OperationOptions{}
	for _, opt := range options {
		opt.applyDelete(opts)
	}

	exists, existingMeta, err := tx.lookupForCondition(uri)
	if err != nil {
		return err
	}
	if err := conditionalHelper.ApplyConditionalDelete(exists, existingMeta, &opts.ConditionalOptions); err != nil {
		return withStoreErrorKey(err, "Delete", uri)
	}
	if !exists {
		return NewStoreError(ErrCodeNotFound, "Delete", uri, "Object not found", nil, false)
	}
	if !opts.IfMatch.IsSet() {
		if prior, ok := tx.operations[uri]; ok {
			opts.ConditionalOptions = prior.resolvedOptions.ConditionalOptions
		} else {
			opts.IfMatch.SetRight(existingMeta.ETag)
		}
	}

	// Add to operations map and maintain order
	tx.operations[uri] = &operation{
		opType:          "delete",
		uri:             uri,
		options:         []any{options},
		resolvedOptions: *opts,
	}

	// Add to ordered list if not already there
	inOrder := false
	for _, existingUri := range tx.opOrder {
		if existingUri == uri {
			inOrder = true
			break
		}
	}

	if !inOrder {
		tx.opOrder = append(tx.opOrder, uri)
	}

	// Log the operation
	tx.logOperation("prepare-delete", uri, true, "")

	return nil
}

// checkState verifies the transaction is valid and not expired
func (tx *StorageTransaction) checkState() error {
	// Check if transaction is closed
	if tx.closed {
		return NewStoreError(ErrCodeTransactionClosed, "Transaction", "", "Transaction is closed", nil, false)
	}

	// Check if transaction has timed out
	if tx.timeout > 0 && time.Since(tx.startTime) > tx.timeout {
		return NewStoreError(ErrCodeTimeout, "Transaction", "", "Transaction timed out", nil, false)
	}

	return nil
}

// Get retrieves an item by URI
func (tx *StorageTransaction) Get(uri string, options ...TransactionGetOption) ([]byte, Metadata, error) {
	// Check if transaction is closed or timed out
	if err := tx.checkState(); err != nil {
		return nil, Metadata{}, err
	}

	tx.mutex.RLock()

	// Check if the item is in our operation map as a delete
	if op, exists := tx.operations[uri]; exists && op.opType == "delete" {
		tx.mutex.RUnlock()
		return nil, Metadata{}, NewStoreError(ErrCodeNotFound, "Get", uri, "Object marked for deletion in transaction", nil, false)
	}

	// Check if the item is in our operation map as a put
	if op, exists := tx.operations[uri]; exists && op.opType == "put" {
		// For put operations, return the data and basic metadata
		etag := calculateETag(op.data)
		metadata := Metadata{
			ETag:         etag,
			LastModified: time.Now(),
			Size:         int64(len(op.data)),
		}

		// Apply conditional options
		opts := &OperationOptions{}
		applyGetOptions(opts, options)

		// Handle ETag conditions
		if opts.IfMatch.IsRight() && metadata.ETag != opts.IfMatch.Right() {
			tx.mutex.RUnlock()
			return nil, Metadata{}, NewStoreError(ErrCodePreconditionFailed, "Get", uri, "ETag doesn't match", nil, false)
		}

		if opts.IfNoMatch.IsRight() && metadata.ETag == opts.IfNoMatch.Right() {
			tx.mutex.RUnlock()
			return nil, metadata, NewStoreError(ErrCodeNotModified, "Get", uri, "Not modified", nil, false)
		}

		tx.mutex.RUnlock()
		return op.data, metadata, nil
	}

	// Check if we have already fetched this item
	if data, exists := tx.items[uri]; exists {
		metadata := tx.metadata[uri]

		// Apply conditional options
		opts := &OperationOptions{}
		applyGetOptions(opts, options)

		// Handle ETag conditions
		if opts.IfMatch.IsRight() && metadata.ETag != opts.IfMatch.Right() {
			tx.mutex.RUnlock()
			return nil, Metadata{}, NewStoreError(ErrCodePreconditionFailed, "Get", uri, "ETag doesn't match", nil, false)
		}

		if opts.IfNoMatch.IsRight() && metadata.ETag == opts.IfNoMatch.Right() {
			tx.mutex.RUnlock()
			return nil, metadata, NewStoreError(ErrCodeNotModified, "Get", uri, "Not modified", nil, false)
		}

		tx.mutex.RUnlock()
		return data, metadata, nil
	}

	// Release read lock before making external call
	tx.mutex.RUnlock()

	// Apply options
	opts := &TransactionOptions{}
	applyTransactionOptions(opts, options)
	applyGetOptions(&opts.OperationOptions, options)

	// If not in transaction, get from storage
	data, metadata, err := tx.storage.Get(tx.ctx, uri, opts.ConditionalOptions)
	if err != nil {
		return nil, Metadata{}, err
	}

	// Cache the item - must acquire write lock
	tx.mutex.Lock()
	tx.items[uri] = data
	tx.metadata[uri] = metadata
	tx.mutex.Unlock()

	return data, metadata, nil
}

// Head retrieves metadata for an item
func (tx *StorageTransaction) Head(uri string, options ...TransactionHeadOption) (Metadata, error) {
	// Check if transaction is closed or timed out
	if err := tx.checkState(); err != nil {
		return Metadata{}, err
	}

	tx.mutex.RLock()
	defer tx.mutex.RUnlock()

	// Check if the item is in our operation map as a delete
	if op, exists := tx.operations[uri]; exists && op.opType == "delete" {
		return Metadata{}, NewStoreError(ErrCodeNotFound, "Head", uri, "Object marked for deletion in transaction", nil, false)
	}

	// Check if the item is in our operation map as a put
	if op, exists := tx.operations[uri]; exists && op.opType == "put" {
		// For put operations, return basic metadata
		etag := calculateETag(op.data)
		return Metadata{
			ETag:         etag,
			LastModified: time.Now(),
			Size:         int64(len(op.data)),
		}, nil
	}

	// Check if we have already fetched this item's metadata
	if metadata, exists := tx.metadata[uri]; exists {
		return metadata, nil
	}

	opts := &OperationOptions{}
	applyHeadOptions(opts, options)

	// If not in transaction, get from storage
	metadata, err := tx.storage.Head(tx.ctx, uri, opts)
	if err != nil {
		return Metadata{}, err
	}

	// Cache the metadata
	tx.metadata[uri] = metadata
	return metadata, nil
}

// generateTransactionID creates a unique ID for the transaction
func generateTransactionID() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		// Fall back to time-based ID if random fails
		return fmt.Sprintf("tx-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("tx-%s", hex.EncodeToString(b))
}

// TransactionCoordinator manages distributed transactions
type TransactionCoordinator struct {
	transactions map[string]Transaction
	mutex        sync.RWMutex
}

// Global transaction coordinator
var (
	globalCoordinator     *TransactionCoordinator
	globalCoordinatorOnce sync.Once
)

// GetTransactionCoordinator returns the global transaction coordinator
func GetTransactionCoordinator() *TransactionCoordinator {
	globalCoordinatorOnce.Do(func() {
		globalCoordinator = &TransactionCoordinator{
			transactions: make(map[string]Transaction),
		}
	})
	return globalCoordinator
}

// RegisterTransaction adds a transaction to the coordinator
func (c *TransactionCoordinator) RegisterTransaction(tx Transaction) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.transactions[tx.GetID()] = tx
}

// UnregisterTransaction removes a transaction from the coordinator
func (c *TransactionCoordinator) UnregisterTransaction(txID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.transactions, txID)
}

// GetTransaction retrieves a transaction by ID
func (c *TransactionCoordinator) GetTransaction(txID string) (Transaction, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	tx, ok := c.transactions[txID]
	return tx, ok
}

// ListTransactions returns all active transactions
func (c *TransactionCoordinator) ListTransactions() []Transaction {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	result := make([]Transaction, 0, len(c.transactions))
	for _, tx := range c.transactions {
		result = append(result, tx)
	}
	return result
}

// RecoverTransaction attempts to recover a transaction's state
func (c *TransactionCoordinator) RecoverTransaction(txID string) error {
	_, ok := c.GetTransaction(txID)
	if !ok {
		return fmt.Errorf("transaction not found: %s", txID)
	}

	return nil
}

// CleanupStaleTransactions aborts transactions that have been inactive
func (c *TransactionCoordinator) CleanupStaleTransactions(maxAge time.Duration) {
	c.mutex.Lock()
	var staleIDs []string
	for id, tx := range c.transactions {
		if time.Since(tx.GetStartTime()) > maxAge {
			staleIDs = append(staleIDs, id)
		}
	}
	c.mutex.Unlock()

	for _, id := range staleIDs {
		tx, ok := c.GetTransaction(id)
		if ok {
			// Attempt to abort the transaction
			tx.Rollback() // Ignoring error as we're cleaning up
		}
	}
}

// operation represents a pending operation in a transaction
type operation struct {
	opType          string // "put" or "delete"
	uri             string // target URI
	data            []byte // data to write (for put operations)
	options         []any  // original operation options
	resolvedOptions OperationOptions
}

// TypedTransactionView provides a type-safe view of a transaction for specific entity types
type TypedTransactionView[T BaseEntity] struct {
	store *Store[T]
	tx    Transaction
}

// Get retrieves an entity by key
func (v *TypedTransactionView[T]) Get(key string) (T, error) {
	data, _, err := v.tx.Get(v.store.ToURI(key))
	if err != nil {
		var zero T
		return zero, err
	}

	var entity T
	if err := v.store.codec.Unmarshal(data, &entity); err != nil {
		var zero T
		return zero, NewStoreError(ErrCodeInvalidData, "Get", key, "Failed to unmarshal entity", err, false)
	}

	return entity, nil
}

// Put stores an entity by key
func (v *TypedTransactionView[T]) Put(key string, entity T) error {
	// Marshal entity to bytes
	data, err := v.store.codec.Marshal(entity)
	if err != nil {
		return NewStoreError(ErrCodeInvalidData, "Put", key, "Failed to marshal entity", err, false)
	}

	ent := entity.GetEntity()
	opts := &TransactionOptions{}
	opts.ContentType.Set(v.store.codec.ContentType())
	opts.ETag.Set(calculateETag(data))
	opts.Metadata.SetKey("version", strconv.FormatInt(ent.Version, 10))

	// Perform the put operation within the transaction
	return v.tx.Put(v.store.ToURI(key), data, opts)
}

// Delete removes an entity by key
func (v *TypedTransactionView[T]) Delete(key string) error {
	return v.tx.Delete(v.store.ToURI(key))
}

// Helper function to calculate ETag (MD5 hash)
func calculateETag(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}
