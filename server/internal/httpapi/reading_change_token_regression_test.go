package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// A permission-only write is not a content change. The real unconditional
// HTTP session POST keeps the published revision, the reading detail keeps the
// title visible, and the captured bytes still read. A real edit refuses the
// stale observation over the same HTTP route until the real scan republishes
// the item, and sessions captured before the edit stay stale afterwards.
func TestReadingPermissionChangeKeepsRevisionAndRealEditRepublishes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	// The scan admits the file, but the detail route composes the ready Reading
	// attribution projection. Run the real producer once (which adopts the
	// publication's own facts and queues the projection) and drain the actual
	// bounded catalogue worker, the same composition the other real Reading
	// detail reads use. Without it the first detail read is a stale projection
	// (409) before any file change, not a missing title.
	readingPublishFileFacts(t, r)
	h := New(r.d)
	token := r.f.owner.AccessToken
	ctx := context.Background()
	detail := func() *httptest.ResponseRecorder {
		return readingHTTP(t, h, "GET", "/v1/items/"+r.item+"/detail", token, "", nil)
	}
	// The detail assertion keeps expecting exactly 200; on failure it reports
	// the refusal body so a composition fault is diagnosable, never silent.
	expectDetail := func(label string) {
		t.Helper()
		if w := detail(); w.Code != 200 {
			t.Fatalf("%s: %d %s", label, w.Code, w.Body.String())
		}
	}
	// The real unconditional session POST, exactly as the client makes it.
	open := func() ReadingSessionResponse {
		return readingOpened(t, r, h)
	}
	// One bounded current extent through the same source handler the client uses.
	source := func(sessionID, revision string) *httptest.ResponseRecorder {
		query := url.Values{"sourceId": {r.source}, "sessionId": {sessionID}}
		req := httptest.NewRequest("GET", "/v1/read/items/"+r.item+"/source?"+query.Encode(), nil)
		req.SetPathValue("id", r.item)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("If-Match", `"`+revision+`"`)
		req.Header.Set("Range", "bytes=0-4")
		w := httptest.NewRecorder()
		r.d.serveReadingSource(w, req)
		return w
	}
	// A normal full rescan: the real scanner, then the bounded catalogue worker
	// that publishes the Reading attribution the scan queued. The HTTP reads
	// below run only after that derivation drain, as the live server does.
	rescan := func() {
		readingScan(t, r.f, r.library)
		readingSeriesDrain(t, r)
	}

	expectDetail("reading detail before the change")
	first := open()
	if err := os.Chmod(r.path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(r.path, 0600) })
	// chmod moves the file's ctime and nothing a reader can observe.
	again := open()
	if again.Revision != first.Revision {
		t.Fatalf("permission-only change moved the revision: %s -> %s", first.Revision, again.Revision)
	}
	expectDetail("permission-only change hid the reading title")
	if w := source(first.SessionID, first.Revision); w.Code != 206 || w.Body.String() != "%PDF-" {
		t.Fatalf("permission-only change did not serve the current source: %d %q", w.Code, w.Body.String())
	}
	// A normal full rescan after the permission-only change republishes nothing:
	// the published revision the client already holds stays current.
	rescan()
	unchanged := open()
	if unchanged.Revision != first.Revision {
		t.Fatalf("permission-only rescan republished %s -> %s", first.Revision, unchanged.Revision)
	}
	if unchanged.Size != first.Size {
		t.Fatalf("permission-only rescan moved the size: %d -> %d", first.Size, unchanged.Size)
	}
	expectDetail("permission-only rescan hid the reading title")

	// A real edit changes the bytes, the size and the modification time.
	if err := os.Chmod(r.path, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n% second edition\n")...)
	if err = os.WriteFile(r.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if w := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{r.item, r.source}); w.Code != 412 {
		t.Fatalf("stale observation served after a real edit: %d %s", w.Code, w.Body.String())
	}
	expectDetail("real edit hid the reading title")
	// The real scan republishes the item; it never disappears.
	rescan()
	fresh := open()
	if fresh.Revision == first.Revision {
		t.Fatal("republished publication kept the stale revision")
	}
	if fresh.Size != int64(len(raw)) {
		t.Fatalf("republished publication has size %d want %d", fresh.Size, len(raw))
	}
	expectDetail("republished reading title")
	if w := source(fresh.SessionID, fresh.Revision); w.Code != 206 || w.Body.String() != "%PDF-" {
		t.Fatalf("republished publication did not serve current bytes: %d %q", w.Code, w.Body.String())
	}
	// Sessions captured before the edit still refuse the stale observation.
	for _, stale := range []string{first.SessionID, again.SessionID, unchanged.SessionID} {
		if _, err = r.s.OpenSession(ctx, r.p, stale); !errors.Is(err, reading.ErrChanged) {
			t.Fatalf("pre-edit session opened a stale observation: %v", err)
		}
	}
	// The captured reader extent proves the current object, not only a manifest.
	reader, err := r.s.OpenSession(ctx, r.p, fresh.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	head := make([]byte, 5)
	if n, readErr := reader.ReadAt(head, 0); n != 5 || string(head) != "%PDF-" {
		t.Fatalf("current publication did not open: %d %q %v", n, head, readErr)
	}
}
