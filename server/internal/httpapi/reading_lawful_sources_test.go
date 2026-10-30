package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// comicInfoWriter reads the fixture's own ComicInfo.xml, independently of the
// server's publication and database, using the same exact root entry and field
// the real parser accepts.
func comicInfoWriter(t *testing.T, raw []byte) string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range z.File {
		if entry.Name != "ComicInfo.xml" {
			continue
		}
		body, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		var info struct {
			Writer string `xml:"Writer"`
		}
		if err = xml.NewDecoder(body).Decode(&info); err != nil {
			t.Fatal("lawful ComicInfo.xml", err)
		}
		return strings.TrimSpace(info.Writer)
	}
	t.Fatal("lawful input has no ComicInfo.xml")
	return ""
}

// These optional large fixtures are retained outside Git by the lane owner.
// Their provenance is in publisher-fixture-custody.json. A configured run must
// use the exact lawful input, never a synthetic substitute or source revision
// derived from the fixture hash. No public routing/browser proof is implied.
func lawfulReadingInput(t *testing.T, name string, size int64, digest string) []byte {
	t.Helper()
	root := os.Getenv("PORTICO_READING_FIXTURE_DIR")
	if root == "" {
		t.Skip("set PORTICO_READING_FIXTURE_DIR to the retained lawful publication inputs")
	}
	path := filepath.Join(root, name)
	info, err := os.Stat(path)
	if err != nil || info.Size() != size {
		t.Fatal("missing or changed lawful input", name, err)
	}
	b, err := os.ReadFile(path)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(b)) != digest {
		t.Fatal("lawful input digest mismatch", name, err)
	}
	return b
}

func TestReadingPrivateLawfulPDFExactRanges(t *testing.T) {
	// Government Publishing Office, CDOC-110hdoc50; original 85-page PDF.
	raw := lawfulReadingInput(t, "us-constitution.pdf", 4020598, "7f29c4a79e310cc7bcee5f3fd07fb1367e463bd4530fa5d854ebdd4fd4402e57")
	r := newReadingFixture(t, "book", "pdf", raw)
	session, publication, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	revision := publication.Selection.Revision
	if publication.Selection.Size != int64(len(raw)) || publication.Selection.SourceID != r.source {
		t.Fatal("wrong selected source size/identity", publication.Selection)
	}
	w := r.bytesRequest(t, "HEAD", "/source", session, revision, r.f.owner.AccessToken, nil)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(raw)) || w.Header().Get("ETag") != `"`+revision+`"` {
		t.Fatal("HEAD facts", w.Code, w.Header())
	}
	delivered := sha256.New()
	chunks := 0
	for start := 0; start < len(raw); start += 64 << 10 {
		end := min(start+64<<10, len(raw))
		w := r.bytesRequest(t, "GET", "/source", session, revision, r.f.owner.AccessToken, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", start, end-1)})
		if w.Code != 206 || w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end-1, len(raw)) || w.Header().Get("Content-Length") != strconv.Itoa(end-start) || !bytes.Equal(w.Body.Bytes(), raw[start:end]) {
			t.Fatalf("exact PDF extent %d..%d failed: status=%d, headers=%v", start, end, w.Code, w.Header())
		}
		if w.Header().Get("ETag") != `"`+revision+`"` || w.Header().Get("Cache-Control") != "private, no-store, no-transform" {
			t.Fatal("PDF extent lost its captured revision/private scope")
		}
		delivered.Write(w.Body.Bytes())
		chunks++
	}
	if fmt.Sprintf("%x", delivered.Sum(nil)) != "7f29c4a79e310cc7bcee5f3fd07fb1367e463bd4530fa5d854ebdd4fd4402e57" {
		t.Fatal("bounded authorized extents do not reconstruct the actual PDF")
	}
	// This page is a known actual fixture fact, not a server-estimated count.
	page := int64(84)
	position, saved, err := r.s.SaveSessionProgress(context.Background(), r.p, session.ID, 0, reading.Location{Format: "pdf", PageIndex: &page, Progression: 1})
	if err != nil || !saved || position.SourceID != r.source || position.SourceRevision != revision || *position.Location.PageIndex != 84 {
		t.Fatal("actual version position", position, saved, err)
	}
	if err := r.f.id.LogoutToken(context.Background(), r.f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	w = r.bytesRequest(t, "GET", "/source", session, revision, r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-65535", "If-None-Match": `"` + revision + `"`})
	if w.Code == 206 || w.Code == 304 || bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
		t.Fatal("revoked reader received a cached or ranged body", w.Code)
	}
	t.Logf("actual PDF: %d exact authorized 64KiB extents, %d total bytes; source revision came from live inventory/session", chunks, len(raw))
}

