package readingtest

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/nwaples/rardecode/v2"
)

// The literal-only RAR 2.9 encoder is only useful if the pinned reader decodes
// it as a legal stream, so both compressed fixtures are checked here.
func TestCBR4LZFixturesDecode(t *testing.T) {
	pages := comicMembers()[:2]
	for name, raw := range map[string][]byte{
		"compressed": CBR4Compressed(),
		"solid":      CBR4Solid(),
	} {
		t.Run(name, func(t *testing.T) {
			reader, err := rardecode.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			for index, page := range pages {
				header, err := reader.Next()
				if err != nil {
					t.Fatalf("page %d header: %v", index, err)
				}
				if header.Name != page.name || header.UnPackedSize != int64(len(page.data)) {
					t.Fatalf("page %d header=%q size=%d", index, header.Name, header.UnPackedSize)
				}
				if got := header.Solid; got != (name == "solid" && index == 1) {
					t.Fatalf("page %d solid=%v", index, got)
				}
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Fatalf("page %d: %v", index, err)
				}
				if !bytes.Equal(body, page.data) {
					t.Fatalf("page %d decoded %d bytes, want %d", index, len(body), len(page.data))
				}
			}
			if _, err := reader.Next(); !errors.Is(err, io.EOF) {
				t.Fatalf("end: %v", err)
			}
		})
	}
}
