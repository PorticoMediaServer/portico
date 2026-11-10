package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// fakeEngine stands in for the media engine. It plays the original unless a lower quality
// was asked for or the device has a remembered failure, and counts the streams it runs.
type fakeEngine struct {
	mu       sync.Mutex
	running  map[string]playback.StartRequest
	started  int
	notReady bool
	busy     bool
	// hold, when set, makes Start wait until it is closed.
	hold chan struct{}
}

func (f *fakeEngine) CheckCapabilities(json.RawMessage) error { return nil }

func (f *fakeEngine) Qualities(db.PlaybackVersion, db.ItemKind, string) []playback.QualityOption {
	return []playback.QualityOption{{Mode: "original"}, {Mode: "automatic"}, {Mode: "fixed", ID: "1080p", MaxHeight: 1080},
		{Mode: "fixed", ID: "720p", MaxHeight: 720}, {Mode: "fixed", ID: "480p", MaxHeight: 480}}
}

func (f *fakeEngine) Plan(r playback.PlanRequest) (playback.Plan, error) {
	failed := map[string]bool{}
	for _, failure := range r.Failures {
		failed[failure.Route] = true
	}
	route := playback.RouteOriginal
	if r.Choice.Quality.MaxHeight > 0 || failed[playback.RouteOriginal] {
		route = playback.RouteRepackaged
	}
	if route == playback.RouteRepackaged && (r.Choice.Quality.MaxHeight > 0 || failed[route]) {
		route = playback.RouteConverted
	}
	if failed[playback.RouteConverted] {
		return playback.Plan{}, playback.ErrUnsupported
	}
	plan := playback.Plan{Mode: "stream", Player: "mpv", Route: route, Quality: r.Choice.Quality, AudioTrackID: r.Choice.AudioTrackID}
	if route == playback.RouteOriginal {
		plan.Mode = "direct"
	}
	// With no audio chosen, the track marked default plays.
	for _, t := range r.Version.Tracks {
		if plan.AudioTrackID == "" && t.Kind == db.StreamAudio && t.Default {
			plan.AudioTrackID = playback.TrackID(t)
		}
	}
	if r.Choice.SubtitleTrackID != "" && !r.Choice.SubtitlesOff {
		plan.Subtitle = &playback.SubtitleInForce{TrackID: r.Choice.SubtitleTrackID, Kind: "text", Delivery: "sidecar"}
	}
	// A subtitle the app draws itself is not part of the stream.
	plan.Key = fmt.Sprint(r.Version.PublicID, route, plan.AudioTrackID, r.Choice.Quality.ID)
	return plan, nil
}

func (f *fakeEngine) Start(_ context.Context, r playback.StartRequest) (playback.Stream, error) {
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return playback.Stream{}, playback.ErrBusy
	}
	if f.running == nil {
		f.running = map[string]playback.StartRequest{}
	}
	f.running[r.SessionID] = r
	f.started++
	return playback.Stream{Grant: fmt.Sprint("grant", f.started), URL: fmt.Sprintf("/v1/media/grant%d/master.m3u8", f.started), Mode: r.Plan.Mode}, nil
}

func (f *fakeEngine) Stop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, id)
}

func (f *fakeEngine) Ready(ctx context.Context, _ string) error {
	f.mu.Lock()
	wait := f.notReady
	f.mu.Unlock()
	if wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeEngine) streams() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.running)
}

// theatre is a server with an owner signed in on the main profile, a fake media engine, a
// clock the test moves, and a small library: a film, a show with two episodes and a song.
type theatre struct {
	*testing.T
	s      *Server
	engine *fakeEngine
	owner  Session
	clock  time.Time
	film   string
	eps    [2]string
	song   string
	libs   map[string]db.Library
	path   string
}

const hour = int64(3_600_000)

