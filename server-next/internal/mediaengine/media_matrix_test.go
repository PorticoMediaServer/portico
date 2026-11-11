package mediaengine_test

// The media matrix: small real files made with ffmpeg are played through the media engine,
// over HTTP as an app plays them, for four kinds of device, and what comes out is measured
// with ffprobe against what the plan said would come out. Where the two disagree the test
// fails, naming the file and the device: a person would see wrong colours, a sideways or
// combed picture, silence, a jump at a seek, or subtitles missing.
//
// It takes minutes, so it runs only with PORTICO_MEDIA_MATRIX=1 (the media matrix workflow
// sets it). PORTICO_FFMPEG and PORTICO_FFPROBE point it at another ffmpeg, such as Portico's
// own bundle. PORTICO_OWNER_CLIPS names a folder of the owner's own short clips for what
// ffmpeg cannot make (testdata/OWNER-CLIPS.md). PORTICO_TEST_HARDWARE_BACKEND (qsv, vaapi,
// nvenc) adds conversions on the graphics chip, timed against real time.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// The devices a file is played for: the server's own baseline (an app that has said
// nothing), a browser, a TV that takes HEVC and HDR (with a receiver), and a TV that takes
// neither 10-bit HEVC nor HDR.
var matrixProfiles = []string{"baseline", "chromium", "shield_avr", "androidtv"}

// clipSeconds is the length of every generated file: four segments of a stream, so a seek
// can land in the third.
const clipSeconds = 20

// A seek is a start at seekMs: the stream then begins with the segment holding it.
const seekMs = 13_000

// The picture subtitle is a white box in the middle of the picture from 5 to 9 seconds.
const pgsFrom, pgsTo = 5, 9

func matrixTools(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	if os.Getenv("PORTICO_MEDIA_MATRIX") != "1" {
		t.Skip("the media matrix runs in its own workflow job; set PORTICO_MEDIA_MATRIX=1 to run it")
	}
	ffmpeg, ffprobe = os.Getenv("PORTICO_FFMPEG"), os.Getenv("PORTICO_FFPROBE")
	if ffmpeg == "" {
		ffmpeg = decodertest.QualifiedFFmpeg(t)
	}
	if ffprobe == "" {
		ffprobe = decodertest.QualifiedFFprobe(t)
	}
	return ffmpeg, ffprobe
}

// sample is one generated file: what it is made with and how.
type sample struct {
	name string
	// file is its name in the library; the folder takes the title from it.
	file string
	// needs are the encoders and filters it is made with. Without them it is skipped.
	needs []string
	// args makes it; dir holds the subtitle files written for it. A second command, when
	// there is one, makes the file from the first one's output.
	args func(dir, out string) [][]string
	// vfr says the frame rate varies on purpose, so no one rate is expected.
	vfr bool
	// burn says its picture subtitle is also burned in, for these profiles.
	burn []string
	// owner says it is one of the owner's clips, not made from the test picture.
	owner bool
	// text says it carries the SRT and ASS subtitles, checked for words and times.
	text bool
}

func lavfi(size string, rate int) []string {
	return []string{"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=%d", size, rate), "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"}
}

// common ends a command: inputs and codecs, then the length and a keyframe every two
// seconds, so a copied picture can be cut into the stream's segments.
func common(out string, rest ...string) []string {
	return append(rest, "-t", strconv.Itoa(clipSeconds), "-g", "48", out)
}

// toHDR turns the test picture, an SDR one, into the same picture in 10-bit HDR, the way a
// mastering would: a person sees the same brightness once it is tone-mapped back.
func toHDR(transfer string) string {
	return "zscale=tin=bt709:pin=bt709:min=bt709:t=" + transfer + ":p=bt2020:m=bt2020nc:npl=100:rangein=tv:range=tv,format=yuv420p10le"
}

const masterDisplay = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1)"

var samples = []sample{
	{name: "hevc-hdr10", file: "HDR Ten (2020).mkv", needs: []string{"libx265", "eac3", "zscale"}, burn: []string{"chromium", "shield_avr"},
		args: func(dir, out string) [][]string {
			args := append(lavfi("320x180", 24), "-i", filepath.Join(dir, "box.sup"), "-map", "0:v", "-map", "1:a", "-map", "2:s",
				"-vf", toHDR("smpte2084"), "-c:v", "libx265", "-preset", "ultrafast",
				"-x265-params", "log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display="+masterDisplay+":max-cll=1000,400",
				"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
				"-af", "aformat=channel_layouts=5.1", "-c:a", "eac3", "-c:s", "copy")
			return [][]string{common(out, args...)}
		}},
	{name: "hevc-hlg", file: "HLG (2020).mp4", needs: []string{"libx265", "aac", "zscale"},
		args: func(dir, out string) [][]string {
			args := append(lavfi("320x180", 24), "-vf", toHDR("arib-std-b67"), "-c:v", "libx265", "-preset", "ultrafast",
				"-x265-params", "log-level=error:repeat-headers=1:colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc", "-tag:v", "hvc1",
				"-color_primaries", "bt2020", "-color_trc", "arib-std-b67", "-colorspace", "bt2020nc", "-c:a", "aac", "-ac", "2", "-movflags", "+faststart")
			return [][]string{common(out, args...)}
		}},
	{name: "h264-8bit", file: "Eight Bit (2020).mp4", needs: []string{"libx264", "aac"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
				"-c:a", "aac", "-ac", "2", "-movflags", "+faststart")...)}
		}},
	{name: "h264-10bit", file: "Ten Bit (2020).mkv", needs: []string{"libx264", "flac"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-pix_fmt", "yuv420p10le", "-c:v", "libx264", "-preset", "ultrafast", "-profile:v", "high10",
				"-af", "aformat=channel_layouts=5.1", "-c:a", "flac")...)}
		}},
	{name: "mpeg2-interlaced", file: "Interlaced (2020).ts", needs: []string{"mpeg2video", "ac3"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("720x576", 50), "-vf", "tinterlace=mode=interleave_top,setfield=tff", "-c:v", "mpeg2video",
				"-flags", "+ilme+ildct", "-top", "1", "-b:v", "3M", "-af", "aformat=channel_layouts=5.1", "-c:a", "ac3")...)}
		}},
	{name: "vp9", file: "VP Nine (2020).webm", needs: []string{"libvpx-vp9", "libopus"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "300k",
				"-c:a", "libopus", "-ac", "2")...)}
		}},
	{name: "av1", file: "AV One (2020).mkv", needs: []string{"libsvtav1", "libopus"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-c:v", "libsvtav1", "-preset", "12", "-svtav1-params", "svt-log-level=1",
				"-c:a", "libopus", "-ac", "2")...)}
		}},
	{name: "rotated", file: "Rotated (2020).mp4", needs: []string{"libx264", "aac"},
		args: func(dir, out string) [][]string {
			upright := filepath.Join(dir, "upright.mp4")
			return [][]string{
				common(upright, append(lavfi("320x180", 30), "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-ac", "2")...),
				{"-display_rotation", "90", "-i", upright, "-c", "copy", "-movflags", "+faststart", out},
			}
		}},
	{name: "vfr", file: "Variable Rate (2020).mkv", needs: []string{"libx264", "aac"}, vfr: true,
		args: func(dir, out string) [][]string {
			// A second at 60 frames, a second at 20, and so on, as a phone records.
			return [][]string{common(out, append(lavfi("320x180", 60), "-vf", `select='lt(mod(t\,2)\,1)+not(mod(n\,3))'`, "-fps_mode", "vfr",
				"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-ac", "2")...)}
		}},
	{name: "dts", file: "DTS (2020).mkv", needs: []string{"libx264", "dca"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
				"-af", "aformat=channel_layouts=5.1", "-c:a", "dca", "-strict", "-2")...)}
		}},
	{name: "flac-7.1", file: "Seven One (2020).mkv", needs: []string{"libx264", "flac"},
		args: func(dir, out string) [][]string {
			return [][]string{common(out, append(lavfi("320x180", 24), "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
				"-af", "aformat=channel_layouts=7.1", "-c:a", "flac")...)}
		}},
	{name: "subtitles", file: "Subtitled (2020).mkv", needs: []string{"libx264", "aac", "srt", "ass"}, text: true, burn: []string{"chromium"},
		args: func(dir, out string) [][]string {
			args := append(lavfi("320x180", 24), "-i", filepath.Join(dir, "words.srt"), "-i", filepath.Join(dir, "styled.ass"), "-i", filepath.Join(dir, "box.sup"),
				"-map", "0:v", "-map", "1:a", "-map", "2:s", "-map", "3:s", "-map", "4:s",
				"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", "-metadata:s:s:2", "language=ger",
				"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-ac", "2", "-c:s:0", "srt", "-c:s:1", "ass", "-c:s:2", "copy")
			return [][]string{common(out, args...)}
		}},
}

