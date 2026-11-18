package mediaengine_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// discReader is the disc reader that came with an FFmpeg, which the server reads discs with,
// or "" when there is none or the server cannot hand it a folder (it is not Linux).
func discReader(ffmpeg string) string {
	reader, err := exec.LookPath(filepath.Join(filepath.Dir(ffmpeg), mediatools.Name("portico-disc")))
	if err != nil || runtime.GOOS != "linux" {
		return ""
	}
	return reader
}

// On a server that cannot read discs (its media tools have no disc reader), a film disc kept
// as a folder is listed, and pressing Play on it is answered plainly: discs can't be played
// here, and trying again will not help. It used to be answered as a file that could not be
// reached right now, with "try again".
func TestPlayingADiscIsRefusedInPlainWords(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	if discReader(ffmpeg) != "" {
		t.Skip("these media tools read discs, so a disc plays: TestADiscPlaysItsMainFeature")
	}
	h, d := newHouse(t, ffmpeg, decodertest.QualifiedFFprobe(t))
	library := filepath.Join(t.TempDir(), "Films")
	disc := filepath.Join(library, "Disc Film (2019)", "BDMV")
	if err := os.MkdirAll(disc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disc, "index.bdmv"), []byte("INDX0200"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(context.Background(), "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Skipf("the scan lists no title for a disc folder here: %+v", page.Items)
	}
	for _, ask := range []func() (int, string){
		func() (int, string) {
			w := h.do("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID})
			raw, _ := io.ReadAll(w.Body)
			return w.StatusCode, string(raw)
		},
		func() (int, string) {
			w := h.do("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil)
			raw, _ := io.ReadAll(w.Body)
			return w.StatusCode, string(raw)
		},
	} {
		code, body := ask()
		if code != 501 || !strings.Contains(body, `"disc_playback_unavailable"`) || !strings.Contains(body, "Discs can't be played on this server.") ||
			!strings.Contains(body, `"retry":false`) {
			t.Fatalf("playing a disc answered %d: %s", code, body)
		}
	}
}

// A film disc, kept as a folder or as an image, plays its main feature as the server's stream,
// never as the disc itself, its picture converted: its playlist names segments that are the title's
// picture, a segment past the Blu-ray's first clip (a seek) plays from its place, and the disc
// played again resumes where it was left.
func TestADiscPlaysItsMainFeature(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	reader := discReader(ffmpeg)
	if reader == "" {
		decodertest.Unavailable(t, "these media tools have no disc reader (portico-disc)")
	}
	// The server finds that reader once it has asked the FFmpeg what it can do, which happens
	// beside the house's start; the scan may come first.
	t.Setenv("PORTICO_PORTICO-DISC", reader)
	for _, fixture := range []string{"bluray-two-clip-bdmv", "dvd-two-title-video-ts", "bluray-udf250.iso"} {
		t.Run(fixture, func(t *testing.T) {
			h, d := newHouse(t, ffmpeg, ffprobe)
			library := filepath.Join(t.TempDir(), "Films")
			source, film := filepath.Join("..", "..", "..", "fixtures", "discs", fixture), filepath.Join(library, "Disc Film (2019)")
			var err error
			if strings.HasSuffix(fixture, ".iso") {
				if err = os.MkdirAll(film, 0o755); err == nil {
					var image []byte
					if image, err = os.ReadFile(source); err == nil {
						err = os.WriteFile(filepath.Join(film, "Disc Film (2019).iso"), image, 0o644)
					}
				}
			} else {
				err = os.CopyFS(film, os.DirFS(source))
			}
			if err != nil {
				t.Fatal(err)
			}
			lib, err := d.CreateLibrary(context.Background(), "Films", db.LibraryMovies, "en", []string{library})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(context.Background(), lib); err != nil {
				t.Fatal(err)
			}
			var page api.ItemPage
			h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
			if len(page.Items) != 1 {
				t.Fatalf("the library lists %+v", page.Items)
			}
			item := page.Items[0].ID
			// The scan read the disc's main feature: its length is the title's.
			var options api.PlaybackOptions
			h.call("GET", "/v1/items/"+item+"/playback-options", nil, &options, 200)
			if len(options.Versions) != 1 || options.Versions[0].DurationMs < 71_900 || options.Versions[0].DurationMs > 72_100 {
				t.Fatalf("the scan stored %+v", options.Versions)
			}

			var session api.PlaybackSession
			h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
			p := session.Presentation
			if p.Mode != "stream" || p.Route != playback.RouteConverted || p.Decision.Video == nil || p.Decision.Video.Action != "transcode" ||
				!strings.HasSuffix(p.URL, "/master.m3u8") {
				t.Fatalf("a disc should play as the server's stream, its picture converted: %+v", p)
			}
			// The fixtures' main features are 72 seconds. The first segment, and the one holding 42
			// seconds, past the Blu-ray's first clip (it ends at 37), each start where they should.
			header, segments := discSegments(t, h, p.URL)
			if len(segments) != 12 {
				t.Fatalf("the playlist lists %d segments", len(segments))
			}
			for n, want := range map[int]float64{0: 0, 7: 42} {
				if at := discPicture(t, h, ffprobe, header, segments[n]); at < want || at > want+0.5 {
					t.Fatalf("segment %d starts at %.3f seconds, want %.0f", n, at, want)
				}
			}
			h.call("PUT", "/v1/playback/sessions/"+session.ID+"/progress", api.PlaybackProgress{State: "playing", PositionMs: 42_000}, nil, 200)
			h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)

			// Played again, it resumes where it was left.
			h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
			if session.Presentation.StartPositionMs != 42_000 {
				t.Fatalf("the disc resumed at %d ms", session.Presentation.StartPositionMs)
			}
			header, segments = discSegments(t, h, session.Presentation.URL)
			if at := discPicture(t, h, ffprobe, header, segments[7]); at < 42 || at > 42.5 {
				t.Fatalf("resumed, segment 7 starts at %.3f seconds", at)
			}
			h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
		})
	}
}

// discSegments is the picture's playlist a stream names: the address of its header, for a
// stream of fragments, and of each segment.
func discSegments(t *testing.T, h *house, master string) (header string, segments []string) {
	t.Helper()
	base := master[:strings.LastIndex(master, "/")+1]
	code, _, playlist := h.media(master)
	for code == 200 && !strings.Contains(string(playlist), "#EXTINF") {
		// The master playlist names the picture's own.
		variant := ""
		for _, line := range strings.Split(string(playlist), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				variant = line
				break
			}
		}
		if variant == "" {
			t.Fatalf("the playlist names nothing:\n%s", playlist)
		}
		master = base + variant
		base = master[:strings.LastIndex(master, "/")+1]
		code, _, playlist = h.media(master)
	}
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		if start := strings.Index(line, `URI="`); strings.HasPrefix(line, "#EXT-X-MAP") && start >= 0 {
			header = base + strings.SplitN(line[start+5:], `"`, 2)[0]
		} else if line != "" && !strings.HasPrefix(line, "#") {
			segments = append(segments, base+line)
		}
	}
	if code != 200 || len(segments) == 0 {
		t.Fatalf("the picture's playlist answered %d:\n%s", code, playlist)
	}
	return header, segments
}

// discPicture fetches a segment, after its header when it has one, and checks it holds a
// picture ffprobe reads. It returns when that picture starts, in seconds.
func discPicture(t *testing.T, h *house, ffprobe, header, segment string) float64 {
	t.Helper()
	var joined []byte
	for _, address := range []string{header, segment} {
		if address == "" {
			continue
		}
		code, _, body := h.media(address)
		if code != 200 || len(body) == 0 {
			t.Fatalf("%s answered %d with %d bytes", address, code, len(body))
		}
		joined = append(joined, body...)
	}
	file := filepath.Join(t.TempDir(), "segment")
	if err := os.WriteFile(file, joined, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,height,start_time", "-of", "csv=p=0", file).CombinedOutput()
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(fields) != 3 {
		t.Fatalf("%s is not a picture: %q (%v)", segment, out, err)
	}
	height, _ := strconv.Atoi(fields[1])
	start, startErr := strconv.ParseFloat(fields[2], 64)
	if err != nil || fields[0] == "" || height == 0 || startErr != nil {
		t.Fatalf("%s is not a picture: %q (%v)", segment, out, err)
	}
	return start
}
