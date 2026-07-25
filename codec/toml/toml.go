package toml

import (
	"bytes"

	"github.com/bryanmatteson/atomicstore/codec"

	"github.com/pelletier/go-toml/v2"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"toml",
		func() codec.Codec { return &Codec{} },
		[]string{"application/toml"},
		[]string{"toml"},
	)
}

// Codec implements codec.Codec for TOML
type Codec struct {
	indent           string
	multilineArrays  bool
	multilineStrings bool
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	buf := new(bytes.Buffer)
	marshaler := toml.NewEncoder(buf)

	// Apply configuration
	if c.indent != "" {
		marshaler.SetIndentTables(true)
		marshaler.SetIndentSymbol(c.indent)
	}

	if c.multilineArrays {
		marshaler.SetArraysMultiline(true)
	}

	if err := marshaler.Encode(v); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	return toml.Unmarshal(data, v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/toml"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		indent:           c.indent,
		multilineArrays:  c.multilineArrays,
		multilineStrings: c.multilineStrings,
	}
}
