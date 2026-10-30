package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
)

func TestMusicAudioDecodeContainerAliasesR36(t *testing.T) {
	for _, tc := range []struct {
		declared, measured string
		want               bool
	}{
		{"mov", "mp4", true}, {"m4a", "mp4", true}, {"m4b", "mp4", true},
		{"mp4", "mov", true}, {"matroska", "mka", true}, {"mkv", "mka", true},
		{"mka", "matroska", true}, {"aac", "adts", true}, {"adts", "aac", true},
		{"webm", "mka", false}, {"matroska", "webm", false},
		{"mp4", "mka", false}, {"ogg", "adts", false},
	} {
		t.Run(tc.declared+"/"+tc.measured, func(t *testing.T) {
			cap := AudioDecodeCap{Codec: "aac", Containers: []string{tc.declared}, MaxSampleRate: 48000, MaxBitDepth: 24, MaxChannels: 2}
			f := audiofacts.Facts{Container: tc.measured, Codec: "aac", SampleRate: 44100, BitDepth: 16, Channels: 2}
			if got := cap.decodes(f); got != tc.want {
				t.Fatalf("decode %s from %s: %v, want %v", f.Codec, f.Container, got, tc.want)
			}
			if _, got := findCap([]AudioDecodeCap{cap}, f.Codec, f.Container); got != tc.want {
				t.Fatalf("conversion target %s/%s: %v, want %v", f.Container, f.Codec, got, tc.want)
			}
			if !tc.want {
				return
			}
			for _, change := range []func(*audiofacts.Facts){
				func(f *audiofacts.Facts) { f.Codec = "flac" },
				func(f *audiofacts.Facts) { f.SampleRate = 96000 },
				func(f *audiofacts.Facts) { f.BitDepth = 32 },
				func(f *audiofacts.Facts) { f.Channels = 6 },
			} {
				other := f
				change(&other)
				if cap.decodes(other) {
					t.Fatal("container alias widened decoder limits", other)
				}
			}
		})
	}
}