const srtText = "1\n00:00:02,000 --> 00:00:04,500\nWords from the SRT track\n\n2\n00:00:14,000 --> 00:00:16,000\nA line after the seek\n"

const assText = "[Script Info]\nScriptType: v4.00+\nPlayResX: 320\nPlayResY: 180\n\n[V4+ Styles]\n" +
	"Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Alignment, MarginV\n" +
	"Style: Default,Arial,18,&H0000FFFF,1,1,2,10\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n" +
	"Dialogue: 0,0:00:05.00,0:00:07.50,Default,{\\an8\\c&H00FF00&}Styled words\n" +
	"Dialogue: 0,0:00:15.00,0:00:17.00,Default,{\\b0}Styled after the seek\n"

// The cues each text subtitle must deliver, in title time, whatever the stream. A cue may
// move by a frame or so (the source file's own start); more than a tenth of a second is wrong.
type cue struct {
	words    string
	from, to float64
}

var textCues = map[string][]cue{
	"srt": {{"Words from the SRT track", 2, 4.5}, {"A line after the seek", 14, 16}},
	"ass": {{"Styled words", 5, 7.5}, {"Styled after the seek", 15, 17}},
}

var vttCue = regexp.MustCompile(`(\d+):(\d\d):(\d\d)\.(\d{3}) --> (\d+):(\d\d):(\d\d)\.(\d{3})[^\n]*\n([^\n]+)`)

// cues reads a WebVTT file's cues: their words and times in seconds.
func cues(vtt string) []cue {
	var out []cue
	seconds := func(h, m, s, ms string) float64 {
		n := func(v string) float64 { f, _ := strconv.ParseFloat(v, 64); return f }
		return n(h)*3600 + n(m)*60 + n(s) + n(ms)/1000
	}
	for _, c := range vttCue.FindAllStringSubmatch(vtt, -1) {
		out = append(out, cue{words: strings.TrimSpace(c[9]), from: seconds(c[1], c[2], c[3], c[4]), to: seconds(c[5], c[6], c[7], c[8])})
	}
	return out
}

// pgsBox is a picture subtitle: a white box w×h at x,y on a width×height picture, shown from
// one second to another, as a Blu-ray carries it.
func pgsBox(width, height, x, y, w, h, from, to int) []byte {
	var out bytes.Buffer
	segment := func(seconds int, kind byte, body []byte) {
		out.WriteString("PG")
		binary.Write(&out, binary.BigEndian, uint32(seconds*90000))
		binary.Write(&out, binary.BigEndian, uint32(0))
		out.WriteByte(kind)
		binary.Write(&out, binary.BigEndian, uint16(len(body)))
		out.Write(body)
	}
	u16 := func(v int) []byte { return []byte{byte(v >> 8), byte(v)} }
	composition := func(number int, objects byte) []byte {
		body := append(append(u16(width), u16(height)...), 0x10)
		body = append(append(body, u16(number)...), 0x80, 0, 0, objects)
		if objects > 0 {
			body = append(append(append(body, 0, 0, 0, 0), u16(x)...), u16(y)...)
		}
		return body
	}
	// An empty picture at 0 seconds: ffmpeg moves an input to start at zero, and this keeps
	// the box at its own time.
	segment(0, 0x16, composition(0, 0))
	segment(0, 0x80, nil)
	segment(from, 0x16, composition(1, 1))
	segment(from, 0x17, append(append([]byte{1, 0, 0, 0, 0, 0}, u16(width)...), u16(height)...))
	segment(from, 0x14, []byte{0, 0, 0, 16, 128, 128, 0, 1, 235, 128, 128, 255})
	// Each row is one run of colour 1, then the end of the line: a few bytes a row, so even a
	// large box fits one segment.
	var rle []byte
	for row := 0; row < h; row++ {
		rle = append(rle, 0, 0xc0|byte(w>>8), byte(w), 1, 0, 0)
	}
	n := len(rle) + 4
	object := append([]byte{0, 0, 0, 0xc0, byte(n >> 16), byte(n >> 8), byte(n)}, append(u16(w), u16(h)...)...)
	segment(from, 0x15, append(object, rle...))
	segment(from, 0x80, nil)
	segment(to, 0x16, composition(2, 0))
	segment(to, 0x80, nil)
	return out.Bytes()
}

