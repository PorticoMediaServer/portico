package mediaengine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The two files really delivered over HTTP must contain both pictures. Their
// uneven lengths and four-second keyframes keep seeks off the six-second grid.
func TestASplitVideoPlaysBothParts(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	library := filepath.Join(t.TempDir(), "Films")
	name := "Split Film (2026)"
	folder := filepath.Join(library, name)
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for n, color := range []string{"red", "blue"} {
		file := filepath.Join(folder, fmt.Sprintf("%s - pt%d.mp4", name, n+1))
		duration := []string{"13", "11"}[n]
		out, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color="+color+":s=640x360:r=24",
			"-f", "lavfi", "-i", "sine=frequency="+[]string{"300", "900"}[n]+":sample_rate=48000", "-t", duration, "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2",
			"-g", "96", "-c:a", "aac", "-ac", "2", file).CombinedOutput()
		if err != nil {
			t.Fatalf("making part: %v: %s", err, out)
		}
	}
	ctx := context.Background()
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("scanned %d films", len(page.Items))
	}
	item := page.Items[0].ID
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+item+"/playback-options", nil, &options, 200)
	if len(options.Versions) != 1 || options.Versions[0].DurationMs != 24000 {
		t.Fatal("the split version must last 24 seconds")
	}

	play := func(profile string, converted bool, start int64) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "streaming", "profiles", profile+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var capability struct {
			Document json.RawMessage `json:"document"`
		}
		if err := json.Unmarshal(raw, &capability); err != nil {
			t.Fatal(err)
		}
		h.call("PUT", "/v1/devices/"+h.device+"/capabilities", capability.Document, nil, 204)
		request := api.PlaybackStart{ItemID: item, StartPositionMs: &start}
		if converted {
			request.Quality = &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}
		}
		response := h.do("POST", "/v1/playback/sessions", request)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 201 && response.StatusCode != 202 {
			t.Fatalf("play start status %d", response.StatusCode)
		}
		var session api.PlaybackSession
		if err := json.Unmarshal(body, &session); err != nil {
			t.Fatal(err)
		}
		p := session.Presentation
		want := "repackaged"
		if converted {
			want = "converted"
		}
		if p.Route != want {
			t.Fatalf("%s route=%s, want %s", profile, p.Route, want)
		}
		if !converted && (p.Decision.Video.Action != "copy" || p.Decision.Audio.Action != "copy") {
			t.Fatal("joining compatible files changed picture or sound")
		}
		if start == 0 {
			played := h.listen(t, p.URL, ffmpeg, ffprobe)
			if played.seconds < 23.7 || played.seconds > 24.4 {
				t.Fatalf("%s delivered %.3f seconds, want both parts' 24 seconds", profile, played.seconds)
			}
			assertVideoColour(t, ffmpeg, played.file, 3, false)
			assertVideoColour(t, ffmpeg, played.file, 18, true)
			if got := toneAt(t, ffmpeg, played.file, 18); got != 900 {
				t.Fatalf("second part sound=%dHz, want 900Hz", got)
			}
		} else {
			chunk, from, length := h.videoAt(t, p.URL, float64(start)/1000)
			file := filepath.Join(t.TempDir(), "seek")
			if err := os.WriteFile(file, chunk, 0600); err != nil {
				t.Fatal(err)
			}
			assertVideoColour(t, ffmpeg, file, 0.5, true)
			// Seeing blue alone misses a replayed GOP at a cold seek: the
			// closing segment must cover exactly its advertised remaining time.
			out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", file).Output()
			if err != nil {
				t.Fatal(err)
			}
			first, end := math.Inf(1), math.Inf(-1)
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Split(line, ",")
				if len(fields) < 2 {
					continue
				}
				at, err := strconv.ParseFloat(fields[0], 64)
				if err != nil {
					t.Fatal(err)
				}
				span, _ := strconv.ParseFloat(fields[1], 64)
				first, end = min(first, at), max(end, at+span)
			}
			// Two frames of encoder/demuxer rounding, never a replayed GOP.
			t.Logf("%s cold seek: video starts %.6f s (playlist %.6f), lasts %.6f s (playlist %.6f)", profile, first, from, end-first, length)
			if math.Abs(first-from) > 0.1 || math.Abs(end-first-length) > 0.1 {
				t.Fatalf("cold seek covers %.3f..%.3f, playlist promises %.3f..%.3f", first, end, from, from+length)
			}
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}
	// The three apps receive the same complete HLS title, keeping compatible
	// codecs; a lower quality uses the same parts through conversion.
	for _, profile := range []string{"chromium", "android", "androidtv"} {
		play(profile, false, 0)
	}
	play("androidtv", false, 18000)
	play("chromium", true, 0)
	play("android", true, 18000)
	// Episodes use the same version shape, rather than treating the second
	// file as the next episode in a queue.
	shows := filepath.Join(t.TempDir(), "Shows")
	episode := filepath.Join(shows, "Split Show (2026)", "Season 01")
	if err := os.MkdirAll(episode, 0700); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		source := filepath.Join(folder, fmt.Sprintf("%s - pt%d.mp4", name, n))
		target := filepath.Join(episode, fmt.Sprintf("Split Show - S01E01 - pt%d.mp4", n))
		if err := os.Link(source, target); err != nil {
			t.Fatal(err)
		}
	}
	showLib, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{shows})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, showLib); err != nil {
		t.Fatal(err)
	}
	h.call("GET", "/v1/items?library="+showLib.PublicID+"&kind=episode", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("scanned %d episodes", len(page.Items))
	}
	item = page.Items[0].ID
	play("chromium", false, 18000)
}

