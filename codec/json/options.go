package json

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Standard config struct for compatibility
type Config struct {
	Indent       string
	EscapeHTML   bool
	AllowUnknown bool
	UseNumber    bool
}

func (c Config) ApplyTo(codec codec.Codec) error {
	jsonCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *json.Codec, got %T", codec)
	}

	if c.Indent != "" {
		jsonCodec.indent = c.Indent
	}

	jsonCodec.escapeHTML = c.EscapeHTML
	jsonCodec.allowUnknown = c.AllowUnknown
	jsonCodec.useNumber = c.UseNumber

	return nil
}
