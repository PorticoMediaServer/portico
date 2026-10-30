package mediaanalysis

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect/segmentsynth"
)

// The accuracy harness: synthetic shows and a film, generated with ffmpeg
// from seeds (no media is stored), decoded through the detector's own decoder
// recipes and scored against the truth the generator knows.
//
// The short version runs whenever ffmpeg is installed. The full set is slower:
//
//	PORTICO_SEGMENT_ACCURACY=1 go test ./internal/mediaanalysis -run SegmentAccuracy -v

type truth struct{ start, end float64 } // seconds; zero length means "none"

type harnessEpisode struct {
	name           string
	season, number int
	parts          segmentsynth.Programme
	intro, credits truth
	scene          bool // content follows the credits
}

// buildEpisode lays out cold open, intro, body, credits and tail.
func buildEpisode(name string, season, number int, seed uint64, coldOpen float64, intro segmentsynth.Signal, introLength, body float64, credits segmentsynth.Signal, creditsLength, post float64, overlay bool) harnessEpisode {
	e := harnessEpisode{name: name, season: season, number: number}
	t := 0.0
	add := func(p segmentsynth.Part) { e.parts = append(e.parts, p); t += p.Length }
	if coldOpen > 0 {
		add(segmentsynth.Part{Signal: segmentsynth.Speech(seed), Length: coldOpen})
	}
	if intro != nil {
		signal := intro
		if overlay {
			// Dialogue over the intro music: the intro varies slightly.
			signal = segmentsynth.Mix(intro, segmentsynth.Speech(seed+7), 0.35)
		}
		e.intro = truth{t, t + introLength + 1}
		add(segmentsynth.Part{Signal: signal, Length: introLength})
		add(segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 1})
	}
	add(segmentsynth.Part{Signal: segmentsynth.Speech(seed + 1000), Length: body})
	if credits != nil {
		e.credits = truth{t, t + creditsLength}
		add(segmentsynth.Part{Signal: credits, Length: creditsLength})
	}
	if post > 0 {
		e.scene = true
		add(segmentsynth.Part{Signal: segmentsynth.Speech(seed + 2000), Length: post})
	} else {
		add(segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 2})
		if credits != nil {
			e.credits.end = t
		}
	}
	return e
}

type harness struct {
	t      *testing.T
	ffmpeg string
	dir    string
	server *httptest.Server
}

func newHarness(t *testing.T) *harness {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	h := &harness{t: t, ffmpeg: ffmpeg, dir: dir, server: httptest.NewServer(http.FileServer(http.Dir(dir)))}
	t.Cleanup(h.server.Close)
	return h
}

func (h *harness) run(args ...string) {
	h.t.Helper()
	out, err := exec.Command(h.ffmpeg, append([]string{"-hide_banner", "-v", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		h.t.Fatalf("ffmpeg %v: %v %s", args, err, out)
	}
}

// episodeFile encodes the episode's audio over a plain moving picture, as AAC
// in Matroska: the codec's artefacts are part of the test.
func (h *harness) episodeFile(e harnessEpisode) (string, int64) {
	pcm := filepath.Join(h.dir, e.name+".pcm")
	if err := os.WriteFile(pcm, e.parts.Render(16000), 0o600); err != nil {
		h.t.Fatal(err)
	}
	file := e.name + ".mkv"
	duration := e.parts.Duration()
	h.run("-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=320x180:rate=5:duration=%.3f", duration), "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", pcm,
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "38", "-g", "50", "-c:a", "aac", "-b:a", "96k", "-ac", "2", "-ar", "48000", "-shortest", filepath.Join(h.dir, file))
	_ = os.Remove(pcm)
	return file, int64(math.Round(duration * 1e6))
}

// decode runs one recipe against a generated file through HTTP, as the
// bridge serves files to the real decoder.
func (h *harness) decode(file string) Decode {
	return func(ctx context.Context, _ string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
		args, err := decoder.AnalysisArgs(h.server.URL+"/"+file, spec)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, h.ffmpeg, args...)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err = cmd.Start(); err != nil {
			return err
		}
		consumeErr := consume(out)
		_, _ = io.Copy(io.Discard, out)
		if err = cmd.Wait(); err != nil {
			return fmt.Errorf("%v: %s", err, stderr.String())
		}
		return consumeErr
	}
}

