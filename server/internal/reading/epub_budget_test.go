package reading

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEPUBSpineBudget(t *testing.T) {
	raw := fixtureEPUB(t, `<html><body><p>Original text.</p></body></html>`)
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := [][2]string{}
	for _, entry := range z.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		content := string(b)
		if entry.Name == "OEBPS/content.opf" {
			content = strings.Replace(content, `<itemref idref="c"/>`, strings.Repeat(`<itemref idref="c"/>`, MaxEntries+1), 1)
		}
		entries = append(entries, [2]string{entry.Name, content})
	}
	raw = fixtureZIP(t, entries)
	if _, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded repeated spine references", err)
	}
	source := SequentialSource{Size: int64(len(raw)), Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }}
	if _, err := OpenSequentialEPUB(context.Background(), source); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded sequential spine references", err)
	}
}
