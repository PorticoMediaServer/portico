package preparedmedia

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func musicCover(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	seed := uint32(1)
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			seed = seed*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{byte(seed >> 24), byte(seed >> 16), byte(seed >> 8), 255})
		}
	}
	var out bytes.Buffer
	if e := png.Encode(&out, img); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func TestMusicMetadataRoundTripAndBoundsR29(t *testing.T) {
	tags := audiotags.Tags{Values: map[string][]string{"title": {"Cue track"}, "artist": {"Artist one", "Artist two"}, "musicbrainz_releasetrackid": {"recording-id"}, "track": {"2"}, "total_tracks": {"9"}}, Pictures: []audiotags.Picture{{Type: 3, MIME: "image/png", Data: musicCover(t)}}, Lyrics: []audiotags.Lyric{{Language: "und", Format: "lrc", Text: "[00:00.00] Freely generated fixture"}}}
	audio := []byte("an opaque audio payload; this test proves metadata, not decode")
	for _, codec := range []string{"flac", "opus", "aac"} {
		t.Run(codec, func(t *testing.T) {
			var input bytes.Buffer
			switch codec {
			case "flac":
				input.WriteString("fLaC")
				input.Write([]byte{128, 0, 0, 34})
				input.Write(make([]byte, 34))
				input.Write(audio)
			case "aac":
				input.Write(mp4Atom("ftyp", []byte("M4A \x00\x00\x00\x00M4A isom")))
				input.Write(mp4Atom("moov", mp4Atom("udta", nil)))
				input.Write(mp4Atom("mdat", audio))
			case "opus":
				head := append([]byte("OpusHead"), []byte{1, 2, 0, 0, 128, 187, 0, 0, 0, 0, 0}...)
				for i, p := range [][]byte{head, []byte("OpusTags\x00\x00\x00\x00\x00\x00\x00\x00"), audio} {
					page := make([]byte, 28+len(p))
					copy(page, "OggS")
					if i == 0 {
						page[5] = 2
					}
					if i == 2 {
						page[5] = 4
					}
					binary.LittleEndian.PutUint32(page[14:], 42)
					page[26] = 1
					page[27] = byte(len(p))
					copy(page[28:], p)
					if e := writeOggPage(&input, page, uint32(i)); e != nil {
						t.Fatal(e)
					}
				}
			}
			var out bytes.Buffer
			if e := copyMusicMetadata(&out, &input, codec, tags); e != nil {
				t.Fatal(e)
			}
			if e := verifyMusicMetadata(bytes.NewReader(out.Bytes()), int64(out.Len()), tags); e != nil {
				t.Fatal(e)
			}
			if !bytes.Contains(out.Bytes(), audio) {
				t.Fatal("audio payload changed")
			}
			if codec == "opus" {
				for r := bytes.NewReader(out.Bytes()); r.Len() > 0; {
					p, e := readOggPage(r)
					if e != nil {
						t.Fatal(e)
					}
					want := binary.LittleEndian.Uint32(p[22:])
					clear(p[22:26])
					if oggChecksum(p) != want {
						t.Fatal("invalid page checksum")
					}
				}
			}
		})
	}
	for _, codec := range []string{"flac", "opus", "aac"} {
		if e := copyMusicMetadata(&bytes.Buffer{}, bytes.NewReader([]byte("broken")), codec, tags); e == nil {
			t.Fatal("malformed container accepted", codec)
		}
	}
}

// This is an actual recipe -> bounded metadata writer -> ffprobe -> full
// packet decode proof. Every source, image and lyric is generated in the test.
func TestMusicDownloadEncodedMediaR29(t *testing.T) {
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	probe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	cover := musicCover(t)
	if e = os.WriteFile(filepath.Join(dir, "cover.png"), cover, 0600); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(dir, "source.flac")
	if raw, e := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=sample_rate=88200:duration=0.25", "-i", filepath.Join(dir, "cover.png"), "-map", "0:a", "-map", "1:v", "-c:a", "flac", "-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:v", "copy", "-disposition:v", "attached_pic", "-metadata", "title=Generated source", "-metadata", "artist=Fixture artist", source).CombinedOutput(); e != nil {
		t.Fatalf("source %v %s", e, raw)
	}
	raw, e := exec.Command(probe, "-v", "error", "-show_format", "-show_streams", "-of", "json", source).Output()
	if e != nil {
		t.Fatal(e)
	}
	original, e := parseProbe(raw)
	if e != nil {
		t.Fatal(e)
	}
	file, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	st, _ := file.Stat()
	tags := audiotags.Read(file, st.Size())
	if len(tags.Pictures) != 1 || !bytes.Equal(tags.Pictures[0].Data, cover) {
		t.Fatal("source cover missing")
	}
	web := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer web.Close()
	for _, id := range []string{"music-cd-quality-v1", "music-opus-320-v1", "music-opus-96-v1", "music-aac-320-v1", "music-aac-96-v1", "music-original-flac-v1"} {
		t.Run(id, func(t *testing.T) {
			profile, e := profile(id)
			if e != nil {
				t.Fatal(e)
			}
			r := recipeFor(profile)
			r.Source = original.recipeSource()
			args, e := decoder.PreparedArgs(web.URL+"/source.flac", r)
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(ff, args...)
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			encoded, e := cmd.Output()
			if e != nil {
				t.Fatal(e, diagnostic.String())
			}
			var decorated bytes.Buffer
			if e = copyMusicMetadata(&decorated, bytes.NewReader(encoded), r.MusicCodec, tags); e != nil {
				t.Fatal(e)
			}
			name := filepath.Join(dir, id+".media")
			if e = os.WriteFile(name, decorated.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
			raw, e = exec.Command(probe, "-v", "error", "-show_format", "-show_streams", "-of", "json", name).Output()
			if e != nil {
				t.Fatal(e)
			}
			observed, e := parseProbeMode(raw, true)
			if e != nil {
				t.Fatal(e)
			}
			if observed.Duration == 0 {
				args := audiofacts.Args(name)
				for i := range args {
					if args[i] == "-protocol_whitelist" {
						args[i+1] = "file"
					}
				}
				actual, err := exec.Command(probe, args...).Output()
				if err != nil {
					t.Fatal(err)
				}
				measured, err := audiofacts.Parse(bytes.NewReader(actual))
				if err != nil {
					t.Fatal(err)
				}
				observed, e = measuredMusicProbe(observed, measured)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = acceptOutput(original, observed, profile); e != nil {
				t.Fatalf("output %+v: %v", observed, e)
			}
			if e = verifyMusicMetadata(bytes.NewReader(decorated.Bytes()), int64(decorated.Len()), tags); e != nil {
				t.Fatal(e)
			}
			if output, e := exec.Command(ff, "-v", "error", "-xerror", "-i", name, "-map", "0:a:0", "-f", "null", "-").CombinedOutput(); e != nil {
				t.Fatalf("decode %v %s", e, output)
			}
		})
	}
}
