package codec

// Codec handles serialization/deserialization of items
type Codec interface {
	// Marshal converts an item to bytes
	Marshal(v any) ([]byte, error)

	// Unmarshal converts bytes to an item
	Unmarshal(data []byte, v any) error

	// ContentType returns the MIME type for this codec
	ContentType() string
}

type Config interface {
	ApplyTo(codec Codec) error
}

// Option defines a function that configures a codec
type Option func(Codec) error

// WithConfig returns an option that applies a config object to a codec
func WithConfig(config Config) Option {
	return config.ApplyTo
}