func newTheatre(t *testing.T) *theatre {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	th := &theatre{T: t, s: s, path: path, engine: &fakeEngine{}, clock: time.Now(), libs: map[string]db.Library{}}
	s.now = func() time.Time { return th.clock }
	s.playback.SetEngine(th.engine, th.engine)
	th.owner = onMain(t, s, setup(t, s))
	ctx := context.Background()

	video := db.Stream{Kind: db.StreamVideo, Codec: "hevc", Width: 1920, Height: 1080}
	audio := func(language string, isDefault bool) db.Stream {
		return db.Stream{Kind: db.StreamAudio, Codec: "eac3", Language: language, Channels: 6, Default: isDefault}
	}
	subtitle := func(language string, sdh bool) db.Stream {
		return db.Stream{Kind: db.StreamSubtitle, Codec: "subrip", Language: language, HearingImpaired: sdh}
	}
	file := func(name string, streams ...db.Stream) db.ImportFile {
		role := db.FileMedia
		if strings.HasSuffix(name, ".srt") {
			role = db.FileSubtitle
		}
		return db.ImportFile{FileRef: db.FileRef{Name: name}, Role: role, Size: 1 << 20, Container: "matroska", DurationMS: hour, Streams: streams}
	}
	// Both episodes have credits at forty minutes: the first confirmed across the show, the
	// second found in that one file only, which is not trusted.
	credits := func(source db.MarkerSource) []db.Marker {
		return []db.Marker{{Kind: db.MarkerCredits, Source: source, StartMS: 40 * 60_000, EndMS: hour}}
	}
	version := func(name string, sidecars ...string) []db.ImportVersion {
		v := db.ImportVersion{Parts: []db.ImportPart{{File: db.FileRef{Name: name}}}}
		for _, sidecar := range sidecars {
			v.Sidecars = append(v.Sidecars, db.FileRef{Name: sidecar})
		}
		return []db.ImportVersion{v}
	}
	one := 1
	two := 2
	age := 12
	imports := map[string]db.ImportBatch{
		db.LibraryMovies: {
			Files: []db.ImportFile{
				file("heat.mkv", video, audio("eng", true), audio("jpn", false), subtitle("eng", false), subtitle("eng", true)),
				file("heat.fr.srt", subtitle("fra", false)),
			},
			Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "heat"}}, Title: "Heat", RatingAge: &age,
				Versions: version("heat.mkv", "heat.fr.srt")}},
		},
		db.LibraryShows: {
			Files: []db.ImportFile{
				file("e1.mkv", video, audio("deu", true), audio("eng", false), subtitle("eng", false)),
				file("e2.mkv", video, audio("deu", true), audio("eng", false), subtitle("eng", false)),
			},
			Items: []db.ImportItem{
				{Kind: db.KindShow, Keys: []db.ItemKey{{Kind: db.KeyFolder, Key: "dark"}}, Title: "Dark", RatingAge: &age},
				{Kind: db.KindSeason, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "dark/1"}}, Parent: &db.ItemKey{Kind: db.KeyFolder, Key: "dark"},
					Title: "Season 1", IndexNumber: &one},
				{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "e1"}}, Parent: &db.ItemKey{Kind: db.KeyName, Key: "dark/1"},
					Title: "Secrets", IndexNumber: &one, ParentIndex: &one, Versions: marked(version("e1.mkv"), credits(db.MarkerConfirmed))},
				{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "e2"}}, Parent: &db.ItemKey{Kind: db.KeyName, Key: "dark/1"},
					Title: "Lies", IndexNumber: &two, ParentIndex: &one, Versions: marked(version("e2.mkv"), credits(db.MarkerFromAnalysis))},
			},
		},
		db.LibraryMusic: {
			Files: []db.ImportFile{file("song.flac", db.Stream{Kind: db.StreamAudio, Codec: "flac", Channels: 2, Default: true})},
			Items: []db.ImportItem{{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "song"}}, Title: "Song", RatingAge: &age,
				Versions: version("song.flac")}},
		},
	}
	viewer := db.Viewer{UserID: 1, ProfileID: 1}
	for kind, batch := range imports {
		lib, err := s.db.CreateLibrary(ctx, kind, kind, "en", []string{"/media/" + kind})
		if err != nil {
			t.Fatal(err)
		}
		th.libs[kind] = lib
		batch.LibraryID, batch.PathID, batch.Scan, batch.Dirs = lib.ID, lib.Paths[0].ID, 1, []db.ImportDir{{}}
		if _, err := s.db.Import(ctx, batch); err != nil {
			t.Fatal(err)
		}
		page, err := s.db.Items(ctx, viewer, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindMovie, db.KindEpisode, db.KindTrack},
			Sort: db.SortTitle})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			switch item.Title {
			case "Heat":
				th.film = item.PublicID
			case "Secrets":
				th.eps[0] = item.PublicID
			case "Lies":
				th.eps[1] = item.PublicID
			case "Song":
				th.song = item.PublicID
			}
		}
	}
	if th.film == "" || th.eps[0] == "" || th.eps[1] == "" || th.song == "" {
		t.Fatalf("the library was not imported: %+v", th)
	}
	return th
}

func marked(versions []db.ImportVersion, markers []db.Marker) []db.ImportVersion {
	versions[0].Markers = markers
	return versions
}

// start plays an item and returns the status and the session.
func (th *theatre) start(token, key string, body PlaybackStart) (int, PlaybackSession) {
	th.Helper()
	r := newRequest("POST", "/v1/playback/sessions", token, body)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := serve(th.s, r)
	var out PlaybackSession
	if w.Code < 300 {
		if err := jsonInto(w, &out); err != nil {
			th.Fatal(err)
		}
	}
	return w.Code, out
}

func (th *theatre) play(token, item string) PlaybackSession {
	th.Helper()
	code, session := th.start(token, "", PlaybackStart{ItemID: item})
	if code != 201 {
		th.Fatalf("start answered %d", code)
	}
	return session
}

func (th *theatre) report(token, id, state string, positionMs int64) int {
	th.Helper()
	return status(th.T, th.s, "PUT", "/v1/playback/sessions/"+id+"/progress", token, PlaybackProgress{State: state, PositionMs: positionMs})
}

func (th *theatre) state(item string) db.State {
	th.Helper()
	detail, err := th.s.db.Item(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, item)
	if err != nil {
		th.Fatal(err)
	}
	return detail.State
}

func (th *theatre) options(token, item, query string) PlaybackOptions {
	th.Helper()
	var out PlaybackOptions
	if w := call(th.T, th.s, "GET", "/v1/items/"+item+"/playback-options"+query, token, nil, &out); w.Code != 200 {
		th.Fatalf("options answered %d: %s", w.Code, w.Body)
	}
	return out
}

