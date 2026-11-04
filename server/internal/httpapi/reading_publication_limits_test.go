package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingEPUBOversizedMemberRefusedAtOpen(t *testing.T) {
	original := readingtest.EPUB()
	z, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	w := zip.NewWriter(&raw)
	for _, entry := range z.File {
		if err := w.Copy(entry); err != nil {
			t.Fatal(err)
		}
	}
	member, err := w.CreateHeader(&zip.FileHeader{Name: "EPUB/large.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(member, strings.NewReader(strings.Repeat("x", 20<<20)), 20<<20); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := newReadingFixture(t, "book", "epub", raw.Bytes())
	response := readingHTTP(t, New(r.d), "POST", "/v1/reading/sessions", r.f.owner.AccessToken, "", ReadingSessionRequest{r.item, r.source})
	if response.Code != 422 || v1Code(response) != "reading_publication_too_large" {
		t.Fatalf("20 MiB member at open: %d %s", response.Code, response.Body.String())
	}
}

func TestReadingEPUBManifestPublishesResourceLimits(t *testing.T) {
	r := newReadingFixture(t, "book", "epub")
	response := readingHTTP(t, New(r.d), "POST", "/v1/reading/sessions", r.f.owner.AccessToken, "", ReadingSessionRequest{r.item, r.source})
	var manifest struct {
		Limits struct{ MemberBytes, TotalBytes int64 }
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &manifest) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	if manifest.Limits.MemberBytes != 16<<20 || manifest.Limits.TotalBytes != 256<<20 {
		t.Fatalf("manifest limits = %+v", manifest.Limits)
	}
}
