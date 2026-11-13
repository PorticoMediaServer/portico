package mediaengine_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The copied packet clock puts the first batch's end at 60 s inside a part.
const partSeconds = 2.7
const partPacketSeconds = 105.0 * 1152 / 44100

// Each part in a batch has its own tone, so seeks cannot silently play an earlier part.
func partTone(n int) int { return 300 + 30*(n%24) }

// manyPartBooks writes books of the given numbers of parts into one library, scans it and
// returns the books' ids in that order. Each part is 2.7 s of its tone.
func manyPartBooks(t *testing.T, h *house, d *db.DB, ffmpeg string, counts ...int) []string {
	t.Helper()
	if _, err := mediaexec.Configure(mediaexec.Options{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tones := t.TempDir()
	for n := range 24 {
		if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", fmt.Sprintf("sine=frequency=%d:sample_rate=44100:duration=%g", partTone(n), partSeconds), "-ac", "2", "-c:a", "libmp3lame", "-b:a", "64k",
			filepath.Join(tones, fmt.Sprintf("%02d.mp3", n))).CombinedOutput(); err != nil {
			t.Fatalf("making a tone: %v\n%s", err, out)
		}
	}
	library := filepath.Join(t.TempDir(), "Audiobooks")
	for b, count := range counts {
		folder := filepath.Join(library, "Ann Author", fmt.Sprintf("Book %d", b))
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		for n := range count {
			raw, err := os.ReadFile(filepath.Join(tones, fmt.Sprintf("%02d.mp3", n%24)))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("%03d.mp3", n+1)), raw, 0o644); err != nil {
				t.Fatal(err)
			}
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
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=audiobook&limit=50", nil, &page, 200)
	ids := make([]string, len(counts))
	for _, it := range page.Items {
		var b int
		if _, err := fmt.Sscanf(it.Title, "Book %d", &b); err == nil && b < len(ids) {
			ids[b] = it.ID
		}
	}
	for b, id := range ids {
		if id == "" {
			t.Fatalf("book %d of %d parts is not in the library: %+v", b, counts[b], page.Items)
		}
	}
	return ids
}

// playFrom starts a book at a place and checks the segment holding it, fetched first as a
// player does, is the right part's sound.
func playFrom(t *testing.T, h *house, ffmpeg, id string, atMs int64) api.PlaybackSession {
	t.Helper()
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: id, StartPositionMs: &atMs}, &session, 201)
	p := session.Presentation
	if !strings.HasSuffix(p.URL, ".m3u8") || p.StartPositionMs != atMs {
		t.Fatalf("start at %d ms: %+v", atMs, p)
	}
	// The tone half a second into the segment, which holds the part playing there.
	segment := int(atMs / 6000)
	part := int((float64(segment*6) + 0.5) / partPacketSeconds)
	h.segmentTone(t, p.URL, ffmpeg, segment, partTone(part))
	return session
}

