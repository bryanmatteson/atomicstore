package cbor

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines CBOR-specific configuration options
type Config struct {
	DetectCycles     bool   // Whether to detect and reject cycles
	IndefiniteLength bool   // Use indefinite-length encoding
	CanonicalOutput  bool   // Whether to use canonical mode
	TimeTag          uint64 // Custom tag for time.Time values (0 = default)
}

func (c Config) ApplyTo(codec codec.Codec) error {
	cborCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *cbor.Codec, got %T", codec)
	}

	cborCodec.detectCycles = c.DetectCycles
	cborCodec.indefiniteLength = c.IndefiniteLength
	cborCodec.canonicalOutput = c.CanonicalOutput

	if c.TimeTag > 0 {
		cborCodec.timeTag = c.TimeTag
	}

	return nil
}