// The box, on the 320×180 frame every comparison is made on.
const boxX, boxY, boxW, boxH = 100, 70, 120, 40

// recorder is the media engine as the session layer sees it, with two differences for the
// test: it plans for the device the test names, and it keeps the plan it made.
type recorder struct {
	*mediaengine.Engine
	mu     sync.Mutex
	device json.RawMessage
	last   *streaming.DeliveryPlan
}

func (r *recorder) Plan(request playback.PlanRequest) (playback.Plan, error) {
	r.mu.Lock()
	request.Capabilities = r.device
	r.mu.Unlock()
	plan, err := r.Engine.Plan(request)
	if delivery, ok := mediaengine.PlannedDelivery(plan); err == nil && ok {
		r.mu.Lock()
		r.last = &delivery
		r.mu.Unlock()
	}
	return plan, err
}

// matrix is a whole server with the owner signed in, and what the test knows of it.
type matrix struct {
	*house
	db              *db.DB
	rec             *recorder
	ffmpeg, ffprobe string
	documents       map[string]json.RawMessage
	encoders        string
	mu              sync.Mutex
	summary         []string
	libraries       int
}

func newMatrix(t *testing.T, ffmpeg, ffprobe string) *matrix {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	data := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(data, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.New(ctx, d, data, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	previous := decoder.CurrentToolchain()
	engine, err := mediaengine.New(ctx, d, mediaengine.Options{Folder: filepath.Join(data, "transcodes"), FFmpeg: ffmpeg, FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{Engine: engine}
	handler.Playback().SetEngine(rec, engine)
	handler.ServeMediaWith(engine.ServeMedia)
	go engine.Run(ctx)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		wait, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if err := engine.Shutdown(wait); err != nil {
			t.Errorf("stopping the streams: %v", err)
		}
		cancel()
		decoder.RestoreToolchain(previous)
		d.Close()
	})
	h := &house{T: t, url: server.URL, data: data}
	code, err := os.ReadFile(filepath.Join(data, "setup-code.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var owner api.SignInResponse
	h.call("POST", "/v1/setup/owner", api.SetupOwnerRequest{SetupCode: strings.TrimSpace(string(code)), Username: "owner",
		Password: "a long enough test password", Device: api.DeviceInfo{InstallationID: strings.Repeat("m", 32), Name: "Test player"}}, &owner, 200)
	h.token = owner.Session.AccessToken
	var session api.Session
	h.call("POST", "/v1/profiles/"+owner.Profiles[0].ID+"/select", api.ProfileSelect{}, &session, 200)
	h.token = session.AccessToken

	m := &matrix{house: h, db: d, rec: rec, ffmpeg: ffmpeg, ffprobe: ffprobe}
	profiles, err := os.ReadFile(filepath.Join("..", "streaming", "testdata", "client-profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(profiles, &m.documents); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatalf("asking ffmpeg for its encoders: %v", err)
	}
	filters, _ := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	m.encoders = string(out) + string(filters)
	version, _ := exec.Command(ffmpeg, "-hide_banner", "-version").Output()
	first, _, _ := strings.Cut(string(version), "\n")
	t.Logf("ffmpeg: %s (%s)", first, ffmpeg)
	return m
}

func (m *matrix) has(name string) bool {
	return regexp.MustCompile(`(?m)^\s*\S+\s+` + regexp.QuoteMeta(name) + `\s`).MatchString(m.encoders)
}

func (m *matrix) run(t *testing.T, args ...string) {
	t.Helper()
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	if out, err := exec.Command(m.ffmpeg, full...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// title puts a file in a library of its own, scans it, and returns the title's id with the
// ids of its subtitle tracks by kind (srt, ass, pgs).
func (m *matrix) title(t *testing.T, path string) (string, map[string]string) {
	t.Helper()
	m.mu.Lock()
	m.libraries++
	name := fmt.Sprintf("Films %d", m.libraries)
	m.mu.Unlock()
	ctx := context.Background()
	lib, err := m.db.CreateLibrary(ctx, name, db.LibraryMovies, "en", []string{filepath.Dir(filepath.Dir(path))})
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := (&scan.Scanner{DB: m.db, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil || stats.ItemsNew != 1 {
		t.Fatalf("scanning %s: %+v, %v", path, stats, err)
	}
	var page api.ItemPage
	m.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	var options api.PlaybackOptions
	m.call("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil, &options, 200)
	subtitles := map[string]string{}
	for _, s := range options.Versions[0].Subtitles {
		switch strings.ToLower(s.Format) {
		case "srt", "subrip":
			subtitles["srt"] = s.ID
		case "ass", "ssa":
			subtitles["ass"] = s.ID
		case "pgs", "hdmv_pgs_subtitle":
			subtitles["pgs"] = s.ID
		}
	}
	return page.Items[0].ID, subtitles
}

// start starts a session for the profile and returns it with the plan it was given. A
// refusal comes back as its answer, with no session.
func (m *matrix) start(t *testing.T, profile string, body api.PlaybackStart) (*api.PlaybackSession, streaming.DeliveryPlan, string) {
	t.Helper()
	m.rec.mu.Lock()
	m.rec.device, m.rec.last = m.documents[profile], nil
	if profile == "baseline" {
		m.rec.device = nil
	}
	m.rec.mu.Unlock()
	key := fmt.Sprintf("%s-%d", profile, time.Now().UnixNano())
	deadline := time.Now().Add(2 * time.Minute)
	for {
		w := m.do("POST", "/v1/playback/sessions", body, "Idempotency-Key", key)
		raw, _ := io.ReadAll(w.Body)
		w.Body.Close()
		var session api.PlaybackSession
		switch {
		case w.StatusCode == http.StatusAccepted && time.Now().Before(deadline):
			time.Sleep(300 * time.Millisecond)
			continue
		case w.StatusCode != http.StatusCreated:
			return nil, streaming.DeliveryPlan{}, fmt.Sprintf("%d %s", w.StatusCode, raw)
		}
		if err := json.Unmarshal(raw, &session); err != nil {
			t.Fatal(err)
		}
		m.rec.mu.Lock()
		last := m.rec.last
		m.rec.mu.Unlock()
		if last == nil {
			t.Fatal("the session started without the engine planning it")
		}
		return &session, *last, ""
	}
}

// stop ends a session; ending one already ended is fine.
func (m *matrix) stop(session *api.PlaybackSession) {
	m.do("DELETE", "/v1/playback/sessions/"+session.ID, nil).Body.Close()
}

// probed is what ffprobe says of one track of a file.
type probed struct {
	Index      int    `json:"index"`
	Type       string `json:"codec_type"`
	Codec      string `json:"codec_name"`
	PixFmt     string `json:"pix_fmt"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Range      string `json:"color_range"`
	Transfer   string `json:"color_transfer"`
	Primaries  string `json:"color_primaries"`
	FieldOrder string `json:"field_order"`
	Rate       string `json:"avg_frame_rate"`
	Channels   int    `json:"channels"`
	SideData   []struct {
		Rotation float64 `json:"rotation"`
	} `json:"side_data_list"`
	times []float64
}

func (p probed) depth() int {
	for _, depth := range []int{10, 12} {
		if strings.Contains(p.PixFmt, strconv.Itoa(depth)) {
			return depth
		}
	}
	return 8
}

func (p probed) rotated() bool {
	for _, s := range p.SideData {
		if int(math.Abs(s.Rotation))%180 == 90 {
			return true
		}
	}
	return false
}

// display is the picture's size as a person sees it, turned upright.
func (p probed) display() (int, int) {
	if p.rotated() {
		return p.Height, p.Width
	}
	return p.Width, p.Height
}

func (p probed) fps() float64 {
	n, d, _ := strings.Cut(p.Rate, "/")
	num, _ := strconv.ParseFloat(n, 64)
	den, _ := strconv.ParseFloat(d, 64)
	if den == 0 {
		return 0
	}
	return num / den
}

func hdr(transfer string) bool { return transfer == "smpte2084" || transfer == "arib-std-b67" }

// probe reads a file's video and audio tracks, with every packet's time when packets is set.
func (m *matrix) probe(t *testing.T, path string, packets bool) (video, audio *probed) {
	t.Helper()
	args := []string{"-v", "error", "-show_streams", "-of", "json"}
	if packets {
		args = append(args, "-show_entries", "packet=stream_index,pts_time")
	}
	raw, err := exec.Command(m.ffprobe, append(args, path)...).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var out struct {
		Streams []probed `json:"streams"`
		Packets []struct {
			Stream int    `json:"stream_index"`
			PTS    string `json:"pts_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for i := range out.Streams {
		s := &out.Streams[i]
		for _, p := range out.Packets {
			if at, err := strconv.ParseFloat(p.PTS, 64); err == nil && p.Stream == s.Index {
				s.times = append(s.times, at)
			}
		}
		sort.Float64s(s.times)
		switch {
		case s.Type == "video" && video == nil && s.Codec != "mjpeg" && s.Codec != "png":
			video = s
		case s.Type == "audio" && audio == nil:
			audio = s
		}
	}
	return video, audio
}

// playlist is one media playlist of a stream: its initialization file, if any, and its
// segments with how long each says it is.
type playlist struct {
	init, name string
	parts      []string
	lengths    []float64
}

// at is where segment i starts on the stream's clock, by the playlist's own lengths.
func (l playlist) at(i int) float64 {
	var sum float64
	for _, d := range l.lengths[:min(i, len(l.lengths))] {
		sum += d
	}
	return sum
}

// playlists reads a stream's master playlist and the picture and sound playlists it names.
func (m *matrix) playlists(t *testing.T, url string) (string, []playlist) {
	t.Helper()
	base := url[:strings.LastIndex(url, "/")+1]
	var lists []playlist
	var read func(name string)
	read = func(name string) {
		code, _, body := m.media(base + name)
		if code != 200 {
			t.Fatalf("%s answered %d", name, code)
		}
		l := playlist{name: name}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "#EXT-X-MAP:"):
				l.init = attribute(line, "URI")
			case strings.HasPrefix(line, "#EXTINF:"):
				length, _ := strconv.ParseFloat(strings.TrimSuffix(strings.SplitN(line[len("#EXTINF:"):], ",", 2)[0], ","), 64)
				l.lengths = append(l.lengths, length)
			case strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=AUDIO"):
				if uri := attribute(line, "URI"); uri != "" {
					read(uri)
				}
			case strings.HasSuffix(line, ".m3u8") && !strings.HasPrefix(line, "#"):
				read(line)
			case line != "" && !strings.HasPrefix(line, "#"):
				l.parts = append(l.parts, line)
			}
		}
		if len(l.parts) > 0 {
			lists = append(lists, l)
		}
	}
	read(url[len(base):])
	if len(lists) == 0 {
		t.Fatalf("%s names no segments", url)
	}
	return base, lists
}

// segmentAt is the segment of the stream's first playlist that holds a time.
func (m *matrix) segmentAt(t *testing.T, url string, seconds float64) int {
	t.Helper()
	_, lists := m.playlists(t, url)
	for i := range lists[0].parts {
		if lists[0].at(i+1) > seconds {
			return i
		}
	}
	return len(lists[0].parts) - 1
}

// segments fetches segments [from, to) of every playlist of a stream, joins each
// playlist's as a player's buffer would, and returns what is in them, with where the first
// playlist says those segments start and end.
func (m *matrix) segments(t *testing.T, url string, from, to int) (video, audio *probed, files []string, start, end float64) {
	t.Helper()
	base, lists := m.playlists(t, url)
	start, end = lists[0].at(from), lists[0].at(to)
	for _, l := range lists {
		if from >= len(l.parts) {
			t.Fatalf("%s has %d segments, wanted from %d", l.name, len(l.parts), from)
		}
		var joined []byte
		if l.init != "" {
			_, _, body := m.media(base + l.init)
			joined = append(joined, body...)
		}
		for i := from; i < to && i < len(l.parts); i++ {
			code, _, body := m.media(base + l.parts[i])
			if code != 200 || len(body) == 0 {
				t.Fatalf("%s answered %d with %d bytes", l.parts[i], code, len(body))
			}
			joined = append(joined, body...)
		}
		ext := ".ts"
		if l.init != "" {
			ext = ".mp4"
		}
		file := filepath.Join(t.TempDir(), "joined"+ext)
		if err := os.WriteFile(file, joined, 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		v, a := m.probe(t, file, true)
		if video == nil {
			video = v
		}
		if audio == nil {
			audio = a
		}
	}
	return video, audio, files, start, end
}

func attribute(line, name string) string {
	_, rest, found := strings.Cut(line, name+`="`)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(rest, `"`)
	return value
}

// frame decodes the picture at a time into 320×180 grey levels.
func (m *matrix) frame(t *testing.T, at float64, input ...string) []byte {
	t.Helper()
	args := append([]string{"-v", "error", "-nostdin"}, input...)
	args = append(args, "-ss", strconv.FormatFloat(at, 'f', 2, 64), "-frames:v", "1", "-vf", "scale=320:180,format=gray", "-f", "rawvideo", "pipe:1")
	out, err := exec.Command(m.ffmpeg, args...).Output()
	if err != nil || len(out) != 320*180 {
		t.Fatalf("decoding a frame at %.1fs of %v: %v (%d bytes)", at, input, err, len(out))
	}
	return out
}

// brightness is a frame's mean grey level and its shares of near-black and clipped pixels.
func brightness(frame []byte) (mean, black, clipped float64) {
	for _, v := range frame {
		mean += float64(v)
		if v <= 20 {
			black++
		}
		if v >= 250 {
			clipped++
		}
	}
	n := float64(len(frame))
	return mean / n, black / n, clipped / n
}

// boxChange is how much the subtitle's box differs between two frames, inside and outside it.
func boxChange(a, b []byte) (inside, outside float64) {
	var in, out, nIn, nOut float64
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			d := math.Abs(float64(a[y*320+x]) - float64(b[y*320+x]))
			if x >= boxX && x < boxX+boxW && y >= boxY && y < boxY+boxH {
				in, nIn = in+d, nIn+1
			} else {
				out, nOut = out+d, nOut+1
			}
		}
	}
	return in / nIn, out / nOut
}

// finding is one place where what came out is not what was planned.
type finding struct {
	t      *testing.T
	failed []string
}

func (f *finding) check(ok bool, format string, args ...any) {
	if !ok {
		f.failed = append(f.failed, fmt.Sprintf(format, args...))
	}
}

func (f *finding) report(where string) {
	f.t.Helper()
	if len(f.failed) > 0 {
		f.t.Errorf("FINDING %s: what came out is not what was planned:\n  %s", where, strings.Join(f.failed, "\n  "))
	}
}

// checkVideo holds the stream's picture against the plan and the source.
func checkVideo(f *finding, plan streaming.DeliveryPlan, source, out *probed, vfr bool) {
	if out == nil {
		f.check(false, "the stream has no picture")
		return
	}
	sw, sh := source.display()
	ow, oh := out.display()
	if plan.VideoAction != "convert" {
		f.check(out.Codec == source.Codec, "picture copied, so %s, but it is %s", source.Codec, out.Codec)
		f.check(out.depth() == source.depth(), "picture copied, so %d-bit, but it is %d-bit (%s)", source.depth(), out.depth(), out.PixFmt)
		f.check(out.Transfer == source.Transfer, "picture copied, so transfer %q, but it is %q", source.Transfer, out.Transfer)
		f.check(ow == sw && oh == sh, "picture copied, so %dx%d upright, but it shows %dx%d (rotation lost or added)", sw, sh, ow, oh)
		return
	}
	f.check(out.Codec == plan.OutputVideoCodec, "picture converted to %s, but it is %s", plan.OutputVideoCodec, out.Codec)
	depth := max(plan.OutputBitDepth, 8)
	f.check(out.depth() == depth, "planned %d-bit, but it is %d-bit (%s)", depth, out.depth(), out.PixFmt)
	switch {
	case plan.OutputRange == "hdr10":
		f.check(out.Transfer == "smpte2084" && out.Primaries == "bt2020", "planned HDR10, but tagged %s/%s", out.Transfer, out.Primaries)
	case plan.OutputRange == "hlg":
		f.check(out.Transfer == "arib-std-b67" && out.Primaries == "bt2020", "planned HLG, but tagged %s/%s", out.Transfer, out.Primaries)
	case plan.ToneMap:
		f.check(out.Transfer == "bt709" && out.Primaries == "bt709", "planned tone-mapped SDR, but tagged %s/%s", out.Transfer, out.Primaries)
	default:
		f.check(!hdr(out.Transfer), "planned SDR, but tagged %s", out.Transfer)
		f.check(!hdr(source.Transfer) || plan.ToneMapOff, "an HDR picture planned to SDR without tone mapping")
	}
	f.check(out.Range == "tv" || out.Range == "unknown" || out.Range == "", "colour range %q, want limited (tv)", out.Range)
	if plan.TargetHeight > 0 {
		f.check(oh == plan.TargetHeight || out.Height == plan.TargetHeight, "planned %d high, but it is %dx%d", plan.TargetHeight, ow, oh)
	} else {
		f.check(oh <= sh && ow <= sw, "the picture grew from %dx%d to %dx%d", sw, sh, ow, oh)
	}
	if sw > 0 && sh > 0 && ow > 0 && oh > 0 {
		f.check(math.Abs(float64(ow)/float64(oh)-float64(sw)/float64(sh)) < 0.05, "the shape changed from %dx%d to %dx%d (sideways or stretched)", sw, sh, ow, oh)
	}
	if source.FieldOrder != "" && source.FieldOrder != "progressive" && source.FieldOrder != "unknown" {
		f.check(plan.Deinterlace, "an interlaced picture (%s) converted without deinterlacing", source.FieldOrder)
		f.check(out.FieldOrder == "progressive" || out.FieldOrder == "" || out.FieldOrder == "unknown", "still interlaced after conversion (%s)", out.FieldOrder)
	}
	if !vfr && source.fps() > 0 && out.fps() > 0 {
		want := source.fps()
		if plan.MaxFrameRate > 0 && want > plan.MaxFrameRate {
			want = plan.MaxFrameRate
		}
		doubled := plan.Deinterlace && math.Abs(out.fps()-2*want) < 0.5
		f.check(math.Abs(out.fps()-want) < 0.5 || doubled, "frame rate %.3f, want %.3f", out.fps(), want)
	}
}

// checkAudio holds the stream's sound against the plan and the source.
func checkAudio(f *finding, plan streaming.DeliveryPlan, source, out *probed) {
	if source == nil {
		return
	}
	if out == nil {
		f.check(false, "the stream has no sound")
		return
	}
	if plan.AudioAction != "convert" {
		f.check(out.Codec == source.Codec, "sound copied, so %s, but it is %s", source.Codec, out.Codec)
		f.check(out.Channels == source.Channels, "sound copied, so %d channels, but it has %d", source.Channels, out.Channels)
		return
	}
	f.check(out.Codec == plan.OutputAudioCodec, "sound converted to %s, but it is %s", plan.OutputAudioCodec, out.Codec)
	if plan.AudioChannels > 0 {
		f.check(out.Channels == plan.AudioChannels, "planned %d channels, but it has %d", plan.AudioChannels, out.Channels)
	}
}

// checkTimeline holds that each track starts where it should, runs to where it should
// without a hole, and that picture and sound start together.
func checkTimeline(f *finding, video, audio *probed, start, end float64) {
	for _, track := range []*probed{video, audio} {
		if track == nil {
			continue
		}
		if len(track.times) < 10 {
			f.check(false, "%s has only %d packets", track.Type, len(track.times))
			continue
		}
		first, last := track.times[0], track.times[len(track.times)-1]
		f.check(math.Abs(first-start) <= 0.5, "%s starts at %.3fs, want about %.0fs", track.Type, first, start)
		f.check(last >= end-1, "%s ends at %.3fs, want about %.0fs", track.Type, last, end)
		for i := 1; i < len(track.times); i++ {
			if gap := track.times[i] - track.times[i-1]; gap > 0.25 {
				f.check(false, "%s has a %.3fs hole at %.3fs", track.Type, gap, track.times[i-1])
				break
			}
		}
	}
	if video != nil && audio != nil && len(video.times) > 0 && len(audio.times) > 0 {
		f.check(math.Abs(video.times[0]-audio.times[0]) <= 0.3, "picture starts at %.3fs and sound at %.3fs", video.times[0], audio.times[0])
	}
}

// checkText holds that every text subtitle delivered beside the stream has its words at
// their times.
func (m *matrix) checkText(f *finding, session *api.PlaybackSession, tracks map[string]string) {
	served := map[string]string{}
	for _, s := range session.Presentation.Subtitles {
		code, _, body := m.media(s.URL)
		f.check(code == 200 && strings.HasPrefix(string(body), "WEBVTT"), "subtitle %s answered %d: %.80q", s.TrackID, code, body)
		served[s.TrackID] = string(body)
	}
	for kind, wanted := range textCues {
		vtt, ok := served[tracks[kind]]
		if !ok {
			f.check(false, "the %s subtitle is not delivered beside the stream", kind)
			continue
		}
		got := cues(vtt)
		for _, want := range wanted {
			found := false
			for _, c := range got {
				found = found || c.words == want.words && math.Abs(c.from-want.from) <= 0.1 && math.Abs(c.to-want.to) <= 0.1
			}
			f.check(found, "the %s subtitle lacks %q from %.1fs to %.1fs in:\n%s", kind, want.words, want.from, want.to, vtt)
		}
		f.check(!strings.Contains(vtt, `{\`), "the %s subtitle still carries styling codes:\n%s", kind, vtt)
	}
}

func (m *matrix) note(line string) {
	m.mu.Lock()
	m.summary = append(m.summary, line)
	m.mu.Unlock()
}

func describe(plan streaming.DeliveryPlan) string {
	video := plan.VideoAction
	if plan.VideoAction == "convert" {
		video = fmt.Sprintf("convert→%s %dbit %s", plan.OutputVideoCodec, max(plan.OutputBitDepth, 8), cmpOr(plan.OutputRange, "sdr"))
		if plan.ToneMap {
			video += " tone-mapped"
		}
		if plan.HardwareBackend != "" {
			video += " on " + plan.HardwareBackend
		}
	}
	audio := plan.AudioAction
	if plan.AudioAction == "convert" {
		audio = fmt.Sprintf("convert→%s %dch", plan.OutputAudioCodec, plan.AudioChannels)
	}
	return fmt.Sprintf("%-10s %-8s video %-40s audio %s", plan.Strategy, plan.OutputContainer, video, audio)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// play plays one title for one profile and holds what comes out against the plan: the
// stream from its start, then from a seek, its text subtitles, and when asked its picture
// subtitle burned in. It returns the first stream's files for later comparison.
func (m *matrix) play(t *testing.T, name, path, item string, subtitles map[string]string, profile string, s sample) {
	t.Helper()
	video, audio := m.probe(t, path, false)
	if video == nil {
		t.Fatalf("%s has no picture", path)
	}
	body := api.PlaybackStart{ItemID: item, StartFrom: "beginning"}
	if id, ok := subtitles["srt"]; ok {
		body.Subtitles = &api.SubtitlePick{TrackID: &id}
	}
	session, plan, refused := m.start(t, profile, body)
	where := name + " for " + profile
	if refused != "" {
		m.note(fmt.Sprintf("%-18s %-11s refused: %s", name, profile, refused))
		t.Logf("%s: refused, %s", where, refused)
		return
	}
	defer m.stop(session)
	m.note(fmt.Sprintf("%-18s %-11s %s", name, profile, describe(plan)))
	f := &finding{t: t}
	p := session.Presentation
	f.check(p.Route == map[streaming.DeliveryStrategy]string{streaming.DeliveryOriginal: playback.RouteOriginal, streaming.DeliveryCopyRemux: playback.RouteRepackaged,
		streaming.DeliveryAudioConversion: playback.RouteConverted, streaming.DeliveryVideoConversion: playback.RouteConverted}[plan.Strategy],
		"the session says route %s for a plan of %s", p.Route, plan.Strategy)

	if plan.Strategy == streaming.DeliveryOriginal {
		info, _ := os.Stat(path)
		code, header, _ := m.media(p.URL, "Range", "bytes=0-0")
		f.check(code == http.StatusPartialContent && strings.HasSuffix(header.Get("Content-Range"), "/"+strconv.FormatInt(info.Size(), 10)),
			"the original answered %d %q", code, header.Get("Content-Range"))
		if s.text {
			m.checkText(f, session, subtitles)
		}
		f.report(where)
		return
	}

	outVideo, outAudio, files, _, end := m.segments(t, p.URL, 0, 3)
	checkVideo(f, plan, video, outVideo, s.vfr)
	checkAudio(f, plan, audio, outAudio)
	checkTimeline(f, outVideo, outAudio, 0, min(end, clipSeconds))
	if s.text {
		m.checkText(f, session, subtitles)
	}
	if plan.ToneMap && outVideo != nil {
		// Tone-mapped back, the picture looks about as the SDR picture it was made from; an
		// owner's clip is held to being neither black nor washed out.
		mean, black, clipped := brightness(m.frame(t, 3, "-i", files[0]))
		wantMean, wantBlack, wantClipped, tolerance := 110.0, 0.3, 0.1, 90.0
		if !s.owner {
			tolerance = 45
			wantMean, wantBlack, wantClipped = brightness(m.frame(t, 3, "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=24:duration=4", video.Width, video.Height)))
		}
		f.check(math.Abs(mean-wantMean) < tolerance && black <= wantBlack+0.2 && clipped <= wantClipped+0.1,
			"tone-mapped picture: mean %.0f, black %.2f, clipped %.2f; the SDR original: mean %.0f, black %.2f, clipped %.2f",
			mean, black, clipped, wantMean, wantBlack, wantClipped)
	}
	f.report(where)

	// A seek: the stream begins again in the third segment, on time, with no hole.
	position := int64(seekMs)
	body.StartPositionMs, body.StartFrom = &position, ""
	m.stop(session)
	sought, soughtPlan, refused := m.start(t, profile, body)
	if refused != "" {
		t.Errorf("FINDING %s: refused after a seek: %s", where, refused)
		return
	}
	defer m.stop(sought)
	f = &finding{t: t}
	f.check(soughtPlan.Strategy == plan.Strategy, "the seek is planned %s, the start %s", soughtPlan.Strategy, plan.Strategy)
	segment := m.segmentAt(t, sought.Presentation.URL, seekMs/1000.0)
	seekVideo, seekAudio, _, from, to := m.segments(t, sought.Presentation.URL, segment, segment+2)
	checkTimeline(f, seekVideo, seekAudio, from, min(to, clipSeconds))
	if s.text {
		m.checkText(f, sought, subtitles)
	}
	f.report(where + " after a seek")
	m.stop(sought)

	for _, burnFor := range s.burn {
		if burnFor != profile {
			continue
		}
		id, ok := subtitles["pgs"]
		if !ok {
			t.Errorf("FINDING %s: the picture subtitle was not found in the file", where)
			continue
		}
		m.burn(t, where, profile, item, id, files[0])
	}
}

// burn plays the title with its picture subtitle burned in and holds that the box is in the
// picture while it is shown and not after: against the same stream without it.
func (m *matrix) burn(t *testing.T, where, profile, item, pgs, plain string) {
	t.Helper()
	session, plan, refused := m.start(t, profile, api.PlaybackStart{ItemID: item, StartFrom: "beginning",
		Subtitles: &api.SubtitlePick{TrackID: &pgs, Delivery: "burn"}})
	if refused != "" {
		t.Errorf("FINDING %s: burning the picture subtitle was refused: %s", where, refused)
		return
	}
	defer m.stop(session)
	m.note(fmt.Sprintf("%-18s %-11s %s (picture subtitle burned)", strings.Fields(where)[0], profile, describe(plan)))
	f := &finding{t: t}
	f.check(plan.VideoAction == "convert", "burning in needs the picture converted, but it is planned %s", plan.VideoAction)
	_, _, files, _, _ := m.segments(t, session.Presentation.URL, 0, 3)
	shown := (pgsFrom + pgsTo) / 2
	inside, outside := boxChange(m.frame(t, float64(shown), "-i", files[0]), m.frame(t, float64(shown), "-i", plain))
	f.check(inside > 40 && outside < 15, "at %ds the box should differ from the plain stream inside (%.0f, want > 40) and not outside (%.0f, want < 15)", shown, inside, outside)
	inside, outside = boxChange(m.frame(t, pgsTo+1.5, "-i", files[0]), m.frame(t, pgsTo+1.5, "-i", plain))
	f.check(inside < 15, "after the subtitle ends the box is still there (%.0f)", inside)
	f.report(where + " with the picture subtitle burned in")
}

// TestMediaMatrix plays every generated file for every profile.
func TestMediaMatrix(t *testing.T) {
	ffmpeg, ffprobe := matrixTools(t)
	m := newMatrix(t, ffmpeg, ffprobe)
	t.Cleanup(func() { t.Logf("what was planned:\n%s", strings.Join(m.summary, "\n")) })
	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			for _, need := range s.needs {
				if !m.has(need) {
					t.Skipf("this ffmpeg cannot make %s: it has no %s", s.name, need)
				}
			}
			dir := t.TempDir()
			for name, data := range map[string][]byte{"words.srt": []byte(srtText), "styled.ass": []byte(assText),
				"box.sup": pgsBox(320, 180, boxX, boxY, boxW, boxH, pgsFrom, pgsTo)} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "library", strings.TrimSuffix(s.file, filepath.Ext(s.file)), s.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, args := range s.args(dir, path) {
				m.run(t, args...)
			}
			item, subtitles := m.title(t, path)
			for _, profile := range matrixProfiles {
				t.Run(profile, func(t *testing.T) { m.play(t, s.name, path, item, subtitles, profile, s) })
			}
		})
	}
}

// The owner's clips, for what ffmpeg cannot make. Each is played for every profile like a
// generated file; a refusal is reported, since the plan may rightly refuse (Dolby Vision 5
// without a reshaping ffmpeg).
var ownerClips = []string{"dv-p5.mkv", "dv-p7.mkv", "dv-p8.mp4", "hdr10plus.mkv", "truehd-atmos.mkv", "dts-hd-ma.mkv"}

func TestMediaMatrixOwnerClips(t *testing.T) {
	ffmpeg, ffprobe := matrixTools(t)
	folder := os.Getenv("PORTICO_OWNER_CLIPS")
	if folder == "" {
		t.Skip("no owner clips: set PORTICO_OWNER_CLIPS to their folder (see testdata/OWNER-CLIPS.md)")
	}
	m := newMatrix(t, ffmpeg, ffprobe)
	t.Cleanup(func() { t.Logf("what was planned:\n%s", strings.Join(m.summary, "\n")) })
	for _, name := range ownerClips {
		t.Run(strings.TrimSuffix(name, filepath.Ext(name)), func(t *testing.T) {
			clip := filepath.Join(folder, name)
			if _, err := os.Stat(clip); err != nil {
				t.Skipf("%s is not in %s", name, folder)
			}
			title := "Owner " + strings.TrimSuffix(name, filepath.Ext(name)) + " (2020)"
			path := filepath.Join(t.TempDir(), "library", title, title+filepath.Ext(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			// A link, so a large clip is not copied: the scanner lists links.
			if err := os.Symlink(clip, path); err != nil {
				t.Fatal(err)
			}
			item, subtitles := m.title(t, path)
			delete(subtitles, "srt")
			for _, profile := range matrixProfiles {
				t.Run(profile, func(t *testing.T) { m.play(t, name, path, item, subtitles, profile, sample{name: name, owner: true}) })
			}
		})
	}
}

// TestMediaMatrixHardware converts on the graphics chip named by
// PORTICO_TEST_HARDWARE_BACKEND, for a browser that takes only H.264: an HEVC film at 1080p,
// a 4K HDR10 one tone-mapped to SDR, and the 1080p one with its picture subtitle burned in.
// Each must be planned on that chip, come out as planned, and be made at least as fast as it
// plays. The speeds are logged as SPEED lines.
func TestMediaMatrixHardware(t *testing.T) {
	ffmpeg, ffprobe := matrixTools(t)
	backend := os.Getenv("PORTICO_TEST_HARDWARE_BACKEND")
	if backend == "" {
		t.Skip("no graphics chip named: set PORTICO_TEST_HARDWARE_BACKEND to qsv, vaapi or nvenc")
	}
	// What each backend's own trial says, so a fall back to software comes with its reason.
	detector := &decoder.HardwareDetector{Timeout: 30 * time.Second}
	if libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil); err == nil {
		detector.Libraries = libraries
	}
	for _, probe := range detector.Report(context.Background(), ffmpeg, os.Getenv("PORTICO_TEST_HARDWARE_DEVICE")) {
		t.Logf("hardware trial: %s available=%v reason=%s encoder=%s native filters=%v", probe.Backend, probe.Available, probe.Reason, probe.Encoder, probe.NativeFilters)
	}
	m := newMatrix(t, ffmpeg, ffprobe)
	t.Cleanup(func() { t.Logf("what was planned:\n%s", strings.Join(m.summary, "\n")) })
	dir := t.TempDir()
	sup := filepath.Join(dir, "box.sup")
	if err := os.WriteFile(sup, pgsBox(1920, 1080, 600, 420, 720, 240, pgsFrom, pgsTo), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each film in a library of its own.
	generate := func(file string, args ...string) string {
		path := filepath.Join(t.TempDir(), "library", strings.TrimSuffix(file, filepath.Ext(file)), file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		m.run(t, common(path, args...)...)
		return path
	}
	sdr := generate("Ten Eighty (2020).mkv", append(lavfi("1920x1080", 24), "-i", sup, "-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-pix_fmt", "yuv420p", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error", "-c:a", "aac", "-ac", "2", "-c:s", "copy")...)
	hdr := generate("Four K (2020).mkv", append(lavfi("3840x2160", 24), "-vf", toHDR("smpte2084"), "-c:v", "libx265", "-preset", "ultrafast",
		"-x265-params", "log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display="+masterDisplay+":max-cll=1000,400",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-c:a", "aac", "-ac", "2")...)

	for _, c := range []struct {
		name, path string
		burn       bool
	}{{"hevc-1080p", sdr, false}, {"hevc-4k-hdr10-tone-mapped", hdr, false}, {"hevc-1080p-burned", sdr, true}} {
		t.Run(c.name, func(t *testing.T) {
			item, subtitles := m.title(t, c.path)
			body := api.PlaybackStart{ItemID: item, StartFrom: "beginning"}
			if c.burn {
				id := subtitles["pgs"]
				body.Subtitles = &api.SubtitlePick{TrackID: &id, Delivery: "burn"}
			}
			// The chip is tried out beside the server's start; until then a conversion is
			// planned in software. Wait for it, up to three minutes.
			var session *api.PlaybackSession
			var plan streaming.DeliveryPlan
			var began time.Time
			for deadline := time.Now().Add(3 * time.Minute); ; {
				var refused string
				began = time.Now()
				session, plan, refused = m.start(t, "web", body)
				if refused != "" {
					t.Fatalf("FINDING %s for web: refused: %s", c.name, refused)
				}
				if plan.HardwareBackend == backend || time.Now().After(deadline) {
					break
				}
				m.stop(session)
				time.Sleep(5 * time.Second)
			}
			defer m.stop(session)
			m.note(fmt.Sprintf("%-26s web  %s", c.name, describe(plan)))
			f := &finding{t: t}
			f.check(plan.HardwareBackend == backend, "planned on %s, not %s: see the hardware trial lines for why", plan.HardwareBackend, backend)
			video, audio := m.probe(t, c.path, false)
			outVideo, outAudio, files, _, _ := m.segments(t, session.Presentation.URL, 0, 99)
			elapsed := time.Since(began)
			speed := float64(clipSeconds) / elapsed.Seconds()
			t.Logf("SPEED %s on %s: %d seconds of %dx%d made in %.1fs, %.2fx real time", c.name, plan.HardwareBackend, clipSeconds, video.Width, video.Height, elapsed.Seconds(), speed)
			f.check(speed >= 1, "made at %.2fx real time: a viewer would wait", speed)
			checkVideo(f, plan, video, outVideo, false)
			checkAudio(f, plan, audio, outAudio)
			checkTimeline(f, outVideo, outAudio, 0, clipSeconds)
			if plan.ToneMap && outVideo != nil {
				mean, black, clipped := brightness(m.frame(t, 3, "-i", files[0]))
				wantMean, wantBlack, wantClipped := brightness(m.frame(t, 3, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=4"))
				f.check(math.Abs(mean-wantMean) < 45 && black <= wantBlack+0.2 && clipped <= wantClipped+0.1,
					"tone-mapped picture: mean %.0f, black %.2f, clipped %.2f; the SDR original: mean %.0f, black %.2f, clipped %.2f",
					mean, black, clipped, wantMean, wantBlack, wantClipped)
			}
			if c.burn {
				// The box covers the middle of the picture from 5 to 9 seconds.
				shown := m.frame(t, 7, "-i", files[0])
				after := m.frame(t, 3, "-i", files[0])
				inside, _ := boxChange(shown, after)
				var bright float64
				for y := boxY; y < boxY+boxH; y++ {
					for x := boxX; x < boxX+boxW; x++ {
						bright += float64(shown[y*320+x])
					}
				}
				bright /= boxW * boxH
				f.check(bright > 200 && inside > 40, "the burned box is not in the picture at 7s (brightness %.0f, change %.0f)", bright, inside)
			}
			f.report(c.name + " on " + backend)
		})
	}
}
