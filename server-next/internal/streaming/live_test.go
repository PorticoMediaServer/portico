package streaming

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// testChannel is a live channel's input made from an MPEG-TS file: it sends the file at speed
// times real time from wherever it is asked, as a tuner would, then stops (the source going
// off the air) or, with holdAtEnd, waits as a source that has paused sending.
type testChannel struct {
	data      []byte
	speed     float64
	holdAtEnd bool

	mu      sync.Mutex
	started time.Time
	stopped error
	reads   int
}

func (c *testChannel) ReadFrom(ctx context.Context, at time.Time, w io.Writer, began func(time.Time)) error {
	c.mu.Lock()
	c.reads++
	if c.started.IsZero() {
		c.started = time.Now()
	}
	c.mu.Unlock()
	if began != nil {
		began(time.Now())
	}
	const chunk = 188 * 64
	rate := float64(len(c.data)) / 60 * c.speed // the file is a minute of channel
	sent, since := 0, time.Now()
	for sent < len(c.data) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := min(sent+chunk, len(c.data))
		if _, err := w.Write(c.data[sent:end]); err != nil {
			return err
		}
		sent = end
		if ahead := time.Duration(float64(sent)/rate*float64(time.Second)) - time.Since(since); ahead > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(ahead):
			}
		}
	}
	if c.holdAtEnd {
		<-ctx.Done()
		return ctx.Err()
	}
	c.mu.Lock()
	c.stopped = errors.New("the channel stopped")
	c.mu.Unlock()
	return c.stopped
}

func (c *testChannel) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *testChannel) Window() (time.Time, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		return time.Now(), time.Now()
	}
	return c.started, time.Now()
}

func (c *testChannel) Stopped() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// broadcast writes a minute of channel as MPEG-TS: MPEG-2 picture with MPEG audio, as over
// the air, or H.264 with AAC, as many internet channels send.
func (r *mediaRig) broadcast(name string, internet bool) string {
	r.t.Helper()
	path := filepath.Join(r.library, name)
	codecs := []string{"-c:v", "mpeg2video", "-q:v", "6", "-c:a", "mp2"}
	if internet {
		codecs = []string{"-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-c:a", "aac"}
	}
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "60", "-pix_fmt", "yuv420p", "-ac", "2"}, codecs...)
	args = append(args, "-f", "mpegts", path)
	if out, err := exec.Command(r.ffmpeg, args...).CombinedOutput(); err != nil {
		r.t.Fatalf("broadcast: %v\n%s", err, out)
	}
	return path
}

// livePlan plans a channel for a browser from what is measured of it, as the media engine does.
func (r *mediaRig) livePlan(path string) DeliveryPlan {
	r.t.Helper()
	facts, err := mediafacts.Probe{Binary: r.ffprobe}.Inspect(context.Background(), path)
	if err != nil {
		r.t.Fatal(err)
	}
	plan, err := Plan(PlanRequest{
		File: FileFacts{ID: "live", Container: "mpegts", VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height,
			DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true, Streams: facts.Streams},
		Device: planTestDevice(r.t, "chromium"), Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FragmentedOutput: true,
		Choice: Choice{AssetID: "live", QualityID: "original"},
	})
	if err != nil {
		r.t.Fatalf("plan: %v", err)
	}
	return plan
}

func (r *mediaRig) channel(path string, speed float64, hold bool) *testChannel {
	r.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	return &testChannel{data: data, speed: speed, holdAtEnd: hold}
}

// liveGet asks for one file of a viewing's HLS the way the live route does.
func (r *mediaRig) liveGet(session, file string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/live/streams/x/"+file, nil)
	response := httptest.NewRecorder()
	name, _, _ := strings.Cut(file, "?")
	r.streams.ServeLive(response, request, session, name)
	return response
}

type livePlaylist struct {
	segments, dates, inits []string
	sequence               string
	ended                  bool
	discontinuities        int
}

