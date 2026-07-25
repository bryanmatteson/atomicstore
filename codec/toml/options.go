package toml

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines TOML-specific configuration options
type Config struct {
	Indent             string // Indentation string
	ArraysWithNewlines bool   // Whether to format arrays with newlines
	MultilineStrings   bool   // Whether to use multiline strings
}

func (c Config) ApplyTo(codec codec.Codec) error {
	tomlCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *toml.Codec, got %T", codec)
	}

	if c.Indent != "" {
		tomlCodec.indent = c.Indent
	}

	tomlCodec.multilineArrays = c.ArraysWithNewlines
	tomlCodec.multilineStrings = c.MultilineStrings

	return nil
}