// Descriptive stream titles do not prevent copying. A genuinely different
// layout retains main's playable first file until mixed video joining lands.
func TestSplitVideoTitlesJoinAndDifferentLayoutsKeepPlaying(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	library := filepath.Join(t.TempDir(), "Films")
	folder := filepath.Join(library, "Named Film (2026)")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	var second string
	for n, colour := range []string{"red", "blue"} {
		file := filepath.Join(folder, fmt.Sprintf("Named Film (2026) - pt%d.mkv", n+1))
		title := fmt.Sprintf("Part %d sound", n+1)
		out, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color="+colour+":s=320x180:r=24",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2",
			"-g", "48", "-c:a", "aac", "-ac", "2", "-metadata:s:a:0", "title="+title, file).CombinedOutput()
		if err != nil {
			t.Fatalf("making named part: %v: %s", err, out)
		}
		out, err = exec.Command(ffprobe, "-v", "error", "-show_entries", "stream_tags=title", "-of", "json", file).Output()
		if err != nil || !strings.Contains(string(out), title) {
			t.Fatalf("fixture must carry its different stream title: %v: %s", err, out)
		}
		second = file
	}
	ctx := context.Background()
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	for _, differentLayout := range []bool{false, true} {
		if differentLayout {
			// Remove the second part's sound, leaving the first part untouched.
			silent := filepath.Join(t.TempDir(), "silent.mkv")
			if out, err := exec.Command(ffmpeg, "-v", "error", "-i", second, "-map", "0:v:0", "-c", "copy", silent).CombinedOutput(); err != nil {
				t.Fatalf("removing second sound: %v: %s", err, out)
			}
			if err := os.Rename(silent, second); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
			t.Fatal(err)
		}
		var page api.ItemPage
		h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
		if len(page.Items) != 1 {
			t.Fatalf("scanned %d films", len(page.Items))
		}
		response := h.do("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID})
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 201 && response.StatusCode != 202 {
			t.Fatalf("play start status %d", response.StatusCode)
		}
		var session api.PlaybackSession
		if err := json.Unmarshal(body, &session); err != nil {
			t.Fatal(err)
		}
		played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
		want := 16.0
		if differentLayout {
			want = 8
		}
		if math.Abs(played.seconds-want) > 0.4 {
			t.Fatalf("different layout=%v: delivered %.3f seconds, want %.0f", differentLayout, played.seconds, want)
		}
		assertVideoColour(t, ffmpeg, played.file, 2, false)
		if !differentLayout {
			if session.Presentation.Decision.Video.Action != "copy" || session.Presentation.Decision.Audio.Action != "copy" {
				t.Fatal("different titles should still copy picture and sound")
			}
			assertVideoColour(t, ffmpeg, played.file, 10, true)
		}
		t.Logf("different layout=%v: route=%s, delivered %.6f s", differentLayout, session.Presentation.Route, played.seconds)
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}
}

func assertVideoColour(t *testing.T, ffmpeg, file string, at float64, blue bool) {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-v", "error", "-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", file,
		"-frames:v", "1", "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "-").Output()
	if err != nil || len(out) != 3 {
		t.Fatalf("decoding picture at %.3f: %v (%d bytes)", at, err, len(out))
	}
	if blue && (out[2] < 200 || out[0] > 40) || !blue && (out[0] < 200 || out[2] > 40) {
		t.Fatalf("at %.3f: RGB=%v, blue=%v", at, out, blue)
	}
}

// Fetch the segment a player first asks for after a cold seek, including the
// initialization for fragmented MP4. Do not fetch earlier segments first.
func (h *house) videoAt(t *testing.T, url string, position float64) ([]byte, float64, float64) {
	t.Helper()
	status, _, body := h.media(url)
	if status != 200 {
		t.Fatalf("video playlist status %d", status)
	}
	base := url[:strings.LastIndex(url, "/")+1]
	lines := strings.Split(string(body), "\n")
	for n, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			return h.videoAt(t, base+strings.TrimSpace(lines[n+1]), position)
		}
	}
	var init []byte
	for _, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") {
			name := strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`)
			status, _, init = h.media(base + name)
			if status != 200 {
				t.Fatalf("video initialization status %d", status)
			}
		}
	}
	at := 0.0
	for n, line := range lines {
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		length, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
		if err != nil {
			t.Fatal(err)
		}
		if position < at+length {
			status, _, segment := h.media(base + strings.TrimSpace(lines[n+1]))
			if status != 200 {
				t.Fatalf("video segment status %d", status)
			}
			return append(init, segment...), at, length
		}
		at += length
	}
	t.Fatalf("seek %.3f lies outside %.3f seconds", position, at)
	return nil, 0, 0
}
