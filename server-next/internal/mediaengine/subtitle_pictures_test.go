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

// Devices declaring bitmap rendering keep the picture while fetching original
// packets beside HLS. Others, and explicit video-only requests, get burn-in.
func TestDeviceSubtitleRenderingDecidesOriginalDeliveryOrBurn(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d, sources := newHouseWith(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library, inputs := t.TempDir(), t.TempDir()
	film := filepath.Join(library, "Pictures (2020).mkv")
	sup := filepath.Join(inputs, "box.sup")
	pgs := pgsBox(320, 180, 70, 40, 180, 30)
	if err := os.WriteFile(sup, pgs, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v\n%s", err, out)
		}
	}
	run("-f", "lavfi", "-i", "color=c=blue:size=320x180:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-fix_sub_duration", "-i", sup,
		"-map", "0:v", "-map", "1:a", "-map", "2:s", "-map", "2:s", "-map", "2:s", "-t", "6", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-c:s:0", "copy", "-c:s:1", "dvdsub", "-c:s:2", "dvbsub", film)
	capsule := filepath.Join(inputs, "dvd.mks")
	run("-i", film, "-map", "0:3", "-c:s", "copy", "-f", "matroska", capsule)
	raw, err := os.ReadFile(capsule)
	if err != nil {
		t.Fatal(err)
	}
	idx, sub, err := subtitles.VobSubOriginal(raw)
	if err != nil {
		t.Fatal(err)
	}
	sidecars := map[string][]byte{"Pictures (2020).en.idx": idx, "Pictures (2020).en.SUB": sub, "Pictures (2020).fr.sup": pgs}
	for name, raw := range sidecars {
		if err := os.WriteFile(filepath.Join(library, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
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
	if len(options.Versions[0].Subtitles) != 5 {
		t.Fatalf("a DVD pair is one track: %+v", options.Versions[0].Subtitles)
	}
	var selectedID string
	for _, track := range options.Versions[0].Subtitles {
		if len(track.Deliveries) != 1 {
			t.Fatalf("no drawable form: %+v", track)
		}
		if track.Format == "hdmv_pgs_subtitle" {
			selectedID = track.ID
		}
	}
	setRendering := func(kinds []string) {
		t.Helper()
		profile, err := os.ReadFile(filepath.Join("..", "streaming", "profiles", "chromium.json"))
		if err != nil {
			t.Fatal(err)
		}
		var wrapped struct {
			Document map[string]any `json:"document"`
		}
		if err := json.Unmarshal(profile, &wrapped); err != nil {
			t.Fatal(err)
		}
		native := wrapped.Document["engines"].(map[string]any)["native"].(map[string]any)
		native["subtitleRendering"] = kinds
		h.call("PUT", "/v1/devices/"+h.device+"/capabilities", wrapped.Document, nil, 204)
	}
	var session api.PlaybackSession
	start := func(delivery string) {
		t.Helper()
		session = h.startPlayback(api.PlaybackStart{ItemID: page.Items[0].ID, Subtitles: &api.SubtitlePick{TrackID: &selectedID, Delivery: delivery}})
	}
	checkOriginals := func() {
		t.Helper()
		setRendering([]string{"text", "ass", "pgs", "vobsub", "dvb"})
		start("embeddedClient")
		if shown := session.Presentation.Subtitle; shown == nil || shown.Delivery != "device" || shown.Kind != "pgs" || session.Presentation.Decision.Video.Action == "transcode" {
			t.Fatalf("declared picture renderer should keep the picture: %+v", session.Presentation)
		}
		seen := map[string]int{}
		var urls []string
		for _, file := range session.Presentation.Subtitles {
			if file.Format == "vtt" {
				continue
			}
			seen[file.Format]++
			code, header, raw := h.media(file.URL)
			if code != 200 || file.TimeDomain != "source-relative" || header.Get("Cache-Control") != "private, no-store" {
				t.Fatalf("original: %d %+v", code, file)
			}
			urls = append(urls, file.URL)
			switch file.Format {
			case "pgs":
				if !bytes.HasPrefix(raw, []byte("PG")) {
					t.Fatal("PGS lost its packets")
				}
			case "vobsub":
				if !bytes.Contains(raw, []byte("VobSub index file")) || file.CompanionURL == "" {
					t.Fatal("DVD index or companion lost")
				}
				code, _, packets := h.media(file.CompanionURL)
				if code != 200 || !bytes.HasPrefix(packets, []byte{0, 0, 1, 0xba}) {
					t.Fatal("DVD packets lost")
				}
				urls = append(urls, file.CompanionURL)
			case "dvb":
				path := filepath.Join(inputs, "served.mks")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				probe, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_name", "-of", "compact", path).CombinedOutput()
				if err != nil || !strings.Contains(string(probe), "dvb_subtitle") {
					t.Fatalf("DVB capsule: %v %s", err, probe)
				}
			default:
				t.Fatalf("unexpected original kind %s", file.Format)
			}
			if file.LinesURL != "" {
				code, _, raw := h.media(file.LinesURL)
				var lines subtitles.Lines
				if code != 200 || json.Unmarshal(raw, &lines) != nil || lines.Total != 1 || lines.Forced != 0 {
					t.Fatalf("picture lines: %d %s", code, raw)
				}
				urls = append(urls, file.LinesURL)
			}
		}
		if seen["pgs"] != 2 || seen["vobsub"] != 2 || seen["dvb"] != 1 {
			t.Fatalf("missing original forms: %v", seen)
		}
		nudge := int64(500)
		h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{SubtitleOffsetMs: &nudge}, &session, 200)
		for _, file := range session.Presentation.Subtitles {
			if file.Format != "vtt" && file.OffsetMs != 500 {
				t.Fatal("original's timing nudge was stale")
			}
		}
		cache, err := filepath.Glob(filepath.Join(h.data, "transcodes", "*", "*", "subtitle-originals", "picture-*.ready"))
		if err != nil || len(cache) != 5 {
			t.Fatalf("originals must stay in stream output: %v %v", cache, err)
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
		for _, path := range cache {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("stopped stream kept picture files")
			}
		}
		for _, url := range urls {
			if code, _, _ := h.media(url); code != 404 {
				t.Fatal("stopped picture grant still served")
			}
		}
	}
	checkOriginals()
	// Remote tracks use the decoder's isolated bridge; sidecars use the same
	// configured source, including the packet file's actual uppercase extension.
	media, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"Films/Remote Pictures (2020).mkv": string(media)}
	for name, raw := range sidecars {
		files["Films/"+strings.Replace(name, "Pictures (2020)", "Remote Pictures (2020)", 1)] = string(raw)
	}
	dav := remotetest.NewDAV(t, files)
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
	h.call("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil, &options, 200)
	selectedID = ""
	for _, track := range options.Versions[0].Subtitles {
		if track.Format == "hdmv_pgs_subtitle" {
			selectedID = track.ID
		}
	}
	checkOriginals()
	setRendering([]string{"text"})
	start("")
	if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.Delivery != "burn" || session.Presentation.Decision.Video.Action != "transcode" {
		t.Fatal("device without bitmap renderer did not get burn-in")
	}
	setRendering([]string{"text", "pgs"})
	start("burn")
	if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.Delivery != "burn" {
		t.Fatal("explicit video-only request ignored")
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	for _, track := range options.Versions[0].Subtitles {
		if track.Format == "idx" {
			selectedID = track.ID
		}
	}
	start("burn")
	if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.Kind != "vobsub" {
		t.Fatal("DVD sidecar was not selected")
	}
	segment, _, _ := h.videoAt(t, session.Presentation.URL, 1.5)
	burned := filepath.Join(inputs, "burned.mp4")
	if err := os.WriteFile(burned, segment, 0600); err != nil {
		t.Fatal(err)
	}
	frame, err := exec.Command(ffmpeg, "-v", "error", "-i", burned, "-vf", "select='eq(n,48)'",
		"-frames:v", "1", "-pix_fmt", "gray", "-f", "rawvideo", "-").Output()
	if err != nil || len(frame) == 0 {
		t.Fatalf("reading burned DVD frame: %v (%d bytes)", err, len(frame))
	}
	if frame[maxAt(frame)] < 180 {
		t.Fatal("DVD sidecar did not appear in the picture")
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)

}
