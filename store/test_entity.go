package store

import (
	"encoding/json"
	"fmt"
)

// TestEntity is a sample entity for testing
type TestEntity struct {
	Entity
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// NewTestEntity creates a new test entity
func NewTestEntity(name string, value int) *TestEntity {
	return &TestEntity{
		Name:  name,
		Value: value,
	}
}

// Marshal returns the JSON encoding of the entity
func (e *TestEntity) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// String returns a string representation of the entity
func (e *TestEntity) String() string {
	return fmt.Sprintf("TestEntity{ID: %s, Version: %d, Name: %s, Value: %d}",
		e.ID, e.Version, e.Name, e.Value)
}

// Equal returns true if two entities are equal
func (e *TestEntity) Equal(other *TestEntity) bool {
	return e.ID == other.ID &&
		e.Version == other.Version &&
		e.Name == other.Name &&
		e.Value == other.Value
}

// Clone returns a deep copy of the entity
func (e *TestEntity) Clone() *TestEntity {
	return &TestEntity{
		Entity: e.Entity,
		Name:   e.Name,
		Value:  e.Value,
	}
}

// WithID returns a copy with the specified ID
func (e *TestEntity) WithID(id string) *TestEntity {
	clone := e.Clone()
	clone.ID = id
	return clone
}

// WithVersion returns a copy with the specified version
func (e *TestEntity) WithVersion(version int64) *TestEntity {
	clone := e.Clone()
	clone.Version = version
	return clone
}

// WithName returns a copy with the specified name
func (e *TestEntity) WithName(name string) *TestEntity {
	clone := e.Clone()
	clone.Name = name
	return clone
}

// WithValue returns a copy with the specified value
func (e *TestEntity) WithValue(value int) *TestEntity {
	clone := e.Clone()
	clone.Value = value
	return clone
}
