package vod

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"testing"
	"time"
)

func fixture(t *testing.T, audio string) string { return fixtureLengths(t, audio, 1299, 43.3) }
func fixtureLengths(t *testing.T, audio string, frames int, audioDuration float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "markers.mp4")
	args := []string{"-nostdin", "-v", "error", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", "320x64", "-r", "30", "-i", "pipe:0"}
	if audio != "" {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=0.4*sin(2*PI*(220*t+11*t*t)):s=48000:d=%.6f", audioDuration), "-map", "0:v", "-map", "1:a", "-c:a", audio, "-b:a", "192k")
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "10", "-pix_fmt", "yuv420p", "-bf", "0", "-g", "30", "-t", fmt.Sprintf("%.6f", math.Max(float64(frames)/30, audioDuration)+1), "-movflags", "+faststart", path)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, decodertest.QualifiedFFmpeg(t), args...)
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < frames; n++ {
		frame := make([]byte, 320*64*3)
		for y := 0; y < 64; y++ {
			for x := 0; x < 320; x++ {
				v := byte(20)
				if n&(1<<(x/20)) != 0 {
					v = 235
				}
				i := (y*320 + x) * 3
				frame[i] = v
				frame[i+1] = v
				frame[i+2] = v
			}
		}
		if _, e = in.Write(frame); e != nil {
			t.Fatal(e)
		}
	}
	in.Close()
	if e = cmd.Wait(); e != nil {
		t.Fatal(e, stderr.String())
	}
	return path
}
func firstMarker(t *testing.T, path string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, e := exec.CommandContext(ctx, decodertest.QualifiedFFmpeg(t), "-nostdin", "-v", "error", "-i", path, "-map", "0:v:0", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) != 320*64 {
		t.Fatal(len(raw))
	}
	n := 0
	for bit := 0; bit < 16; bit++ {
		if raw[32*320+bit*20+10] > 128 {
			n |= 1 << bit
		}
	}
	return n
}
func TestRealFiniteWindowsGlobalMarkersAndPacketEdges(t *testing.T) {
	for _, audio := range []string{"", "aac", "libopus"} {
		t.Run(fmt.Sprint("audio", audio), func(t *testing.T) {
			path := fixture(t, audio)
			f, e := os.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			enc, e := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
			if e != nil {
				t.Fatal(e)
			}
			input := testInput(t, f)
			s, e := enc.Analyze(context.Background(), input)
			if e != nil {
				t.Fatal("admission", e)
			}
			if math.Abs(s.Duration-43.3) > .05 {
				t.Fatal(s)
			}
			var reference []float64
			if audio != "" {
				reference = decodeAudio(t, path)
			}
			for n, index := range []int{0, 5, 1, 7, 5} {
				w, e := enc.Encode(context.Background(), s, input, Request{Index: index, GenerationDir: filepath.Join(t.TempDir(), fmt.Sprint("generation", n))})
				if e != nil {
					t.Fatal("window", index, e)
				}
				if marker := firstMarker(t, w.Path); marker != index*180 {
					t.Fatalf("index%d actual marker%d", index, marker)
				}
				if w.Files != 1 || w.Bytes <= 0 || w.Start-w.DecodeFrom > 12 {
					t.Fatal(w)
				}
				if audio != "" {
					audioLag(t, reference, w)
				}
				t.Logf("index=%d actual=%+v work=%+v", index, w.Streams, w.EncodeWork)
			}
		})
	}
}

func decodeAudio(t *testing.T, path string) []float64 {
	t.Helper()
	raw, err := exec.Command(decodertest.QualifiedFFmpeg(t), "-nostdin", "-v", "error", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float64, len(raw)/4)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
	}
	return out
}
func audioLag(t *testing.T, reference []float64, w Window) {
	t.Helper()
	audio := decodeAudio(t, w.Path)
	first := 0.0
	for _, s := range w.Streams {
		if s.Kind == "audio" {
			first = s.First
		}
	}
	for _, at := range []float64{w.Start + .3, w.End - .3} {
		center := int(math.Round((at - first) * 48000))
		source := int(math.Round(at * 48000))
		best := -2.0
		lag := 0
		for delta := -4800; delta <= 4800; delta += 8 {
			ab, aa, bb := 0.0, 0.0, 0.0
			valid := true
			for n := 0; n < 2048; n += 2 {
				a, b := center+n, source+delta+n
				if a < 0 || a >= len(audio) || b < 0 || b >= len(reference) {
					valid = false
					break
				}
				x, y := audio[a], reference[b]
				ab += x * y
				aa += x * x
				bb += y * y
			}
			if valid && aa > 0 && bb > 0 {
				score := ab / math.Sqrt(aa*bb)
				if score > best {
					best = score
					lag = delta
				}
			}
		}
		if best < .98 || math.Abs(float64(lag)/48000) > .040 {
			t.Fatalf("audio marker time %.3f lag %d correlation %.5f", at, lag, best)
		}
		t.Logf("audio sourceTime=%.3f lagSamples=%d correlation=%.6f", at, lag, best)
	}
}

