package preparedmedia

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

func numberMetadataMP4(t *testing.T, tags audiotags.Tags) []byte {
	t.Helper()
	input := append(mp4Atom("ftyp", []byte("M4A \x00\x00\x00\x00M4A isom")), mp4Atom("moov", nil)...)
	input = append(input, mp4Atom("mdat", []byte("opaque metadata-only fixture"))...)
	var out bytes.Buffer
	if err := copyMP4Metadata(&out, bytes.NewReader(input), tags); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMusicDownloadNumberMetadataKeepsTotalsAndUnrepresentableText(t *testing.T) {
	for _, row := range []struct {
		name   string
		values map[string][]string
	}{
		{"orphan totals", map[string][]string{"total_tracks": {"9"}, "total_discs": {"2"}}},
		{"compound", map[string][]string{"track": {"2/9"}, "disc": {"1/2"}}},
		{"explicit totals", map[string][]string{"track": {"2"}, "disc": {"1"}, "total_tracks": {"9"}, "total_discs": {"2"}}},
		{"conflicting totals", map[string][]string{"track": {"2/9"}, "total_tracks": {"10"}}},
		{"vinyl index", map[string][]string{"track": {"A1"}, "disc": {"Side A"}, "total_tracks": {"9"}}},
		{"wide number", map[string][]string{"track": {"70000"}, "total_tracks": {"80000"}}},
		{"multiple values", map[string][]string{"track": {"2", "3"}, "total_tracks": {"9", "10"}}},
		{"leading zero", map[string][]string{"track": {"02/09"}, "disc": {"01"}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			expected := audiotags.Tags{Values: row.values}
			output := numberMetadataMP4(t, expected)
			if err := verifyMusicMetadata(bytes.NewReader(output), int64(len(output)), expected); err != nil {
				t.Fatal("valid source metadata was rejected after conversion", err)
			}
			got := audiotags.Read(bytes.NewReader(output), int64(len(output)))
			if len(got.Warnings) != 0 {
				t.Fatal(got.Warnings)
			}
			if row.name == "orphan totals" && (len(got.Values["track"]) != 0 || len(got.Values["disc"]) != 0) {
				t.Fatal("an index was invented", got.Values)
			}
		})
	}
}

func TestMusicDownloadCompoundVerifierAcceptsExactTextAndRequiresItsTotal(t *testing.T) {
	expected := audiotags.Tags{Values: map[string][]string{"track": {"2/9"}, "disc": {"1/2"}}}
	// Exact FLAC comment retention is correct; it must not be compared only to
	// an MP4-normalized index. This fixture proves metadata, not audio decode.
	input := append([]byte{'f', 'L', 'a', 'C', 128, 0, 0, 34}, make([]byte, 34)...)
	var exact bytes.Buffer
	if err := copyMusicMetadata(&exact, bytes.NewReader(input), "flac", expected); err != nil {
		t.Fatal(err)
	}
	if err := verifyMusicMetadata(bytes.NewReader(exact.Bytes()), int64(exact.Len()), expected); err != nil {
		t.Fatal("exact compound tags were rejected", err)
	}
	changed := numberMetadataMP4(t, audiotags.Tags{Values: map[string][]string{"track": {"2"}, "total_tracks": {"8"}, "disc": {"1"}, "total_discs": {"2"}}})
	if err := verifyMusicMetadata(bytes.NewReader(changed), int64(len(changed)), expected); !errors.Is(err, ErrOutput) {
		t.Fatal("lost compound total was accepted", err)
	}
}

func TestMusicDownloadNumberMetadataSurvivesRealConversions(t *testing.T) {
	testtier.Media(t, "encodes actual FLAC/Opus/AAC downloads and verifies retained numbers and decoded samples")
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.flac")
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=sample_rate=44100:duration=0.05", "-ac", "2", "-c:a", "flac", "-metadata", "track=2/9", "-metadata", "disc=1/2", source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, output)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	tags := audiotags.Read(bytes.NewReader(raw), int64(len(raw)))
	if len(tags.Values["track"]) != 1 || tags.Values["track"][0] != "2/9" || len(tags.Values["disc"]) != 1 || tags.Values["disc"][0] != "1/2" {
		t.Fatal("source numbering missing", tags.Values)
	}
	bridge := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer bridge.Close()
	for _, codec := range []string{"flac", "opus", "aac"} {
		t.Run(codec, func(t *testing.T) {
			quality := "320"
			if codec == "flac" {
				quality = "cd-quality"
			}
			recipe := decoder.PreparedRecipe{AudioOnly: true, MusicQuality: quality, MusicCodec: codec, Source: decoder.PreparedSource{AudioCodec: "flac", AudioSampleRate: 44100, AudioBitDepth: 16, AudioChannels: []int{2}}}
			args, err := decoder.PreparedArgs(bridge.URL+"/source.flac", recipe)
			if err != nil {
				t.Fatal(err)
			}
			var diagnostic bytes.Buffer
			encode := exec.Command(ffmpeg, args...)
			encode.Stderr = &diagnostic
			encoded, err := encode.Output()
			if err != nil {
				t.Fatal(err, diagnostic.String())
			}
			plainPath := filepath.Join(root, codec+"-plain.media")
			if err := os.WriteFile(plainPath, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			plainDecode := exec.Command(ffmpeg, "-v", "error", "-xerror", "-i", plainPath, "-map", "0:a:0", "-f", "s16le", "-")
			diagnostic.Reset()
			plainDecode.Stderr = &diagnostic
			plainPCM, err := plainDecode.Output()
			if err != nil || len(plainPCM) == 0 {
				t.Fatal("plain prepared audio did not decode", codec, err, diagnostic.String())
			}
			for _, tc := range []struct {
				name string
				tags audiotags.Tags
			}{{"compound", tags}, {"orphan", audiotags.Tags{Values: map[string][]string{"total_tracks": {"9"}, "total_discs": {"2"}}}}} {
				var output bytes.Buffer
				if err := copyMusicMetadata(&output, bytes.NewReader(encoded), codec, tc.tags); err != nil {
					t.Fatal(tc.name, err)
				}
				if err := verifyMusicMetadata(bytes.NewReader(output.Bytes()), int64(output.Len()), tc.tags); err != nil {
					t.Fatal(tc.name, err)
				}
				path := filepath.Join(root, codec+"-"+tc.name+".media")
				if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				decode := exec.Command(ffmpeg, "-v", "error", "-xerror", "-i", path, "-map", "0:a:0", "-f", "s16le", "-")
				diagnostic.Reset()
				decode.Stderr = &diagnostic
				pcm, err := decode.Output()
				if err != nil || !bytes.Equal(pcm, plainPCM) {
					t.Fatal("decorated download did not decode", codec, tc.name, err, len(pcm), diagnostic.String())
				}
			}
		})
	}
}
