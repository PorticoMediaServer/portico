package mediaengine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A book in several files, scanned from a library and played over HTTP: what the listener is
// given, from the start and from a place inside its second part. Rule 60 of playback: a book
// in several files plays as one book, with one scrubber and one time left.
func TestAMultiFileAudiobookPlaysAsOneBook(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Audiobooks")
	book := filepath.Join(library, "Ann Author", "Three Parts")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three parts of 20 s, each its own tone, so where the listener is can be heard.
	for part, tone := range []int{300, 600, 900} {
		file := filepath.Join(book, fmt.Sprintf("%02d.mp3", part+1))
		if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", "sine=frequency="+strconv.Itoa(tone)+":sample_rate=44100:duration=20", "-ac", "2", "-c:a", "libmp3lame", "-b:a", "128k",
			"-metadata", "album=Three Parts", "-metadata", "artist=Ann Author", "-metadata", "track="+strconv.Itoa(part+1), file).CombinedOutput(); err != nil {
			t.Fatalf("making part %d: %v\n%s", part+1, err, out)
		}
	}
	lib, err := d.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=audiobook", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	item := page.Items[0].ID
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+item+"/playback-options", nil, &options, 200)
	if d := options.Versions[0].DurationMs; d < 59_000 || d > 61_000 {
		t.Fatalf("the book's version lasts %d ms, want its three parts' 60 s", d)
	}

	for _, start := range []int64{0, 30_000} {
		var session api.PlaybackSession
		h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item, StartPositionMs: &start}, &session, 201)
		p := session.Presentation
		if !strings.HasSuffix(p.URL, ".m3u8") || p.StartPositionMs != start {
			t.Fatalf("start at %d ms: %+v", start, p)
		}
		// MP3 plays in a browser's HLS as it is: the parts are joined, not converted.
		if p.Route == "converted" {
			t.Errorf("start at %d ms: the book was converted to be joined: %+v", start, *p.Decision.Audio)
		}
		h.segmentTone(t, p.URL, ffmpeg, int(start/6000), 300*(1+int(start/20_000)))
		played := h.listen(t, p.URL, ffmpeg, ffprobe)
		if played.seconds < 59 || played.seconds > 61 {
			t.Fatalf("start at %d ms: what plays lasts %.1f s, want the whole book's 60 s", start, played.seconds)
		}
		// A place in the stream is a place in the whole book: 30 s is ten seconds into part
		// two, 41 s one second into part three.
		for at, tone := range map[float64]int{0.5: 300, 30: 600, 41: 900} {
			if got := toneAt(t, ffmpeg, played.file, at); got != tone {
				t.Errorf("start at %d ms: %.1f s into the book plays %d Hz, want part %d's %d Hz", start, at, got, tone/300, tone)
			}
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}

	// The place is whole-book time too: a listener 45 s in (five seconds into part three)
	// reports it, stops, and comes back to the same place in part three, less the rewind
	// when resuming (rule 61). A seek through the session is a place in the whole book.
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
	h.call("PUT", "/v1/playback/sessions/"+session.ID+"/progress", api.PlaybackProgress{State: "playing", PositionMs: 45_000}, nil, 200)
	var sought api.PlaybackSession
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{Seek: &api.PlaybackSeek{PositionMs: 41_000, ID: "seek-1"}}, &sought, 200)
	if sought.Presentation.URL != session.Presentation.URL || sought.Presentation.SeekID != "seek-1" {
		t.Errorf("a seek inside the book changed the stream or was not taken: %+v", sought.Presentation)
	}
	stop := int64(45_000)
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, api.PlaybackStop{PositionMs: &stop}, nil, 204)
	var resumed api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &resumed, 201)
	at := resumed.Presentation.StartPositionMs
	if at < 15_000 || at > 45_000 {
		t.Fatalf("a book left 45 s in resumes at %d ms", at)
	}
	want := 300 * (1 + int(at/20_000))
	// The player asks first for the segment holding its place, made by the converter started
	// there and by no other: that segment alone is the right part of the book.
	h.segmentTone(t, resumed.Presentation.URL, ffmpeg, int(at/6000), want)
	played := h.listen(t, resumed.Presentation.URL, ffmpeg, ffprobe)
	if got := toneAt(t, ffmpeg, played.file, float64(at)/1000+0.2); got != want {
		t.Errorf("resumed at %d ms the book plays %d Hz, want part %d's %d Hz", at, got, want/300, want)
	}
	t.Logf("left at 45 s, the book resumes at %d ms, in part %d", at, want/300)
	h.call("DELETE", "/v1/playback/sessions/"+resumed.ID, nil, nil, 204)
}

