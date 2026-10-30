package assets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMusepackBasicProbeUsesValidatedHeaderTimeline(t *testing.T) {
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("actual ffprobe unavailable")
	}
	for _, codec := range []string{"mpc7", "mpc8"} {
		t.Run(codec, func(t *testing.T) {
			name := "tone-sv7.mpc"
			if codec == "mpc8" {
				name = "tone-sv8.mpc"
			}
			path, err := filepath.Abs(filepath.Join("../decoder/testdata/music-formats-r29", name))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := (Probe{Binary: probe}).InspectScan(context.Background(), path)
			if err != nil || facts.AudioCodec != codec || facts.Duration != .5 || len(facts.Streams) != 1 || facts.Streams[0].Codec != codec || facts.Streams[0].Detail.SampleRate != 44100 || facts.Streams[0].Channels != 2 {
				t.Fatalf("Basic probe did not compose real header timeline: %+v %v", facts, err)
			}
		})
	}
}

func TestMusepackHeaderCannotBypassObservedFactsOrReadAuthority(t *testing.T) {
	path, err := filepath.Abs("../decoder/testdata/music-formats-r29/tone-sv8.mpc")
	if err != nil {
		t.Fatal(err)
	}
	facts := Facts{AudioCodec: "mpc8", Streams: []Stream{{Type: "audio", Codec: "mpc8", Channels: 2, Detail: StreamDetail{SampleRate: 48000}}}}
	if err := (Probe{}).completeMusepackTimeline(context.Background(), path, &facts); err == nil {
		t.Fatal("mismatched observed sample rate accepted")
	}
	facts.Streams[0].Detail.SampleRate = 44100
	denied := errors.New("scan source no longer admitted")
	if err := (Probe{ReadGuard: func(context.Context, string) error { return denied }}).completeMusepackTimeline(context.Background(), path, &facts); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[10] ^= 1
	bad := filepath.Join(t.TempDir(), "bad.mpc")
	if err := os.WriteFile(bad, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Probe{}).completeMusepackTimeline(context.Background(), bad, &facts); err == nil {
		t.Fatal("bad header CRC accepted")
	}
}
