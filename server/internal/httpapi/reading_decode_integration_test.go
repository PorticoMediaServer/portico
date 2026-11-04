package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingUnreadableEPUBMemberKeepsSession(t *testing.T) {
	r := newReadingFixture(t, "book", "epub")
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var changed bytes.Buffer
	w := zip.NewWriter(&changed)
	for _, entry := range z.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		header := entry.FileHeader
		var output io.Writer
		if entry.Name == "mimetype" {
			output, err = w.CreateRaw(&header)
		} else {
			output, err = w.CreateHeader(&header)
		}
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "EPUB/chapter.xhtml" {
			body = bytes.Replace(body, []byte("</body>"), []byte("<script>alert(1)</script></body>"), 1)
		}
		if _, err = output.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(r.path, changed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	session, document, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	// A bad requested resource is input validation, not an observed decode error.
	response := r.bytesRequest(t, "GET", "/assets/EPUB/missing.xhtml", session, document.Selection.Revision, r.f.owner.AccessToken, nil)
	if response.Code != 400 {
		t.Fatal(response.Code, response.Body.String())
	}
	probe, err := r.s.OpenSession(context.Background(), r.p, session.ID)
	if err != nil {
		t.Fatal("invalid input retired the book", err)
	}
	probe.Close()
	response = r.bytesRequest(t, "GET", "/assets/EPUB/chapter.xhtml", session, document.Selection.Revision, r.f.owner.AccessToken, nil)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "reading_member_unreadable") || bytes.Contains(response.Body.Bytes(), []byte("<script>")) {
		t.Fatal("unsafe member response", response.Code, response.Body.String())
	}
	response = r.bytesRequest(t, "GET", "/assets/EPUB/nav.xhtml", session, document.Selection.Revision, r.f.owner.AccessToken, nil)
	if response.Code != 200 {
		t.Fatal("bad chapter retired the session", response.Code, response.Body.String())
	}
}

func TestReadingUnreadableComicPageKeepsSession(t *testing.T) {
	raw := replaceReadingMember(t, readingtest.CBZ(), "page1.png", []byte("not an image"))
	r := newReadingFixture(t, "comic", "cbz", raw)
	r.fileAuthor = fixtureFileAuthor
	h := New(r.d)
	m := readingOpened(t, r, h)
	base := "/v1/reading/sessions/" + m.SessionID
	response := readingHTTP(t, h, "GET", base+"/pages/0", r.f.owner.AccessToken, m.Revision, nil)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "reading_member_unreadable") {
		t.Fatal("bad page was not a member error", response.Code, response.Body.String())
	}
	response = readingHTTP(t, h, "GET", base+"/pages/1", r.f.owner.AccessToken, m.Revision, nil)
	if response.Code != 200 || !bytes.HasPrefix(response.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatal("next page could not be read", response.Code, response.Body.String())
	}
}

func replaceReadingMember(t *testing.T, raw []byte, name string, replacement []byte) []byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var changed bytes.Buffer
	w := zip.NewWriter(&changed)
	for _, entry := range z.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == name {
			body = replacement
		}
		header := entry.FileHeader
		output, err := w.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = output.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return changed.Bytes()
}
