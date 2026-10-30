package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

type readingFixture struct {
	f                           *v1Fixture
	d                           Dependencies
	s                           *reading.Service
	p                           identity.Principal
	library, item, source, path string
	fileAuthor                  string
}

// fixtureFileAuthor is the one exact creator the built-in readingtest EPUB and
// CBZ publications carry. A caller-supplied publication does not assume one.
const fixtureFileAuthor = "Portico"

// The scan, identity, permissions, schema, helper and filesystem are real. No
// replacement Reading tables, permissive authorizer or playback source trick
// is installed. This private-boundary check does not claim generated API/UI
// composition; that is checked separately after canonical contracts land.
func newReadingFixture(t *testing.T, kind, format string, publications ...[]byte) *readingFixture {
	t.Helper()
	f := newV1Fixture(t, 0)
	root := t.TempDir()
	name := "Original publication." + format
	var raw []byte
	switch format {
	case "epub":
		raw = readingtest.EPUB()
	case "pdf":
		raw = readingtest.PDF()
	case "cbz":
		raw = readingtest.CBZ()
	}
	if len(publications) > 1 {
		t.Fatal("one actual source publication is required")
	}
	if len(publications) == 1 {
		raw = publications[0]
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.cat.SetStorage(f.deps.Storage)
	library, err := f.cat.Create("Original reading", kind, root)
	if err != nil {
		t.Fatal(err)
	}
	// This fixture requests each scan explicitly. A monitor's startup scan can
	// otherwise replace the requested job's sealed barrier just as the helper
	// stops ingestion, leaving attribution correctly parked on an OPEN scan.
	// Use the owner's real setting so only the requested scans own this source.
	settingsPath := "/v1/admin/libraries/" + library.ID + "/settings"
	w := readingHTTP(t, f.handler, "GET", settingsPath, f.owner.AccessToken, "", nil)
	var document administration.LibraryDocument
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &document) != nil {
		t.Fatalf("reading fixture settings: %d %s", w.Code, w.Body.String())
	}
	document.Settings.MonitorChanges = false
	w = readingHTTP(t, f.handler, "PUT", settingsPath, f.owner.AccessToken, "", administration.Change[administration.LibrarySettings]{ExpectedRevision: document.Revision, OperationID: "reading-fixture-monitor", Settings: document.Settings})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &document) != nil || document.Settings.MonitorChanges {
		t.Fatalf("reading fixture explicit scans: %d %s", w.Code, w.Body.String())
	}
	readingScan(t, f, library.ID)
	d := f.deps
	d.Hosted = nil
	d.directSchema = recordingaccess.NewSchema()
	d.Reading = nil
	s := NewReadingService(d)
	d.Reading = s
	previous := f.id.OnFamilyRevokedTx
	f.id.OnFamilyRevokedTx = func(ctx context.Context, tx *sql.Tx, family identity.FamilyState) error {
		if previous != nil {
			if err := previous(ctx, tx, family); err != nil {
				return err
			}
		}
		return s.OnFamilyRevokedTx(ctx, tx, family)
	}
	t.Cleanup(func() { s.Close() })
	p, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	r := &readingFixture{f: f, d: d, s: s, p: p, library: library.ID, path: path}
	if len(publications) == 0 && (format == "epub" || format == "cbz") {
		r.fileAuthor = fixtureFileAuthor
	}
	if err = f.db.QueryRow(`SELECT pid(e.public_id),a.token FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_assets a ON a.id=p.asset_id WHERE cl.library_id=?`, library.ID).Scan(&r.item, &r.source); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *readingFixture) bytesRequest(t *testing.T, method, path string, session *reading.Session, revision, token string, headers map[string]string) *httptest.ResponseRecorder {
	return r.bytesRequestWithQuery(t, method, path, session, revision, token, headers, nil)
}

func (r *readingFixture) bytesRequestWithQuery(t *testing.T, method, path string, session *reading.Session, revision, token string, headers map[string]string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	query := url.Values{"sourceId": {r.source}, "sessionId": {session.ID}}
	for key, values := range extra {
		query[key] = values
	}
	req := httptest.NewRequest(method, "/v1/read/items/"+r.item+path+"?"+query.Encode(), nil)
	req.SetPathValue("id", r.item)
	if strings.HasPrefix(path, "/assets/") {
		req.SetPathValue("asset", strings.TrimPrefix(path, "/assets/"))
	}
	if strings.HasPrefix(path, "/pages/") {
		req.SetPathValue("page", strings.TrimPrefix(path, "/pages/"))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if revision != "" {
		req.Header.Set("If-Match", `"`+revision+`"`)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	switch {
	case path == "/source":
		r.d.serveReadingSource(w, req)
	case strings.HasPrefix(path, "/assets/"):
		r.d.serveReadingAsset(w, req)
	default:
		r.d.serveReadingPage(w, req)
	}
	return w
}

func readingOwnerLogin(t *testing.T, r *readingFixture) identity.DirectSignIn {
	t.Helper()
	login, err := r.f.id.DirectLogin(context.Background(), "owner", "long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	return login
}

func readingScan(t *testing.T, f *v1Fixture, library string) {
	t.Helper()
	readingScanWithin(t, f, library, 30*time.Second)
}

func readingScanWithin(t *testing.T, f *v1Fixture, library string, timeout time.Duration) {
	t.Helper()
	scan := ingestion.New(f.db, f.cat, assets.Probe{})
	scan.SetStorage(f.deps.Storage)
	job, err := scan.Queue(library)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); scan.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("reading scanner did not stop")
		}
	})
	deadline := time.Now().Add(timeout)
	for {
		state, err := scan.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			if state.Warnings != 0 {
				t.Fatalf("reading scan warnings: %+v", state)
			}
			break
		}
		if state.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("reading scan did not publish: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	// COMPLETE path publication is now separate durable catalogue work. These
	// tests ask for completed attribution, rather than the approved interim view.
	worker := compactcatalog.NewWorker(f.db)
	settleCtx, stop := context.WithDeadline(context.Background(), deadline)
	defer stop()
	for settleCtx.Err() == nil {
		var pending bool
		if err := f.db.QueryRowContext(settleCtx, `SELECT EXISTS(SELECT 1 FROM reading_path_scope q JOIN catalog_libraries cl ON cl.id=q.library_id WHERE cl.library_id=? AND q.queued=1) OR EXISTS(SELECT 1 FROM reading_path_dirty_work w JOIN catalog_libraries cl ON cl.id=w.library_id WHERE cl.library_id=?) OR EXISTS(SELECT 1 FROM reading_path_build)`, library, library).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		worked, err := worker.Step(settleCtx, 64)
		if err != nil {
			t.Fatal(err)
		}
		if worked == 0 {
			// D1 may deliberately await a real quiet gap after consecutive
			// scans. Tight iteration counts can expire before that clock moves.
			// Keep the caller's existing whole-scan deadline, and pace only idle
			// turns; successful attribution/byte assertions remain unchanged.
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("completed Reading path attribution did not drain before the scan deadline")
}
