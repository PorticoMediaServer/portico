package reading

import (
	"archive/zip"
	"bytes"
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
