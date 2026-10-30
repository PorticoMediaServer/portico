package decoder

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAudioSeekMatchesFullPrefixTrim(t *testing.T) {
	bin, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	for _, codec := range []string{"flac", "mp3", "aac"} {
		t.Run(codec, func(t *testing.T) {
			ext, encoder := codec, codec
			if codec == "mp3" {
				encoder = "libmp3lame"
			}
			if codec == "aac" {
				ext = "m4a"
			}
			file := filepath.Join(dir, "tone."+ext)
			cmd := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=713:sample_rate=44100:duration=9", "-c:a", encoder, file)
			if b, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("fixture %s %v", b, e)
			}
			priming := int64(0)
			if codec == "mp3" {
				priming = 1105
			}
			if codec == "aac" {
				priming = 1024
			}
			spec := AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 10000000, AudioStart: priming + 6*44100, AudioFrames: 44100, AudioOut: 44100, AudioRate: 44100, AudioChannels: 1, AudioBitDepth: 16, AudioPriming: priming}
			convert := func(s AnalysisSpec) []byte {
				args, e := AnalysisArgs(server.URL+"/tone."+ext, s)
				if e != nil {
					t.Fatal(e)
				}
				cmd := exec.CommandContext(context.Background(), bin, args...)
				b, e := cmd.Output()
				if e != nil {
					t.Fatal(e)
				}
				decode := exec.Command(bin, "-v", "error", "-i", "pipe:0", "-f", "s16le", "pipe:1")
				decode.Stdin = bytes.NewReader(b)
				pcm, e := decode.Output()
				if e != nil {
					t.Fatal(e)
				}
				return pcm
			}
			full := convert(spec)
			spec.AudioSourceRate = 44100
			seek := convert(spec)
			if !bytes.Equal(full, seek) {
				maxDiff := 0
				different := 0
				for i := 0; i+1 < min(len(full), len(seek)); i += 2 {
					a := int(int16(binary.LittleEndian.Uint16(full[i:])))
					b := int(int16(binary.LittleEndian.Uint16(seek[i:])))
					diff := a - b
					if diff < 0 {
						diff = -diff
					}
					if diff > maxDiff {
						maxDiff = diff
					}
					if diff != 0 {
						different++
					}
				}
				if codec == "aac" && maxDiff <= 1 && len(full) == len(seek) {
					return
				}
				t.Fatalf("seek changed %s samples: %d/%d, max difference %d, different %d, first %v/%v", codec, len(full), len(seek), maxDiff, different, full[:24], seek[:24])
			}
		})
	}
}
