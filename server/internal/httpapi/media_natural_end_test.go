//go:build linux || darwin

package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mediaEndFixture is a real router, identity and catalogue over a real database
// with one owner and direct-playable films, so a media read below is answered
// by the same code the server uses, not by a stub.
type mediaEndFixture struct {
	t       *testing.T
	db      *sql.DB
	root    string
	handler http.Handler
	player  *playback.Service
	owner   identity.Principal
	items   []string
}

func newMediaEndFixture(t *testing.T, films int) *mediaEndFixture {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	player := playback.New(db)
	// No converter is started: these sessions publish their own artifacts the
	// way the producer does, which is what a read at the end of a clip meets.
	hls, err := playback.NewHLS(context.Background(), db, filepath.Join(root, "hls"), "/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := hls.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	player.ConfigureHLS(hls)
	f := &mediaEndFixture{t: t, db: db, root: root, player: player, handler: New(Dependencies{DB: db, Identity: id, Catalog: cat, Ingestion: ingestion.New(db, cat, assets.Probe{}), Playback: player, Hosted: host})}
	setupToken, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"authMode": "local", "setupToken": string(setupToken), "username": "owner", "password": "long-test-password", "name": "Test"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	setup := httptest.NewRequest("POST", "/v1/setup", bytes.NewReader(body))
	setup.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(w, setup)
	if w.Code != 201 {
		t.Fatal("setup", w.Code)
	}
	var auth identity.Envelope
	if err = json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	if f.owner, err = id.Authenticate(auth.AccessToken); err != nil {
		t.Fatal(err)
	}
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range films {
		path := filepath.Join(root, fmt.Sprintf("Film %02d.mkv", i))
		if err = os.WriteFile(path, []byte("fixture source pin"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = seedHTTPMovie(context.Background(), db, lib.ID, path, assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 30}); err != nil {
			t.Fatal(err)
		}
	}
	settleCompactCatalogue(t, db)
	items, _, err := cat.List(catalog.Viewer{Profile: auth.Viewer.ProfileID, Libraries: []string{lib.ID}}, lib.ID, "", 10)
	if err != nil || len(items) != films {
		t.Fatal("catalog", err)
	}
	for _, item := range items {
		f.items = append(f.items, item.ID)
	}
	return f
}

func (f *mediaEndFixture) session(item, request string) playback.Session {
	f.t.Helper()
	session, err := f.player.Create(f.owner, item, "automatic", request)
	if err != nil {
		f.t.Fatal(err)
	}
	if !strings.HasSuffix(session.StreamURL, "/master.m3u8") {
		f.t.Fatalf("session is not a stream: %q", session.StreamURL)
	}
	if err = os.MkdirAll(filepath.Join(f.root, "hls", session.ID), 0700); err != nil {
		f.t.Fatal(err)
	}
	return session
}

func (f *mediaEndFixture) generation(session string) string {
	f.t.Helper()
	var generation int
	if err := f.db.QueryRow(`SELECT generation FROM playback_sessions WHERE id=?`, session).Scan(&generation); err != nil {
		f.t.Fatal(err)
	}
	return strconv.Itoa(generation)
}

func (f *mediaEndFixture) get(url string) *httptest.ResponseRecorder {
	f.t.Helper()
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	return w
}

func segmentURL(session playback.Session, name string) string {
	return strings.TrimSuffix(session.StreamURL, "master.m3u8") + name
}

// TestMediaAtNaturalEndIsAnEndAndRealFailureStaysAnError covers E-mobile-27.
// A short clip's last read arrives while the app stops the session the way a
// player does at the end of a title. That read is the end of the presentation
// (the hidden 404 presentation_ended the grant already answers), never a 500
// from an untyped "playback stopped" internal error; a genuinely failed
// conversion stays an error and is never reported as an end.
func TestMediaAtNaturalEndIsAnEndAndRealFailureStaysAnError(t *testing.T) {
	f := newMediaEndFixture(t, 4)

	// Ordinary delivery of the last segment, so the fixture is real media.
	delivered := f.session(f.items[0], "media-eof-delivered")
	dir := filepath.Join(f.root, "hls", delivered.ID)
	if err := os.WriteFile(filepath.Join(dir, "generation"), []byte(f.generation(delivered.ID)), 0600); err != nil {
		t.Fatal(err)
	}
	media := []byte("encoded media at the end of a short title")
	if err := os.WriteFile(filepath.Join(dir, "segment-000000.ts"), media, 0600); err != nil {
		t.Fatal(err)
	}
	if w := f.get(segmentURL(delivered, "segment-000000.ts")); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), media) || w.Header().Get("Content-Type") != "video/mp2t" {
		t.Fatalf("the last segment: %d %q %q", w.Code, w.Body.Bytes(), w.Header().Get("Content-Type"))
	}

	// A live session whose player asks for the slot at the known end. Thirty
	// seconds on the six-second grid is slots 0..4, so slot 5 is past the end
	// the server already knows. That is the presentation's end too, not an
	// unknown artifact and not a 500.
	past := f.session(f.items[3], "media-eof-past-end")
	if err := os.WriteFile(filepath.Join(f.root, "hls", past.ID, "generation"), []byte(f.generation(past.ID)), 0600); err != nil {
		t.Fatal(err)
	}
	if w := f.get(segmentURL(past, "segment-000005.ts")); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"presentation_ended"`) || strings.Contains(w.Body.String(), `"code":"internal"`) {
		t.Fatalf("a read at the known end of a live session: %d %s", w.Code, w.Body.String())
	}

	// The natural end. The generation stamp is a FIFO, so the request blocks
	// exactly where the producer does, after the grant is resolved and before
	// the segment is admitted; that is the moment the client stops the session.
	ended := f.session(f.items[1], "media-eof-ended")
	endedGeneration := f.generation(ended.ID)
	gate := f.generationFIFO(ended.ID)
	response := f.request(segmentURL(ended, "segment-000000.ts"))
	gate.req = response
	writer := gate.await(t)
	// The app stops the presentation at the end of the clip; the in-flight read
	// is now the one E-mobile-27 saw.
	if err := f.player.Stop(f.owner, ended.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(endedGeneration)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	end := response.join()
	if end.Code != http.StatusNotFound || !strings.Contains(end.Body.String(), `"code":"presentation_ended"`) || strings.Contains(end.Body.String(), `"code":"internal"`) {
		t.Fatalf("a read at the natural end: %d %s", end.Code, end.Body.String())
	}

	// Control: a real conversion failure is still an error, and is not the end.
	failing := f.session(f.items[2], "media-eof-failed")
	failDir := filepath.Join(f.root, "hls", failing.ID)
	if err := os.WriteFile(filepath.Join(failDir, "generation"), []byte(f.generation(failing.ID)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'failed') ON CONFLICT(session_id) DO UPDATE SET status='failed'`, failing.ID, failDir); err != nil {
		t.Fatal(err)
	}
	failed := f.get(segmentURL(failing, "segment-000000.ts"))
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), `"code":"internal"`) || strings.Contains(failed.Body.String(), "presentation_ended") {
		t.Fatalf("a failed conversion: %d %s", failed.Code, failed.Body.String())
	}
}