// segmentTone fetches one segment of an HLS playlist before any other and checks the tone
// it begins with.
func (h *house) segmentTone(t *testing.T, url, ffmpeg string, index, want int) {
	t.Helper()
	code, _, body := h.media(url)
	if code != 200 {
		t.Fatalf("%s answered %d", url, code)
	}
	// A master playlist names the playlist the sound is in.
	if strings.Contains(string(body), "#EXT-X-STREAM-INF") || strings.Contains(string(body), "#EXT-X-MEDIA:TYPE=AUDIO") {
		base := url[:strings.LastIndex(url, "/")+1]
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=AUDIO") && strings.Contains(line, `URI="`) {
				uri := line[strings.Index(line, `URI="`)+5:]
				h.segmentTone(t, base+uri[:strings.Index(uri, `"`)], ffmpeg, index, want)
				return
			}
			if strings.HasPrefix(line, "#EXT-X-STREAM-INF") && i+1 < len(lines) {
				h.segmentTone(t, base+strings.TrimSpace(lines[i+1]), ffmpeg, index, want)
				return
			}
		}
	}
	var segments []string
	init := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") {
			init = strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`)
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			segments = append(segments, line)
		}
	}
	if index >= len(segments) {
		t.Fatalf("segment %d of %d", index, len(segments))
	}
	base := url[:strings.LastIndex(url, "/")+1]
	code, _, segment := h.media(base + segments[index])
	if code != 200 {
		t.Fatalf("segment %d answered %d", index, code)
	}
	if init != "" {
		_, _, head := h.media(base + init)
		segment = append(head, segment...)
	}
	file := filepath.Join(t.TempDir(), "segment")
	if err := os.WriteFile(file, segment, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := toneAt(t, ffmpeg, file, 0.5); got != want {
		t.Errorf("segment %d, the first fetched, plays %d Hz, want %d Hz", index, got, want)
	}
}

// heard is what a stream sounds like: how long it lasts and the tone it starts with.
type heard struct {
	seconds float64
	tone    int
	file    string
}

// listen fetches a stream as a player would (the file itself, or every segment of an HLS
// playlist joined) and says how long it lasts and which tone it begins with.
func (h *house) listen(t *testing.T, url, ffmpeg, ffprobe string) heard {
	t.Helper()
	var joined []byte
	if strings.HasSuffix(url, ".m3u8") {
		joined = h.joinPlaylist(t, url)
	} else {
		code, _, body := h.media(url)
		if code != 200 {
			t.Fatalf("%s answered %d", url, code)
		}
		joined = body
	}
	file := filepath.Join(t.TempDir(), "heard")
	if err := os.WriteFile(file, joined, 0o600); err != nil {
		t.Fatal(err)
	}
	// From the packets: the stream's segments are cut by separate converter runs, so the
	// file's own estimate of its length is not to be trusted, but every packet's time is.
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("probing what plays: %v", err)
	}
	first, end := -1.0, 0.0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		at, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		length := 0.0
		if len(fields) > 1 {
			length, _ = strconv.ParseFloat(fields[1], 64)
		}
		if first < 0 || at < first {
			first = at
		}
		end = max(end, at+length)
	}
	seconds := end - max(first, 0)
	return heard{seconds: seconds, tone: toneAt(t, ffmpeg, file, 0), file: file}
}

// toneAt is the frequency of the second of a sound from at seconds in, to the nearest of the
// parts' tones.
func toneAt(t *testing.T, ffmpeg, file string, at float64) int {
	t.Helper()
	raw, err := exec.Command(ffmpeg, "-v", "error", "-i", file, "-ss", strconv.FormatFloat(at, 'f', 3, 64), "-t", "1", "-ac", "1", "-ar", "8000",
		"-f", "s16le", "-").Output()
	if err != nil || len(raw) < 1000 {
		t.Fatalf("decoding what plays: %v", err)
	}
	// Zero crossings over the second give the frequency.
	crossings, previous := 0, int16(0)
	for i := 0; i+1 < len(raw); i += 2 {
		v := int16(raw[i]) | int16(raw[i+1])<<8
		if previous < 0 && v >= 0 {
			crossings++
		}
		previous = v
	}
	hz := crossings * 8000 / (len(raw) / 2)
	best := 300
	for _, tone := range []int{300, 600, 900} {
		if abs(hz-tone) < abs(hz-best) {
			best = tone
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// joinPlaylist follows an HLS playlist (and an audio or media playlist a master names) and
// joins every segment it lists, after its init file when it has one.
func (h *house) joinPlaylist(t *testing.T, url string) []byte {
	t.Helper()
	code, _, body := h.media(url)
	if code != 200 {
		t.Fatalf("%s answered %d: %s", url, code, body)
	}
	base := url[:strings.LastIndex(url, "/")+1]
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		// A master playlist: follow its audio rendition, else its first media playlist.
		if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=AUDIO") && strings.Contains(line, `URI="`) {
			uri := line[strings.Index(line, `URI="`)+5:]
			return h.joinPlaylist(t, base+uri[:strings.Index(uri, `"`)])
		}
	}
	var joined []byte
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") && i+1 < len(lines) {
			return h.joinPlaylist(t, base+strings.TrimSpace(lines[i+1]))
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") {
			name := strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`)
			_, _, init := h.media(base + name)
			joined = append(joined, init...)
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			code, _, segment := h.media(base + line)
			if code != 200 {
				t.Fatalf("%s answered %d", line, code)
			}
			joined = append(joined, segment...)
		}
	}
	return joined
}

// A book whose parts are not encoded alike (here the first part MP3, the second AAC) cannot be
// copied into one stream: it is converted to one, and plays through both parts, from its start
// and from a place inside its second part.
func TestABookInPartsEncodedDifferentlyPlaysThroughBoth(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Audiobooks")
	book := filepath.Join(library, "Ann Author", "Two Kinds")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	for part, kind := range []struct {
		name  string
		tone  int
		codec []string
	}{{"01.mp3", 300, []string{"-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100"}}, {"02.m4a", 600, []string{"-c:a", "aac", "-b:a", "128k", "-ar", "48000"}}} {
		args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", "sine=frequency=" + strconv.Itoa(kind.tone) + ":duration=20", "-ac", "2"}, kind.codec...)
		args = append(args, "-metadata", "album=Two Kinds", "-metadata", "artist=Ann Author", "-metadata", "track="+strconv.Itoa(part+1),
			filepath.Join(book, kind.name))
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("making part %d: %v\n%s", part+1, err, out)
		}
	}
	lib, err := d.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=audiobook", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	for _, start := range []int64{0, 25_000} {
		var session api.PlaybackSession
		h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, StartPositionMs: &start}, &session, 201)
		p := session.Presentation
		if !strings.HasSuffix(p.URL, ".m3u8") || p.StartPositionMs != start || p.Route != "converted" {
			t.Fatalf("start at %d ms: a book whose parts differ should be converted into one stream: %+v", start, p)
		}
		h.segmentTone(t, p.URL, ffmpeg, int(start/6000), 300*(1+int(start/20_000)))
		played := h.listen(t, p.URL, ffmpeg, ffprobe)
		if played.seconds < 39 || played.seconds > 41 {
			t.Fatalf("start at %d ms: what plays lasts %.1f s, want both parts' 40 s", start, played.seconds)
		}
		for at, tone := range map[float64]int{0.5: 300, 19: 300, 21: 600, 30: 600, 38.5: 600} {
			if got := toneAt(t, ffmpeg, played.file, at); got != tone {
				t.Errorf("start at %d ms: %.1f s into the book plays %d Hz, want %d Hz", start, at, got, tone)
			}
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}
}