// A start sent twice with the same key is one session and one stream; a start that needs a
// moment answers "preparing" and the same request then gets the same session; a start on a
// device that is already playing replaces what it played.
func TestStartIsSafeToRepeatAndADevicePlaysOneThing(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	th.engine.notReady = true
	code, first := th.start(token, "key-1", PlaybackStart{ItemID: th.film})
	if code != 202 || first.RetryAfterMs == 0 || first.ID == "" {
		t.Fatalf("a start whose stream is not ready answered %d %+v", code, first)
	}
	th.engine.mu.Lock()
	th.engine.notReady = false
	th.engine.mu.Unlock()
	code, again := th.start(token, "key-1", PlaybackStart{ItemID: th.film})
	if code != 201 || again.ID != first.ID || again.Presentation.URL == "" || again.Lease.ReportEveryMs != 10_000 {
		t.Fatalf("the repeated start answered %d %+v, first was %s", code, again, first.ID)
	}
	if code, third := th.start(token, "key-1", PlaybackStart{ItemID: th.film}); code != 200 || third.ID != first.ID || th.engine.started != 1 {
		t.Fatalf("a third send answered %d session %s after %d streams", code, third.ID, th.engine.started)
	}
	if code, _ := th.start(token, "key-1", PlaybackStart{ItemID: th.song}); code != 422 {
		t.Fatalf("the same key for a different start answered %d", code)
	}
	// The same device starts something else: the film's session is over and its stream gone.
	song := th.play(token, th.song)
	var old PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+first.ID, token, nil, &old)
	if old.State != "ended" || old.End == nil || old.End.Reason != "replaced" || th.engine.streams() != 1 || song.Kind != "audio" {
		t.Fatalf("after a second start on the device: %+v, %d streams", old, th.engine.streams())
	}
	// A title this profile may not see is not found, and so is someone else's session.
	if code, _ := th.start(token, "", PlaybackStart{ItemID: "itm_nothing"}); code != 404 {
		t.Fatalf("an unknown title answered %d", code)
	}
	other := onMain(t, th.s, signIn(t, th.s, 5))
	if code := status(t, th.s, "GET", "/v1/playback/sessions/"+song.ID, other.AccessToken, nil); code != 404 {
		t.Fatalf("another device read this device's session: %d", code)
	}
}

// Stop is safe to repeat, saves the final position, and an unknown session stops fine.
func TestStopTwiceAndLeaseExpiry(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	for range 2 {
		if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(20 * 60_000)}); code != 204 {
			t.Fatalf("stop answered %d", code)
		}
	}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/pbs_unknown", token, nil); code != 204 {
		t.Fatalf("stopping an unknown session answered %d", code)
	}
	if got := th.state(th.film); got.PositionMS != 20*60_000 || th.engine.streams() != 0 {
		t.Fatalf("after stop: position %d, %d streams", got.PositionMS, th.engine.streams())
	}
	if code := th.report(token, session.ID, "playing", 21*60_000); code != 410 {
		t.Fatalf("a report to a stopped session answered %d", code)
	}
	if th.options(token, th.film, "").Resume.PositionMs != 20*60_000 {
		t.Fatal("the film does not resume where it was stopped")
	}

	// A device that goes quiet for two minutes loses its session; its last word is saved.
	session = th.play(token, th.film)
	th.report(token, session.ID, "playing", 30*60_000)
	th.clock = th.clock.Add(119 * time.Second)
	th.s.playback.Sweep(context.Background())
	if code := th.report(token, session.ID, "playing", 32*60_000); code != 200 {
		t.Fatalf("a report inside the lease answered %d", code)
	}
	th.clock = th.clock.Add(121 * time.Second)
	th.s.playback.Sweep(context.Background())
	var ended PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+session.ID, token, nil, &ended)
	if ended.State != "ended" || ended.End.Reason != "lease_expired" || th.engine.streams() != 0 || th.state(th.film).PositionMS != 32*60_000 {
		t.Fatalf("after the lease ran out: %+v, position %d", ended, th.state(th.film).PositionMS)
	}
	// Later the ended session is forgotten: the app is told it is unknown and starts again.
	th.clock = th.clock.Add(3 * time.Minute)
	th.s.playback.Sweep(context.Background())
	if code := th.report(token, session.ID, "playing", 0); code != 404 {
		t.Fatalf("a report to a forgotten session answered %d", code)
	}
}

func ptr(n int64) *int64 { return &n }