type score struct {
	kind             string
	tp, fp, fn       int
	autoFP           int
	startErr, endErr []float64
	unsafeAutomatic  []string
}

func (s *score) add(label string, want truth, got []segmentdetect.Detection) {
	wanted := want.end > want.start
	var best *segmentdetect.Detection
	for i := range got {
		if got[i].Kind == s.kind {
			best = &got[i]
			break
		}
	}
	switch {
	case best == nil && wanted:
		s.fn++
	case best != nil && !wanted:
		s.fp++
		if best.Confidence >= 0.85 {
			s.autoFP++
			s.unsafeAutomatic = append(s.unsafeAutomatic, label)
		}
	case best != nil:
		start, end := float64(best.StartUS)/1e6, float64(best.EndUS)/1e6
		overlap := math.Min(end, want.end) - math.Max(start, want.start)
		union := math.Max(end, want.end) - math.Min(start, want.start)
		if overlap/union < 0.5 {
			s.fp++
			s.fn++
			if best.Confidence >= 0.85 {
				s.autoFP++
				s.unsafeAutomatic = append(s.unsafeAutomatic, label)
			}
			return
		}
		s.tp++
		s.startErr = append(s.startErr, math.Abs(start-want.start))
		s.endErr = append(s.endErr, math.Abs(end-want.end))
	}
}

func (s *score) report(t *testing.T) (precision, recall, meanStart, meanEnd float64) {
	precision = float64(s.tp) / math.Max(1, float64(s.tp+s.fp))
	recall = float64(s.tp) / math.Max(1, float64(s.tp+s.fn))
	mean := func(v []float64) float64 {
		total := 0.0
		for _, x := range v {
			total += x
		}
		return total / math.Max(1, float64(len(v)))
	}
	worst := func(v []float64) float64 {
		w := 0.0
		for _, x := range v {
			w = math.Max(w, x)
		}
		return w
	}
	meanStart, meanEnd = mean(s.startErr), mean(s.endErr)
	t.Logf("%-8s precision %.3f recall %.3f (tp %d fp %d fn %d) boundary error start mean %.2fs max %.2fs, end mean %.2fs max %.2fs; wrong automatic %d",
		s.kind, precision, recall, s.tp, s.fp, s.fn, meanStart, worst(s.startErr), meanEnd, worst(s.endErr), s.autoFP)
	return
}

// evaluateShow measures every episode, then runs the comparison exactly as the
// worker does: neighbours chosen by segmentNeighbours, then Intro and Credits.
func (h *harness) evaluateShow(episodes []harnessEpisode, intro, credits *score) {
	ctx := context.Background()
	evidence := make([]segmentdetect.Evidence, len(episodes))
	files := make([]showFile, len(episodes))
	started := time.Now()
	for i, e := range episodes {
		file, duration := h.episodeFile(e)
		plan := planSegments("episode", SegmentModeStandard, duration, true, true)
		var err error
		evidence[i], err = extractSegmentEvidence(ctx, h.decode(file), "harness", plan, duration, decoder.PreviewVideoFacts{})
		if err != nil {
			h.t.Fatalf("%s: %v", e.name, err)
		}
		files[i] = showFile{ObjectID: e.name, ResultID: e.name, Episode: int64(i + 1), Season: e.season, Number: e.number}
	}
	h.t.Logf("measured %d episodes in %v", len(episodes), time.Since(started).Round(time.Millisecond))
	for i, e := range episodes {
		var openings, closings []segmentdetect.Fingerprint
		for _, n := range segmentNeighbours(files, i, segmentNeighbourCount) {
			openings = append(openings, evidence[n].Opening)
			closings = append(closings, evidence[n].Closing)
		}
		var got []segmentdetect.Detection
		if d, ok := segmentdetect.Intro(evidence[i].Opening, segmentdetect.MatchRegion(evidence[i].Opening, openings), evidence[i].DurationUS); ok {
			got = append(got, d)
		}
		if d, ok := segmentdetect.Credits(evidence[i].Closing, segmentdetect.MatchRegion(evidence[i].Closing, closings), evidence[i].DurationUS); ok {
			got = append(got, d)
			if e.scene != d.FollowedByScene {
				h.t.Errorf("%s: post-credits scene %v, detected %v", e.name, e.scene, d.FollowedByScene)
			}
		}
		if os.Getenv("SEGMENT_DEBUG") != "" {
			h.t.Logf("%s truth intro %+v credits %+v got %+v", e.name, e.intro, e.credits, got)
			for k, m := range segmentdetect.MatchRegion(evidence[i].Opening, openings) {
				h.t.Logf("  pair %d: %+v", k, m)
			}
		}
		intro.add(e.name, e.intro, got)
		credits.add(e.name, e.credits, got)
	}
}

