package mediaengine_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// pgsBox is a PGS subtitle (a Blu-ray .sup) holding one white box, shown from one second to
// five: ffmpeg has no PGS encoder, so the few segments are written out here.
func pgsBox(screenW, screenH, x, y, w, h int) []byte {
	var out bytes.Buffer
	segment := func(pts uint32, kind byte, body []byte) {
		out.WriteString("PG")
		binary.Write(&out, binary.BigEndian, pts)
		binary.Write(&out, binary.BigEndian, uint32(0))
		out.WriteByte(kind)
		binary.Write(&out, binary.BigEndian, uint16(len(body)))
		out.Write(body)
	}
	pack := func(values ...any) []byte {
		var b bytes.Buffer
		for _, v := range values {
			binary.Write(&b, binary.BigEndian, v)
		}
		return b.Bytes()
	}
	composition := func(number uint16, state byte, objects byte) []byte {
		body := pack(uint16(screenW), uint16(screenH), byte(0x10), number, state, byte(0), byte(0), objects)
		if objects > 0 {
			body = append(body, pack(uint16(0), byte(0), byte(0), uint16(x), uint16(y))...)
		}
		return body
	}
	window := pack(byte(1), byte(0), uint16(x), uint16(y), uint16(w), uint16(h))
	line := []byte{0, 0xC0 | byte(w>>8), byte(w), 1, 0, 0}
	pixels := bytes.Repeat(line, h)
	length := len(pixels) + 4
	object := append(pack(uint16(0), byte(0), byte(0xC0), byte(length>>16), byte(length>>8), byte(length), uint16(w), uint16(h)), pixels...)
	shown, hidden := uint32(1*90000), uint32(5*90000)
	segment(shown, 0x16, composition(0, 0x80, 1))
	segment(shown, 0x17, window)
	segment(shown, 0x14, []byte{0, 0, 1, 235, 128, 128, 255})
	segment(shown, 0x15, object)
	segment(shown, 0x80, nil)
	segment(hidden, 0x16, composition(1, 0, 0))
	segment(hidden, 0x17, window)
	segment(hidden, 0x80, nil)
	return out.Bytes()
}

// A subtitle is drawn into the picture of a converted stream: a picture subtitle by itself
// on a device that cannot draw one, and any subtitle the app asks to have burned, whether it
// is inside the video or in a file beside it.
func TestSubtitlesAreBurnedIntoThePicture(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Films")
	folder := filepath.Join(library, "Burned (2022)")
	film := filepath.Join(folder, "Burned (2022).mkv")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	words := "1\n00:00:00,500 --> 00:00:05,000\nWORDS WORDS WORDS WORDS\n"
	sup, text := filepath.Join(t.TempDir(), "box.sup"), filepath.Join(t.TempDir(), "words.srt")
	for path, content := range map[string][]byte{sup: pgsBox(640, 360, 220, 40, 200, 30), text: []byte(words),
		strings.TrimSuffix(film, ".mkv") + ".de.srt": []byte(words), strings.TrimSuffix(film, ".mkv") + ".fr.sup": pgsBox(640, 360, 220, 40, 200, 30)} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:size=640x360:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-i", sup, "-i", text,
		"-map", "0:v", "-map", "1:a", "-map", "2:s", "-map", "3:s", "-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=spa",
		"-t", "6", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s:0", "copy", "-c:s:1", "srt",
		film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
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
	tracks := map[string]string{}
	for _, s := range options.Versions[0].Subtitles {
		tracks[s.Source+" "+s.Language] = s.ID
	}

	// brightest is the brightest point of a part of the picture two seconds in: blue is dark,
	// a subtitle is white.
	var brightestAt func(session api.PlaybackSession, crop string, frame int) byte
	brightest := func(session api.PlaybackSession, crop string) byte { return brightestAt(session, crop, 48) }
	brightestAt = func(session api.PlaybackSession, crop string, frame int) byte {
		t.Helper()
		if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.Delivery != "burn" || session.Presentation.Container != "mpegts_hls" {
			t.Fatalf("the subtitle should be burned into a converted picture: %+v", session.Presentation)
		}
		base := session.Presentation.URL[:strings.LastIndex(session.Presentation.URL, "/")+1]
		code, _, segment := h.media(base + "segment-000000.ts")
		if code != 200 {
			t.Fatalf("the first segment answered %d", code)
		}
		file := filepath.Join(t.TempDir(), "segment.ts")
		os.WriteFile(file, segment, 0o600)
		var complaint bytes.Buffer
		read := exec.Command(ffmpeg, "-v", "error", "-i", file, "-vf", fmt.Sprintf("select='eq(n,%d)',crop=%s", frame, crop), "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "gray", "-")
		read.Stderr = &complaint
		raw, err := read.Output()
		if err != nil || len(raw) == 0 {
			t.Fatalf("reading a frame of a %d byte segment: %v %s", len(segment), err, complaint.String())
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
		return raw[maxAt(raw)]
	}
	const box, bottom = "200:30:220:40", "640:90:0:270"
	for name, c := range map[string]struct {
		track, delivery, crop string
	}{
		"a picture subtitle inside the video, nobody asking": {tracks["embedded eng"], "", box},
		"words inside the video, burning asked for":          {tracks["embedded spa"], "burn", bottom},
		"words in a file beside it, burning asked for":       {tracks["sidecar de"], "burn", bottom},
		"a picture file beside it, burning asked for":        {tracks["sidecar fr"], "burn", box},
	} {
		if c.track == "" {
			t.Fatalf("%s: the title's subtitles are %v", name, tracks)
		}
		var session api.PlaybackSession
		h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, Subtitles: &api.SubtitlePick{TrackID: &c.track, Delivery: c.delivery}}, &session, 201)
		if light := brightest(session, c.crop); light < 180 {
			t.Fatalf("%s: where the subtitle should be the picture is no brighter than %d", name, light)
		}
	}

	// The viewer's timing nudge is burned in with the subtitle. The box inside the video is
	// gone by five seconds in; nudged three seconds later, it is still there then. (Last, because
	// a nudge is kept for the title.)
	late := int64(3000)
	var nudged api.PlaybackSession
	track := tracks["embedded eng"]
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, Subtitles: &api.SubtitlePick{TrackID: &track, Delivery: "burn"}}, &nudged, 201)
	plain := nudged.Presentation.URL
	h.call("PATCH", "/v1/playback/sessions/"+nudged.ID, api.PlaybackChange{SubtitleOffsetMs: &late}, &nudged, 200)
	if nudged.Presentation.URL == plain {
		t.Fatal("a new nudge for a burned subtitle should be a new stream")
	}
	if light := brightestAt(nudged, "200:30:220:40", 120); light < 180 {
		t.Fatalf("five seconds in, a box nudged three seconds later should still be there; the picture is no brighter than %d", light)
	}
}

func maxAt(raw []byte) int {
	at := 0
	for i, v := range raw {
		if v > raw[at] {
			at = i
		}
	}
	return at
}
