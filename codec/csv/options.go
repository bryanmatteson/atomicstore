package csv

import (
	"fmt"

	"github.com/bryanmatteson/atomicstore/codec"
)

// Config defines CSV-specific configuration options
type Config struct {
	Comma              rune     // Field delimiter (default ',')
	Comment            rune     // Comment character (default '#')
	UseFieldsPerRecord bool     // Whether to enforce specific number of fields
	FieldsPerRecord    int      // Number of fields per record (if UseFieldsPerRecord is true)
	LazyQuotes         bool     // Allow lazy quotes
	TrimLeadingSpace   bool     // Trim leading space in fields
	Headers            []string // Column headers
}

func (c Config) ApplyTo(codec codec.Codec) error {
	csvCodec, ok := codec.(*Codec)
	if !ok {
		return fmt.Errorf("expected *csv.Codec, got %T", codec)
	}

	if c.Comma != 0 {
		csvCodec.comma = c.Comma
	}

	if c.Comment != 0 {
		csvCodec.comment = c.Comment
	}

	csvCodec.useFieldsPerRecord = c.UseFieldsPerRecord

	if c.UseFieldsPerRecord && c.FieldsPerRecord > 0 {
		csvCodec.fieldsPerRecord = c.FieldsPerRecord
	}

	csvCodec.lazyQuotes = c.LazyQuotes
	csvCodec.trimLeadingSpace = c.TrimLeadingSpace

	if len(c.Headers) > 0 {
		// Create a copy of the headers
		csvCodec.headers = make([]string, len(c.Headers))
		copy(csvCodec.headers, c.Headers)
	}

	return nil
}