func TestDescriptorIdentityHelper(t *testing.T) {
	if os.Getenv("VOD_FSTAT_HELPER") != "1" {
		return
	}
	f := os.NewFile(3, "source")
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() {
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(Identity{s.Size(), s.ModTime().UnixNano()})
	os.Exit(0)
}
func testInput(t *testing.T, f *os.File) Input {
	t.Helper()
	st, e := f.Stat()
	if e != nil {
		t.Fatal(e)
	}
	return Input{File: f, Identity: Identity{st.Size(), st.ModTime().UnixNano()}, Validate: func(ctx context.Context, file *os.File, want Identity) error {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDescriptorIdentityHelper$")
		cmd.Env = append(os.Environ(), "VOD_FSTAT_HELPER=1")
		cmd.ExtraFiles = []*os.File{file}
		raw, e := cmd.Output()
		if e != nil {
			return e
		}
		var got Identity
		if e = json.Unmarshal(raw, &got); e != nil {
			return e
		}
		if got != want {
			return ErrChanged
		}
		return nil
	}}
}

func TestBoundsAndCancellation(t *testing.T) {
	path := fixture(t, "aac")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	input := testInput(t, f)
	enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
	source, err := enc.Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		limits Limits
		kind   error
	}{
		{"output", Limits{OutputBytes: 1}, ErrBudget},
		{"probe", Limits{ProbeBytes: 1}, ErrBudget},
		{"deadline", Limits{EncodeTimeout: time.Nanosecond}, context.DeadlineExceeded},
		{"active-deadline", Limits{EncodeTimeout: 100 * time.Millisecond}, context.DeadlineExceeded},
		{"preroll", Limits{MaxPreroll: 1}, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounded, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), tc.limits)
			dir := filepath.Join(t.TempDir(), "generation")
			_, err := bounded.Encode(context.Background(), source, input, Request{Index: 5, GenerationDir: dir})
			if !errors.Is(err, tc.kind) {
				t.Fatalf("got %v want %v", err, tc.kind)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("failed generation retained: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := enc.Encode(ctx, source, input, Request{Index: 0, GenerationDir: filepath.Join(t.TempDir(), "cancel")}); err == nil {
		t.Fatal("cancel accepted")
	}
	changed := input
	changed.Identity.Size++
	if _, err := enc.Encode(context.Background(), source, changed, Request{Index: 0}); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "owned-elsewhere")
	if err := os.WriteFile(sentinel, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Encode(context.Background(), source, input, Request{Index: 0, GenerationDir: dir}); err == nil {
		t.Fatal("existing generation accepted")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("existing directory damaged", err)
	}
}

func TestOptionalRepresentativeSource(t *testing.T) {
	path := os.Getenv("PORTICO_VOD_TEST_SOURCE")
	if path == "" {
		t.Skip("optional readonly corpus source")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
	input := testInput(t, f)
	source, err := enc.Analyze(context.Background(), input)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Logf("representative source explicitly rejected: %v", err)
			return
		}
		t.Fatal("admission", err)
	}
	t.Logf("source duration=%f fps=%f audio=%s", source.Duration, source.FPS, source.AudioCodec)
	for _, index := range []int{0, 5, 1, IntervalCount(source.Duration) - 1} {
		w, err := enc.Encode(context.Background(), source, input, Request{Index: index, GenerationDir: filepath.Join(t.TempDir(), "generation")})
		if err != nil {
			t.Fatal("window", index, err)
		}
		t.Logf("index=%d preroll=%f streams=%+v bytes=%d", index, w.Start-w.DecodeFrom, w.Streams, w.Bytes)
	}
}

func TestUnequalTerminalExtents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames int
		audio  float64
		reject bool
	}{
		{"hold-video", 1296, 43.3, false}, {"pad-audio", 1299, 43.2, false}, {"pad-audio-limit", 1299, 43.05, false}, {"excessive", 1280, 43.3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fixtureLengths(t, "aac", tc.frames, tc.audio)
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
			input := testInput(t, f)
			source, err := enc.Analyze(context.Background(), input)
			if tc.reject {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatal("expected explicit excessive-tail rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(source.Duration-43.3) > .03 {
				t.Fatal(source)
			}
			w, err := enc.Encode(context.Background(), source, input, Request{Index: 7, GenerationDir: filepath.Join(t.TempDir(), "generation")})
			if err != nil {
				t.Fatal(err)
			}
			if firstMarker(t, w.Path) != 1260 {
				t.Fatal("incorrect final-window marker")
			}
			audioLag(t, decodeAudio(t, path), w)
			raw, err := exec.Command(decodertest.QualifiedFFmpeg(t), "-nostdin", "-v", "error", "-i", w.Path, "-map", "0:v:0", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1").Output()
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) < 320*64 {
				t.Fatal("no terminal video")
			}
			last := raw[len(raw)-320*64:]
			marker := 0
			for bit := 0; bit < 16; bit++ {
				if last[32*320+bit*20+10] > 128 {
					marker |= 1 << bit
				}
			}
			if marker != tc.frames-1 {
				t.Fatalf("last video marker %d want %d", marker, tc.frames-1)
			}
			t.Logf("videoEnd=%f audioEnd=%f extent=%f streams=%+v", source.VideoEnd, source.AudioEnd, source.Duration, w.Streams)
		})
	}
}

