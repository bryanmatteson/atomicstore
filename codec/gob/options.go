package gob

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines GOB-specific configuration options
type Config struct {
	UseCompression bool  // Whether to use compression
	RegisterTypes  []any // Types to register with gob.Register
}

func (c Config) ApplyTo(codec codec.Codec) error {
	gobCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *gob.Codec, got %T", codec)
	}

	gobCodec.useCompression = c.UseCompression

	// Register types if specified
	for _, t := range c.RegisterTypes {
		gobCodec.registerType(t)
	}

	return nil
}
