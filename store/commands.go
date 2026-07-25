package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Command represents a storage operation that can be executed
type Command interface {
	Execute(ctx context.Context) error
}

// CommandResult represents a storage operation that returns a result
type CommandResult[T BaseEntity] interface {
	Execute(ctx context.Context) (T, error)
}

// PutCommand creates a put command to store data at a URI
type PutCommand struct {
	storage Storage
	uri     string
	data    []byte
	options []PutOption
}

// NewPutCommand creates a new put command
func NewPutCommand(storage Storage, uri string, data []byte, options ...PutOption) *PutCommand {
	return &PutCommand{
		storage: storage,
		uri:     uri,
		data:    data,
		options: options,
	}
}

// Execute runs the put command
func (c *PutCommand) Execute(ctx context.Context) error {
	_, err := c.storage.Put(ctx, c.uri, c.data, c.options...)
	return err
}

// PutWithResultCommand creates a put command that returns metadata
type PutWithResultCommand struct {
	storage Storage
	uri     string
	data    []byte
	options []PutOption
}

// NewPutWithResultCommand creates a new put command with result
func NewPutWithResultCommand(storage Storage, uri string, data []byte, options ...PutOption) *PutWithResultCommand {
	return &PutWithResultCommand{
		storage: storage,
		uri:     uri,
		data:    data,
		options: options,
	}
}

// Execute runs the put command and returns metadata
func (c *PutWithResultCommand) Execute(ctx context.Context) (Metadata, error) {
	return c.storage.Put(ctx, c.uri, c.data, c.options...)
}

// GetCommand creates a get command to retrieve data from a URI
type GetCommand[T BaseEntity] struct {
	store    *Store[T]
	key      string
	options  []GetOption
	validate func(T) error
}

// NewGetCommand creates a new get command
func NewGetCommand[T BaseEntity](store *Store[T], key string, options ...GetOption) *GetCommand[T] {
	return &GetCommand[T]{
		store:   store,
		key:     key,
		options: options,
	}
}

// WithValidation adds a validation function to the get command
func (c *GetCommand[T]) WithValidation(validate func(T) error) *GetCommand[T] {
	c.validate = validate
	return c
}

// Execute runs the get command
func (c *GetCommand[T]) Execute(ctx context.Context) (T, error) {
	entity, _, err := c.store.Get(ctx, c.key, c.options...)
	if err != nil {
		var zero T
		return zero, err
	}

	// Apply validation if provided
	if c.validate != nil {
		if err := c.validate(entity); err != nil {
			var zero T
			return zero, err
		}
	}

	return entity, nil
}

// DeleteCommand creates a delete command
type DeleteCommand struct {
	storage Storage
	uri     string
	options []DeleteOption
}

// NewDeleteCommand creates a new delete command
func NewDeleteCommand(storage Storage, uri string, options ...DeleteOption) *DeleteCommand {
	return &DeleteCommand{
		storage: storage,
		uri:     uri,
		options: options,
	}
}

// Execute runs the delete command
func (c *DeleteCommand) Execute(ctx context.Context) error {
	return c.storage.Delete(ctx, c.uri, c.options...)
}

// CopyCommand creates a command to copy data between URIs
type CopyCommand struct {
	sourceStorage      Storage
	destinationStorage Storage
	sourceURI          string
	destinationURI     string
	options            []CopyOption
}

// NewCopyCommand creates a new copy command
func NewCopyCommand(
	sourceStorage Storage,
	destinationStorage Storage,
	sourceURI string,
	destinationURI string,
	options ...CopyOption,
) *CopyCommand {
	return &CopyCommand{
		sourceStorage:      sourceStorage,
		destinationStorage: destinationStorage,
		sourceURI:          sourceURI,
		destinationURI:     destinationURI,
		options:            options,
	}
}

// Execute runs the copy command
func (c *CopyCommand) Execute(ctx context.Context) error {
	// Check if both storages implement StreamHandler for optimized copy
	sourceHandler, sourceOk := c.sourceStorage.(StreamHandler)
	destHandler, destOk := c.destinationStorage.(StreamHandler)

	if sourceOk && destOk && sourceHandler == destHandler {
		// Use optimized copy if same handler
		_, err := sourceHandler.CopyStream(ctx, c.sourceURI, c.destinationURI, c.options...)
		return err
	}

	// Fallback to manual copy
	// Get source data as stream
	stream, metadata, err := c.sourceStorage.GetStream(ctx, c.sourceURI)
	if err != nil {
		return fmt.Errorf("failed to get source: %w", err)
	}
	defer stream.Close()

	// Extract metadata options from copy options
	var metadataOptions []PutOption
	for _, opt := range c.options {
		if putOpt, ok := opt.(PutOption); ok {
			metadataOptions = append(metadataOptions, putOpt)
		}
	}

	// Add content type if available from source
	if metadata.ContentType != "" {
		metadataOptions = append(metadataOptions, WithContentType(metadata.ContentType))
	}

	// Copy source metadata if not overridden
	if metadata.UserMetadata != nil {
		for k, v := range metadata.UserMetadata {
			metadataOptions = append(metadataOptions, WithMetadata(k, v))
		}
	}

	// Put to destination
	_, err = c.destinationStorage.PutStream(ctx, c.destinationURI, stream, metadataOptions...)
	if err != nil {
		return fmt.Errorf("failed to put destination: %w", err)
	}

	return nil
}

