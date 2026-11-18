package mediaengine_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A film on a WebDAV source the owner added is scanned through the source, offered with what
// was read from it, and played: as it is by byte range, and converted at a lower quality.
// Where this host has the decoder's isolated bridge the sandbox is required throughout.
func TestAFilmOnRemoteStoragePlays(t *testing.T) {
	testRemoteFilm(t, 1)
}

// A film in two files on remote storage plays as one film, as it would from a disk here: both
// parts copied into one stream on the film's clock, and both converted at a lower quality.
func TestASplitRemoteFilmPlaysBothParts(t *testing.T) {
	testRemoteFilm(t, 2)
}

// The same with every folder of the test (conversions included) reached through a link, as
// every folder under /var is on macOS: the converter is handed where its folder really is.
func TestAFilmOnRemoteStoragePlaysThroughALinkedFolder(t *testing.T) {
	real, err := os.MkdirTemp("", "real-")
	if err != nil {
		t.Fatal(err)
	}
	link := real + "-link"
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(link); os.RemoveAll(real) })
	t.Setenv("TMPDIR", link)
	testRemoteFilm(t, 1)
}

func testRemoteFilm(t *testing.T, partCount int) {
	t.Helper()
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	files := map[string]string{}
	var original []byte
	for n := 1; n <= partCount; n++ {
		// Each part its own tone, so which part is playing can be heard.
		film := filepath.Join(t.TempDir(), "film.mp4")
		if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:sample_rate=48000", 600*n-300),
			"-t", "12", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
			"-movflags", "+faststart", film).CombinedOutput(); err != nil {
			t.Fatalf("making the film: %v\n%s", err, out)
		}
		var err error
		if original, err = os.ReadFile(film); err != nil {
			t.Fatal(err)
		}
		name := "Small Film (2020).mp4"
		if partCount > 1 {
			name = fmt.Sprintf("Small Film (2020) - pt%d.mp4", n)
		}
		files["Media/Films/Small Film (2020)/"+name] = string(original)
	}
	dav := remotetest.NewDAV(t, files)
	h, d, sources := newHouseWith(t, ffmpeg, ffprobe)
	ctx := context.Background()

	var source api.RemoteSource
	h.call("POST", "/v1/admin/remote-sources", api.RemoteSourceCreate{Kind: "webdav", Name: "NAS",
		WebDAV: &api.WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &source, 200)
	row, err := d.RemoteSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Made with its folder, as the owner makes one: a library always has a folder.
	lib, err := d.NewLibrary(ctx, db.NewLibrary{Name: "Films", Kind: db.LibraryMovies, Language: "en",
		RemoteFolders: []db.RemoteFolder{{SourceID: row.ID, Path: "Media/Films"}}})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d, Remote: sources.Open, ReaderProber: scan.RemoteProber{}}
	stats, err := scanner.Scan(ctx, libraries[0])
	if err != nil || stats.ItemsNew != 1 || stats.Probed != partCount {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 || page.Items[0].Title != "Small Film" {
		t.Fatalf("the library lists %+v", page.Items)
	}
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil, &options, 200)
	if v := options.Versions[0]; len(v.Video) != 1 || v.Video[0].Height != 720 || len(v.Audio) != 1 || v.DurationMs != int64(partCount*12000) {
		t.Fatalf("the version is %+v", v)
	}

	session := h.startPlayback(api.PlaybackStart{ItemID: page.Items[0].ID})
	first := session.Presentation
	if partCount > 1 {
		// Two files are one stream: copied as they are into it, not played as two.
		if first.Route != playback.RouteRepackaged || first.Decision.Video.Action != "copy" || first.Decision.Audio.Action != "copy" {
			t.Fatalf("the parts should be copied into one stream: %+v", first)
		}
		assertBothRemoteParts(t, h.listen(t, first.URL, ffmpeg, ffprobe), ffmpeg, "copied")
	} else {
		if first.Mode != "direct" || first.Route != playback.RouteOriginal {
			t.Fatalf("the film should play as it is: %+v", first)
		}
		code, header, body := h.media(first.URL, "Range", "bytes=1000-1999")
		if code != http.StatusPartialContent || !bytes.Equal(body, original[1000:2000]) ||
			header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(original)) {
			t.Fatalf("a byte range answered %d %v with %d bytes", code, header, len(body))
		}
	}

	position := int64(0)
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{PositionMs: &position,
		Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 200)
	second := session.Presentation
	if second.Route != playback.RouteConverted || !strings.HasSuffix(second.URL, "/master.m3u8") {
		t.Fatalf("a lower quality should be a conversion: %+v", second)
	}
	if second.Notice != "" {
		t.Fatalf("parts on remote storage play whole, yet the person is told %q", second.Notice)
	}
	base := second.URL[:strings.LastIndex(second.URL, "/")+1]
	code, _, playlist := h.media(second.URL)
	if !strings.Contains(string(playlist), "#EXTINF:") {
		var variant string
		for _, line := range strings.Split(string(playlist), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				variant = line
				break
			}
		}
		code, _, playlist = h.media(base + variant)
	}

	var segment string
	for _, line := range strings.Split(string(playlist), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			segment = line
			break
		}
	}
	if code != 200 || segment == "" {
		t.Fatalf("the picture's playlist answered %d:\n%s", code, playlist)
	}
	if code, _, body := h.media(base + segment); code != 200 || len(body) == 0 {
		t.Fatalf("the first converted segment answered %d with %d bytes", code, len(body))
	}
	played := h.listen(t, second.URL, ffmpeg, ffprobe)
	if partCount > 1 {
		assertBothRemoteParts(t, played, ffmpeg, "converted")
	} else if played.seconds < 11.8 || played.seconds > 12.4 {
		t.Fatalf("delivered %.3f seconds, want the film's 12 seconds", played.seconds)
	}
	t.Logf("%d parts: converted %.6f s", partCount, played.seconds)
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}