// A quality change makes a new stream where the player was and the old address is gone; a
// subtitle the app draws itself changes nothing in the stream; a change that cannot be made
// leaves playback as it was.
func TestChangeMidPlay(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	path := "/v1/playback/sessions/" + session.ID
	var changed PlaybackSession
	body := PlaybackChange{Quality: &playback.Quality{Mode: "fixed", ID: "720p"}, PositionMs: ptr(15 * 60_000)}
	if w := call(t, th.s, "PATCH", path, token, body, &changed); w.Code != 200 {
		t.Fatalf("change answered %d: %s", w.Code, w.Body)
	}
	p := changed.Presentation
	if p.URL == session.Presentation.URL || p.Generation != 2 || p.StartPositionMs != 15*60_000 || p.Route != "converted" ||
		p.Quality.MaxHeight != 720 || th.engine.streams() != 1 || th.engine.started != 2 {
		t.Fatalf("after a quality change: %+v, %d streams, %d started", p, th.engine.streams(), th.engine.started)
	}
	options := th.options(token, th.film, "")
	var french string
	for _, s := range options.Versions[0].Subtitles {
		if s.Language == "fra" && s.Source == "sidecar" {
			french = s.ID
		}
	}
	if w := call(t, th.s, "PATCH", path, token, PlaybackChange{Subtitles: &SubtitlePick{TrackID: &french}}, &changed); w.Code != 200 ||
		changed.Presentation.URL != p.URL || changed.Presentation.Subtitle == nil || th.engine.started != 2 {
		t.Fatalf("a subtitle change answered %d %+v after %d streams", w.Code, changed.Presentation, th.engine.started)
	}
	if code := status(t, th.s, "PATCH", path, token, PlaybackChange{Quality: &playback.Quality{Mode: "fixed", ID: "144p"}}); code != 400 {
		t.Fatalf("an unknown quality answered %d", code)
	}
	var still PlaybackSession
	call(t, th.s, "GET", path, token, nil, &still)
	if still.Presentation.URL != p.URL || still.State != "playing" {
		t.Fatalf("a refused change moved playback: %+v", still.Presentation)
	}
	// Pause and seek are saved at once.
	call(t, th.s, "PATCH", path, token, PlaybackChange{State: "paused", PositionMs: ptr(16 * 60_000)}, &changed)
	if changed.State != "paused" || changed.Lease.ReportEveryMs != 30_000 || th.state(th.film).PositionMS != 16*60_000 {
		t.Fatalf("after pause: %+v, saved %d", changed, th.state(th.film).PositionMS)
	}
	call(t, th.s, "PATCH", path, token, PlaybackChange{Seek: &PlaybackSeek{PositionMs: 40 * 60_000, ID: "seek-1"}}, &changed)
	if changed.Presentation.SeekID != "seek-1" || th.state(th.film).PositionMS != 40*60_000 {
		t.Fatalf("after seek: %+v, saved %d", changed.Presentation, th.state(th.film).PositionMS)
	}
}

// No limit unless the owner sets one. At a person's limit a start is refused and the account's
// streams can be listed and stopped; a paused stream counts; a change never counts twice;
// lowering a limit cuts nobody off; the server's limit holds for everyone.
func TestStreamLimits(t *testing.T) {
	th := newTheatre(t)
	phone := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	tablet := onMain(t, th.s, signIn(t, th.s, 3)).AccessToken
	first := th.play(phone, th.film)
	second := th.play(tv, th.eps[0])
	third := th.play(tablet, th.song)
	owner := th.owner.AccessToken
	if code := status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", owner, StreamLimits{PerPerson: 2}); code != 200 {
		t.Fatalf("setting the limit answered %d", code)
	}
	// Lowering the limit below what is playing ends nothing, and a change is not a new stream.
	if th.engine.streams() != 3 {
		t.Fatalf("%d streams after the limit was lowered", th.engine.streams())
	}
	call(t, th.s, "PATCH", "/v1/playback/sessions/"+first.ID, phone, PlaybackChange{State: "paused"}, nil)
	if code := status(t, th.s, "PATCH", "/v1/playback/sessions/"+second.ID, tv, PlaybackChange{Quality: &playback.Quality{Mode: "fixed", ID: "480p"}}); code != 200 {
		t.Fatalf("a quality change at the limit answered %d", code)
	}
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+third.ID, tablet, nil)
	w := serve(th.s, newRequest("POST", "/v1/playback/sessions", tablet, PlaybackStart{ItemID: th.song}))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "stream_limit_reached") {
		t.Fatalf("a third stream with a paused one counted answered %d: %s", w.Code, w.Body)
	}
	var playing AccountPlayback
	call(t, th.s, "GET", "/v1/playback/sessions", tablet, nil, &playing)
	if len(playing.Items) != 2 || playing.Limit != 2 || playing.Items[0].Title != "Heat" || playing.Items[0].State != "paused" {
		t.Fatalf("the account's streams: %+v", playing)
	}
	// One of them is stopped from the device that was refused, and then it may play.
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+playing.Items[0].ID, tablet, nil); code != 204 {
		t.Fatalf("stopping another device's stream answered %d", code)
	}
	th.play(tablet, th.song)
	// The same device starting something else is not one more stream.
	th.play(tablet, th.film)

	// A person's own number wins over the default, and nobody's lifts the server's.
	limits := StreamLimits{Total: 2, PerPerson: 1, People: map[string]int{th.owner.Viewer.AccountID: 5}}
	status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", owner, limits)
	w = serve(th.s, newRequest("POST", "/v1/playback/sessions", phone, PlaybackStart{ItemID: th.song}))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "server_stream_limit_reached") {
		t.Fatalf("a start at the server's limit answered %d: %s", w.Code, w.Body)
	}
}