// A book of 300 parts plays: from its start (all of it, every part where it belongs), from
// its middle and from inside its last part, alone and four books at once. Every run of its
// converter is handed at most a batch of parts, so the sandbox's limit of 32 files a tool is
// never met, however long the book.
func TestABookOfThreeHundredPartsPlays(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ids := manyPartBooks(t, h, d, ffmpeg, 300, 300, 300, 300)
	whole := 300 * partPacketSeconds
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+ids[0]+"/playback-options", nil, &options, 200)
	if got := options.Versions[0].DurationMs; got < int64(whole*1000)-1 || got > int64(whole*1000)+1 {
		t.Fatalf("the scrubber ends at %d ms, but the copied book lasts %.3f s", got, whole)
	}
	// Alone, from the start: every part, in order, for the whole length.
	session := playFrom(t, h, ffmpeg, ids[0], 0)
	played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
	if played.seconds < whole-1 || played.seconds > whole+1 {
		t.Fatalf("the book plays for %.1f s, want its %.1f s", played.seconds, whole)
	}
	for _, part := range []int{0, 22, 23, 24, 150, 299} {
		at := float64(part)*partPacketSeconds + 1
		if got := toneAt(t, ffmpeg, played.file, at); got != partTone(part) {
			t.Errorf("%.1f s into the book plays %d Hz, want part %d's %d Hz", at, got, part+1, partTone(part))
		}
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	// From the middle and from inside the last part, alone.
	for _, at := range []int64{int64(whole * 500), int64((whole - 1.5) * 1000)} {
		session := playFrom(t, h, ffmpeg, ids[0], at/6000*6000)
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}

	// Four independent devices on the same server. Starts on one device replace
	// its previous session, so sharing the first device would not test four streams.
	clients := []*house{h}
	raw, err := os.ReadFile(filepath.Join("..", "streaming", "profiles", "chromium.json"))
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	for n := 1; n < 4; n++ {
		client := &house{T: t, url: h.url, data: h.data, engine: h.engine}
		var signed api.SignInResponse
		client.call("POST", "/v1/auth/sign-in", api.SignInRequest{Username: "owner", Password: testPlayerPassword,
			Device: api.DeviceInfo{InstallationID: strings.Repeat(strconv.Itoa(n+1), 32), Name: fmt.Sprintf("Player %d", n+1)}}, &signed, 200)
		client.token = signed.Session.AccessToken
		var selected api.Session
		client.call("POST", "/v1/profiles/"+signed.Profiles[0].ID+"/select", api.ProfileSelect{}, &selected, 200)
		client.token, client.device = selected.AccessToken, selected.DeviceID
		client.call("PUT", "/v1/devices/"+selected.DeviceID+"/capabilities", profile.Document, nil, 204)
		clients = append(clients, client)
	}
	sessions := make([]api.PlaybackSession, 4)
	positions := make([]int64, 4)
	for b, client := range clients {
		positions[b] = int64(b) * int64(whole*1000/4) / 6000 * 6000
		client.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: ids[b], StartPositionMs: &positions[b]}, &sessions[b], 201)
	}
	var wg sync.WaitGroup
	for b, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			segment := int(positions[b] / 6000)
			part := int((float64(segment*6) + 0.5) / partPacketSeconds)
			client.segmentTone(t, sessions[b].Presentation.URL, ffmpeg, segment, partTone(part))
		}()
	}
	wg.Wait()
	for b, client := range clients {
		var current api.PlaybackSession
		client.call("GET", "/v1/playback/sessions/"+sessions[b].ID, nil, &current, 200)
		if current.State != "playing" {
			t.Errorf("device %d stopped: %s", b+1, current.State)
		}
		client.call("DELETE", "/v1/playback/sessions/"+sessions[b].ID, nil, nil, 204)
	}

}

