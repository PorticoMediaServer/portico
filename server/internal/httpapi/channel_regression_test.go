package httpapi

// Live TV and Library Channels regression suite (Plan — Client Playback
// Migration §10, B1). Every behavior runs through a channelDriver on the real
// linear runtime (FFmpeg and the decoder sandbox), so the same suite proves the
// /v2 occurrence implementation today and the v1 session implementation after
// the move (B4 adds its driver; B6 retires this one's /v2 driver). Skipped where
// the runtime can't run (no FFmpeg, no sandbox): portico-runner runs it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	library "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
)

// channelFixtureFence hashes catalog observation parts for library-channel fixtures.
func channelFixtureFence(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// channelView is what a viewer sees of a channel playback, whatever the protocol.
type channelView struct {
	Status                      string // preparing | active | recoverable | ended
	ErrorCode                   string
	Name                        string
	ProgrammeID, Programme      string
	Next                        string
	StreamURL                   string
	BufferGeneration            string
	WindowStartUS, LiveEdgeUS   int64
	WindowEndUS                 int64
	DesiredState                string
	AcknowledgedSeek, SeekError string
}

// channelDriver is one protocol's way to play a channel.
type channelDriver interface {
	name() string
	tune(t *testing.T, ref playback.LinearReference) string
	read(t *testing.T, h string) channelView
	// intent sets play/pause, and optionally a seek (position in µs on the
	// channel timeline, or live).
	intent(t *testing.T, h, state string, seekUS *int64, live bool) (seekID string)
	observe(t *testing.T, h string, v channelView, positionUS int64, ackSeek string)
	stop(t *testing.T, h string)
	// retry asks a failed (recoverable) channel to start its source again.
	retry(t *testing.T, h string)
	// lapse ends the viewer's lease as if they stopped reporting (test hook).
	lapse(t *testing.T, h string)
	// surf tunes ref in place of h (one slot: the old one ends).
	surf(t *testing.T, h string, ref playback.LinearReference) string
	// noTuner tunes ref when every tuner is in use and checks the refusal.
	noTuner(t *testing.T, ref playback.LinearReference)
}

type channelSuite struct {
	f       *tl11CurrentPlaybackHTTPFixture
	channel playback.LinearReference
	film    string
	live    *livechannels.Store
	state   string
}

type tl11CurrentPlaybackHTTPFixture struct {
	d       Dependencies
	h       http.Handler
	token   string
	t       *testing.T
	catalog *catalogtest.Catalog
	item    catalogtest.Item
}

func tl11CurrentPlaybackHTTPWith(t *testing.T, beforeStart func(rt *playbackruntime.Runtime, db *sql.DB, state string, hostedControl *hosted.Service)) *tl11CurrentPlaybackHTTPFixture {
	t.Helper()
	helper := os.Getenv("PORTICO_PLAYBACK_TEST_HELPER")
	if helper == "" {
		t.Fatal("explicit reviewed helper binary required")
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(state, "media")
	if err = os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(state, "current.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	session, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	host, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(helper)
	managed, err := mounts.New(db, state, "", helper, store)
	if err != nil {
		t.Fatal(err)
	}
	store.Guard = managed.Guard
	store.MountedRoot = managed.RootFor
	rt, err := playbackruntime.New(db, ident, store)
	if err != nil {
		t.Fatal(err)
	}
	if beforeStart != nil {
		beforeStart(rt, db, state, host)
	}
	film := filepath.Join(media, "film.mp4")
	info, err := os.Stat(film)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	lib := c.Library("library", "Test", "movie", media)
	item := c.Movie(lib, film, "Test", 0)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: film, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 320, Height: 240, Duration: 90})
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
	})
	c.Drain()
	if err = rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := rt.Shutdown(ctx); e != nil {
			t.Errorf("shutdown %v", e)
		}
		if n := store.Supervisor.Active(); n != 0 {
			t.Errorf("remaining source helpers %d", n)
		}
	})
	d := Dependencies{Origins: []string{"http://127.0.0.1:19412"}, DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: host, Storage: store, Mounts: managed, PlaybackRuntime: rt}
	return &tl11CurrentPlaybackHTTPFixture{d: d, h: New(d), token: session.AccessToken, t: t, catalog: c, item: item}
}

func (f *tl11CurrentPlaybackHTTPFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func requireChannelTools(t *testing.T) (string, string) {
	t.Helper()
	testtier.Media(t, "real FFmpeg and a live channel pipeline")
	ffmpeg, e1 := exec.LookPath("ffmpeg")
	ffprobe, e2 := exec.LookPath("ffprobe")
	if e1 != nil || e2 != nil {
		t.Skip("the channel runtime needs FFmpeg")
	}
	return ffmpeg, ffprobe
}

// newChannelSuite: the real runtime, and a published two-programme Library
// Channel over a real 90-second film ("First" now, "Second" from boundary on).
func newChannelSuite(t *testing.T, boundary time.Duration) *channelSuite {
	t.Helper()
	ffmpeg, _ := requireChannelTools(t)
	var state string
	var live *livechannels.Store
	var film string
	f := tl11CurrentPlaybackHTTPWith(t, func(rt *playbackruntime.Runtime, db *sql.DB, dir string, hostedControl *hosted.Service) {
		state = dir
		film = filepath.Join(dir, "media", "film.mp4")
		if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=90", "-f", "lavfi", "-i", "sine=frequency=441:sample_rate=48000:duration=90",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-movflags", "+faststart", film).CombinedOutput(); err != nil {
			t.Fatalf("film: %v %s", err, out)
		}
		var err error
		live, err = livechannels.New(db)
		if err != nil {
			t.Fatal(err)
		}
		scheduled, err := library.New(db)
		if err != nil {
			t.Fatal(err)
		}
		if err = rt.ConfigureChannels(playbackruntime.LinearConfig{DB: db, Live: live, Library: scheduled, Policy: recordingaccess.Policy{Cached: hostedControl}, CacheDirectory: filepath.Join(dir, "linear-media"), LockDirectory: filepath.Join(dir, "live-source-locks"), FFmpeg: "ffmpeg", FFprobe: "ffprobe"}); err != nil {
			t.Fatal(err)
		}
	})
	if ok, why := f.d.PlaybackRuntime.Linear.Available(); !ok {
		t.Skipf("the channel runtime is unavailable on this host: %s", why)
	}
	// Library Channels on v1 sessions open their title through the media input
	// every v1 read uses (B5).
	renderer, err := subtitlevideo.New(subtitlevideo.Options{DB: f.d.DB, Storage: f.d.Storage, Mounts: f.d.Mounts, Directory: filepath.Join(state, "subtitle-video"), FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	// The channel work that borrows these inputs ends first (Shutdown is
	// idempotent; the fixture's own cleanup calls it again), then the inputs.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = f.d.PlaybackRuntime.Shutdown(ctx)
		renderer.Close()
		// Its source helpers retire asynchronously; the fixture checks none remain.
		for f.d.Storage.Supervisor.Active() != 0 && ctx.Err() == nil {
			time.Sleep(20 * time.Millisecond)
		}
	})
	f.d.PlaybackRuntime.Linear.UseMediaInputs(func(ctx context.Context, item, asset string) (subtitles.RenderInput, error) {
		return renderer.OpenSubtitleInput(ctx, item, asset, "")
	})
	film = filepath.Join(state, "media", "film.mp4")
	now := time.Now().UTC()
	s := &channelSuite{f: f, film: film, live: live, state: state, channel: playback.LinearReference{Kind: "library-channel", ChannelID: "channel", Generation: "generation"}}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO playback_origin_roots(id) VALUES('library')`, nil},
		{`INSERT OR IGNORE INTO playback_origin_assets(id) VALUES(?)`, []any{f.item.Token}},
		{`INSERT OR IGNORE INTO playback_origin_items(id) VALUES(?)`, []any{f.item.ID}},
		{`INSERT OR IGNORE INTO playback_origin_associations(item_id,asset_id) VALUES(?,?)`, []any{f.item.ID, f.item.Token}},
	} {
		if _, err := f.d.DB.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	var ri, ai, ii, assi string
	var rr, ar, ir, assr int64
	if err := f.d.DB.QueryRow(`SELECT r.incarnation,r.revision,a.incarnation,a.revision,i.incarnation,i.revision,l.incarnation,l.revision FROM playback_origin_roots r,playback_origin_assets a,playback_origin_items i,playback_origin_associations l WHERE r.id='library' AND a.id=? AND i.id=? AND l.item_id=? AND l.asset_id=?`, f.item.Token, f.item.ID, f.item.ID, f.item.Token).Scan(&ri, &rr, &ai, &ar, &ii, &ir, &assi, &assr); err != nil {
		t.Fatal(err)
	}
	fence := channelFixtureFence("catalog-observation-v1", f.item.Public, "library", f.item.Token, ri, fmt.Sprint(rr), ai, fmt.Sprint(ar), ii, fmt.Sprint(ir), assi, fmt.Sprint(assr))
	config, _ := json.Marshal(library.Config{Version: "1", ID: "channel", Name: "Members channel", Enabled: true, ViewerAccess: "server-members"})
	start, cut, end := now.Add(-5*time.Second).UnixMilli(), now.Add(boundary).UnixMilli(), now.Add(85*time.Second).UnixMilli()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES('channel',1,?,1,0,'Members channel','generation','ready')`, []any{string(config)}},
		{`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('generation','channel',1,?,'','','published','complete','',?,?,?,0,?)`, []any{string(config), start, end, end, now.UnixMilli()}},
		{`INSERT INTO lc_entries(generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason) VALUES('generation','first','channel',?,?,?,?,'library',?,'First','','',0,'')`, []any{start, cut, f.item.ID, f.item.Token, fence}},
		{`INSERT INTO lc_entries(generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason) VALUES('generation','second','channel',?,?,?,?,'library',?,'Second','','',0,'')`, []any{cut, end, f.item.ID, f.item.Token, fence}},
	} {
		if _, err := f.d.DB.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	f.catalog.Drain()
	return s
}

