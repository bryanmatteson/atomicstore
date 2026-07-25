package msgpack

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines MessagePack-specific configuration options
type Config struct {
	UseCompression bool   // Whether to use compression
	UseCustomTags  bool   // Whether to use custom struct tags
	CustomTagName  string // Custom tag name to use
	OmitEmpty      bool   // Whether to omit empty fields
}

func (c Config) ApplyTo(codec codec.Codec) error {
	msgpackCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *msgpack.Codec, got %T", codec)
	}

	msgpackCodec.useCompression = c.UseCompression
	msgpackCodec.useCustomTags = c.UseCustomTags

	if c.CustomTagName != "" {
		msgpackCodec.customTagName = c.CustomTagName
	}

	msgpackCodec.omitEmpty = c.OmitEmpty

	return nil
}
