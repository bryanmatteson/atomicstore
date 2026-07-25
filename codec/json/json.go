// codec/json/json.go
package json

import (
	"bytes"
	"encoding/json"

	"github.com/bryanmatteson/atomicstore/codec"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"json",
		func() codec.Codec { return &Codec{} },
		[]string{"application/json"},
		[]string{"json"},
	)
}

// Codec implements codec.Codec for JSON
type Codec struct {
	indent       string
	escapeHTML   bool
	allowUnknown bool
	useNumber    bool
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if c.escapeHTML {
		enc.SetEscapeHTML(c.escapeHTML)
	}
	if c.indent != "" {
		enc.SetIndent("", c.indent)
	}
	err := enc.Encode(v)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if c.useNumber {
		decoder.UseNumber()
	}
	if !c.allowUnknown {
		decoder.DisallowUnknownFields()
	}
	return decoder.Decode(v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/json"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		indent:     c.indent,
		escapeHTML: c.escapeHTML,
	}
}