func waitChannel(t *testing.T, d channelDriver, h string, within time.Duration, what string, ok func(channelView) bool) channelView {
	t.Helper()
	deadline := time.Now().Add(within)
	var v channelView
	for {
		v = d.read(t, h)
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: after %s the channel is %+v", what, within, v)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// admin calls an owner route (its caching is its own; media routes are no-store).
func (s *channelSuite) admin(method, url string, body any) *httptest.ResponseRecorder {
	return s.v1(method, url, body, nil)
}

// v1 calls a /v1 route as the viewer: JSON, an idempotency key on POST, and any
// extra headers (If-Match).
func (s *channelSuite) v1(method, url string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, url, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+s.f.token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if method == "POST" {
		r.Header.Set("Idempotency-Key", fmt.Sprintf("channels-%d", time.Now().UnixNano()))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.f.h.ServeHTTP(w, r)
	return w
}

// media GETs a channel media URL (the playlist, or a segment relative to it).
func (s *channelSuite) media(url string) (int, string) {
	w := s.f.call("GET", url, nil)
	return w.Code, w.Body.String()
}

// firstSegment is the first segment URL a playlist names.
func firstSegment(playlistURL, playlist string) string {
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "/") {
				return line
			}
			return path.Join(path.Dir(playlistURL), line)
		}
	}
	return ""
}

func active(v channelView) bool {
	return v.Status == "active" && v.StreamURL != "" && v.LiveEdgeUS > v.WindowStartUS
}

// channelDrivers are the protocols the suite proves.
var channelDrivers = []func(t *testing.T, s *channelSuite) channelDriver{newV1ChannelDriver}

func eachChannelDriver(t *testing.T, boundary time.Duration, run func(t *testing.T, s *channelSuite, d channelDriver)) {
	for _, make := range channelDrivers {
		s := newChannelSuite(t, boundary)
		d := make(t, s)
		t.Run(d.name(), func(t *testing.T) { run(t, s, d) })
	}
}

// A tune plays: the playlist and its segments are served, and the viewer sees
// the channel, the programme on now and the next one.
func TestChannelsALibraryTuneServesMedia(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		if v.Name != "Members channel" || v.Programme != "First" || v.Next != "Second" {
			t.Fatalf("what the viewer sees: %+v", v)
		}
		code, playlist := s.media(v.StreamURL)
		if code != 200 || !strings.Contains(playlist, "#EXTINF") {
			t.Fatalf("playlist %d %s", code, playlist)
		}
		segment := firstSegment(v.StreamURL, playlist)
		if code, body := s.media(segment); code != 200 || len(body) < 188 {
			t.Fatalf("segment %s: %d, %d bytes", segment, code, len(body))
		}
		d.stop(t, h)
	})
}