func harnessShows(full bool) [][]harnessEpisode {
	themeA, endA := segmentsynth.Theme(11), segmentsynth.Theme(12)
	var showA []harnessEpisode
	// Season 1: cold opens of different lengths, one shortened intro, one
	// with dialogue over the intro, some post-credits scenes.
	s1 := []struct {
		cold, introLen, body, post float64
		overlay                    bool
	}{{0, 50, 110, 0, false}, {75, 50, 100, 20, false}, {130, 50, 200, 0, false}, {32, 30, 105, 15, false}, {8, 50, 115, 0, true}, {60, 50, 100, 0, false}}
	if !full {
		s1 = s1[:3]
	}
	for i, c := range s1 {
		showA = append(showA, buildEpisode(fmt.Sprintf("a-s1e%d", i+1), 1, i+1, uint64(100+i), c.cold, themeA, c.introLen, c.body, endA, 40, c.post, c.overlay))
	}
	shows := [][]harnessEpisode{showA}
	if !full {
		return shows
	}
	// Season 2 changes the intro; season 3 has one episode and shares it, so
	// it is confirmed from season 2. A special has no intro at all.
	themeB := segmentsynth.Theme(21)
	for i := range 3 {
		showA = append(showA, buildEpisode(fmt.Sprintf("a-s2e%d", i+1), 2, i+1, uint64(200+i), float64(20+i*40), themeB, 45, 110, endA, 40, 0, false))
	}
	showA = append(showA, buildEpisode("a-s3e1", 3, 1, 300, 50, themeB, 45, 120, endA, 40, 0, false))
	showA = append(showA, buildEpisode("a-s0e1", 0, 1, 400, 0, nil, 0, 200, nil, 0, 0, false))
	// A show with no intro and no shared credits.
	var showB []harnessEpisode
	for i := range 4 {
		showB = append(showB, buildEpisode(fmt.Sprintf("b-s1e%d", i+1), 1, i+1, uint64(500+i), 30, nil, 0, 180, nil, 0, 0, false))
	}
	return [][]harnessEpisode{showA, showB}
}

func TestSegmentAccuracyHarness(t *testing.T) {
	full := os.Getenv("PORTICO_SEGMENT_ACCURACY") != ""
	h := newHarness(t)
	intro, credits := &score{kind: "intro"}, &score{kind: "credits"}
	for _, show := range harnessShows(full) {
		// The worker keeps episodes in season and number order.
		sort.SliceStable(show, func(i, j int) bool {
			if show[i].season != show[j].season {
				return show[i].season < show[j].season
			}
			return show[i].number < show[j].number
		})
		h.evaluateShow(show, intro, credits)
	}
	for _, s := range []*score{intro, credits} {
		precision, recall, start, end := s.report(t)
		if precision < 0.95 || recall < 0.9 || start > 1 || end > 1.5 || s.autoFP > 0 {
			t.Errorf("%s accuracy below the bar (wrong automatic: %v)", s.kind, s.unsafeAutomatic)
		}
	}
	if full {
		h.evaluateFilm(t)
	}
}