// When a sign-in is ended, the account is turned off or the profile loses the library, what
// was playing stops at once and its address no longer works.
func TestRevocationEndsPlaybackAtOnce(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	member := addMember(t, th.s, owner, "sam", "member")
	sam := signInAs(t, th.s, "sam", testPassword, 7)
	ended := func(what string) {
		t.Helper()
		// The session is over before the request that caused it returns; the stream follows.
		for range 200 {
			if th.engine.streams() == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s: the stream was not stopped", what)
	}
	session := th.play(sam.AccessToken, th.film)
	if code := status(t, th.s, "DELETE", "/v1/auth/session", sam.AccessToken, nil); code != 204 {
		t.Fatalf("sign-out answered %d", code)
	}
	if got := th.s.playback.All(); len(got) != 0 {
		t.Fatalf("after sign-out %d sessions still run", len(got))
	}
	ended("sign-out")

	// Access to the film's library is taken away while it plays.
	sam = signInAs(t, th.s, "sam", testPassword, 7)
	session = th.play(sam.AccessToken, th.film)
	var libraries []string
	all, _ := th.s.db.AllLibraries(context.Background())
	for _, lib := range all {
		if lib.Kind != db.LibraryMovies {
			libraries = append(libraries, lib.PublicID)
		}
	}
	no := false
	if w := call(t, th.s, "PATCH", "/v1/admin/members/"+member.ID, owner, MemberEdit{AllLibraries: &no, Libraries: &libraries}, nil); w.Code != 200 {
		t.Fatalf("limiting libraries answered %d: %s", w.Code, w.Body)
	}
	ended("library access removed")
	code, fresh := renew(t, th.s, sam.RefreshToken, 7, "after-access-change")
	if code != 200 {
		t.Fatalf("renewing after the access change answered %d", code)
	}
	sam = fresh
	var over PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+session.ID, sam.AccessToken, nil, &over)
	if over.State != "ended" || over.End.Reason != "access_removed" {
		t.Fatalf("after access was removed: %+v", over)
	}
	// What it may still see keeps playing through a change that does not touch it.
	th.play(sam.AccessToken, th.song)
	yes := true
	if w := call(t, th.s, "PATCH", "/v1/admin/members/"+member.ID, owner, MemberEdit{Disabled: &yes}, nil); w.Code != 200 {
		t.Fatalf("turning the account off answered %d: %s", w.Code, w.Body)
	}
	if got := th.s.playback.All(); len(got) != 0 {
		t.Fatalf("after the account was turned off %d sessions still run", len(got))
	}
	ended("account turned off")
}

// Progress is kept in memory and written in batches; pausing writes at once. A title passes
// its watched point once: it is marked watched, its resume point cleared and one play
// counted, however many more reports follow. A later Unwatched is not undone by them.
func TestProgressBatchesAndThePlayIsCountedOnce(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	ctx := context.Background()
	session := th.play(token, th.film)
	th.clock = th.clock.Add(10 * time.Second)
	th.report(token, session.ID, "playing", 10*60_000)
	if got := th.state(th.film); got.PositionMS != 0 {
		t.Fatalf("a report was written before the batch: %d", got.PositionMS)
	}
	th.s.playback.Save(ctx, db.Background)
	if got := th.state(th.film); got.PositionMS != 10*60_000 || got.Watched {
		t.Fatalf("after the batch: %+v", got)
	}
	th.clock = th.clock.Add(10 * time.Second)
	th.report(token, session.ID, "paused", 10*60_000+10_000)
	if got := th.state(th.film); got.PositionMS != 10*60_000+10_000 {
		t.Fatalf("a pause was not written at once: %d", got.PositionMS)
	}
	// Past 95%: watched, no resume point, one play.
	for n, position := range []int64{57 * 60_000, 58 * 60_000, 59 * 60_000} {
		th.clock = th.clock.Add(time.Minute)
		th.report(token, session.ID, "playing", position)
		th.s.playback.Save(ctx, db.Background)
		if got := th.state(th.film); !got.Watched || got.PositionMS != 0 || got.PlayCount != 1 {
			t.Fatalf("report %d past the watched point: %+v", n, got)
		}
	}
	// The person marks it unwatched while the credits run. Later reports leave that alone.
	no := false
	if _, _, err := th.s.db.SetState(ctx, db.Viewer{UserID: 1, ProfileID: 1}, th.film, db.StateEdit{Watched: &no}); err != nil {
		t.Fatal(err)
	}
	th.clock = th.clock.Add(10 * time.Second)
	th.report(token, session.ID, "playing", 59*60_000+30_000)
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(hour)})
	if got := th.state(th.film); got.Watched || got.PlayCount != 1 {
		t.Fatalf("after Unwatched and more reports: %+v", got)
	}
	// Playing it again is a second play.
	session = th.play(token, th.film)
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(hour)})
	if got := th.state(th.film); !got.Watched || got.PlayCount != 2 {
		t.Fatalf("after a second play: %+v", got)
	}
	// A song keeps no resume point and no watched mark, only plays.
	session = th.play(token, th.song)
	th.report(token, session.ID, "paused", 30*60_000)
	if got := th.state(th.song); got.PositionMS != 0 || got.PlayCount != 0 {
		t.Fatalf("a song half played: %+v", got)
	}
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(hour)})
	if got := th.state(th.song); got.Watched || got.PlayCount != 1 {
		t.Fatalf("a song played to the end: %+v", got)
	}
	// A few seconds in is not "started": nothing to resume.
	session = th.play(token, th.eps[0])
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(20_000)})
	if got := th.state(th.eps[0]); got.PositionMS != 0 {
		t.Fatalf("twenty seconds in was kept as a resume point: %+v", got)
	}
}

