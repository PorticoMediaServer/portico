package persistence

import (
	"math"
	"testing"
)

// Malformed records from a damaged page are refused, never decoded past their
// header or into a panic.
func TestDecodeRecordRefusesMalformedRecords(t *testing.T) {
	maxSerial := []byte{0x0b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}
	for _, record := range [][]byte{
		maxSerial,          // a serial type whose width overflows
		{3, 15, 128, 1, 2}, // a serial type running past the header
		{0},                // a header shorter than its own size field
		{2, 1},             // a value past the end
	} {
		if values, ok := decodeRecord(record); ok {
			t.Fatalf("decoded %v from %v", values, record)
		}
	}
	values, ok := decodeRecord([]byte{3, 1, 0x13, 0xff, 'a', 'b', 'c'})
	if !ok || len(values) != 2 || values[0] != int64(-1) || values[1] != "abc" {
		t.Fatalf("well-formed record: %v %v", values, ok)
	}
	_ = math.MaxInt64
}
