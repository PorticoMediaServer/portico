package reading

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// An archive whose own list of entries is longer than any book needs is refused before the
// list is read, so a small file cannot make the server hold millions of names.
func TestAnArchiveWithTooManyEntriesIsRefused(t *testing.T) {
	for _, n := range []int{maxEntries, maxEntries + 1} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i := range n {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprint(i), Method: zip.Store}); err != nil {
				t.Fatal(err)
			}
		}
		zw.Close()
		_, err := openArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if (err == nil) != (n <= maxEntries) {
			t.Fatalf("%d entries: %v", n, err)
		}
	}
}

// The zip reader reads entries until one is not an entry and checks their number only modulo
// 65,536, so an end record can understate both the number and the size of the list. Such an
// archive is refused too, instead of having its whole list read.
func TestAnArchiveThatUnderstatesItsListIsRefused(t *testing.T) {
	const n = 65_536 + 2
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range n {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprint(i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	raw := buf.Bytes()
	// Keep the entries and their list, and end with a plain end record that says 2 entries in 46 bytes.
	list, end := bytes.Index(raw, []byte("PK\x01\x02")), bytes.Index(raw, []byte("PK\x06\x06"))
	record := make([]byte, 22)
	copy(record, "PK\x05\x06")
	binary.LittleEndian.PutUint16(record[8:], n%65536)
	binary.LittleEndian.PutUint16(record[10:], n%65536)
	binary.LittleEndian.PutUint32(record[12:], 46)
	binary.LittleEndian.PutUint32(record[16:], uint32(list))
	raw = append(raw[:end:end], record...)
	if a, err := openArchive(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatalf("an archive of %d entries whose end record says 2 was read, %d entries kept", n, len(a.names))
	}
}