// An audio or subtitle pick is remembered for the title, and for an episode it carries to
// the show's other episodes, subtitles off included, until another pick replaces it.
func TestTrackPicksAreRemembered(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	options := th.options(token, th.eps[0], "")
	audio := map[string]string{}
	for _, a := range options.Versions[0].Audio {
		audio[a.Language] = a.ID
	}
	subtitle := options.Versions[0].Subtitles[0].ID
	if options.Preferred.AudioID != audio["deu"] || options.Preferred.SubtitleID != "" || options.Preferred.Quality.Mode != "original" ||
		options.Container == nil || options.Container.Kind != "show" {
		t.Fatalf("defaults for an episode: %+v %+v", options.Preferred, options.Container)
	}
	session := th.play(token, th.eps[0])
	pick := PlaybackChange{Audio: &AudioPick{TrackID: audio["eng"]}, Subtitles: &SubtitlePick{TrackID: &subtitle}}
	if code := status(t, th.s, "PATCH", "/v1/playback/sessions/"+session.ID, token, pick); code != 200 {
		t.Fatalf("picking tracks answered %d", code)
	}
	// The next episode, on another device, starts in English with English subtitles.
	tv := onMain(t, th.s, signIn(t, th.s, 4)).AccessToken
	next := th.options(tv, th.eps[1], "")
	if next.Preferred.AudioID != audio["eng"] || next.Preferred.SubtitleID == "" {
		t.Fatalf("the next episode did not follow the pick: %+v", next.Preferred)
	}
	// Subtitles are turned off on episode 2: off carries back to episode 1's show-wide choice,
	// but episode 1 keeps its own pick, and the film was never touched.
	session = th.play(tv, th.eps[1])
	status(t, th.s, "PATCH", "/v1/playback/sessions/"+session.ID, tv, PlaybackChange{Subtitles: &SubtitlePick{}})
	if again := th.options(tv, th.eps[1], ""); again.Preferred.SubtitleID != "" || again.Preferred.AudioID != audio["eng"] {
		t.Fatalf("subtitles off was not remembered: %+v", again.Preferred)
	}
	if first := th.options(token, th.eps[0], ""); first.Preferred.SubtitleID == "" {
		t.Fatalf("episode 1 lost its own pick: %+v", first.Preferred)
	}
	if film := th.options(token, th.film, ""); film.Preferred.SubtitleID != "" {
		t.Fatalf("a pick on a show reached a film: %+v", film.Preferred)
	}
	// On mobile data video defaults to 720p; elsewhere to the original.
	if mobile := th.options(token, th.film, "?network=cellular"); mobile.Preferred.Quality.ID != "720p" || mobile.Plan.Route != "converted" {
		t.Fatalf("the mobile-data default: %+v", mobile.Preferred)
	}
}

// A way of playing that failed on a device is remembered for that device and file: the
// session moves to the next way, the next start begins there, the owner can see it and
// forget it, and it expires by itself.
func TestRememberedFailures(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	path := "/v1/playback/sessions/" + session.ID
	var moved PlaybackSession
	failure := PlaybackChange{Failure: &playback.Failure{Code: "decode_error"}, PositionMs: ptr(60_000)}
	if w := call(t, th.s, "PATCH", path, token, failure, &moved); w.Code != 200 || moved.Presentation.Route != "repackaged" ||
		moved.Presentation.StartPositionMs != 60_000 {
		t.Fatalf("after Original failed: %d %+v", w.Code, moved.Presentation)
	}
	status(t, th.s, "DELETE", path, token, nil)
	// Signed out and in again on the same device, it still starts with the next way.
	status(t, th.s, "DELETE", "/v1/auth/session", token, nil)
	again := onMain(t, th.s, signIn(t, th.s, 0)).AccessToken
	if next := th.play(again, th.film); next.Presentation.Route != "repackaged" {
		t.Fatalf("the next start on the device took the %s way", next.Presentation.Route)
	}
	// Another device is not affected.
	other := onMain(t, th.s, signIn(t, th.s, 9)).AccessToken
	if next := th.play(other, th.film); next.Presentation.Route != "original" {
		t.Fatalf("another device took the %s way", next.Presentation.Route)
	}
	var list Page[RememberedFailure]
	call(t, th.s, "GET", "/v1/admin/playback/failures", again, nil, &list)
	if len(list.Items) != 1 || list.Items[0].Route != "original" || list.Items[0].Title != "Heat" || list.Items[0].DeviceName != "Device 0" ||
		list.Items[0].ExpiresAt.Sub(list.Items[0].FailedAt) != 7*24*time.Hour {
		t.Fatalf("the owner's list: %+v", list.Items)
	}
	if code := status(t, th.s, "DELETE", "/v1/admin/playback/failures/"+list.Items[0].ID, again, nil); code != 204 {
		t.Fatalf("forgetting answered %d", code)
	}
	if next := th.play(again, th.film); next.Presentation.Route != "original" {
		t.Fatalf("after forgetting, the start took the %s way", next.Presentation.Route)
	}
	// When every way has failed the change is refused and nothing more is tried.
	session = th.play(again, th.film)
	path = "/v1/playback/sessions/" + session.ID
	uncertain := PlaybackChange{Failure: &playback.Failure{Code: "stalled_without_data"}}
	for _, want := range []int{200, 200, http.StatusUnprocessableEntity} {
		if code := status(t, th.s, "PATCH", path, again, uncertain); code != want {
			t.Fatalf("a failure report answered %d, want %d", code, want)
		}
	}
	// An uncertain failure is forgotten after six hours, and Original is tried again.
	th.clock = th.clock.Add(6*time.Hour + time.Minute)
	again = onMain(t, th.s, signIn(t, th.s, 0)).AccessToken
	if next := th.play(again, th.film); next.Presentation.Route != "original" {
		t.Fatalf("after six hours the start took the %s way", next.Presentation.Route)
	}
}

