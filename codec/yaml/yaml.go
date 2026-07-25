package yaml

import (
	"bytes"

	"github.com/bryanmatteson/atomicstore/codec"

	"gopkg.in/yaml.v3"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"yaml",
		func() codec.Codec { return &Codec{} },
		[]string{"application/yaml", "text/yaml"},
		[]string{"yaml", "yml"},
	)
}

// Codec implements codec.Codec for YAML
type Codec struct {
	indent    int
	useFlow   bool
	useSingle bool
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)

	// Apply configuration options
	if c.indent > 0 {
		enc.SetIndent(c.indent)
	}

	// The yaml.v3 library doesn't directly expose useFlow and useSingle
	// as encoder options, but in a real implementation you would apply
	// these settings if the library supports them

	if err := enc.Encode(v); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/yaml"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		indent:    c.indent,
		useFlow:   c.useFlow,
		useSingle: c.useSingle,
	}
}
