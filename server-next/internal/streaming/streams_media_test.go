package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// These tests run real conversions of small generated files through the whole
// runtime: plan, start, serve by grant, stop. Each one ends with no converter
// process and no file left behind.

// The test executable dispatches the same confinement helpers as the server.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type mediaRig struct {
	t       *testing.T
	streams *Streams
	library string
	folder  string
	ffmpeg  string
	ffprobe string
}

func newMediaRig(t *testing.T) *mediaRig {
	t.Helper()
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	previous := decoder.CurrentToolchain()
	toolchain, err := decoder.ProbeToolchain(context.Background(), ffmpeg)
	if err != nil {
		t.Fatalf("toolchain: %v", err)
	}
	decoder.ConfigureToolchain(toolchain)
	t.Cleanup(func() { decoder.RestoreToolchain(previous) })
	rig := &mediaRig{t: t, library: t.TempDir(), folder: t.TempDir(), ffmpeg: ffmpeg, ffprobe: ffprobe}
	ctx, cancel := context.WithCancel(context.Background())
	streams, err := New(ctx, Options{
		Folder: rig.folder, FFmpeg: ffmpeg, Libraries: func() []string { return []string{rig.library} },
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.streams = streams
	go streams.Run(ctx)
	t.Cleanup(func() {
		wait, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if err := streams.Shutdown(wait); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		cancel()
		rig.nothingLeft()
	})
	return rig
}

// nothingLeft fails the test if a converter this test process started is still
// running, or if anything is left in the conversion folder.
func (r *mediaRig) nothingLeft() {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		left := converterProcesses()
		entries, _ := os.ReadDir(r.folder)
		if len(left) == 0 && len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			r.t.Fatalf("left behind: converter processes %v, files %v", left, names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// converterProcesses lists the ffmpeg, ffprobe and sandbox processes descended
// from this test process. Linux only; elsewhere it reports none.
func converterProcesses() []int {
	if runtime.GOOS != "linux" {
		return nil
	}
	parents, names := map[int]int{}, map[int]string{}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...; comm may contain spaces and parentheses.
		text := string(raw)
		open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
		if open < 0 || close < open {
			continue
		}
		fields := strings.Fields(text[close+1:])
		if len(fields) < 2 || fields[0] == "Z" {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		parents[pid], names[pid] = parent, text[open+1:close]
	}
	var found []int
	for pid, name := range names {
		if name != "ffmpeg" && name != "ffprobe" && name != "bwrap" {
			continue
		}
		for at := pid; at > 1; at = parents[at] {
			if at == os.Getpid() {
				found = append(found, pid)
				break
			}
		}
	}
	sort.Ints(found)
	return found
}

// generate writes a small test file into the library with ffmpeg's own
// synthetic picture and tone.
func (r *mediaRig) generate(name string, seconds int, codecs ...string) string {
	r.t.Helper()
	path := filepath.Join(r.library, name)
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", strconv.Itoa(seconds), "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2",
	}
	args = append(append(args, codecs...), path)
	if out, err := exec.Command(r.ffmpeg, args...).CombinedOutput(); err != nil {
		r.t.Fatalf("generate %s: %v\n%s", name, err, out)
	}
	return path
}

// request is the plan request for a file as the Chromium profile would play it.
func (r *mediaRig) request(path string) (PlanRequest, os.FileInfo) {
	r.t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		r.t.Fatal(err)
	}
	facts, err := mediafacts.Probe{Binary: r.ffprobe}.Inspect(context.Background(), path)
	if err != nil {
		r.t.Fatalf("probe %s: %v", path, err)
	}
	file := FileFacts{
		ID: filepath.Base(path), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
		Duration: facts.Duration, Size: info.Size(), Width: facts.Width, Height: facts.Height,
		DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true, Streams: facts.Streams,
	}
	return PlanRequest{
		File: file, Device: planTestDevice(r.t, "chromium"), Settings: DefaultDeliveryConfiguration(),
		TranscodingEnabled: true, FragmentedOutput: true,
	}, info
}

// start plans a file and starts its stream at a position.
func (r *mediaRig) start(session, path string, positionMs int64, adjust func(*PlanRequest)) (Stream, DeliveryPlan) {
	r.t.Helper()
	request, info := r.request(path)
	if adjust != nil {
		adjust(&request)
	}
	plan, err := Plan(request)
	if err != nil {
		r.t.Fatalf("plan %s: %v", path, err)
	}
	stream, err := r.streams.Start(context.Background(), StartRequest{
		SessionID: session, Plan: plan, Request: &request, Path: path, Size: info.Size(), Modified: info.ModTime(), PositionMs: positionMs,
	})
	if err != nil {
		r.t.Fatalf("start %s: %v", path, err)
	}
	return stream, plan
}

// get asks for one file of a stream the way the media route does.
func (r *mediaRig) get(ctx context.Context, grant, file, byteRange string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/media/"+grant+"/"+file, nil).WithContext(ctx)
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response := httptest.NewRecorder()
	r.streams.ServeMedia(response, request, grant, file)
	return response
}

// fetch gets a file, repeating while the server says it is still preparing.
func (r *mediaRig) fetch(grant, file string) []byte {
	r.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		response := r.get(context.Background(), grant, file, "")
		if response.Code == http.StatusOK {
			return response.Body.Bytes()
		}
		if response.Code != http.StatusServiceUnavailable || time.Now().After(deadline) {
			r.t.Fatalf("GET %s: %d %s", file, response.Code, response.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// playlist is one media playlist of a stream: its initialization file, if it
// has one, and its segment files in order.
type playlist struct {
	name     string
	init     string
	segments []string
}

// playlists reads a stream's master playlist and every playlist it names.
func (r *mediaRig) playlists(grant string) []playlist {
	r.t.Helper()
	var out []playlist
	var read func(name string)
	read = func(name string) {
		p := playlist{name: name}
		for _, line := range strings.Split(string(r.fetch(grant, name)), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "#EXT-X-MAP:"):
				p.init = quotedAttribute(line, "URI")
			case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
				if uri := quotedAttribute(line, "URI"); strings.HasSuffix(uri, ".m3u8") && !strings.HasPrefix(uri, "text-") {
					read(uri)
				}
			case strings.HasSuffix(line, ".m3u8") && !strings.HasPrefix(line, "#"):
				read(line)
			case line != "" && !strings.HasPrefix(line, "#"):
				p.segments = append(p.segments, line)
			}
		}
		if len(p.segments) > 0 {
			out = append(out, p)
		}
	}
	read("master.m3u8")
	if len(out) == 0 {
		r.t.Fatal("the stream published no segments")
	}
	return out
}

func quotedAttribute(line, name string) string {
	_, rest, found := strings.Cut(line, name+`="`)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(rest, `"`)
	return value
}

// packets downloads segments [from, to) of every playlist of a stream, joins
// each playlist's segments into one file as a player's buffer would, and
// returns what ffprobe finds: the codec of each track and the presentation time
// of every packet, in order.
type track struct {
	kind, codec string
	times       []float64
}

func (r *mediaRig) packets(grant string, from, to int) []track {
	r.t.Helper()
	var tracks []track
	for _, p := range r.playlists(grant) {
		if from >= len(p.segments) {
			r.t.Fatalf("%s has %d segments, wanted from %d", p.name, len(p.segments), from)
		}
		var joined []byte
		if p.init != "" {
			joined = append(joined, r.fetch(grant, p.init)...)
		}
		for i := from; i < to && i < len(p.segments); i++ {
			joined = append(joined, r.fetch(grant, p.segments[i])...)
		}
		ext := ".ts"
		if p.init != "" {
			ext = ".mp4"
		}
		file := filepath.Join(r.t.TempDir(), "joined"+ext)
		if err := os.WriteFile(file, joined, 0600); err != nil {
			r.t.Fatal(err)
		}
		raw, err := exec.Command(r.ffprobe, "-v", "error", "-show_entries", "stream=index,codec_type,codec_name",
			"-show_entries", "packet=stream_index,pts_time", "-of", "json", file).Output()
		if err != nil {
			r.t.Fatalf("ffprobe %s: %v", p.name, err)
		}
		var probed struct {
			Streams []struct {
				Index int    `json:"index"`
				Type  string `json:"codec_type"`
				Codec string `json:"codec_name"`
			} `json:"streams"`
			Packets []struct {
				Stream int    `json:"stream_index"`
				PTS    string `json:"pts_time"`
			} `json:"packets"`
		}
		if err = json.Unmarshal(raw, &probed); err != nil {
			r.t.Fatal(err)
		}
		for _, s := range probed.Streams {
			if s.Type != "video" && s.Type != "audio" {
				continue
			}
			tr := track{kind: s.Type, codec: s.Codec}
			for _, packet := range probed.Packets {
				if packet.Stream == s.Index {
					if at, err := strconv.ParseFloat(packet.PTS, 64); err == nil {
						tr.times = append(tr.times, at)
					}
				}
			}
			sort.Float64s(tr.times)
			tracks = append(tracks, tr)
		}
	}
	return tracks
}

// requireContinuous checks a stream's tracks carry the planned codecs and that
// each track's timestamps start where they should and run on without a hole:
// across segment boundaries, and across the point a seek restarted the converter.
func (r *mediaRig) requireContinuous(tracks []track, plan DeliveryPlan, startSeconds, endSeconds float64) {
	r.t.Helper()
	want := map[string]string{"video": plan.OutputVideoCodec, "audio": plan.OutputAudioCodec}
	seen := map[string]bool{}
	for _, tr := range tracks {
		seen[tr.kind] = true
		if want[tr.kind] != "" && tr.codec != want[tr.kind] {
			r.t.Fatalf("%s is %s, planned %s", tr.kind, tr.codec, want[tr.kind])
		}
		if len(tr.times) < 10 {
			r.t.Fatalf("%s has only %d packets", tr.kind, len(tr.times))
		}
		if first := tr.times[0]; first < startSeconds-0.5 || first > startSeconds+0.5 {
			r.t.Fatalf("%s starts at %.3fs, wanted about %.0fs", tr.kind, first, startSeconds)
		}
		if last := tr.times[len(tr.times)-1]; last < endSeconds-1 {
			r.t.Fatalf("%s ends at %.3fs, wanted about %.0fs", tr.kind, last, endSeconds)
		}
		for i := 1; i < len(tr.times); i++ {
			if gap := tr.times[i] - tr.times[i-1]; gap > 0.25 {
				r.t.Fatalf("%s has a %.3fs hole at %.3fs", tr.kind, gap, tr.times[i-1])
			}
		}
	}
	if !seen["video"] || !seen["audio"] {
		r.t.Fatalf("stream is missing a track: %v", seen)
	}
}

func (r *mediaRig) ready(session string) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		err := r.streams.Ready(ctx, session)
		if err == nil {
			return
		}
		if ctx.Err() != nil || !errors.Is(err, ErrSegmentPreparing) {
			r.t.Fatalf("ready: %v", err)
		}
	}
}

func TestOriginalFileIsServedByByteRange(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("direct.mp4", 4, "-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart")
	stream, plan := rig.start("s1", path, 0, nil)
	if stream.Mode != "direct" || plan.Strategy != DeliveryOriginal || !strings.HasSuffix(stream.URL, "/original") {
		t.Fatalf("a file the device plays was not offered as it is: %+v %s", stream, plan.Strategy)
	}
	rig.ready("s1")
	whole, _ := os.ReadFile(path)
	response := rig.get(context.Background(), stream.Grant, "original", "bytes=100-299")
	if response.Code != http.StatusPartialContent || response.Body.Len() != 200 || string(response.Body.Bytes()) != string(whole[100:300]) {
		t.Fatalf("range answered %d with %d bytes", response.Code, response.Body.Len())
	}
	if got := response.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("content type %q", got)
	}
	if response = rig.get(context.Background(), stream.Grant, "master.m3u8", ""); response.Code != http.StatusNotFound {
		t.Fatalf("a file this stream does not have answered %d", response.Code)
	}
	rig.streams.Stop("s1")
	if response = rig.get(context.Background(), stream.Grant, "original", ""); response.Code != http.StatusNotFound {
		t.Fatalf("a stopped stream's address answered %d", response.Code)
	}
}