// A device publishes what its players can do, as one document that is kept as it is.
func TestCapabilitiesAreStoredAsPublished(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	document := `{"version":3,"engines":{"native":{"video":["h264"]},"mpv":{"video":["hevc","av1"]}}}`
	r := newRequest("PUT", "/v1/devices/current/capabilities", token, json.RawMessage(document))
	if w := serve(th.s, r); w.Code != 204 {
		t.Fatalf("publishing answered %d: %s", w.Code, w.Body)
	}
	w := call(t, th.s, "GET", "/v1/devices/"+th.owner.DeviceID+"/capabilities", token, nil, nil)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != document {
		t.Fatalf("reading back answered %d: %s", w.Code, w.Body)
	}
	other := onMain(t, th.s, signIn(t, th.s, 3))
	if code := status(t, th.s, "GET", "/v1/devices/current/capabilities", other.AccessToken, nil); code != 404 {
		t.Fatalf("a device that published nothing answered %d", code)
	}
}

// A title's subtitles are listed with where each came from; a timing nudge is kept for that
// title and profile and does not reach another title; lyrics beside a song are read, timed
// lines in order.
func TestSubtitlesOffsetAndLyrics(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	var list ItemSubtitles
	call(t, th.s, "GET", "/v1/items/"+th.film+"/subtitles", token, nil, &list)
	sources := ""
	for _, s := range list.Items {
		sources += s.Language + ":" + s.Source + fmt.Sprint(s.SDH) + ";"
	}
	if sources != "eng:embeddedfalse;eng:embeddedtrue;fra:sidecarfalse;" || list.OffsetMs != 0 {
		t.Fatalf("the film's subtitles: %s offset %d", sources, list.OffsetMs)
	}
	// Nudged in the player: kept for the film, there on the next start, not on the episode.
	session := th.play(token, th.film)
	nudge := int64(-750)
	if code := status(t, th.s, "PATCH", "/v1/playback/sessions/"+session.ID, token, PlaybackChange{SubtitleOffsetMs: &nudge}); code != 200 {
		t.Fatalf("a timing nudge answered %d", code)
	}
	call(t, th.s, "GET", "/v1/items/"+th.film+"/subtitles", token, nil, &list)
	var other ItemSubtitles
	call(t, th.s, "GET", "/v1/items/"+th.eps[0]+"/subtitles", token, nil, &other)
	if list.OffsetMs != -750 || other.OffsetMs != 0 {
		t.Fatalf("the nudge: film %d, episode %d", list.OffsetMs, other.OffsetMs)
	}
	if code := status(t, th.s, "PUT", "/v1/items/"+th.film+"/subtitles/offset", token, SubtitleOffset{OffsetMs: 0}); code != 200 {
		t.Fatalf("clearing the nudge answered %d", code)
	}
	call(t, th.s, "GET", "/v1/items/"+th.film+"/subtitles", token, nil, &list)
	if list.OffsetMs != 0 {
		t.Fatalf("the nudge was not cleared: %d", list.OffsetMs)
	}

	lines, offset, timed := parseLyrics("[ar:Band]\n[offset:+120]\n[00:12.50][01:02.5]Chorus\n[00:01.00]First line\nSpoken")
	if !timed || offset != 120 || len(lines) != 4 || lines[0].Text != "Spoken" || *lines[1].AtMs != 1000 || *lines[2].AtMs != 12500 ||
		*lines[3].AtMs != 62500 || lines[3].Text != "Chorus" {
		t.Fatalf("parsed lyrics: %+v offset %d", lines, offset)
	}
	var lyrics Page[Lyrics]
	if w := call(t, th.s, "GET", "/v1/items/"+th.song+"/lyrics", token, nil, &lyrics); w.Code != 200 || len(lyrics.Items) != 0 {
		t.Fatalf("a song with no lyrics file answered %d: %s", w.Code, w.Body)
	}
	// A song in a real folder with a lyrics file beside it.
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "tune.lrc"), []byte("[00:05.00]Hello\n[00:09.00]Again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lib, err := th.s.db.CreateLibrary(ctx, "Tunes", db.LibraryMusic, "en", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	age := 0
	_, err = th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{
			{FileRef: db.FileRef{Name: "tune.flac"}, Role: db.FileMedia, Size: 1, DurationMS: 60_000},
			{FileRef: db.FileRef{Name: "tune.lrc"}, Role: db.FileLyrics, Size: 30},
		},
		Items: []db.ImportItem{{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "tune"}}, Title: "Tune", RatingAge: &age,
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "tune.flac"}}}, Sidecars: []db.FileRef{{Name: "tune.lrc"}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := th.s.db.Items(ctx, db.Viewer{UserID: 1, ProfileID: 1}, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindTrack}})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("the tune was not imported: %v", err)
	}
	call(t, th.s, "GET", "/v1/items/"+page.Items[0].PublicID+"/lyrics", token, nil, &lyrics)
	if len(lyrics.Items) != 1 || lyrics.Items[0].Format != "lrc" || lyrics.Items[0].Origin != "sidecar" || len(lyrics.Items[0].Lines) != 2 ||
		lyrics.Items[0].Lines[1].Text != "Again" {
		t.Fatalf("lyrics read from the file: %+v", lyrics.Items)
	}
}

