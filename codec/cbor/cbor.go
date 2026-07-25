package cbor

import (
	"github.com/bryanmatteson/atomicstore/codec"

	"github.com/fxamacker/cbor/v2"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"cbor",
		func() codec.Codec { return &Codec{} },
		[]string{"application/cbor"},
		[]string{"cbor"},
	)
}

// Codec implements codec.Codec for CBOR
type Codec struct {
	detectCycles     bool
	indefiniteLength bool
	canonicalOutput  bool
	timeTag          uint64
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	// Create encoder options based on configuration
	opts := cbor.EncOptions{}

	// Set canonical mode if requested
	if c.canonicalOutput {
		opts.Sort = cbor.SortCanonical
		opts.ShortestFloat = cbor.ShortestFloat16
	}

	// Set indefinite length if requested
	if c.indefiniteLength {
		opts.IndefLength = cbor.IndefLengthAllowed
	} else {
		opts.IndefLength = cbor.IndefLengthForbidden
	}

	// Set time tag if specified
	if c.timeTag > 0 {
		// Use RFC3339 for simplicity, library doesn't expose TimeTag directly
		opts.Time = cbor.TimeRFC3339
	}

	// Create encoder mode with the given options
	encMode, err := opts.EncMode()
	if err != nil {
		return nil, err
	}

	// Marshal the data
	return encMode.Marshal(v)
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	// Create decoder options based on configuration
	opts := cbor.DecOptions{
		MaxArrayElements: 1000000, // Reasonable default
		MaxMapPairs:      1000000, // Reasonable default
	}

	// Set duplicate key checking based on cycle detection
	if c.detectCycles {
		opts.DupMapKey = cbor.DupMapKeyEnforcedAPF
	} else {
		opts.DupMapKey = cbor.DupMapKeyQuiet
	}

	// Use RFC3339 time formatting if time tag is specified
	if c.timeTag > 0 {
		opts.TimeTag = cbor.DecTagOptional
	}

	// Create decoder mode with the given options
	decMode, err := opts.DecMode()
	if err != nil {
		return err
	}

	// Unmarshal the data
	return decMode.Unmarshal(data, v)
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "application/cbor"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	return &Codec{
		detectCycles:     c.detectCycles,
		indefiniteLength: c.indefiniteLength,
		canonicalOutput:  c.canonicalOutput,
		timeTag:          c.timeTag,
	}
}
