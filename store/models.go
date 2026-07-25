package store

import (
	"reflect"
	"time"
	"unsafe"
)

// Entity defines the interface for identifiable entities
type BaseEntity interface {
	GetEntity() Entity
	entity()
}

// Base provides a base implementation of the Entity interface
type Entity struct {
	// ID is the unique identifier for the entity
	ID string `json:"id,omitempty"`

	// Version is the entity version for optimistic concurrency
	Version int64 `json:"version,omitempty"`
}

func (e Entity) GetEntity() Entity { return e }
func (e Entity) entity()           {}

func GetEntityMut[E BaseEntity](e *E) *Entity {
	rv := reflect.ValueOf(e)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}

	fld := rv.FieldByName("Entity")
	if !fld.IsValid() {
		if rv.Type() != reflect.TypeFor[Entity]() {
			return nil
		}
		fld = rv
	}

	return fld.Addr().Interface().(*Entity)
}

// GetEntityUnsafe requires that Entity is embedded as the first field
// in the struct. It uses unsafe.Pointer to bypass reflection.
func GetEntityUnsafe[E BaseEntity, P *E](e P) *Entity {
	return (*Entity)(unsafe.Pointer(e))
}

// Metadata contains metadata about a stored object
type Metadata struct {
	// ETag is the entity tag for the object
	ETag string

	// LastModified is the timestamp when the object was last modified
	LastModified time.Time

	// Size is the size of the object in bytes
	Size int64

	// ContentType is the MIME type of the object
	ContentType string

	// ContentEncoding specifies the encoding of the object
	ContentEncoding string

	// StorageClass specifies the storage class of the object
	StorageClass string

	// VersionID is the version ID for versioned objects
	VersionID string

	// UserMetadata contains user-defined metadata
	UserMetadata map[string]string
}

// Entry represents an object or prefix in a list result
type Entry struct {
	// Key is the object key
	Key string

	// IsPrefix indicates whether this is a common prefix
	IsPrefix bool

	// Metadata contains the object metadata (if included)
	Metadata Metadata
}

// Field represents a value that may be set or unset
type Field[T any] struct {
	isSet bool
	value T
}

// IsSet returns true if the field has a value
func (f *Field[T]) IsSet() bool {
	return f.isSet
}

// Get returns the current value
func (f *Field[T]) Get() T {
	return f.value
}

// Set sets the value and marks it as set
func (f *Field[T]) Set(value T) {
	f.value = value
	f.isSet = true
}

// SetDefault sets the value only if it's not already set
func (f *Field[T]) SetDefault(value T) {
	if !f.isSet {
		f.value = value
		f.isSet = true
	}
}

// SetDefaultFrom sets the value from a function only if not already set
func (f *Field[T]) SetDefaultFrom(fn func() T) {
	if !f.isSet {
		f.value = fn()
		f.isSet = true
	}
}

// Or returns the field's value or the provided default
func (f *Field[T]) Or(defVal T) T {
	if f.isSet {
		return f.value
	}
	return defVal
}

// Match calls fn if the field's value matches the provided value
func (f *Field[T]) Match(value T, fn func()) {
	if f.isSet && any(f.value) == any(value) {
		fn()
	}
}

// Unset clears the field
func (f *Field[T]) Unset() {
	f.isSet = false
	var zero T
	f.value = zero
}

// MapField represents a map that may be set or unset
type MapField[K comparable, V any] struct {
	Field[map[K]V]
}

// ToMapField creates a MapField from a map
func ToMapField[K comparable, V any](m map[K]V) MapField[K, V] {
	var field MapField[K, V]
	field.Set(m)
	return field
}

// SetKey sets a key-value pair in the map
func (f *MapField[K, V]) SetKey(key K, value V) {
	if !f.isSet {
		f.Set(make(map[K]V))
	}
	f.value[key] = value
}

// Cloned returns a copy of the map
func (f *MapField[K, V]) Cloned() map[K]V {
	if !f.isSet {
		return nil
	}

	result := make(map[K]V, len(f.value))
	for k, v := range f.value {
		result[k] = v
	}
	return result
}

// SliceField represents a slice that may be set or unset
type SliceField[T any] struct {
	Field[[]T]
}

// ToSliceField creates a SliceField from a slice
func ToSliceField[T any](s []T) SliceField[T] {
	var field SliceField[T]
	field.Set(s)
	return field
}

