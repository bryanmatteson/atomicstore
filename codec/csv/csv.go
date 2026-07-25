package csv

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"reflect"
	"strings"

	"github.com/bryanmatteson/atomicstore/codec"
)

func init() {
	// Register this codec
	codec.DefaultRegistry.Register(
		"csv",
		func() codec.Codec { return &Codec{comma: ','} },
		[]string{"text/csv"},
		[]string{"csv"},
	)
}

// Codec implements codec.Codec for CSV data
type Codec struct {
	comma              rune
	comment            rune
	useFieldsPerRecord bool
	fieldsPerRecord    int
	lazyQuotes         bool
	trimLeadingSpace   bool
	headers            []string
}

// Marshal implements codec.Codec
func (c *Codec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Apply configuration
	writer.Comma = c.comma

	// Handle different input types
	val := reflect.ValueOf(v)
	switch {
	case isSliceOfSliceOfString(val):
		// [][]string format - direct encoding
		records := v.([][]string)
		if err := writer.WriteAll(records); err != nil {
			return nil, err
		}

	case isSliceOfStruct(val):
		// []struct format - convert to CSV
		if err := c.writeStructSlice(writer, val); err != nil {
			return nil, err
		}

	case isStructWithSlice(val):
		// Special container type with headers and data
		if err := c.writeStructWithSlice(writer, val); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported type for CSV encoding: %T", v)
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Unmarshal implements codec.Codec
func (c *Codec) Unmarshal(data []byte, v any) error {
	reader := csv.NewReader(bytes.NewReader(data))

	// Apply configuration
	reader.Comma = c.comma
	reader.Comment = c.comment
	reader.LazyQuotes = c.lazyQuotes
	reader.TrimLeadingSpace = c.trimLeadingSpace

	if c.useFieldsPerRecord && c.fieldsPerRecord > 0 {
		reader.FieldsPerRecord = c.fieldsPerRecord
	}

	// Read all records
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	// Handle different target types
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Ptr {
		return fmt.Errorf("target must be a pointer")
	}

	elem := val.Elem()
	switch {
	case elem.Type() == reflect.TypeOf([][]string{}):
		// [][]string format - direct decoding
		*v.(*[][]string) = records

	case isSliceOfStructPtr(elem):
		// []*struct format - convert to structs
		return c.readIntoStructSlice(records, elem)

	case isStructWithSlicePtr(elem):
		// Special container type with headers and data
		return c.readIntoStructWithSlice(records, elem)

	default:
		return fmt.Errorf("unsupported target type for CSV decoding: %T", v)
	}

	return nil
}

// ContentType implements codec.Codec
func (c *Codec) ContentType() string {
	return "text/csv"
}

// Clone creates a copy of this codec
func (c *Codec) Clone() codec.Codec {
	clone := &Codec{
		comma:              c.comma,
		comment:            c.comment,
		useFieldsPerRecord: c.useFieldsPerRecord,
		fieldsPerRecord:    c.fieldsPerRecord,
		lazyQuotes:         c.lazyQuotes,
		trimLeadingSpace:   c.trimLeadingSpace,
	}

	// Copy headers if present
	if len(c.headers) > 0 {
		clone.headers = make([]string, len(c.headers))
		copy(clone.headers, c.headers)
	}

	return clone
}

// Helper functions for type checking
func isSliceOfSliceOfString(val reflect.Value) bool {
	if val.Kind() != reflect.Slice {
		return false
	}

	elemType := val.Type().Elem()
	if elemType.Kind() != reflect.Slice {
		return false
	}

	return elemType.Elem().Kind() == reflect.String
}

func isSliceOfStruct(val reflect.Value) bool {
	if val.Kind() != reflect.Slice {
		return false
	}

	elemType := val.Type().Elem()
	return elemType.Kind() == reflect.Struct
}

func isSliceOfStructPtr(val reflect.Value) bool {
	if val.Kind() != reflect.Slice {
		return false
	}

	elemType := val.Type().Elem()
	return elemType.Kind() == reflect.Ptr && elemType.Elem().Kind() == reflect.Struct
}

func isStructWithSlice(val reflect.Value) bool {
	// Check for a struct that contains a slice field
	if val.Kind() != reflect.Struct {
		return false
	}

	// Look for a field that's a slice
	for i := 0; i < val.NumField(); i++ {
		if val.Field(i).Kind() == reflect.Slice {
			return true
		}
	}

	return false
}

func isStructWithSlicePtr(val reflect.Value) bool {
	// Check for a pointer to a struct that contains a slice field
	if val.Kind() != reflect.Struct {
		return false
	}

	// Look for a field that's a slice
	for i := 0; i < val.NumField(); i++ {
		if val.Field(i).Kind() == reflect.Slice {
			return true
		}
	}

	return false
}

// Helper function to write a slice of structs as CSV
func (c *Codec) writeStructSlice(writer *csv.Writer, val reflect.Value) error {
	if val.Len() == 0 {
		return nil
	}

	// Get field names from the struct type
	fieldNames := c.extractFieldNames(val.Type().Elem())

	// Use configured headers if available, otherwise use field names
	headers := c.headers
	if len(headers) == 0 {
		headers = fieldNames
	}

	// Write headers
	if err := writer.Write(headers); err != nil {
		return err
	}

	// Write each struct as a row
	for i := 0; i < val.Len(); i++ {
		row := c.structToRow(val.Index(i), fieldNames)
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// Helper function to write a struct containing a slice field as CSV
func (c *Codec) writeStructWithSlice(writer *csv.Writer, val reflect.Value) error {
	// Find the slice field
	var sliceField reflect.Value
	var sliceFieldName string

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if field.Kind() == reflect.Slice {
			sliceField = field
			sliceFieldName = val.Type().Field(i).Name
			break
		}
	}

	if !sliceField.IsValid() {
		return fmt.Errorf("no slice field found in struct")
	}

	// Check if we have a slice of structs
	if !isSliceOfStruct(sliceField) {
		return fmt.Errorf("field %s is not a slice of structs", sliceFieldName)
	}

	// Delegate to writeStructSlice
	return c.writeStructSlice(writer, sliceField)
}

// Helper function to read CSV into a slice of struct pointers
func (c *Codec) readIntoStructSlice(records [][]string, sliceVal reflect.Value) error {
	if len(records) == 0 {
		return nil
	}

	// First row is headers
	headers := records[0]

	// Create a new slice with the correct capacity
	elemType := sliceVal.Type().Elem()
	newSlice := reflect.MakeSlice(sliceVal.Type(), 0, len(records)-1)

	// Process each data row
	for i := 1; i < len(records); i++ {
		// Create a new struct
		newStruct := reflect.New(elemType.Elem())

		// Fill the struct fields based on headers
		if err := c.fillStructFromRow(newStruct.Elem(), headers, records[i]); err != nil {
			return err
		}

		// Append to our slice
		newSlice = reflect.Append(newSlice, newStruct)
	}

	// Set the result back to the original slice
	sliceVal.Set(newSlice)
	return nil
}

// Helper function to read CSV into a struct containing a slice
func (c *Codec) readIntoStructWithSlice(records [][]string, structVal reflect.Value) error {
	// Find the slice field
	var sliceField reflect.Value
	var sliceFieldName string

	for i := 0; i < structVal.NumField(); i++ {
		field := structVal.Field(i)
		if field.Kind() == reflect.Slice {
			sliceField = field
			sliceFieldName = structVal.Type().Field(i).Name
			break
		}
	}

	if !sliceField.IsValid() {
		return fmt.Errorf("no slice field found in struct")
	}

	// Check if we have a slice of structs
	if !isSliceOfStructPtr(sliceField) {
		return fmt.Errorf("field %s is not a slice of struct pointers", sliceFieldName)
	}

	// Create a new slice
	newSlice := reflect.New(sliceField.Type()).Elem()

	// Process the records
	if err := c.readIntoStructSlice(records, newSlice); err != nil {
		return err
	}

	// Set the new slice back to the field
	sliceField.Set(newSlice)
	return nil
}

// Helper function to extract field names from a struct type
func (c *Codec) extractFieldNames(structType reflect.Type) []string {
	var fieldNames []string

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Try to get name from csv tag
		tag := field.Tag.Get("csv")
		if tag != "" && tag != "-" {
			fieldNames = append(fieldNames, tag)
		} else {
			fieldNames = append(fieldNames, field.Name)
		}
	}

	return fieldNames
}

// Helper function to convert a struct to a CSV row
func (c *Codec) structToRow(structVal reflect.Value, fieldNames []string) []string {
	row := make([]string, len(fieldNames))

	for i, name := range fieldNames {
		fieldVal := c.getFieldByNameOrTag(structVal, name)
		if fieldVal.IsValid() {
			row[i] = fmt.Sprintf("%v", fieldVal.Interface())
		}
	}

	return row
}

// Helper function to fill a struct from a CSV row
func (c *Codec) fillStructFromRow(structVal reflect.Value, headers []string, row []string) error {
	for i, header := range headers {
		if i >= len(row) {
			break
		}

		fieldVal := c.getFieldByNameOrTag(structVal, header)
		if !fieldVal.IsValid() || !fieldVal.CanSet() {
			continue
		}

		// Basic string conversion - in a real implementation, you'd convert based on field type
		if fieldVal.Kind() == reflect.String {
			fieldVal.SetString(row[i])
		}
		// Additional type conversions would go here
	}

	return nil
}

// Helper function to get a field by name or CSV tag
func (c *Codec) getFieldByNameOrTag(structVal reflect.Value, nameOrTag string) reflect.Value {
	// First try direct field access
	field := structVal.FieldByName(nameOrTag)
	if field.IsValid() {
		return field
	}

	// Then try by CSV tag
	structType := structVal.Type()
	for i := 0; i < structType.NumField(); i++ {
		fieldType := structType.Field(i)
		tag := fieldType.Tag.Get("csv")

		if tag == nameOrTag || strings.Split(tag, ",")[0] == nameOrTag {
			return structVal.Field(i)
		}
	}

	return reflect.Value{}
}
