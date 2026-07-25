package yaml

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines YAML-specific configuration options
type Config struct {
	Indent    int  // Indentation level (number of spaces)
	UseFlow   bool // Use flow style for collections
	UseSingle bool // Use single quotes instead of double quotes
}

func (c Config) ApplyTo(codec codec.Codec) error {
	yamlCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *yaml.Codec, got %T", codec)
	}

	if c.Indent > 0 {
		yamlCodec.indent = c.Indent
	}

	yamlCodec.useFlow = c.UseFlow
	yamlCodec.useSingle = c.UseSingle

	return nil
}