// Append adds items to the slice
func (f *SliceField[T]) Append(items ...T) {
	if !f.isSet {
		f.Set(make([]T, 0, len(items)))
	}
	f.value = append(f.value, items...)
}

// Cloned returns a copy of the slice
func (f *SliceField[T]) Cloned() []T {
	if !f.isSet {
		return nil
	}

	result := make([]T, len(f.value))
	copy(result, f.value)
	return result
}

// Either represents a value that can be one of two types
type Either[L, R any] struct {
	isLeft bool
	isSet  bool
	left   L
	right  R
}

// IsSet returns true if either value is set
func (e *Either[L, R]) IsSet() bool {
	return e.isSet
}

// IsLeft returns true if the left value is set
func (e *Either[L, R]) IsLeft() bool {
	return e.isSet && e.isLeft
}

// IsRight returns true if the right value is set
func (e *Either[L, R]) IsRight() bool {
	return e.isSet && !e.isLeft
}

// Left returns the left value
func (e *Either[L, R]) Left() L {
	return e.left
}

// Right returns the right value
func (e *Either[L, R]) Right() R {
	return e.right
}

// SetLeft sets the left value
func (e *Either[L, R]) SetLeft(value L) {
	e.left = value
	e.isLeft = true
	e.isSet = true
	var zero R
	e.right = zero
}

// SetRight sets the right value
func (e *Either[L, R]) SetRight(value R) {
	e.right = value
	e.isLeft = false
	e.isSet = true
	var zero L
	e.left = zero
}

// Get returns both values
func (e *Either[L, R]) Get() (L, R) {
	return e.left, e.right
}

// Match matches against the contained value
func (e *Either[L, R]) Match(leftFn func(L), rightFn func(R)) {
	if !e.isSet {
		return
	}

	if e.isLeft {
		leftFn(e.left)
	} else {
		rightFn(e.right)
	}
}

// OrLeft returns the left value or a default
func (e *Either[L, R]) OrLeft(defVal L) L {
	if e.isSet && e.isLeft {
		return e.left
	}
	return defVal
}

// OrRight returns the right value or a default
func (e *Either[L, R]) OrRight(defVal R) R {
	if e.isSet && !e.isLeft {
		return e.right
	}
	return defVal
}

// SetDefaultLeft sets the left value if not already set
func (e *Either[L, R]) SetDefaultLeft(value L) {
	if !e.isSet {
		e.SetLeft(value)
	}
}

// SetDefaultRight sets the right value if not already set
func (e *Either[L, R]) SetDefaultRight(value R) {
	if !e.isSet {
		e.SetRight(value)
	}
}

// SetDefaultFrom sets values from a function if not already set
func (e *Either[L, R]) SetDefaultFrom(fn func() (L, R)) {
	if !e.isSet {
		l, r := fn()
		e.left = l
		e.right = r
		e.isLeft = true
		e.isSet = true
	}
}

// UnsetLeft clears the left value
func (e *Either[L, R]) UnsetLeft() {
	if e.isSet && e.isLeft {
		e.isSet = false
		e.isLeft = false
		var zero L
		e.left = zero
	}
}

// UnsetRight clears the right value
func (e *Either[L, R]) UnsetRight() {
	if e.isSet && !e.isLeft {
		e.isSet = false
		e.isLeft = false
		var zero R
		e.right = zero
	}
}

type Optional[T any] struct {
	isSet bool
	value T
}

func (o *Optional[T]) IsSet() bool {
	return o.isSet
}
func (o *Optional[T]) Get() T {
	return o.value
}
func (o *Optional[T]) Set(value T) {
	o.value = value
	o.isSet = true
}
func (o *Optional[T]) Unset() {
	o.isSet = false
	var zero T
	o.value = zero
}
func (o *Optional[T]) Or(defVal T) T {
	if o.isSet {
		return o.value
	}
	return defVal
}
func (o *Optional[T]) SetDefault(value T) {
	if !o.isSet {
		o.value = value
		o.isSet = true
	}
}
func (o *Optional[T]) SetDefaultFrom(fn func() T) {
	if !o.isSet {
		o.value = fn()
		o.isSet = true
	}
}
func (o *Optional[T]) Match(fn func(T)) {
	if o.isSet {
		fn(o.value)
	}
}
func (o *Optional[T]) MatchOrElse(fn func(T), elseFn func()) {
	if o.isSet {
		fn(o.value)
	} else {
		elseFn()
	}
}