// Books of 31, 32 and 33 parts, at the sandbox's limit of 32 files a tool and either side of
// it, play whole.
func TestBooksAtTheFileLimitPlay(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	counts := []int{31, 32, 33}
	for b, id := range manyPartBooks(t, h, d, ffmpeg, counts...) {
		session := playFrom(t, h, ffmpeg, id, 0)
		played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
		whole := float64(counts[b]) * partPacketSeconds
		if played.seconds < whole-1 || played.seconds > whole+1 {
			t.Errorf("a book of %d parts plays for %.1f s, want %.1f s", counts[b], played.seconds, whole)
		}
		last := counts[b] - 1
		if got := toneAt(t, ffmpeg, played.file, float64(last)*partPacketSeconds+1); got != partTone(last) {
			t.Errorf("a book of %d parts: its last part plays %d Hz, want %d Hz", counts[b], got, partTone(last))
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}
	// A seek across the first batch's boundary (60 s) lands in the next batch at the right
	// part: the segment there is fetched first, so the run that makes it starts there.
	ids := manyPartBooks(t, h, d, ffmpeg, 40)
	session := playFrom(t, h, ffmpeg, ids[0], 60_000)
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}

// Nothing is heard twice or lost where one run's batch ends and the next run's begins (60 s,
// inside a part): the stream's timestamps run on without a gap or an overlap there, and its
// sound is sample for sample the book's own. The parts are noise, so no stretch of sound looks
// like another.
func TestABatchJoinIsInaudible(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	folder := filepath.Join(t.TempDir(), "Audiobooks", "Ann Author", "Noise")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	var list strings.Builder
	for n := range 30 {
		file := filepath.Join(folder, fmt.Sprintf("%02d.mp3", n+1))
		if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", fmt.Sprintf("anoisesrc=d=%g:c=white:r=44100:a=0.3:s=%d", partSeconds, n+1), "-ac", "1", "-c:a", "libmp3lame", "-b:a", "128k",
			file).CombinedOutput(); err != nil {
			t.Fatalf("making a part: %v\n%s", err, out)
		}
		fmt.Fprintf(&list, "file '%s'\nduration %.6f\n", file, partPacketSeconds)
	}
	lib, err := d.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{filepath.Dir(filepath.Dir(folder))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=audiobook", nil, &page, 200)
	// Read through the first batch and across its hand-over, as a listener does.
	start := int64(0)
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, StartPositionMs: &start}, &session, 201)
	base := session.Presentation.URL[:strings.LastIndex(session.Presentation.URL, "/")+1]
	segments := h.mediaSegments(t, session.Presentation.URL)
	var joined, before []byte
	// Fetch through the first batch in order, so a far-ahead request cannot
	// replace it with a fresh run starting at segment nine.
	for n := 0; n <= 11; n++ {
		code, _, body := h.media(base + segments[n])
		if code != 200 {
			t.Fatalf("segment %d answered %d", n, code)
		}
		if n >= 9 {
			joined = append(joined, body...)
		}
		if n == 9 {
			before = body
		}
	}
	joins := [][]byte{joined}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	// A restart before the batch edge must cut on the same packet as the run
	// from zero. Starting at 48 s used to shift the 60 s cut by one MP3 frame.
	start = 48_000
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, StartPositionMs: &start}, &session, 201)
	base = session.Presentation.URL[:strings.LastIndex(session.Presentation.URL, "/")+1]
	for _, n := range []int{8, 10} {
		code, _, body := h.media(base + segments[n])
		if code != 200 {
			t.Fatalf("restarted segment %d answered %d", n, code)
		}
		if n == 10 {
			joins = append(joins, append(append([]byte{}, before...), body...))
		}
	}
	for join, joined := range joins {
		t.Run(fmt.Sprintf("join-%d", join), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "join.ts")
			if err := os.WriteFile(file, joined, 0o600); err != nil {
				t.Fatal(err)
			}
			// Timestamps: each packet starts where the one before ends, across the join.
			out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", file).Output()
			if err != nil {
				t.Fatal(err)
			}
			end := -1.0
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fields := strings.Split(line, ",")
				at, _ := strconv.ParseFloat(fields[0], 64)
				length, _ := strconv.ParseFloat(fields[1], 64)
				if end >= 0 && (at-end > 0.001 || end-at > 0.001) {
					t.Errorf("at %.4f s the stream's timestamps jump by %+.4f s", at, at-end)
				}
				end = at + length
			}
			// Sound: the joined segments decoded from 59.5 to 60.5 s are the book's own sound there,
			// the parts decoded one after another.
			book := filepath.Join(t.TempDir(), "book.ffconcat")
			if err := os.WriteFile(book, []byte("ffconcat version 1.0\n"+list.String()), 0o600); err != nil {
				t.Fatal(err)
			}

			reference := filepath.Join(t.TempDir(), "reference.ts")
			if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "concat", "-safe", "0", "-i", book,
				"-c:a", "copy", "-f", "mpegts", "-muxdelay", "0", "-muxpreload", "0", reference).CombinedOutput(); err != nil {
				t.Fatalf("reference copy: %v %s", err, out)
			}
			// Copying keeps encoder padding. Compare against one uninterrupted copied run,
			// at absolute packet timestamps, so a segment's first packet need not land on 54.000 s.
			places := []float64{59.5}
			if join == 0 {
				places = append(places, 65.5) // First normal batch after the cold six-second head.
			}
			for _, at := range places {
				ours := decodeStretch(t, ffmpeg, []string{"-copyts", "-i", file}, at, 1)
				theirs := decodeStretch(t, ffmpeg, []string{"-copyts", "-i", reference}, at, 1)
				shift, worst := bestAlignment(ours, theirs)
				if shift != 0 || worst != 0 {
					t.Errorf("across %.1f s the sound is %d samples off the book's (largest sample difference %d)", at+.5, shift, worst)
				}
			}
		})
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}