// At the programme boundary the same playback moves to the next programme and
// keeps serving (a retune within the playback, not a new one).
func TestChannelsTheProgrammeBoundaryRetunesTheSamePlayback(t *testing.T) {
	eachChannelDriver(t, 12*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		waitChannel(t, d, h, 30*time.Second, "the tune becomes active", active)
		v := waitChannel(t, d, h, 45*time.Second, "the next programme is on", func(v channelView) bool { return v.ProgrammeID == "second" && active(v) })
		if code, _ := s.media(v.StreamURL); code != 200 {
			t.Fatalf("after the boundary the playlist is %d", code)
		}
		d.stop(t, h)
	})
}

// Pause, a seek within the retained window (acknowledged by the viewer's
// report), and go live.
func TestChannelsPauseSeekAndGoLive(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "a retained window of 6 s", func(v channelView) bool { return active(v) && v.LiveEdgeUS-v.WindowStartUS >= 6_000_000 })
		d.intent(t, h, "paused", nil, false)
		if v = d.read(t, h); v.DesiredState != "paused" {
			t.Fatalf("paused: %+v", v)
		}
		target := v.WindowStartUS + 2_000_000
		seek := d.intent(t, h, "playing", &target, false)
		d.observe(t, h, v, target, seek)
		v = waitChannel(t, d, h, 10*time.Second, "the seek is acknowledged", func(v channelView) bool { return v.AcknowledgedSeek == seek })
		if v.SeekError != "" || v.DesiredState != "playing" {
			t.Fatalf("after the seek: %+v", v)
		}
		d.intent(t, h, "playing", nil, true)
		if v = d.read(t, h); v.Status == "ended" || v.SeekError != "" {
			t.Fatalf("go live: %+v", v)
		}
		d.stop(t, h)
	})
}

// Stop ends the playback and its media stops at once.
func TestChannelsStopFencesMedia(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		d.stop(t, h)
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, _ := s.media(v.StreamURL)
			if code == 403 || code == 404 || code == 410 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("stopped media still answers %d", code)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if after := d.read(t, h); after.Status != "ended" {
			t.Fatalf("after stop: %+v", after)
		}
	})
}

// A member limit that rules out the programme mid-play stops its media (SEC-02).
func TestChannelsARestrictionMidPlayFencesMedia(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		for _, q := range []string{
			`INSERT INTO access_limits VALUES('account',1,0,'{"maxContentRating":"PG","allowUnrated":false}')`,
		} {
			if _, err := s.f.d.DB.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		s.f.catalog.Attributes(s.f.item.ID, "contentRating", "R")
		s.f.catalog.Drain()
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, _ := s.media(v.StreamURL)
			if code == 403 || code == 404 || code == 410 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("restricted media still answers %d", code)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if after := d.read(t, h); after.Status != "ended" && after.StreamURL != "" {
			t.Fatalf("a restricted channel still offers its stream: %+v", after)
		}
		d.stop(t, h)
	})
}

// A source that fails is reported (recoverable, with a code) within seconds;
// once it's back, a retry starts it again.
func TestChannelsAFailedSourceSaysWhyAndARetryRestartsIt(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		aside := s.film + ".aside"
		if err := os.Rename(s.film, aside); err != nil {
			t.Fatal(err)
		}
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 30*time.Second, "the failure is reported", func(v channelView) bool { return v.Status == "recoverable" && v.ErrorCode != "" })
		if v.StreamURL != "" && v.Status == "active" {
			t.Fatalf("a failed source looks active: %+v", v)
		}
		if err := os.Rename(aside, s.film); err != nil {
			t.Fatal(err)
		}
		d.retry(t, h)
		waitChannel(t, d, h, 60*time.Second, "the retry plays", active)
		d.stop(t, h)
	})
}

