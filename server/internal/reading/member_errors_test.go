package reading

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"testing"
)

func TestEPUBUnreadableMemberContent(t *testing.T) {
	for _, tc := range []struct{ name, mime, body string }{
		{"chapter.xhtml", "application/xhtml+xml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><script>unsafe()</script></body></html>`},
		{"style.css", "text/css", `p { background: url(https://example.invalid/secret); }`},
		{"image.png", "image/png", "not an image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixtureZIP(t, [][2]string{{tc.name, tc.body}, {"good.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Readable</p></body></html>`}})
			a, err := openArchive(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			e := &EPUB{archive: a, allowed: map[string]string{tc.name: tc.mime, "good.xhtml": "application/xhtml+xml"}}
			if _, _, err := e.Asset(context.Background(), tc.name); !errors.Is(err, ErrMemberUnreadable) {
				t.Fatalf("content failure = %v", err)
			}
			if _, _, err := e.Asset(context.Background(), "good.xhtml"); err != nil {
				t.Fatal("sibling failed", err)
			}
			if _, _, err := e.Asset(context.Background(), "missing.xhtml"); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrMemberUnreadable) {
				t.Fatalf("invalid request = %v", err)
			}
		})
	}
}

func TestComicMemberLimitsAndSourceErrorsStayDistinct(t *testing.T) {
	large := []byte(fixturePNG(t, 64, 64))
	binary.BigEndian.PutUint32(large[16:20], 32769)
	binary.BigEndian.PutUint32(large[29:33], crc32.ChecksumIEEE(large[12:29]))
	for _, tc := range []struct {
		name      string
		raw       []byte
		readError error
		member    bool
	}{
		{name: "invalid image", raw: []byte("not an image"), member: true},
		{name: "image dimensions", raw: large, member: true},
		{name: "member byte limit", readError: ErrLimit, member: true},
		{name: "source I/O", readError: io.ErrUnexpectedEOF},
		{name: "source currentness", readError: ErrChanged},
		{name: "cancellation", readError: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Comic{Names: []string{"page.png"}, pageReader: func(context.Context, string, int64) ([]byte, error) { return tc.raw, tc.readError }}
			_, err := c.Page(context.Background(), 0, 0)
			if errors.Is(err, ErrMemberUnreadable) != tc.member {
				t.Fatalf("member error = %v, want %v", err, tc.member)
			}
			if tc.readError != nil && !errors.Is(err, tc.readError) {
				t.Fatalf("source error lost: %v", err)
			}
		})
	}
}