// evaluateFilm generates a film whose credits crawl over black, with a
// mid-credits scene and a post-credits scene, and reads it in thorough mode.
func (h *harness) evaluateFilm(t *testing.T) {
	const filmEnd, credits1, mid, credits2, black, post = 1500.0, 80.0, 30.0, 100.0, 3.0, 25.0
	duration := filmEnd + credits1 + mid + credits2 + black + post + 3
	// Lines of type: three-pixel strokes every seven, ten rows in twenty-six,
	// rolling up forty rows a second. Measured like this, real crawls have one
	// to seven sharp edges per lit pixel; a block pattern would have far fewer.
	crawl := "geq=lum='if(between(X,W/4,3*W/4)*lt(mod(Y+T*40,26),10)*lt(mod(X+floor((Y+T*40)/26)*3,7),3),235,16)':cb=128:cr=128"
	parts := []string{
		fmt.Sprintf("testsrc2=size=640x360:rate=4:duration=%v[a]", filmEnd),
		fmt.Sprintf("color=black:size=640x360:rate=4:duration=%v,format=yuv420p,%s[b]", credits1, crawl),
		fmt.Sprintf("testsrc2=size=640x360:rate=4:duration=%v[c]", mid),
		fmt.Sprintf("color=black:size=640x360:rate=4:duration=%v,format=yuv420p,%s[d]", credits2, crawl),
		fmt.Sprintf("color=black:size=640x360:rate=4:duration=%v,format=yuv420p[e]", black),
		fmt.Sprintf("testsrc2=size=640x360:rate=4:duration=%v[f]", post),
		"color=black:size=640x360:rate=4:duration=3,format=yuv420p[g]",
		"[a][b][c][d][e][f][g]concat=n=7:v=1:a=0,format=yuv420p[v]",
	}
	file := "film.mp4"
	h.run("-filter_complex", strings.Join(parts, ";"), "-f", "lavfi", "-i", fmt.Sprintf("anoisesrc=d=%v:c=pink:a=0.05:r=48000", duration),
		"-map", "[v]", "-map", "0:a", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30", "-g", "20", "-c:a", "aac", "-b:a", "64k", "-shortest", filepath.Join(h.dir, file))
	durationUS := int64(duration * 1e6)
	plan := planSegments("movie", SegmentModeThorough, durationUS, true, true)
	started := time.Now()
	e, err := extractSegmentEvidence(context.Background(), h.decode(file), "film", plan, durationUS, decoder.PreviewVideoFacts{})
	if err != nil {
		t.Fatal(err)
	}
	got := fileSegments(e, nil)
	t.Logf("film: read %d frames in %v: %+v", len(e.Frames), time.Since(started).Round(time.Millisecond), got)
	if len(got) != 2 {
		t.Fatalf("film credits %+v", got)
	}
	check := func(label string, gotUS int64, want float64) {
		if d := math.Abs(float64(gotUS)/1e6 - want); d > 2.5 {
			t.Errorf("%s at %.1fs, want %.1fs", label, float64(gotUS)/1e6, want)
		}
	}
	check("credits start", got[0].StartUS, filmEnd)
	check("mid-credits scene", got[0].EndUS, filmEnd+credits1)
	check("crawl start", got[1].StartUS, filmEnd+credits1+mid)
	check("post-credits scene", got[1].EndUS, filmEnd+credits1+mid+credits2+black)
	if !got[0].FollowedByScene || !got[1].FollowedByScene {
		t.Errorf("a scene was swallowed into the credits: %+v", got)
	}
	for _, d := range got {
		if segmentdetect.VisualCreditsProvenance != d.Provenance || d.Confidence >= 0.85 {
			t.Errorf("a single film's picture claimed automatic confidence: %+v", d)
		}
	}
}

// TestSegmentDemoMedia writes a synthetic show and film for trying detection
// end to end (a server library, the review page, the players):
//
//	PORTICO_SEGMENT_MEDIA_OUT=/some/folder go test ./internal/mediaanalysis -run SegmentDemoMedia -v
//
// Season 1 has four eight-minute episodes sharing an intro (cold opens of
// different lengths, colour bars on screen while it plays) and end credits
// (a crawl on black), two of them with a scene after the credits; the film
// ends with a crawl, a mid-credits scene, more crawl and a post-credits scene.
func TestSegmentDemoMedia(t *testing.T) {
	out := os.Getenv("PORTICO_SEGMENT_MEDIA_OUT")
	if out == "" {
		t.Skip("set PORTICO_SEGMENT_MEDIA_OUT to write the demo media")
	}
	h := newHarness(t)
	crawl := "geq=lum='if(between(X,W/4,3*W/4)*lt(mod(Y+T*40,26),10)*lt(mod(X+floor((Y+T*40)/26)*3,7),3),235,16)':cb=128:cr=128"
	type piece struct {
		video  string
		length float64
	}
	write := func(file string, audio segmentsynth.Programme, pieces []piece) {
		pcm := filepath.Join(h.dir, filepath.Base(file)+".pcm")
		if err := os.WriteFile(pcm, audio.Render(16000), 0o600); err != nil {
			t.Fatal(err)
		}
		var graph []string
		labels := ""
		for i, p := range pieces {
			source, filter, _ := strings.Cut(p.video, ",")
			join := "="
			if strings.Contains(source, "=") {
				join = ":"
			}
			if filter != "" {
				filter = "," + filter
			}
			graph = append(graph, fmt.Sprintf("%s%ssize=640x360:rate=24:duration=%.3f,format=yuv420p%s[v%d]", source, join, p.length, filter, i))
			labels += fmt.Sprintf("[v%d]", i)
		}
		graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[v]", labels, len(pieces)))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		h.run("-filter_complex", strings.Join(graph, ";"), "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", pcm,
			"-map", "[v]", "-map", "0:a", "-c:v", "libx264", "-preset", "veryfast", "-crf", "30", "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000", "-shortest", file)
		_ = os.Remove(pcm)
	}
	intro, credits := segmentsynth.Theme(31), segmentsynth.Theme(32)
	show := filepath.Join(out, "TV", "Synthetic Show (2026)", "Season 01")
	for i, c := range []struct{ cold, post float64 }{{0, 0}, {65, 25}, {120, 0}, {30, 20}} {
		seed := uint64(700 + i*10)
		body := 480 - c.cold - 46 - 40 - c.post - 2
		audio := segmentsynth.Programme{{Signal: segmentsynth.Speech(seed), Length: c.cold}, {Signal: intro, Length: 45}, {Signal: segmentsynth.Silence(), Length: 1}, {Signal: segmentsynth.Speech(seed + 1), Length: body}, {Signal: credits, Length: 40}}
		pieces := []piece{{"testsrc2", c.cold}, {"smptebars", 46}, {"testsrc2", body}, {"color=c=black," + crawl, 40}}
		if c.post > 0 {
			audio = append(audio, segmentsynth.Part{Signal: segmentsynth.Speech(seed + 2), Length: c.post})
			pieces = append(pieces, piece{"testsrc2", c.post})
		}
		audio = append(audio, segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 2})
		pieces = append(pieces, piece{"color=c=black", 2})
		var kept []piece
		for _, p := range pieces {
			if p.length > 0 {
				kept = append(kept, p)
			}
		}
		write(filepath.Join(show, fmt.Sprintf("Synthetic Show (2026) - S01E%02d.mkv", i+1)), audio, kept)
	}
	film := segmentsynth.Programme{{Signal: segmentsynth.Speech(900), Length: 900}, {Signal: credits, Length: 213}, {Signal: segmentsynth.Silence(), Length: 3}}
	write(filepath.Join(out, "Movies", "Synthetic Film (2026)", "Synthetic Film (2026).mkv"), film, []piece{{"testsrc2", 900}, {"color=c=black," + crawl, 80}, {"testsrc2", 30}, {"color=c=black," + crawl, 100}, {"color=c=black", 3}, {"testsrc2", 25}, {"color=c=black", 3}})
	t.Logf("wrote the demo show to %s and the film to %s", filepath.Join(out, "TV"), filepath.Join(out, "Movies"))
}