func TestReadingPrivateLawfulComicPagesAndReturnedVariants(t *testing.T) {
	// Six original public-domain Winsor McCay pages from 1905, stored with
	// their actual date labels and original JPEGs, plus actual ComicInfo.
	raw := lawfulReadingInput(t, "little-nemo-six.cbz", 4086313, "ec7f937c2f32a3ed2d04522ef79e073f09e1dda263410dd4dbcb0929d1789671")
	r := newReadingFixture(t, "comic", "cbz", raw)
	session, publication, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	pages := []struct {
		name, digest  string
		width, height int
	}{
		{"1905-10-15-page.jpg", "2f2af0a1480eb620b908ee655de844d74340145d2626d497497c18a2b4a80bcb", 1025, 1400},
		{"1905-10-22-page.jpg", "c5ecc3a9e6eec8258c5c4984e07e534856d2ad672e171e78690c0614f1c89ecb", 1025, 1400},
		{"1905-10-29-page.jpg", "8bace5915fa9fa99431036e1e29597aebd1e3e1dba1e63a8289f2f9d3140fba6", 1025, 1400},
		{"1905-11-05-page.jpg", "16586b20d7940cee3123f2dcb81b1efa10edcf4bbbbf5352e492fff157d56a2d", 1775, 2427},
		{"1905-11-12-page.jpg", "8eae9b9e60c695ce5da0bd136231b6a9e9a8abe52dd82d7e6bee379cfe37d20a", 1775, 2427},
		{"1905-11-19-page.jpg", "7de67d3a65bcc70c1f557c3ddaafd5527d6f4790d4ae95baf626154047a636d0", 1741, 2405},
	}
	if len(publication.PageNames) != len(pages) || publication.Metadata.Author != comicInfoWriter(t, raw) {
		t.Fatal("not the actual six page facts or accepted embedded author", publication)
	}
	revision := publication.Selection.Revision
	for index, page := range pages {
		if publication.PageNames[index] != page.name {
			t.Fatal("invented or reordered page name", publication.PageNames)
		}
		w := r.bytesRequest(t, "GET", "/pages/"+strconv.Itoa(index), session, revision, r.f.owner.AccessToken, nil)
		if w.Code != 200 || fmt.Sprintf("%x", sha256.Sum256(w.Body.Bytes())) != page.digest || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() > 32<<20 {
			t.Fatalf("actual full page%d failed: %d size%d", index, w.Code, w.Body.Len())
		}
		info, format, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil || format != "jpeg" || info.Width != page.width || info.Height != page.height {
			t.Fatal("full page observed facts", index, info, format, err)
		}
	}
	for _, width := range []int{32, 320, 2048} {
		w := r.bytesRequestWithQuery(t, "GET", "/pages/0", session, revision, r.f.owner.AccessToken, nil, url.Values{"width": {strconv.Itoa(width)}})
		if w.Code != 200 || w.Body.Len() > 16<<20 {
			t.Fatal("bounded variant", width, w.Code, w.Body.Len())
		}
		info, format, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		wantWidth := min(width, pages[0].width)
		wantHeight := pages[0].height * wantWidth / pages[0].width
		if err != nil || info.Width != wantWidth || info.Height != wantHeight || w.Header().Get("Content-Type") != "image/"+format || w.Header().Get("ETag") != `"`+revision+`"` {
			t.Fatal("returned variant must own its actual MIME/dimensions", width, info, format, err, w.Header())
		}
	}
	for _, width := range []string{"31", "2049", "0"} {
		w := r.bytesRequestWithQuery(t, "GET", "/pages/0", session, revision, r.f.owner.AccessToken, nil, url.Values{"width": {width}})
		if w.Code != 400 {
			t.Fatal("variant width boundary", width, w.Code)
		}
	}
	if w := r.bytesRequest(t, "GET", "/source", session, revision, r.f.owner.AccessToken, nil); w.Code != 404 {
		t.Fatal("whole CBZ source was exposed", w.Code)
	}
	if w := r.bytesRequest(t, "GET", "/pages/0", session, strings.Repeat("0", 64), r.f.owner.AccessToken, nil); w.Code != 412 {
		t.Fatal("changed revision reached page bytes", w.Code)
	}
	if err := r.f.id.LogoutToken(context.Background(), r.f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	w := r.bytesRequest(t, "GET", "/pages/0", session, revision, r.f.owner.AccessToken, map[string]string{"If-None-Match": `"` + revision + `"`})
	if w.Code == 200 || w.Code == 304 || bytes.HasPrefix(w.Body.Bytes(), []byte{0xff, 0xd8}) {
		t.Fatal("revoked family received a page or warm conditional", w.Code)
	}
	t.Log("six original full pages and three observed returned variants stayed within one authorized publication revision")
}