// A stop that arrives while the stream is still starting leaves no stream behind, and a
// stream the engine replaces by itself becomes the session's, which the app hears of in its
// next progress answer.
func TestStopDuringStartAndAStreamReplacedByTheEngine(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	th.engine.hold = make(chan struct{})
	started := make(chan int)
	go func() {
		code, _ := th.start(token, "", PlaybackStart{ItemID: th.film})
		started <- code
	}()
	var id string
	for range 400 {
		if all := th.s.playback.All(); len(all) == 1 {
			id = all[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("the session never appeared")
	}
	stopped := make(chan int)
	go func() { stopped <- status(t, th.s, "DELETE", "/v1/playback/sessions/"+id, token, nil) }()
	for range 400 {
		if len(th.s.playback.All()) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(th.engine.hold)
	<-started
	if code := <-stopped; code != 204 || th.engine.streams() != 0 {
		t.Fatalf("stop during start answered %d and left %d streams", code, th.engine.streams())
	}
	th.engine.mu.Lock()
	th.engine.hold = nil
	th.engine.mu.Unlock()

	session := th.play(token, th.film)
	th.engine.mu.Lock()
	replaced := th.engine.running[session.ID].Replaced
	th.engine.mu.Unlock()
	replaced(playback.Stream{Grant: "software", URL: "/v1/media/software/master.m3u8", Mode: "stream"})
	var lease PlaybackLease
	call(t, th.s, "PUT", "/v1/playback/sessions/"+session.ID+"/progress", token, PlaybackProgress{State: "playing", PositionMs: 5000}, &lease)
	var now PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+session.ID, token, nil, &now)
	if lease.Generation != 2 || now.Presentation.URL != "/v1/media/software/master.m3u8" || now.Presentation.StartPositionMs != 5000 && now.Presentation.StartPositionMs != 0 {
		t.Fatalf("after the engine replaced the stream: lease %+v, presentation %+v", lease, now.Presentation)
	}
}

// A video is watched when trusted credits start, or at the percentage, whichever comes
// first; credits found in one file only finish nothing and are not skipped without asking.
// While history is paused nothing of the profile's own is saved, but the owner's play
// record has the play, hidden from the profile. What the engine cannot do yet is refused
// by name.
func TestCreditsPausedHistoryAndWhatIsNotAvailableYet(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	ctx := context.Background()
	stopAt := func(item string, positionMs int64) {
		t.Helper()
		session := th.play(token, item)
		status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &positionMs})
	}
	stopAt(th.eps[0], 41*60_000)
	stopAt(th.eps[1], 41*60_000)
	if first, second := th.state(th.eps[0]), th.state(th.eps[1]); !first.Watched || first.PlayCount != 1 || second.Watched || second.PositionMS != 41*60_000 {
		t.Fatalf("past the credits: trusted %+v, untrusted %+v", first, second)
	}
	if a, b := th.options(token, th.eps[0], "").Markers, th.options(token, th.eps[1], "").Markers; len(a) != 1 || !a[0].AutomaticSafe || len(b) != 1 || b[0].AutomaticSafe {
		t.Fatalf("markers: %+v and %+v", a, b)
	}

	// History paused: the film is played to the end. Nothing of the profile's changes.
	if err := th.s.db.SetProfilePreference(ctx, 1, db.PrefPauseHistory, "true", th.clock); err != nil {
		t.Fatal(err)
	}
	stopAt(th.film, 30*60_000)
	stopAt(th.film, hour)
	if got := th.state(th.film); got.Watched || got.PlayCount != 0 || got.PositionMS != 0 {
		t.Fatalf("with history paused the film's state changed: %+v", got)
	}
	records, err := th.s.db.PlayRecords(ctx, 1, 10)
	if err != nil || len(records) != 2 || records[0].Title != "Heat" || !records[0].HiddenFromProfile || records[1].HiddenFromProfile {
		t.Fatalf("the owner's play record: %+v, %v", records, err)
	}

	for body, code := range map[*PlaybackStart]string{
		{ItemID: th.film, DiscTitle: json.RawMessage(`{"id":"x","revision":"y"}`)}:           "disc_playback_unavailable",
		{ItemID: th.film, Subtitles: &SubtitlePick{TrackID: ptr2("3"), Delivery: "sidecar"}}: "subtitle_delivery_unavailable",
	} {
		w := serve(th.s, newRequest("POST", "/v1/playback/sessions", token, body))
		if w.Code != 501 || !strings.Contains(w.Body.String(), code) {
			t.Fatalf("%s: answered %d: %s", code, w.Code, w.Body)
		}
	}
	if th.engine.streams() != 0 {
		t.Fatalf("a refused start left %d streams", th.engine.streams())
	}
}

func ptr2(s string) *string { return &s }