// mediaSegments lists the segments of a book's playlist (the sound's own, if the playlist
// names one).
func (h *house) mediaSegments(t *testing.T, url string) []string {
	t.Helper()
	code, _, body := h.media(url)
	if code != 200 {
		t.Fatalf("%s answered %d", url, code)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// decodeStretch decodes seconds of sound from at seconds into what input names, as samples.
func decodeStretch(t *testing.T, ffmpeg string, input []string, at, seconds float64) []int16 {
	t.Helper()
	args := append([]string{"-v", "error", "-nostdin"}, input...)
	args = append(args, "-ss", strconv.FormatFloat(at, 'f', 6, 64), "-t", strconv.FormatFloat(seconds, 'f', 6, 64), "-ac", "1", "-f", "s16le", "-")
	raw, err := exec.Command(ffmpeg, args...).Output()
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return samples
}

// bestAlignment is the shift of ours against theirs, within two frames, that matches best, and
// the largest sample difference there.
func bestAlignment(ours, theirs []int16) (shift, worst int) {
	best := -1
	for s := -2304; s <= 2304; s++ {
		total, most := 0, 0
		for i := 2304; i+2304 < len(ours) && i+2304 < len(theirs); i += 3 {
			j := i + s
			diff := int(ours[i]) - int(theirs[j])
			if diff < 0 {
				diff = -diff
			}
			total += diff
			most = max(most, diff)
		}
		if best < 0 || total < best {
			best, shift, worst = total, s, most
		}
	}
	return shift, worst
}

// Different codecs also stay below the descriptor bound and continue through a batch.
func TestAMixedBookCrossesABatch(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ids := manyPartBooks(t, h, d, ffmpeg, 33)
	libs, err := d.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(libs[0].Paths[0].Path, "Ann Author", "Book 0", "033.mp3")
	// Keep the name, change the actual container and codec. Probing, not the suffix,
	// establishes the part's format.
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i",
		fmt.Sprintf("sine=frequency=%d:sample_rate=48000:duration=%g", partTone(32), partSeconds),
		"-c:a", "aac", "-f", "ipod", path).CombinedOutput(); err != nil {
		t.Fatalf("AAC part: %v %s", err, out)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(context.Background(), libs[0]); err != nil {
		t.Fatal(err)
	}
	var page api.Item
	h.call("GET", "/v1/items/"+ids[0], nil, &page, 200)
	for part, p := range page.Versions[0].Parts {
		want := int64(math.Round(float64(part) * partPacketSeconds * 1000))
		if p.OffsetMs == nil || *p.OffsetMs != want {
			t.Fatalf("part %d page offset: %v, want %d ms on the playback clock", part+1, p.OffsetMs, want)
		}
	}
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: ids[0]}, &session, 201)
	if session.Presentation.Route != "converted" {
		t.Fatalf("mixed book route: %s", session.Presentation.Route)
	}
	played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
	t.Logf("mixed copied batches play %.6f seconds", played.seconds)
	for _, part := range []int{21, 22, 23, 32} {
		for _, within := range []float64{0.2, 2.5} {
			if got := toneAt(t, ffmpeg, played.file, float64(part)*partPacketSeconds+within); got != partTone(part) {
				t.Errorf("part %d at +%.1f s: heard %d Hz, want %d", part+1, within, got, partTone(part))
			}
		}
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}