func parseLive(t *testing.T, body string) livePlaylist {
	t.Helper()
	var p livePlaylist
	lines := bufio.NewScanner(strings.NewReader(body))
	for lines.Scan() {
		line := lines.Text()
		switch {
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			p.dates = append(p.dates, strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"))
		case strings.HasPrefix(line, "#EXT-X-MAP:URI="):
			p.inits = append(p.inits, strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`))
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			p.sequence = strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")
		case line == "#EXT-X-DISCONTINUITY":
			p.discontinuities++
		case line == "#EXT-X-ENDLIST":
			p.ended = true
		case line != "" && !strings.HasPrefix(line, "#"):
			p.segments = append(p.segments, line)
		}
	}
	if len(p.dates) != len(p.segments) {
		t.Fatalf("%d segments but %d moments:\n%s", len(p.segments), len(p.dates), body)
	}
	return p
}

// livePlaylistUntil reloads a viewing's playlist, as a player does, until ok says so.
func (r *mediaRig) livePlaylistUntil(session, query string, ok func(livePlaylist) bool) livePlaylist {
	r.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		response := r.liveGet(session, LivePlaylist+query)
		if response.Code == http.StatusOK {
			if p := parseLive(r.t, response.Body.String()); ok(p) {
				return p
			}
		} else if response.Code != http.StatusServiceUnavailable {
			r.t.Fatalf("the playlist answered %d: %s", response.Code, response.Body.String())
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("the playlist never got there: %d %s", response.Code, response.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// requirePlays fetches a viewing's segments, joined with their init, and checks the picture
// and sound decode as codec.
func (r *mediaRig) requirePlays(session string, p livePlaylist, video string) {
	r.t.Helper()
	var joined []byte
	ext := ".ts"
	if len(p.inits) > 0 {
		ext = ".mp4"
		joined = append(joined, r.liveGet(session, p.inits[0]).Body.Bytes()...)
	}
	for _, name := range p.segments[:min(3, len(p.segments))] {
		response := r.liveGet(session, name)
		if response.Code != http.StatusOK {
			r.t.Fatalf("segment %s answered %d", name, response.Code)
		}
		joined = append(joined, response.Body.Bytes()...)
	}
	file := filepath.Join(r.t.TempDir(), "played"+ext)
	if err := os.WriteFile(file, joined, 0600); err != nil {
		r.t.Fatal(err)
	}
	facts, err := mediafacts.Probe{Binary: r.ffprobe}.Inspect(context.Background(), file)
	if err != nil {
		r.t.Fatalf("the segments do not play: %v", err)
	}
	if facts.VideoCodec != video || facts.AudioCodec == "" {
		r.t.Fatalf("played %s with %q sound, wanted %s", facts.VideoCodec, facts.AudioCodec, video)
	}
}

// An over-the-air channel (MPEG-2 and MPEG audio) watched in a browser, which plays neither:
// it is converted for that one device, and played from a live playlist whose every segment
// says its moment on the channel's clock. When the channel stops, the playlist ends.
func TestALiveChannelIsConvertedForABrowser(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.broadcast("air.ts", false)
	plan := rig.livePlan(path)
	if plan.Mode != "hls" || plan.VideoAction != "convert" {
		t.Fatalf("MPEG-2 for a browser: mode %s, picture %s (%s)", plan.Mode, plan.VideoAction, plan.Strategy)
	}
	channel := rig.channel(path, 20, false)
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_air", Plan: plan, Input: channel}); err != nil {
		t.Fatal(err)
	}
	p := rig.livePlaylistUntil("lvs_air", "", func(p livePlaylist) bool { return len(p.segments) >= 2 })
	// The channel is sent faster than real time here, so its moments run ahead of the clock:
	// they start where the reading started and go up by each segment's length.
	began, _ := channel.Window()
	var previous time.Time
	for i, date := range p.dates {
		at, err := time.Parse(time.RFC3339Nano, date)
		if err != nil || at.Before(began.Add(-time.Second)) || !at.After(previous) {
			t.Fatalf("segment %d's moment %q is not on the channel's clock (it began at %s)", i, date, began)
		}
		previous = at
	}
	rig.requirePlays("lvs_air", p, plan.OutputVideoCodec)
	// The channel goes off the air: the playlist ends, and nothing is started again.
	p = rig.livePlaylistUntil("lvs_air", "", func(p livePlaylist) bool { return p.ended })
	if status, _ := rig.streams.LiveStatusOf("lvs_air"); !status.Ended || status.Failure != nil {
		t.Fatalf("a channel that stopped: %+v", status)
	}
	if n := channel.readCount(); n != 1 {
		t.Fatalf("the channel was read %d times", n)
	}
	rig.streams.StopLive("lvs_air")
}

// An internet channel already in H.264 and AAC is repackaged for the browser, not converted.
func TestALiveChannelTheBrowserPlaysIsRepackaged(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.broadcast("internet.ts", true)
	plan := rig.livePlan(path)
	if plan.Mode != "hls" || plan.VideoAction != "copy" || plan.AudioAction != "copy" {
		t.Fatalf("H.264 and AAC for a browser: mode %s, picture %s, sound %s (%s)", plan.Mode, plan.VideoAction, plan.AudioAction, plan.Strategy)
	}
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_web", Plan: plan, Input: rig.channel(path, 20, true)}); err != nil {
		t.Fatal(err)
	}
	p := rig.livePlaylistUntil("lvs_web", "", func(p livePlaylist) bool { return len(p.segments) >= 2 })
	rig.requirePlays("lvs_web", p, "h264")
	rig.streams.StopLive("lvs_web")
}

// A second device converting a channel goes through the same limit as any conversion: on a
// server allowed one conversion it is refused with the plain "busy" answer, and once the
// first stops it starts.
func TestASecondDevicesLiveConversionIsHeldToTheLimit(t *testing.T) {
	rig := newMediaRig(t)
	cfg := DefaultDeliveryConfiguration()
	cfg.MaxConversions = 1
	rig.streams.hls.ConfigureSettings(fixedSettings{cfg: cfg})
	path := rig.broadcast("air.ts", false)
	plan := rig.livePlan(path)
	channel := rig.channel(path, 1, true)
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_one", Plan: plan, Input: channel}); err != nil {
		t.Fatal(err)
	}
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_two", Plan: plan, Input: channel}); !errors.Is(err, ErrConversionCapacity) {
		t.Fatalf("a second conversion on a server allowed one: %v", err)
	}
	rig.streams.StopLive("lvs_one")
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_two", Plan: plan, Input: channel}); err != nil {
		t.Fatalf("after the first stopped: %v", err)
	}
	rig.streams.StopLive("lvs_two")
}

// Nobody fetching for a while (the app closed, the TV turned off) stops the converter; the
// viewing stays, and the next playlist request carries on from where it stopped, after a
// discontinuity. A moment asked for that is not in the playlist starts the conversion again
// from there; the same moment asked again, as a player reloading its playlist does, does not.
func TestALiveConversionStopsWhenIdleAndStartsAgainFromAMoment(t *testing.T) {
	idle := liveIdle
	liveIdle = 2 * time.Second
	t.Cleanup(func() { liveIdle = idle })
	rig := newMediaRig(t)
	path := rig.broadcast("air.ts", false)
	plan := rig.livePlan(path)
	channel := rig.channel(path, 4, true)
	if err := rig.streams.StartLive(context.Background(), LiveRequest{SessionID: "lvs_idle", Plan: plan, Input: channel}); err != nil {
		t.Fatal(err)
	}
	first := rig.livePlaylistUntil("lvs_idle", "", func(p livePlaylist) bool { return len(p.segments) >= 2 })
	deadline := time.Now().Add(20 * time.Second)
	for {
		rig.streams.hls.mu.Lock()
		stopped := rig.streams.live["lvs_idle"].cancel == nil
		rig.streams.hls.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the converter kept running with nobody fetching")
		}
		time.Sleep(200 * time.Millisecond)
	}
	resumed := rig.livePlaylistUntil("lvs_idle", "", func(p livePlaylist) bool { return p.discontinuities > 0 })
	if resumed.segments[0] != first.segments[0] {
		t.Fatalf("resuming lost what was made: %v then %v", first.segments, resumed.segments)
	}
	// A moment before anything made: the conversion starts again from there.
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	again := rig.livePlaylistUntil("lvs_idle", "?at="+at, func(p livePlaylist) bool { return len(p.segments) >= 1 })
	if again.segments[0] == first.segments[0] {
		t.Fatal("a moment outside the playlist did not start the conversion again")
	}
	reads := channel.readCount()
	rig.livePlaylistUntil("lvs_idle", "?at="+at, func(p livePlaylist) bool { return true })
	if channel.readCount() != reads {
		t.Fatal("the same moment asked again started the conversion again")
	}
	rig.streams.StopLive("lvs_idle")
}
