package xml

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines XML-specific configuration options
type Config struct {
	Indent    string // Indentation string
	Prefix    string // Line prefix
	UseAttr   bool   // Whether to use attributes over elements when possible
	AddHeader bool   // Whether to add the XML header
	OmitEmpty bool   // Whether to omit empty tags
}

func (c Config) ApplyTo(codec codec.Codec) error {
	xmlCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *xml.Codec, got %T", codec)
	}

	if c.Indent != "" {
		xmlCodec.indent = c.Indent
	}

	if c.Prefix != "" {
		xmlCodec.prefix = c.Prefix
	}

	xmlCodec.useAttr = c.UseAttr
	xmlCodec.addHeader = c.AddHeader
	xmlCodec.omitEmpty = c.OmitEmpty

	return nil
}
