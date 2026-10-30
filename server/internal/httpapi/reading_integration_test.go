package httpapi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingPrivateLocalPDFRangesProgressAndRevocation(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	session, document, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Selection.Format != "pdf" || document.Selection.SourceID != r.source || document.Metadata.Title != "Original publication" {
		t.Fatal(document)
	}
	for _, tc := range []struct {
		method, rangeHeader string
		want                int
	}{{"HEAD", "", 200}, {"GET", "bytes=0-4", 206}, {"GET", "", 416}, {"GET", "bytes=0-999999999", 416}, {"GET", "bytes=0-1,3-4", 416}} {
		w := r.bytesRequest(t, tc.method, "/source", session, document.Selection.Revision, r.f.owner.AccessToken, map[string]string{"Range": tc.rangeHeader})
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.rangeHeader, w.Code, w.Body.String())
		}
		if tc.want == 206 && (w.Body.String() != "%PDF-" || !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes 0-4/")) {
			t.Fatal("wrong exact range", w.Header(), w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store, no-transform" {
			t.Fatal(w.Header())
		}
	}
	if w := r.bytesRequest(t, "GET", "/source", session, "", r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-4"}); w.Code != 428 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.bytesRequest(t, "GET", "/source", session, strings.Repeat("0", 64), r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-4"}); w.Code != 412 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.bytesRequest(t, "GET", "/source", session, document.Selection.Revision, "", map[string]string{"Range": "bytes=0-4"}); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	page := int64(0)
	location := reading.Location{Format: "pdf", PageIndex: &page, Progression: .25}
	position, saved, err := r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, location)
	if err != nil || !saved || position.Revision != 1 || position.ObservedAt == 0 {
		t.Fatal(position, saved, err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, location); !errors.Is(err, reading.ErrConflict) {
		t.Fatal("CAS", err)
	}
	again, err := r.s.SessionProgress(ctx, r.p, session.ID)
	if err != nil || again.Revision != 1 || again.Stale || *again.Location.PageIndex != 0 {
		t.Fatal(again, err)
	}
	source, err := r.s.OpenSession(ctx, r.p, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = r.f.id.LogoutToken(ctx, r.f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if n, err := source.ReadAt(b, 0); n != 0 || err == nil {
		t.Fatalf("revoked source returned%d bytes %q: %v", n, b, err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, session.ID, 1, location); err == nil {
		t.Fatal("revoked session saved progress")
	}
}

func TestReadingPrivateEPUBAndComicActualAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct{ kind, format string }{{"book", "epub"}, {"comic", "cbz"}} {
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			session, document, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
			if err != nil {
				t.Fatal(err)
			}
			if document.Metadata.Author != fixtureFileAuthor {
				t.Fatalf("file author evidence: got %q want %q", document.Metadata.Author, fixtureFileAuthor)
			}
			var path string
			if tc.format == "epub" {
				if len(document.Resources) != 5 {
					t.Fatal(document.Resources)
				}
				path = "/assets/EPUB/chapter.xhtml"
			} else {
				if len(document.PageNames) != 2 || document.PageNames[0] != "page1.png" {
					t.Fatal(document.PageNames)
				}
				path = "/pages/0"
			}
			w := r.bytesRequest(t, "GET", path, session, document.Selection.Revision, r.f.owner.AccessToken, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.format == "epub" && !bytes.Contains(w.Body.Bytes(), []byte("Original chapter")) {
				t.Fatal("not actual EPUB bytes")
			}
			if tc.format == "cbz" && !bytes.HasPrefix(w.Body.Bytes(), []byte{137, 'P', 'N', 'G'}) {
				t.Fatal("not actual image bytes")
			}
			if w = r.bytesRequest(t, "GET", "/source", session, document.Selection.Revision, r.f.owner.AccessToken, nil); w.Code != 404 {
				t.Fatal("archive raw source was exposed", w.Code)
			}
			if err = r.s.CloseSession(r.p, session.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = r.s.OpenSession(context.Background(), r.p, session.ID); err == nil {
				t.Fatal("closed session reopened")
			}
		})
	}
}

func TestReadingPrivateSourceChangeRootEscapeAndSourceDisable(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	session, document, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-2] = 'X'
	if err = os.WriteFile(r.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// A real edit moves the modification time. The session captured before it
	// still refuses to serve the replaced object.
	edited := time.Now().Add(time.Minute)
	if err = os.Chtimes(r.path, edited, edited); err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.OpenSession(ctx, r.p, session.ID); err == nil {
		t.Fatal("stale session served an edited object")
	}
	w := r.bytesRequest(t, "GET", "/source", session, document.Selection.Revision, r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-4"})
	if w.Code == 206 {
		t.Fatal("changed bytes leaked")
	}
	if _, err = dbwork.ExecWrite(ctx, r.f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE library_id=?`, r.library); err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.Resolve(ctx, r.p, r.item, r.source); err == nil {
		t.Fatal("disabled source was eligible")
	}
	// The real rooted helper cannot follow a replaced document outside the root.
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err = os.WriteFile(outside, readingtest.PDF(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, r.path); err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, r.f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=1 WHERE library_id=?`, r.library); err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.Open(ctx, r.p, r.item, r.source, document.Selection.Revision); err == nil {
		t.Fatal("escaped symlink opened")
	}
}

func TestReadingPrivateCancellationAndSessionClosureStopReaders(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	session, _, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(ctx)
	reader, err := r.s.OpenSession(request, r.p, session.ID)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer reader.Close()
	if n, err := reader.ReadAt(make([]byte, 5), 0); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	cancel()
	if err := reader.Context().Err(); err == nil {
		t.Fatal("buffered operations retained a cancelled request")
	}
	if n, err := reader.ReadAt(make([]byte, 5), 0); n != 0 || err == nil {
		t.Fatal("cancelled request returned bytes", n, err)
	}
	if err := reader.Validate(); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled buffered publication fence did not reject request", err)
	}
	// A cancelled extent does not turn its session into a new bearer grant or
	// reopen the old helper. Other requests acquire fresh rooted readers.
	a, err := r.s.OpenSession(ctx, r.p, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := r.s.OpenSession(ctx, r.p, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	login := readingOwnerLogin(t, r)
	if login.Session == nil {
		t.Fatal("missing real sign-in session")
	}
	other, err := r.f.id.Authenticate(login.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.OpenSession(ctx, other, session.ID); err == nil {
		t.Fatal("different sign-in family borrowed the reader session")
	}
	if err = r.s.CloseSession(r.p, session.ID); err != nil {
		t.Fatal(err)
	}
	for _, source := range []*reading.Source{a, b} {
		if err := source.Context().Err(); err == nil {
			t.Fatal("buffered operations retained a closed book")
		}
		if n, err := source.ReadAt(make([]byte, 5), 0); n != 0 || err == nil {
			t.Fatal("closed session returned bytes", n, err)
		}
	}
	if _, err = r.s.OpenSession(ctx, r.p, session.ID); err == nil {
		t.Fatal("closed session reopened")
	}
}