// mediaRequest is one real HTTP media read on its own goroutine. Its context is
// cancelled on cleanup so a read that has not reached the FIFO stops at a real
// request cancellation; a read already inside os.Open or os.ReadFile is not
// cancellable that way, so the FIFO release below unblocks it structurally.
type mediaRequest struct {
	t        *testing.T
	cancel   context.CancelFunc
	done     chan struct{}
	response *httptest.ResponseRecorder
}

func (f *mediaEndFixture) request(target string) *mediaRequest {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &mediaRequest{t: f.t, cancel: cancel, done: make(chan struct{})}
	req := httptest.NewRequest("GET", target, nil).WithContext(ctx)
	go func() {
		defer close(r.done)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		r.response = w
	}()
	return r
}

func (r *mediaRequest) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *mediaRequest) join() *httptest.ResponseRecorder {
	r.t.Helper()
	if w, ok := r.await(30 * time.Second); ok {
		return w
	}
	r.t.Fatal("the media request did not finish")
	return nil
}

// await is the one join shared by test assertions and cleanup. The response is
// published before done closes, so receiving from done orders the read.
func (r *mediaRequest) await(timeout time.Duration) (*httptest.ResponseRecorder, bool) {
	select {
	case <-r.done:
		return r.response, true
	case <-time.After(timeout):
		return nil, false
	}
}

// generationFIFO is a session's generation stamp as a FIFO. It pins the media
// read at exactly one step, after grant resolution and inside the artifact
// read, so the test controls the race instead of sleeping. Its cleanup is
// structural: on every exit path it closes the writer, releases a reader
// blocked in os.Open or os.ReadFile, cancels the request, and joins it. A
// request deadline alone cannot cancel those file operations.
type generationFIFO struct {
	t      *testing.T
	path   string
	req    *mediaRequest
	writer *os.File
}

func (f *mediaEndFixture) generationFIFO(session string) *generationFIFO {
	f.t.Helper()
	g := &generationFIFO{t: f.t, path: filepath.Join(f.root, "hls", session, "generation")}
	if err := syscall.Mkfifo(g.path, 0600); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(g.release)
	return g
}

// await waits until the request has opened the generation stamp, which is only
// possible once it has passed grant resolution and is inside the media route. A
// non-blocking writer fails with ENXIO until that reader exists, so no sleep
// stands in for the ordering.
func (g *generationFIFO) await(t *testing.T) *os.File {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		fd, err := syscall.Open(g.path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			g.writer = os.NewFile(uintptr(fd), g.path)
			return g.writer
		}
		if err != syscall.ENXIO {
			t.Fatalf("open generation stamp: %v", err)
		}
		if g.req.finished() {
			w := g.req.join()
			t.Fatalf("the media request answered before reading the generation: %d %s", w.Code, w.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("the media request never read the generation")
		}
		time.Sleep(time.Millisecond)
	}
}

func (g *generationFIFO) release() {
	if g.req != nil {
		g.req.cancel()
	}
	if g.writer != nil {
		_ = g.writer.Close()
	}
	if g.req == nil {
		return
	}
	// The writer close above gives a reader already inside os.ReadFile EOF. A
	// reader still blocked in os.Open needs a writer to open and close; ENXIO
	// only says it has not arrived yet, so retry until it does or the request
	// has finished on its cancelled context.
	deadline := time.Now().Add(10 * time.Second)
	for !g.req.finished() {
		fd, err := syscall.Open(g.path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = syscall.Close(fd)
			break
		}
		if err != syscall.ENXIO || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := g.req.await(10 * time.Second); !ok {
		g.t.Errorf("the media request did not finish after its FIFO was released")
	}
}