// AtomicUpdateCommand creates a command to atomically update an object
type AtomicUpdateCommand[T BaseEntity] struct {
	store          *Store[T]
	key            string
	updateFunc     func(T) (T, error)
	retries        int
	retryDelay     time.Duration
	initialVersion int64
}

// NewAtomicUpdateCommand creates a new atomic update command
func NewAtomicUpdateCommand[T BaseEntity](
	store *Store[T],
	key string,
	updateFunc func(T) (T, error),
) *AtomicUpdateCommand[T] {
	return &AtomicUpdateCommand[T]{
		store:      store,
		key:        key,
		updateFunc: updateFunc,
		retries:    5,
		retryDelay: 100 * time.Millisecond,
	}
}

// WithRetries sets the retry count
func (c *AtomicUpdateCommand[T]) WithRetries(count int) *AtomicUpdateCommand[T] {
	c.retries = count
	return c
}

// WithRetryDelay sets the delay between retries
func (c *AtomicUpdateCommand[T]) WithRetryDelay(delay time.Duration) *AtomicUpdateCommand[T] {
	c.retryDelay = delay
	return c
}

// WithInitialVersion sets the expected initial version
func (c *AtomicUpdateCommand[T]) WithInitialVersion(version int64) *AtomicUpdateCommand[T] {
	c.initialVersion = version
	return c
}

// Execute runs the atomic update command
func (c *AtomicUpdateCommand[T]) Execute(ctx context.Context) (T, error) {
	var entity T
	var metadata Metadata
	var err error

	// Start retry loop
	var attempts int
	for attempts = 0; attempts <= c.retries; attempts++ {
		// Get current entity
		entity, metadata, err = c.store.Get(ctx, c.key)
		if err != nil {
			var zero T
			return zero, err
		}
		ent := entity.GetEntity()

		// Check initial version if specified and entity implements Entity
		if c.initialVersion > 0 {
			currentVersion := ent.Version
			if currentVersion != c.initialVersion {
				var zero T
				return zero, NewStoreError(
					ErrCodePreconditionFailed,
					"AtomicUpdate",
					c.key,
					fmt.Sprintf("Expected version %d, got %d", c.initialVersion, currentVersion),
					nil,
					false,
				)
			}
		}

		// Apply update function
		updatedEntity, err := c.updateFunc(entity)
		if err != nil {
			var zero T
			return zero, err
		}

		// Update version if entity supports it
		currentVersion := ent.Version
		mut := GetEntityMut(&updatedEntity)
		mut.Version = currentVersion + 1

		// Try to put with If-Match condition
		putOptions := []PutOption{IfMatch(metadata.ETag)}

		_, err = c.store.Put(ctx, c.key, updatedEntity, putOptions...)
		if err == nil {
			// Success
			return updatedEntity, nil
		}

		// If error is not a precondition failure, return it immediately
		if !IsErrorCode(err, ErrCodePreconditionFailed) {
			var zero T
			return zero, err
		}

		// Sleep before next retry
		if attempts < c.retries {
			select {
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			case <-time.After(c.retryDelay):
				// Continue with retry
			}
		}
	}

	// All retries failed
	var zero T
	return zero, NewStoreError(
		ErrCodePreconditionFailed,
		"AtomicUpdate",
		c.key,
		fmt.Sprintf("Failed after %d attempts due to concurrent modifications", attempts),
		nil,
		false,
	)
}

// BatchCommand executes multiple commands as a batch
type BatchCommand struct {
	commands []Command
	// If stopOnError is true, execution stops on first error
	stopOnError bool
}

// NewBatchCommand creates a new batch command
func NewBatchCommand(commands ...Command) *BatchCommand {
	return &BatchCommand{
		commands:    commands,
		stopOnError: false,
	}
}

// WithStopOnError configures whether to stop execution on first error
func (c *BatchCommand) WithStopOnError(stop bool) *BatchCommand {
	c.stopOnError = stop
	return c
}

// Execute runs all commands in the batch
func (c *BatchCommand) Execute(ctx context.Context) error {
	var errs []error

	for _, cmd := range c.commands {
		err := cmd.Execute(ctx)
		if err != nil {
			if c.stopOnError {
				return err
			}
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
