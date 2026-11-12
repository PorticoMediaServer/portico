package mediafacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
)

// The scan's packet clock must match a full read, including codecs whose tail seeks
// and encoder-delay clocks differ.
func TestMeasurePacketsMatchesAFullPacketRead(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ffprobe := decodertest.QualifiedFFprobe(t)
	dir := t.TempDir()
	// Speech-like: loud and quiet stretches, so a variable bitrate varies.
	sound := "anoisesrc=d=95:c=pink:r=44100:a=0.3,volume='if(lt(mod(t,8),4),1,0.05)':eval=frame"
	for _, c := range []struct {
		name string
		args []string
	}{
		{"headed.mp3", []string{"-c:a", "libmp3lame", "-q:a", "4"}},
		{"headerless.mp3", []string{"-c:a", "libmp3lame", "-q:a", "4", "-write_xing", "0"}},
		{"adts.aac", []string{"-c:a", "aac", "-b:a", "64k"}},
		{"pcm.wav", []string{"-c:a", "pcm_s16le"}},
		{"wma.wma", []string{"-c:a", "wmav2", "-b:a", "64k"}},
		{"vorbis.ogg", []string{"-c:a", "libvorbis", "-q:a", "4"}},
		{"aac.m4a", []string{"-c:a", "aac", "-b:a", "64k"}},
		{"opus.ogg", []string{"-ar", "48000", "-c:a", "libopus", "-b:a", "32k"}},
		{"lossless.flac", []string{"-c:a", "flac"}},
	} {
		path := filepath.Join(dir, c.name)
		args := append(append([]string{"-v", "error", "-y", "-f", "lavfi", "-i", sound}, c.args...), path)
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
		}
		if c.name == "adts.aac" {
			// A long raw AAC has no tail index. Its packet list exceeds the probe's
			// output bound; counting ADTS frames keeps that measurement bounded.
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, bytes.Repeat(raw, 8), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		facts, err := Probe{}.Inspect(context.Background(), path)
		if err == nil {
			err = (Probe{}).CompleteAudioPackets(context.Background(), path, &facts)
		}
		if err != nil || facts.Packets == nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := *facts.Packets
		want := fullPacketRead(t, ffprobe, path)
		if d := got.LengthUS - want.LengthUS; d < -1 || d > 1 {
			t.Errorf("%s: packets measured as %d µs, a full read says %d µs", c.name, got.LengthUS, want.LengthUS)
		}
		if got.StartUS != want.StartUS {
			t.Errorf("%s: first packet measured at %d µs, a full read says %d µs", c.name, got.StartUS, want.StartUS)
		}
		t.Logf("%s: gapless %.6f s, packets %.6f s from %d µs", c.name, facts.Duration, float64(got.LengthUS)/1e6, got.StartUS)
	}
	for _, name := range []string{"tone.ape", "tone-sv7.mpc", "tone-sv8.mpc"} {
		path, err := filepath.Abs(filepath.Join("../decoder/testdata/music-formats-r29", name))
		if err != nil {
			t.Fatal(err)
		}
		facts, err := Probe{}.Inspect(context.Background(), path)
		if err == nil {
			err = (Probe{}).CompleteAudioPackets(context.Background(), path, &facts)
		}
		if err != nil || facts.Packets == nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := fullPacketRead(t, ffprobe, path)
		if d := facts.Packets.LengthUS - want.LengthUS; d < -1 || d > 1 {
			t.Errorf("%s: measured %d us, full read %d us", name, facts.Packets.LengthUS, want.LengthUS)
		}
	}

}