func TestIntervalLayout(t *testing.T) {
	for _, tc := range []struct {
		duration  float64
		count     int
		lastStart float64
	}{
		{0, 0, 0}, {math.NaN(), 0, 0}, {math.Inf(1), 0, 0}, {7201, 0, 0},
		{.023, 1, 0}, {6, 1, 0}, {6.023, 1, 0}, {6.25, 1, 0}, {6.251, 2, 6},
		{96, 16, 90}, {96.023, 16, 90}, {96.25, 16, 90}, {96.251, 17, 96},
	} {
		count := IntervalCount(tc.duration)
		if count != tc.count {
			t.Fatalf("duration %f count%d want%d", tc.duration, count, tc.count)
		}
		previous := 0.0
		for i := 0; i < count; i++ {
			start, end, ok := IntervalBounds(tc.duration, i)
			if !ok || start != previous || end <= start || end-start > 6.25 {
				t.Fatalf("duration%f index%d bounds%f..%f", tc.duration, i, start, end)
			}
			previous = end
			if i == count-1 && start != tc.lastStart {
				t.Fatal("last start", start)
			}
		}
		if count > 0 && previous != tc.duration {
			t.Fatal("extent lost")
		}
		if _, _, ok := IntervalBounds(tc.duration, count); ok {
			t.Fatal("out of range accepted")
		}
	}
}

func TestRealMergedTinyAudioTail(t *testing.T) {
	path := fixtureLengths(t, "aac", 2880, 96.023)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
	input := testInput(t, f)
	source, err := enc.Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if IntervalCount(source.Duration) != 16 || source.Duration <= 96 {
		t.Fatal("unexpected extent", source)
	}
	w, err := enc.Encode(context.Background(), source, input, Request{Index: 15, GenerationDir: filepath.Join(t.TempDir(), "merged")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Start != 90 || w.End != source.Duration || firstMarker(t, w.Path) != 2700 {
		t.Fatal(w)
	}
	audioLag(t, decodeAudio(t, path), w)
	if _, err := enc.Encode(context.Background(), source, input, Request{Index: 16, GenerationDir: filepath.Join(t.TempDir(), "extra")}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("tiny extra interval accepted", err)
	}
	t.Logf("extent=%f videoEnd=%f audioEnd=%f merged=%f..%f streams=%+v", source.Duration, source.VideoEnd, source.AudioEnd, w.Start, w.End, w.Streams)
}

func TestSubframeClipExplicitlyUnsupported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subframe.mp4")
	raw, err := exec.Command(decodertest.QualifiedFFmpeg(t), "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=s=320x64:r=120:d=0.025", "-c:v", "libx264", "-pix_fmt", "yuv420p", path).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
	_, err = enc.Analyze(context.Background(), testInput(t, f))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal("subframe/high-rate input must be explicitly unsupported", err)
	}
}

func TestGlobalAudioLatticeAndDecodedSeams(t *testing.T) {
	for _, codec := range []string{"aac", "libopus"} {
		t.Run(codec, func(t *testing.T) {
			path := fixture(t, codec)
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			enc, _ := NewEncoder(decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t), Limits{})
			input := testInput(t, f)
			source, err := enc.Analyze(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			var joined []byte
			var previous float64
			for i := 0; i < 3; i++ {
				w, err := enc.Encode(context.Background(), source, input, Request{Index: i, GenerationDir: filepath.Join(t.TempDir(), "window")})
				if err != nil {
					t.Fatal(i, err)
				}
				for _, stream := range w.Streams {
					if stream.Kind == "audio" {
						if math.Abs(stream.First-previous) > 0.0000223 {
							t.Fatalf("audio seam gap/overlap: %.9f -> %.9f", previous, stream.First)
						}
						previous = stream.End
					}
				}
				b, err := os.ReadFile(w.Path)
				if err != nil {
					t.Fatal(err)
				}
				joined = append(joined, b...)
			}
			output := filepath.Join(t.TempDir(), "joined.ts")
			if err = os.WriteFile(output, joined, 0600); err != nil {
				t.Fatal(err)
			}
			reference := decodeAudio(t, path)
			for _, boundary := range []float64{6, 12} {
				// Include the first decoded frame after the actual independently encoded seam.
				audioLag(t, reference, Window{Path: output, Start: boundary - .32, End: boundary + .30, Streams: []StreamFacts{{Kind: "audio", First: 0}}})
			}
		})
	}
}