// A viewer whose lease lapses (they stopped reporting) is ended, and the media
// stops.
func TestChannelsALapsedLeaseEndsThePlayback(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		d.lapse(t, h)
		deadline := time.Now().Add(15 * time.Second)
		for {
			code, _ := s.media(v.StreamURL)
			if code == 403 || code == 404 || code == 410 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("media after the lease lapsed: %d", code)
			}
			time.Sleep(250 * time.Millisecond)
		}
		if after := d.read(t, h); after.Status == "active" {
			t.Fatalf("a lapsed playback reads active: %+v", after)
		}
	})
}

// Now Playing shows the channel once, by its name; an administrator's terminate
// ends it and its media, with the message.
func TestChannelsNowPlayingShowsTheChannelOnceAndTerminateEndsIt(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		v := waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		w := s.admin("GET", "/v1/admin/sessions", nil)
		var page struct {
			Items []struct {
				ID, Kind string
				Title    string `json:"title"`
				Item     *struct {
					Title string `json:"title"`
				} `json:"item"`
			} `json:"items"`
			Page struct {
				Total int `json:"total"`
			} `json:"page"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("now playing %d %s", w.Code, w.Body)
		}
		if page.Page.Total != 1 || len(page.Items) != 1 || page.Items[0].Kind != "channel" || !strings.Contains(w.Body.String(), "Members channel") {
			t.Fatalf("now playing shows %s", w.Body)
		}
		id := page.Items[0].ID
		if w = s.admin("POST", "/v1/admin/sessions/"+id+":terminate", map[string]any{"message": "Maintenance in five minutes."}); w.Code != 204 {
			t.Fatalf("terminate %d %s", w.Code, w.Body)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, _ := s.media(v.StreamURL)
			if code == 403 || code == 404 || code == 410 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("media after terminate: %d", code)
			}
			time.Sleep(250 * time.Millisecond)
		}
		if after := d.read(t, h); after.Status != "ended" {
			t.Fatalf("after terminate: %+v", after)
		}
	})
}

// Surfing: tuning another channel in place of this one ends it, and one
// playback holds the slot.
func TestChannelsSurfingReplacesThePlaybackInOneSlot(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.channel)
		waitChannel(t, d, h, 45*time.Second, "the tune becomes active", active)
		next := d.surf(t, h, s.channel)
		if next == h {
			t.Fatal("surfing kept the same playback")
		}
		if old := d.read(t, h); old.Status != "ended" {
			t.Fatalf("the playback surfed away from: %+v", old)
		}
		waitChannel(t, d, next, 45*time.Second, "the new tune becomes active", active)
		w := s.admin("GET", "/v1/admin/sessions", nil)
		if !strings.Contains(w.Body.String(), `"total":1`) {
			t.Fatalf("slots after surfing: %s", w.Body)
		}
		d.stop(t, next)
	})
}

// liveChannel publishes a live source whose one channel is a real HLS stream on
// this host (a confirmed LAN root, as an owner confirms it), with the owner's
// tuner limit, and returns its reference.
func (s *channelSuite) liveChannel(t *testing.T, tuners int, captionSource ...bool) playback.LinearReference {
	t.Helper()
	ffmpeg, _ := requireChannelTools(t)
	dir := filepath.Join(s.state, "stream")
	if err := os.MkdirAll(filepath.Join(dir, "live"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A real live source: 30 two-second segments, served as a sliding five-segment
	// playlist that advances with the wall clock and loops (a discontinuity at
	// each wrap), never ending, as an IPTV provider's is.
	const segments, segmentSeconds, window = 30, 2, 5
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x240:rate=25:duration=%d", segments*segmentSeconds), "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=441:sample_rate=48000:duration=%d", segments*segmentSeconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-f", "hls", "-hls_time", fmt.Sprint(segmentSeconds), "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "live", "seg-%d.ts"), filepath.Join(dir, "live", "vod.m3u8")).CombinedOutput(); err != nil {
		t.Fatalf("stream: %v %s", err, out)
	}
	now := time.Now().UTC()
	guide := fmt.Sprintf(`<?xml version="1.0"?><tv><channel id="news"/><programme channel="news" start="%s +0000" stop="%s +0000"><title>Morning news</title></programme></tv>`, now.Add(-time.Hour).Format("20060102150405"), now.Add(time.Hour).Format("20060102150405"))
	if err := os.WriteFile(filepath.Join(dir, "guide.xml"), []byte(guide), 0o600); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	files := http.FileServer(http.Dir(dir))
	withCaptions := len(captionSource) > 0 && captionSource[0]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withCaptions && r.URL.Path == "/live/master.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID=\"source-captions\",NAME=\"English captions\",LANGUAGE=\"en\",INSTREAM-ID=\"SERVICE2\",AUTOSELECT=YES\n#EXT-X-STREAM-INF:BANDWIDTH=800000,CLOSED-CAPTIONS=\"source-captions\"\nindex.m3u8\n"))
			return
		}
		if r.URL.Path != "/live/index.m3u8" {
			files.ServeHTTP(w, r)
			return
		}
		first := int(time.Since(began)/(segmentSeconds*time.Second)) + window
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", segmentSeconds, first, first/segments)
		for n := first; n < first+window; n++ {
			if n%segments == 0 && n != first {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			fmt.Fprintf(&b, "#EXTINF:%d.0,\nseg-%d.ts\n", segmentSeconds, n%segments)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(server.Close)
	sourcePlaylist := "/live/index.m3u8"
	if withCaptions {
		sourcePlaylist = "/live/master.m3u8"
	}
	if err := os.WriteFile(filepath.Join(dir, "list.m3u"), []byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" tvg-chno=\"1\",Northwind News\n"+server.URL+sourcePlaylist+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fixture", func(string, string) bool { return true }, nil
	}
	id := strings.Repeat("ab", 24)
	preview, err := s.live.PreviewRemote(context.Background(), owner, livechannels.SourceFetcher{}, livechannels.RemoteDraft{ID: id, Name: "Demo Live", Kind: "m3u", Locator: server.URL + "/list.m3u", GuideURL: server.URL + "/guide.xml", ConfirmedLANRoots: []string{server.URL + "/"}, RefreshMs: 3600000, OwnerLimit: tuners, ViewerAccess: "server-members"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	source, err := s.live.SaveRemote(context.Background(), owner, preview.ID, strings.Repeat("cd", 24))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	var generation, channel string
	if err = s.f.d.DB.QueryRow(`SELECT s.active_generation,c.channel_id FROM live_sources s JOIN live_channel_versions c ON c.generation_id=s.active_generation WHERE s.id=?`, source.ID).Scan(&generation, &channel); err != nil {
		t.Fatal(err)
	}
	return playback.LinearReference{Kind: "live-source", SourceID: source.ID, ChannelID: channel, Generation: generation}
}

// A live channel plays: its stream comes through the gateway and the decoder,
// and the viewer sees the guide's programme.
func TestChannelsALiveTunePlays(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		ref := s.liveChannel(t, 2)
		h := d.tune(t, ref)
		v := waitChannel(t, d, h, 45*time.Second, "the live tune becomes active", active)
		if code, playlist := s.media(v.StreamURL); code != 200 || !strings.Contains(playlist, "#EXTINF") {
			t.Fatalf("live playlist %d %s", code, playlist)
		}
		d.stop(t, h)
	})
}

// With every tuner of a source in use, another viewer's start is refused and
// says so; the viewer holding the tuner keeps playing.
func TestChannelsAnExhaustedTunerRefusesTheNextViewer(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		ref := s.liveChannel(t, 1)
		first := d.tune(t, ref)
		waitChannel(t, d, first, 45*time.Second, "the first viewer plays", active)
		d.noTuner(t, ref)
		if v := d.read(t, first); !active(v) {
			t.Fatalf("the first viewer after the refusal: %+v", v)
		}
		d.stop(t, first)
	})
}

type v1ChannelDriver struct {
	s        *channelSuite
	observer *httpObserver
	seeks    map[string]int64
}

func newV1ChannelDriver(t *testing.T, s *channelSuite) channelDriver {
	t.Helper()
	// Clients switch to v1 channels on this capability.
	if w := s.v1("GET", "/v1/capabilities", nil, nil); !strings.Contains(w.Body.String(), `"playback_v1_channels":"enabled"`) {
		t.Fatalf("capabilities don't offer v1 channels: %s", w.Body)
	}
	return &v1ChannelDriver{s: s, seeks: map[string]int64{}, observer: newHTTPObserver(t, s.f.d.DB, func(method, path string, body any) *httptest.ResponseRecorder { return s.v1(method, path, body, nil) })}
}

func (d *v1ChannelDriver) name() string { return "v1" }

func v1ChannelID(ref playback.LinearReference) string {
	if ref.Kind == "live-source" {
		return "live:" + ref.SourceID + ":" + ref.ChannelID
	}
	return "library:" + ref.ChannelID
}

func (d *v1ChannelDriver) start(t *testing.T, body map[string]any) (*httptest.ResponseRecorder, playbackv1.SessionView) {
	t.Helper()
	w := d.s.v1("POST", "/v1/playback/sessions", body, nil)
	var v playbackv1.SessionView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w, v
}

func (d *v1ChannelDriver) tune(t *testing.T, ref playback.LinearReference) string {
	t.Helper()
	w, v := d.start(t, map[string]any{"channelId": v1ChannelID(ref), "state": "playing"})
	if w.Code != 201 || v.ID == "" {
		t.Fatalf("tune %d %s", w.Code, w.Body)
	}
	return v.ID
}

func (d *v1ChannelDriver) session(t *testing.T, h string) playbackv1.SessionView {
	t.Helper()
	w := d.s.v1("GET", "/v1/playback/sessions/"+h, nil, nil)
	var v playbackv1.SessionView
	if w.Code == 404 {
		return playbackv1.SessionView{State: "ended"}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatalf("read %d %s", w.Code, w.Body)
	}
	return v
}

func (d *v1ChannelDriver) read(t *testing.T, h string) channelView {
	t.Helper()
	s := d.session(t, h)
	v := channelView{Status: "ended", DesiredState: s.State}
	if s.State == "ended" {
		return v
	}
	l := s.Presentation.Linear
	if l == nil {
		v.Status = "preparing"
		return v
	}
	v.Status, v.ErrorCode, v.Name = l.State, l.ErrorCode, l.Name
	if l.Programme != nil {
		v.ProgrammeID, v.Programme = l.Programme.ID, l.Programme.Title
	}
	if l.Next != nil {
		v.Next = l.Next.Title
	}
	v.StreamURL, v.BufferGeneration = s.Presentation.URL, strconv.Itoa(s.Presentation.Generation)
	v.WindowStartUS, v.WindowEndUS, v.LiveEdgeUS = l.WindowStartMs*1000, l.WindowEndMs*1000, l.LiveEdgeMs*1000
	if l.Seek != nil {
		if l.Seek.Acknowledged {
			v.AcknowledgedSeek = l.Seek.ID
		}
		v.SeekError = l.Seek.Error
	}
	return v
}

func (d *v1ChannelDriver) patch(t *testing.T, h string, body map[string]any) playbackv1.SessionView {
	t.Helper()
	s := d.session(t, h)
	w := d.s.v1("PATCH", "/v1/playback/sessions/"+h, body, map[string]string{"If-Match": `"` + s.Revision + `"`})
	var v playbackv1.SessionView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatalf("patch %v: %d %s", body, w.Code, w.Body)
	}
	return v
}

func (d *v1ChannelDriver) intent(t *testing.T, h, state string, seekUS *int64, live bool) string {
	t.Helper()
	body := map[string]any{"state": state}
	id := ""
	if seekUS != nil || live {
		id = fmt.Sprintf("seek-%d-%d", len(h), time.Now().UnixNano())
		seek := map[string]any{"id": id, "positionMs": 0}
		if live {
			seek["live"] = true
		} else {
			seek["positionMs"] = *seekUS / 1000
		}
		body["seek"] = seek
	}
	d.patch(t, h, body)
	return id
}

func (d *v1ChannelDriver) observe(t *testing.T, h string, v channelView, positionUS int64, _ string) {
	t.Helper()
	generation, _ := strconv.Atoi(d.read(t, h).BufferGeneration)
	d.seeks[h]++ // These synthetic samples jump to a specified channel position.
	d.observer.report(h, generation, map[string]any{"state": "playing", "positionMs": positionUS / 1000, "seekSerial": d.seeks[h]})
}

func (d *v1ChannelDriver) stop(t *testing.T, h string) {
	t.Helper()
	if w := d.s.v1("DELETE", "/v1/playback/sessions/"+h, nil, nil); w.Code >= 300 && w.Code != 404 {
		t.Fatalf("stop %d %s", w.Code, w.Body)
	}
}

// retry: play on a failed channel starts its source again.
func (d *v1ChannelDriver) retry(t *testing.T, h string) {
	d.patch(t, h, map[string]any{"state": "playing"})
}

func (d *v1ChannelDriver) lapse(t *testing.T, h string) {
	t.Helper()
	if _, err := d.s.f.d.DB.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, time.Now().Add(-time.Second).UnixMilli(), h); err != nil {
		t.Fatal(err)
	}
	d.s.f.d.PlaybackRuntime.Wake()
}

// surf: replacesSessionId, one request.
func (d *v1ChannelDriver) surf(t *testing.T, h string, ref playback.LinearReference) string {
	t.Helper()
	w, v := d.start(t, map[string]any{"channelId": v1ChannelID(ref), "state": "playing", "replacesSessionId": h})
	if w.Code != 201 || v.ID == "" {
		t.Fatalf("surf %d %s", w.Code, w.Body)
	}
	return v.ID
}

// noTuner: v1 refuses the start itself (spec §18.6).
func (d *v1ChannelDriver) noTuner(t *testing.T, ref playback.LinearReference) {
	t.Helper()
	w, _ := d.start(t, map[string]any{"channelId": v1ChannelID(ref), "state": "playing"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "no_tuner_available") {
		t.Fatalf("a start with every tuner in use: %d %s", w.Code, w.Body)
	}
}

// The source fixture declares a CEA service. This verifies declaration and
// media authority transport, not that its generated test picture has captions.
func TestChannelsLiveCaptionDeclarationsStayOnAuthenticatedMaster(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		ref := s.liveChannel(t, 2, true)
		h := d.tune(t, ref)
		v := waitChannel(t, d, h, 45*time.Second, "caption-declared live source becomes active", active)
		code, master := s.media(v.StreamURL)
		if code != 200 || !strings.Contains(master, "TYPE=CLOSED-CAPTIONS") || !strings.Contains(master, `INSTREAM-ID="SERVICE2"`) || !strings.Contains(master, `BANDWIDTH=800000,CLOSED-CAPTIONS="source-captions"`) || !strings.Contains(master, "stream.m3u8") || strings.Contains(master, "/live/") {
			t.Fatalf("master %d %s", code, master)
		}
		child := strings.TrimSuffix(v.StreamURL, "index.m3u8") + "stream.m3u8"
		if code, playlist := s.media(child); code != 200 || !strings.Contains(playlist, "#EXTINF") {
			t.Fatalf("authenticated child %d %s", code, playlist)
		}
		seek := fmt.Sprintf("at=%d", max(int64(0), v.WindowStartUS/1000))
		if code, master := s.media(v.StreamURL + "?" + seek); code != 200 || !strings.Contains(master, "stream.m3u8?"+seek) {
			t.Fatalf("seek master %d %s", code, master)
		}
		d.stop(t, h)
		for _, path := range []string{v.StreamURL, child} {
			if code, body := s.media(path); code == 200 {
				t.Fatalf("stopped caption media survived %s %s", path, body)
			}
		}
	})
}

func TestChannelsUndeclaredCaptionsStayAbsent(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		h := d.tune(t, s.liveChannel(t, 2))
		v := waitChannel(t, d, h, 45*time.Second, "undeclared live source becomes active", active)
		if code, playlist := s.media(v.StreamURL); code != 200 || !strings.Contains(playlist, "#EXTINF") || strings.Contains(playlist, "CLOSED-CAPTIONS") || strings.Contains(playlist, "INSTREAM-ID") {
			t.Fatalf("invented declaration %d %s", code, playlist)
		}
		child := strings.TrimSuffix(v.StreamURL, "index.m3u8") + "stream.m3u8"
		if code, body := s.media(child); code != 404 {
			t.Fatalf("undeclared child %d %s", code, body)
		}
		d.stop(t, h)
	})
}
