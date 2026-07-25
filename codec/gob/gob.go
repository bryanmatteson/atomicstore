// codec/gob/gob.go
package gob

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"io"
	"maps"
	"reflect"
	"sync"

	"github.com/bryanmatteson/atomicstore/codec"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"gob",
		func() codec.Codec { return &Codec{} },
		[]string{"application/x-gob"},
		[]string{"gob"},
	)
}

// Codec implements codec.Codec for Go's Gob format
type Codec struct {
	useCompression  bool
	mutex           sync.Mutex
	registeredTypes map[string]bool
}

// registerType registers a type with gob
func (c *Codec) registerType(v any) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.registeredTypes == nil {
		c.registeredTypes = make(map[string]bool)
	}

	// Get type name using reflection
	t := reflect.TypeOf(v)
	typeName := t.String()

	if !c.registeredTypes[typeName] {
		gob.Register(v)
		c.registeredTypes[typeName] = true
	}
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf

	// Apply compression if configured
	var gz *gzip.Writer
	if c.useCompression {
		gz = gzip.NewWriter(&buf)
		w = gz
	}

	enc := gob.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}

	// Close the gzip writer if used
	if c.useCompression {
		if err := gz.Close(); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	buf := bytes.NewBuffer(data)
	var r io.Reader = buf

	// Use decompression if configured
	if c.useCompression {
		gz, err := gzip.NewReader(buf)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}

	dec := gob.NewDecoder(r)
	return dec.Decode(v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/x-gob"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	clone := &Codec{
		useCompression: c.useCompression,
	}

	// Copy registered types
	if c.registeredTypes != nil {
		clone.registeredTypes = make(map[string]bool)
		maps.Copy(clone.registeredTypes, c.registeredTypes)
	}

	return clone
}
