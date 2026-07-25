package msgpack

import (
	"bytes"
	"compress/zlib"
	"io"

	"github.com/bryanmatteson/atomicstore/codec"

	"github.com/vmihailenco/msgpack/v5"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"msgpack",
		func() codec.Codec { return &Codec{} },
		[]string{"application/x-msgpack", "application/msgpack"},
		[]string{"msgpack", "mp"},
	)
}

// Codec implements codec.Codec for MessagePack
type Codec struct {
	useCompression bool
	useCustomTags  bool
	customTagName  string
	omitEmpty      bool
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	buf := new(bytes.Buffer)
	var writer io.Writer = buf
	if c.useCompression {
		compressor := zlib.NewWriter(buf)
		defer compressor.Close()
		writer = compressor
	}
	// Create encoder with options
	encoder := msgpack.GetEncoder()

	encoder.ResetWriter(writer)

	// Configure encoder based on options
	if c.useCustomTags && c.customTagName != "" {
		encoder.SetCustomStructTag(c.customTagName)
	}

	if c.omitEmpty {
		encoder.SetOmitEmpty(true)
	}

	// Marshal the data
	err := encoder.Encode(v)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	var rd io.ReadCloser = io.NopCloser(bytes.NewReader(data))

	if c.useCompression {
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		rd = r
		defer rd.Close()
	}

	// Create decoder with options
	decoder := msgpack.GetDecoder()
	decoder.ResetReader(rd)

	// Configure decoder based on options
	if c.useCustomTags && c.customTagName != "" {
		decoder.SetCustomStructTag(c.customTagName)
	}

	// Decode the data
	return decoder.Decode(v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/x-msgpack"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		useCompression: c.useCompression,
		useCustomTags:  c.useCustomTags,
		customTagName:  c.customTagName,
		omitEmpty:      c.omitEmpty,
	}
}