func TestLibraryFileMustBeRecordedBeneathALibraryFolder(t *testing.T) {
	rig := newMediaRig(t)
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("not in a library"), 0600); err != nil {
		t.Fatal(err)
	}
	original := StartRequest{SessionID: "s1", Plan: DeliveryPlan{Mode: "direct", Strategy: DeliveryOriginal}}
	for _, path := range []string{outside, filepath.Join(rig.library, "missing.mp4")} {
		original.Path = path
		if _, err := rig.streams.Start(context.Background(), original); !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	// A link in a library to a file somewhere else plays: the scanner lists it.
	original.Path = filepath.Join(rig.library, "link.mp4")
	if err := os.Symlink(outside, original.Path); err != nil {
		t.Skip("no symbolic links here")
	}
	stream, err := rig.streams.Start(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if response := rig.get(context.Background(), stream.Grant, "original", ""); response.Body.String() != "not in a library" {
		t.Fatalf("a linked file answered %d with %q", response.Code, response.Body.String())
	}
}

func TestRemuxPlaysWithContinuousTimestamps(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("remux.mkv", 30, "-c:v", "libx264", "-c:a", "aac")
	stream, plan := rig.start("s1", path, 0, nil)
	if stream.Mode != "hls" || plan.VideoAction != "copy" || plan.AudioAction != "copy" {
		t.Fatalf("matroska for a browser was not repackaged: %s video %s audio %s", plan.Strategy, plan.VideoAction, plan.AudioAction)
	}
	rig.ready("s1")
	rig.requireContinuous(rig.packets(stream.Grant, 0, 99), plan, 0, 30)
	rig.streams.Stop("s1")
}

func TestAudioConversionKeepsThePictureAndConvertsTheSound(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("audio.mkv", 20, "-c:v", "libx264", "-c:a", "ac3")
	stream, plan := rig.start("s1", path, 0, nil)
	if plan.VideoAction != "copy" || plan.AudioAction != "convert" {
		t.Fatalf("AC-3 for a browser was not an audio conversion: %s video %s audio %s", plan.Strategy, plan.VideoAction, plan.AudioAction)
	}
	rig.ready("s1")
	rig.requireContinuous(rig.packets(stream.Grant, 0, 99), plan, 0, 20)
	rig.streams.Stop("s1")
}

func TestVideoConversionSeekAndQualityChange(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("convert.avi", 60, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	stream, plan := rig.start("s1", path, 0, nil)
	if plan.VideoAction != "convert" {
		t.Fatalf("MPEG-4 part 2 for a browser was not converted: %s", plan.Strategy)
	}
	rig.ready("s1")
	// The start of the title, then a seek far ahead: the converter moves to the
	// segment asked for instead of converting everything in between.
	rig.requireContinuous(rig.packets(stream.Grant, 0, 2), plan, 0, 12)
	rig.requireContinuous(rig.packets(stream.Grant, 7, 10), plan, 42, 60)

	// A quality change is a new stream at the same position: a new plan, a new
	// address, and the old address dead.
	changed, lower := rig.start("s1", path, 30_000, func(r *PlanRequest) {
		r.Choice.Limit, r.Choice.MaxVideoBitrateBPS, r.Choice.QualityID = true, 300_000, "limit"
	})
	if changed.Grant == stream.Grant {
		t.Fatal("the replacement stream kept the old grant")
	}
	if response := rig.get(context.Background(), stream.Grant, "master.m3u8", ""); response.Code != http.StatusNotFound {
		t.Fatalf("the replaced stream's address answered %d", response.Code)
	}
	rig.ready("s1")
	rig.requireContinuous(rig.packets(changed.Grant, 5, 8), lower, 30, 48)

	if response := rig.get(context.Background(), changed.Grant, "segment-zzzzzz.ts", ""); response.Code != http.StatusNotFound {
		t.Fatalf("an unknown file name answered %d", response.Code)
	}
	if response := rig.get(context.Background(), changed.Grant, "../../etc/passwd", ""); response.Code != http.StatusNotFound {
		t.Fatalf("a path outside the stream answered %d", response.Code)
	}
	rig.streams.Stop("s1")
}

// A player asks for a segment far ahead and goes away while the server is still
// converting towards it. The request stops waiting at once, nobody is left
// waiting for the segment, and the converter that was started for it is
// cancelled once nobody fetches from it, with the stream itself still alive.
func TestSegmentRequestStopsWaitingWhenThePlayerGoesAway(t *testing.T) {
	restore := hlsAbandonedAfterForTest(2 * time.Second)
	defer hlsAbandonedAfterForTest(restore)
	rig := newMediaRig(t)
	path := rig.generate("cancel.avi", 240, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	stream, _ := rig.start("s1", path, 0, nil)
	rig.ready("s1")
	segments := rig.playlists(stream.Grant)[0].segments
	h := rig.streams.hls
	cancelledWhileWaiting := false
	for _, index := range []int{len(segments) - 2, len(segments) / 2, len(segments) / 4} {
		ctx, cancel := context.WithCancel(context.Background())
		answered := make(chan int, 1)
		go func() { answered <- rig.get(ctx, stream.Grant, segments[index], "").Code }()
		// Cancel only once the request is really parked waiting for the segment.
		waiting := false
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !waiting; time.Sleep(5 * time.Millisecond) {
			waiting = h.waitsInFlight() > 0
			if len(answered) > 0 {
				break
			}
		}
		cancel()
		cancelled := time.Now()
		code := <-answered
		if !waiting || code == http.StatusOK {
			continue // the converter got there first; try a segment it has not reached
		}
		if elapsed := time.Since(cancelled); elapsed > 2*time.Second {
			t.Fatalf("the request kept waiting %s after the player went away", elapsed)
		}
		if n := h.waitsInFlight(); n != 0 {
			t.Fatalf("%d waits still open after the only request was cancelled", n)
		}
		cancelledWhileWaiting = true
		break
	}
	if !cancelledWhileWaiting {
		t.Skip("the converter reached every segment before a request could be seen waiting")
	}
	if runtime.GOOS == "linux" {
		deadline := time.Now().Add(30 * time.Second)
		for len(converterProcesses()) > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the converter kept working for a player that had gone: %v", converterProcesses())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if status, _ := rig.streams.Status("s1"); status.State != "playing" {
		t.Fatalf("the stream is %s; only its converter should have stopped", status.State)
	}
	rig.streams.Stop("s1")
}

func TestKilledConverterIsStartedAgain(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("finds the converter through /proc")
	}
	rig := newMediaRig(t)
	path := rig.generate("kill.avi", 120, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	stream, plan := rig.start("s1", path, 0, nil)
	rig.ready("s1")
	rig.packets(stream.Grant, 0, 1)
	killed := converterProcesses()
	if len(killed) == 0 {
		t.Skip("the conversion finished before it could be interrupted")
	}
	for _, pid := range killed {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
	// The viewer carries on: the next segments arrive from a new converter, with
	// timestamps that follow on from what was already played.
	rig.requireContinuous(rig.packets(stream.Grant, 12, 15), plan, 72, 90)
	if status, _ := rig.streams.Status("s1"); status.State != "playing" {
		t.Fatalf("stream is %s after its converter was killed: %+v", status.State, status.Failure)
	}
	rig.streams.Stop("s1")
}

func TestDeletedSourceFailsTheStreamCleanly(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("gone.avi", 120, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	stream, _ := rig.start("s1", path, 0, nil)
	rig.ready("s1")
	last := rig.playlists(stream.Grant)[0]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The running converter still holds the file. A seek needs it opened again.
	deadline := time.Now().Add(60 * time.Second)
	for {
		response := rig.get(context.Background(), stream.Grant, last.segments[len(last.segments)-2], "")
		if response.Code == http.StatusOK {
			t.Fatal("a segment was produced from a deleted file")
		}
		status, _ := rig.streams.Status("s1")
		if status.State == "failed" {
			if status.Failure == nil || status.Failure.Code != FailureSourceUnreadable {
				t.Fatalf("failure %+v", status.Failure)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream never failed: %d %s", response.Code, response.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if response := rig.get(context.Background(), stream.Grant, "master.m3u8", ""); response.Code != http.StatusNotFound {
		t.Fatalf("a failed stream's address answered %d", response.Code)
	}
	rig.streams.Stop("s1")
}

func TestStopDuringStartLeavesNothing(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("stop.avi", 60, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	for i := 0; i < 3; i++ {
		stream, _ := rig.start("s1", path, 0, nil)
		time.Sleep(time.Duration(i) * 150 * time.Millisecond)
		rig.streams.Stop("s1")
		if response := rig.get(context.Background(), stream.Grant, "master.m3u8", ""); response.Code != http.StatusNotFound {
			t.Fatalf("a stopped stream's address answered %d", response.Code)
		}
		if err := rig.streams.Ready(context.Background(), "s1"); !errors.Is(err, ErrGrantEnded) {
			t.Fatalf("ready after stop: %v", err)
		}
		rig.nothingLeft()
	}
}
