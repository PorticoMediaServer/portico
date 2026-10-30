package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// TestInventoryConfigShowsCommittedProgressWithoutAuthorityChange is the wire
// boundary for SRV002: the existing admin DTO's lastProgressAt reflects a
// committed page, the authority generation does not move, and a real disable
// plus a revoked credential are still refused.
func TestInventoryConfigShowsCommittedProgressWithoutAuthorityChange(t *testing.T) {
	f := newV1Fixture(t, 0)
	ctx := context.Background()
	library, source := "movies", "movies"
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RelativePath: ".", Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET root_identity=?,identity_confirmed=1 WHERE id=?`, page.RootIdentity, source); err != nil {
		t.Fatal(err)
	}
	src, err := f.cat.LibrarySource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	job := "http-progress"
	at := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running',?)`, job, library, at); err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) VALUES(?,?,?,?,?,1,'inventory','full')`, job, source, src.Generation, src.Incarnation, src.RootIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'.')`, job); err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?)`, source, job); err != nil {
		t.Fatal(err)
	}
	read := func() catalog.LibrarySource {
		t.Helper()
		w := f.raw("GET", "/v1/admin/libraries/"+library+"/inventory-config", f.owner.AccessToken, nil, nil)
		if w.Code != 200 {
			t.Fatalf("inventory config: %d %s", w.Code, w.Body.String())
		}
		var document struct {
			Sources []catalog.LibrarySource `json:"sources"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if len(document.Sources) != 1 {
			t.Fatalf("sources: %+v", document.Sources)
		}
		return document.Sources[0]
	}
	if before := read(); before.LastProgressAt != "" {
		t.Fatalf("a new source projected progress %q", before.LastProgressAt)
	}
	generation := dbwork.AuthorityGeneration()
	if err = f.cat.CommitInventoryPage(ctx, job, ".", "", page); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after.LastProgressAt == "" {
		t.Fatal("the wire field did not carry the committed progress")
	}
	if _, err = time.Parse(time.RFC3339Nano, after.LastProgressAt); err != nil {
		t.Fatalf("wire progress is not the plain string: %q %v", after.LastProgressAt, err)
	}
	if after.ID != source || after.LibraryID != library || !after.Enabled || after.Path != filepath.Clean(f.root) {
		t.Fatalf("progress changed the existing source DTO: %+v", after)
	}
	if got := dbwork.AuthorityGeneration(); got != generation {
		t.Fatalf("the progress page moved authority across the wire boundary: %d → %d", generation, got)
	}
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE id=?`, source); err != nil {
		t.Fatal(err)
	}
	if disabled := read(); disabled.Enabled {
		t.Fatal("a real source disable was not visible on the existing DTO")
	}
	if got := dbwork.AuthorityGeneration(); got <= generation {
		t.Fatalf("a real source disable did not move AuthorityGeneration: %d", got)
	}
	if err = f.id.LogoutToken(ctx, f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	if w := f.raw("GET", "/v1/admin/libraries/"+library+"/inventory-config", f.owner.AccessToken, nil, nil); w.Code != 401 {
		t.Fatalf("a revoked credential read inventory config: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("GET", "/v1/events?waitMs=0", f.owner.AccessToken, nil, nil); w.Code != 401 {
		t.Fatalf("a revoked credential opened the event feed: %d %s", w.Code, w.Body.String())
	}
}

// TestOpenReadingBodyIsRefusedAfterARealSourceDisable keeps a still-valid open
// byte body honest after a real source transition: nothing about the file
// changes, the source is disabled, and both the already-open reader and a fresh
// /source request are refused.
func TestOpenReadingBodyIsRefusedAfterARealSourceDisable(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	session, document, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	body, err := r.s.OpenSession(ctx, r.p, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	head := make([]byte, 5)
	if n, err := body.ReadAt(head, 0); n != 5 || err != nil || string(head) != "%PDF-" {
		t.Fatalf("the open body did not serve its bytes: %d %q %v", n, head, err)
	}
	if w := r.bytesRequest(t, "GET", "/source", session, document.Selection.Revision, r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-4"}); w.Code != 206 {
		t.Fatalf("a valid /source request before the disable: %d %s", w.Code, w.Body.String())
	}
	info, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, r.f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE library_id=?`, r.library); err != nil {
		t.Fatal(err)
	}
	if n, err := body.ReadAt(head, 0); n != 0 || err == nil {
		t.Fatalf("the open body served %d bytes after its source was disabled: %v", n, err)
	}
	if w := r.bytesRequest(t, "GET", "/source", session, document.Selection.Revision, r.f.owner.AccessToken, map[string]string{"Range": "bytes=0-4"}); w.Code == 206 || w.Code == 200 {
		t.Fatalf("a fresh /source request was served after the disable: %d", w.Code)
	}
	after, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	now, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(now, raw) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("the test changed the file; the refusal must come from the source transition alone")
	}
}

// TestOpenEventStreamClosesOnRevocationThroughTheRealHub runs the production
// feed loop: an already-open, authorised /v1/events stream must end promptly
// when its credential is revoked, with no manual Hub.Wake, heartbeat (25 s,
// beyond this test's deadline) or catch-up doing the work.
func TestOpenEventStreamClosesOnRevocationThroughTheRealHub(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hubCtx, stopHub := context.WithCancel(context.Background())
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		d.Events.Run(hubCtx)
	}()
	defer func() {
		stopHub()
		<-hubDone
	}()
	// Prime: the security subscription is live once a real library write
	// wakes an observer.
	observer, unobserve := d.Events.Subscribe()
	for i := 0; ; i++ {
		if _, err := dbwork.ExecWrite(ctx, d.DB, dbwork.ClassSecurityFence, `INSERT INTO libraries(id,name,kind,root) VALUES(?,'Prime','movie',?)`, "prime-"+strconv.Itoa(i), t.TempDir()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-observer:
		case <-time.After(50 * time.Millisecond):
			if ctx.Err() != nil {
				t.Fatal("the real feed never woke for a library authority write")
			}
			continue
		}
		break
	}
	unobserve()
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+member.AccessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stream: %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before it opened: %v", err)
		}
		if strings.HasPrefix(line, "event: stream.opened") {
			break
		}
	}
	revoked := time.Now()
	if err = d.Identity.LogoutToken(context.Background(), member.AccessToken); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = reader.ReadString('\n'); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the revoked stream stayed live until the client deadline")
			}
			break
		}
	}
	if waited := time.Since(revoked); waited > 3*time.Second {
		t.Fatalf("the revoked stream took %v to close", waited)
	}
	if w := notificationRequest(mux, "GET", member.AccessToken, "/v1/events?waitMs=0", ""); w.Code != 401 {
		t.Fatalf("a revoked credential reopened the feed: %d %s", w.Code, w.Body.String())
	}
}
