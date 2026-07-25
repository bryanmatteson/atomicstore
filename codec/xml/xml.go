package xml

import (
	"encoding/xml"

	"github.com/bryanmatteson/atomicstore/codec"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"xml",
		func() codec.Codec { return &Codec{} },
		[]string{"application/xml", "text/xml"},
		[]string{"xml"},
	)
}

// Codec implements codec.Codec for XML
type Codec struct {
	indent    string
	prefix    string
	useAttr   bool
	addHeader bool
	omitEmpty bool
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	var data []byte
	var err error

	// Apply indentation if specified
	if c.indent != "" {
		data, err = xml.MarshalIndent(v, c.prefix, c.indent)
	} else {
		data, err = xml.Marshal(v)
	}

	if err != nil {
		return nil, err
	}

	// Add XML header if requested
	if c.addHeader {
		header := []byte(xml.Header)
		data = append(header, data...)
	}

	return data, nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	return xml.Unmarshal(data, v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/xml"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		indent:    c.indent,
		prefix:    c.prefix,
		useAttr:   c.useAttr,
		addHeader: c.addHeader,
		omitEmpty: c.omitEmpty,
	}
}
