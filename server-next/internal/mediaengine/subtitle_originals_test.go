package mediaengine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// An app gets the authored styling and the attached font rather than the words
// alone. The attachment's untrusted filename never becomes a server path.
func TestStyledSubtitleOriginalsAndFontsFollowThePlaybackGrant(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d, sources := newHouseWith(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := t.TempDir()
	film := filepath.Join(library, "Styled (2020).mkv")
	ass := "[Script Info]\nScriptType: v4.00+\nPlayResX: 320\nPlayResY: 180\n[V4+ Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour, OutlineColour, Bold, Italic, Alignment\n" +
		"Style: Default,Noto Sans Hebrew,20,&H00FFFFFF,&H00000000,0,0,2\n[Events]\n" +
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\pos(160,90)}Styled words\n"
	script := filepath.Join(t.TempDir(), "script.ass")
	beside := filepath.Join(library, "Styled (2020).fr.ass")
	for _, path := range []string{script, beside} {
		if err := os.WriteFile(path, []byte(ass), 0600); err != nil {
			t.Fatal(err)
		}
	}
	font := filepath.Join("testdata", "subtitle-font", "NotoSansHebrew-Regular.ttf")
	wantFont, err := os.ReadFile(font)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-i", script,
		"-map", "0:v", "-map", "1:a", "-map", "2:s", "-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "copy",
		"-attach", font, "-metadata:s:t:0", "mimetype=application/x-truetype-font", "-metadata:s:t:0", "filename=../../untrusted.ttf", film).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, out)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil, &options, 200)
	if len(options.Versions[0].Subtitles) != 2 || len(options.Versions[0].Subtitles[0].Deliveries) != 2 {
		t.Fatalf("styled tracks must offer authored and text delivery: %+v", options.Versions[0].Subtitles)
	}
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	var originalURLs []string
	for _, file := range session.Presentation.Subtitles {
		code, header, raw := h.media(file.URL)
		if code != 200 {
			t.Fatalf("%s answered %d: %s", file.URL, code, raw)
		}
		if file.Format == "vtt" {
			if !bytes.Contains(raw, []byte("Styled words")) || bytes.Contains(raw, []byte("\\pos")) {
				t.Fatal("plain text lost its words or retained styling")
			}
			continue
		}
		originalURLs = append(originalURLs, file.URL)
		if file.Format != "ass" || file.FontsURL == "" || file.TimeDomain != "source-relative" ||
			!bytes.Contains(raw, []byte("{\\pos(160,90)}Styled words")) || header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("authored subtitle: %+v\n%s", file, raw)
		}
		code, _, raw = h.media(file.FontsURL)
		var fonts subtitles.FontFiles
		if code != 200 || json.Unmarshal(raw, &fonts) != nil || len(fonts.Fonts) != 1 {
			t.Fatalf("fonts: %d %s", code, raw)
		}
		code, _, raw = h.media(fonts.Fonts[0].URL)
		if code != 200 || !bytes.Equal(raw, wantFont) || strings.Contains(fonts.Fonts[0].Name, "..") {
			t.Fatal("font bytes or safe name lost")
		}
		originalURLs = append(originalURLs, fonts.Fonts[0].URL, file.FontsURL)
		if code, _, _ := h.media(file.FontsURL + "/32"); code != 404 {
			t.Fatal("unknown attachment was served")
		}
	}
	if len(originalURLs) != 6 {
		t.Fatalf("missing embedded or sidecar original: %+v", session.Presentation.Subtitles)
	}
	fontPaths, err := filepath.Glob(filepath.Join(h.data, "transcodes", "*", "*", "subtitle-fonts", "burn-fonts", "font-*.ttf"))
	if err != nil || len(fontPaths) != 1 {
		t.Fatalf("font attachment must stay in stream output: %v %v", fontPaths, err)
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	if _, err := os.Stat(fontPaths[0]); !os.IsNotExist(err) {
		t.Fatalf("stopped stream retained its font: %v", err)
	}

	for _, url := range originalURLs {
		if code, _, _ := h.media(url); code != 404 {
			t.Fatalf("stopped subtitle/font answered %d", code)
		}
	}
	// The same files on configured storage use the isolated bridge for
	// extraction, and the storage reader for the sidecar (no local fallback).
	media, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	dav := remotetest.NewDAV(t, map[string]string{"Films/Styled (2020).mkv": string(media), "Films/Styled (2020).fr.ass": ass})
	var source api.RemoteSource
	h.call("POST", "/v1/admin/remote-sources", api.RemoteSourceCreate{Kind: "webdav", Name: "Subtitle NAS",
		WebDAV: &api.WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &source, 200)
	row, err := d.RemoteSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteLib, err := d.NewLibrary(ctx, db.NewLibrary{Name: "Remote films", Kind: db.LibraryMovies, Language: "en",
		RemoteFolders: []db.RemoteFolder{{SourceID: row.ID, Path: "Films"}}})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range libraries {
		if candidate.PublicID == remoteLib.PublicID {
			if _, err := (&scan.Scanner{DB: d, Remote: sources.Open, ReaderProber: scan.RemoteProber{}}).Scan(ctx, candidate); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.call("GET", "/v1/items?library="+remoteLib.PublicID+"&kind=movie", nil, &page, 200)
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	originals := 0
	for _, file := range session.Presentation.Subtitles {
		if file.Format != "ass" {
			continue
		}
		originals++
		code, _, raw := h.media(file.URL)
		if code != 200 || !bytes.Contains(raw, []byte("{\\pos(160,90)}Styled words")) {
			t.Fatalf("remote original: %d %s", code, raw)
		}
		code, _, raw = h.media(file.FontsURL)
		var fonts subtitles.FontFiles
		if code != 200 || json.Unmarshal(raw, &fonts) != nil || len(fonts.Fonts) != 1 {
			t.Fatalf("remote fonts: %d %s", code, raw)
		}
		code, _, raw = h.media(fonts.Fonts[0].URL)
		if code != 200 || !bytes.Equal(raw, wantFont) {
			t.Fatal("remote attachment bytes changed")
		}
	}
	if originals != 2 {
		t.Fatalf("remote file lost an original: %+v", session.Presentation.Subtitles)
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)

}