// fullPacketRead reads every packet of the file's sound.
func fullPacketRead(t *testing.T, ffprobe, path string) Packets {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=time_base:format=start_time:packet=pts,duration",
		"-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Streams []struct {
			TimeBase string `json:"time_base"`
		} `json:"streams"`
		Format struct {
			Start string `json:"start_time"`
		} `json:"format"`
		Packets []struct{ PTS, Duration int64 } `json:"packets"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.Packets) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	base, _ := new(big.Rat).SetString(doc.Streams[0].TimeBase)
	start := new(big.Rat)
	if doc.Format.Start != "" {
		start.SetString(doc.Format.Start)
	}
	first := new(big.Rat).Mul(big.NewRat(doc.Packets[0].PTS, 1), base)
	var end int64
	for _, p := range doc.Packets {
		end = max(end, p.PTS+p.Duration)
	}
	last := new(big.Rat).Mul(big.NewRat(end, 1), base)
	return Packets{StartUS: microseconds(new(big.Rat).Sub(first, start)), LengthUS: microseconds(new(big.Rat).Sub(last, first))}
}

// A second read is optional: even a tool failure or its own deadline must retain
// everything the ordinary probe successfully found. Only the scan may cancel it.
func TestPacketFailurePreservesFacts(t *testing.T) {
	for _, failure := range []error{&exec.ExitError{}, context.DeadlineExceeded, errors.New("remote read failed"), ErrNoPackets} {
		t.Run(failure.Error(), func(t *testing.T) {
			original := Facts{Container: "wav", AudioCodec: "pcm_s16le", Duration: 12.5, BitRate: 705600}
			facts := original
			p := Probe{ScanCommand: func(context.Context, string, []string, string, ScanMode, io.Writer) (bool, error) {
				return true, failure
			}}
			if err := p.CompleteAudioPackets(context.Background(), "track.wav", &facts); err != nil {
				t.Fatal(err)
			}
			if facts.Packets == nil || *facts.Packets != (Packets{}) {
				t.Fatalf("failure was not recorded as unmeasured: %+v", facts.Packets)
			}
			facts.Packets = nil
			if !reflect.DeepEqual(facts, original) {
				t.Fatalf("successful facts changed: %+v", facts)
			}
			scan, cancel := context.WithCancel(context.Background())
			cancel()
			if err := p.CompleteAudioPackets(scan, "track.wav", &facts); !errors.Is(err, context.Canceled) {
				t.Fatalf("scan cancellation: %v", err)
			}
		})
	}
}

// Older CBR encoders often omit Xing/Info/VBRI. They need bounded sampling, not
// a full read; a VBR file with the same missing header still needs a true count.
func TestMP3LengthOnlyWalksVariableBitrate(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	for _, variable := range []bool{false, true} {
		name := "cbr"
		codec := []string{"-c:a", "libmp3lame", "-b:a", "192k", "-write_xing", "0"}
		if variable {
			name = "vbr"
			codec = []string{"-c:a", "libmp3lame", "-q:a", "4", "-write_xing", "0"}
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "headerless.mp3")
			args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "anoisesrc=d=95:c=pink:r=44100:a=0.3,volume='if(lt(t,3),0.01,1)':eval=frame"}
			args = append(append(args, codec...), path)
			if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
				decodertest.Unavailable(t, fmt.Sprintf("fixture: %v %s", err, out))
			}
			facts, err := (Probe{}).Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			before := facts
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			var bytes int64
			p := Probe{OpenReader: func(context.Context, string) (io.ReaderAt, int64, func() error, error) {
				return countedMP3Reader{f, &bytes}, info.Size(), func() error { return nil }, nil
			}}
			if err := p.CompleteMP3Length(context.Background(), path, &facts); err != nil {
				t.Fatal(err)
			}
			if variable {
				if bytes < info.Size() || facts.Duration == before.Duration {
					t.Fatalf("VBR was not counted: read %d of %d, duration %f -> %f", bytes, info.Size(), before.Duration, facts.Duration)
				}
				want := fullPacketRead(t, decodertest.QualifiedFFprobe(t), path)
				if math.Abs(facts.Duration*1e6-float64(want.LengthUS)) > 1 {
					t.Fatalf("VBR length %f, want %d us", facts.Duration, want.LengthUS)
				}
			} else if bytes > (64<<10)+5*8192+20 || !reflect.DeepEqual(facts, before) {
				t.Fatalf("CBR needed only header and samples, keeping its ordinary facts: read %d of %d, duration %f -> %f", bytes, info.Size(), before.Duration, facts.Duration)
			}
		})
	}
}

type countedMP3Reader struct {
	io.ReaderAt
	count *int64
}

func (r countedMP3Reader) ReadAt(b []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(b, off)
	*r.count += int64(n)
	return n, err
}