// assertBothRemoteParts checks a stream of the two-part film lasts both parts' 24 seconds and
// plays the first part's tone, then the second's.
func assertBothRemoteParts(t *testing.T, played heard, ffmpeg, route string) {
	t.Helper()
	if played.seconds < 23.7 || played.seconds > 24.4 {
		t.Fatalf("%s: delivered %.3f seconds, want both parts' 24 seconds", route, played.seconds)
	}
	for at, tone := range map[float64]int{3: 300, 18: 900} {
		if got := toneAt(t, ffmpeg, played.file, at); got != tone {
			t.Fatalf("%s: %.0f s in plays %d Hz, want %d Hz", route, at, got, tone)
		}
	}
}

// Books in several files on remote storage play as one book each, as from a disk here (rule 60
// of playback): parts encoded alike are copied into one stream, parts that differ are converted
// into one, and a place in either is a place in the whole book, from its start and from inside
// a later part. The sandbox is required where this host has it.
func TestBooksInPartsOnRemoteStoragePlayAsOneBook(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	files := map[string]string{}
	part := func(book, name string, tone int, codec ...string) {
		file := filepath.Join(t.TempDir(), name)
		args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", fmt.Sprintf("sine=frequency=%d:duration=20", tone), "-ac", "2"}, codec...)
		args = append(args, "-metadata", "album="+book, "-metadata", "artist=Ann Author", file)
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("making %s: %v\n%s", name, err, out)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		files["Media/Audiobooks/Ann Author/"+book+"/"+name] = string(data)
	}
	mp3 := []string{"-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100"}
	part("Three Parts", "01.mp3", 300, mp3...)
	part("Three Parts", "02.mp3", 600, mp3...)
	part("Three Parts", "03.mp3", 900, mp3...)
	part("Two Kinds", "01.mp3", 300, mp3...)
	part("Two Kinds", "02.m4a", 600, "-c:a", "aac", "-b:a", "128k", "-ar", "48000")
	dav := remotetest.NewDAV(t, files)
	h, d, sources := newHouseWith(t, ffmpeg, ffprobe)
	ctx := context.Background()
	var source api.RemoteSource
	h.call("POST", "/v1/admin/remote-sources", api.RemoteSourceCreate{Kind: "webdav", Name: "NAS",
		WebDAV: &api.WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &source, 200)
	row, err := d.RemoteSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := d.NewLibrary(ctx, db.NewLibrary{Name: "Audiobooks", Kind: db.LibraryAudiobooks, Language: "en",
		RemoteFolders: []db.RemoteFolder{{SourceID: row.ID, Path: "Media/Audiobooks"}}})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := (&scan.Scanner{DB: d, Remote: sources.Open, ReaderProber: scan.RemoteProber{}}).Scan(ctx, libraries[0]); err != nil || stats.ItemsNew != 2 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=audiobook", nil, &page, 200)
	if len(page.Items) != 2 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	for _, item := range page.Items {
		length, converted := 60.0, false
		if item.Title == "Two Kinds" {
			length, converted = 40, true
		}
		for _, start := range []int64{0, 25_000} {
			session := h.startPlayback(api.PlaybackStart{ItemID: item.ID, StartPositionMs: &start})
			p := session.Presentation
			if !strings.HasSuffix(p.URL, ".m3u8") || p.StartPositionMs != start || (p.Route == playback.RouteConverted) != converted {
				t.Fatalf("%s from %d ms: %+v", item.Title, start, p)
			}
			// The segment holding the start, fetched first, is the part holding it.
			h.segmentTone(t, p.URL, ffmpeg, int(start/6000), 0.5, 300*(1+int(start/20_000)))
			played := h.listen(t, p.URL, ffmpeg, ffprobe)
			if played.seconds < length-1 || played.seconds > length+1 {
				t.Fatalf("%s from %d ms: what plays lasts %.1f s, want the whole book's %.0f s", item.Title, start, played.seconds, length)
			}
			for at, tone := range map[float64]int{0.5: 300, 19: 300, 21: 600, 38.5: 600, 41: 900} {
				if at < length {
					if got := toneAt(t, ffmpeg, played.file, at); got != tone {
						t.Errorf("%s from %d ms: %.1f s in plays %d Hz, want %d Hz", item.Title, start, at, got, tone)
					}
				}
			}
			h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
		}
	}
}
