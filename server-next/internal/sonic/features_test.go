package sonic

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func pcm(samples int, frequency float64) []byte {
	out := make([]byte, samples*4)
	for i := 0; i < samples; i++ {
		value := float32(.25 * math.Sin(2*math.Pi*frequency*float64(i)/SampleRate))
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	return out
}

func TestLogMelPatchesFollowMeasuredFramesAndShortTrackPadding(t *testing.T) {
	for _, test := range []struct{ samples, patches int }{{1, 1}, {1000, 1}, {47616, 1}, {47872, 1}, {95488, 2}, {97000, 2}} {
		count := 0
		n, err := LogMel(bytes.NewReader(pcm(test.samples, 1000)), int64(test.samples), func(patch []float32) error {
			if len(patch) != PatchFrames*MelBands {
				t.Fatal("different original model input size")
			}
			for _, v := range patch {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 {
					t.Fatal("invalid numeric feature")
				}
			}
			count++
			return nil
		})
		if err != nil || n != test.patches || count != n {
			t.Fatalf("%d measured samples: %d patches (%d calls), %v", test.samples, n, count, err)
		}
	}
}

func TestLogMelRejectsCorruptInputBudgetAndConsumerFailure(t *testing.T) {
	for _, input := range [][]byte{nil, {0, 0, 0}, {0, 0, 128, 127}} {
		if _, err := LogMel(bytes.NewReader(input), 100, func([]float32) error { return nil }); err == nil {
			t.Fatal("accepted empty, partial or non-finite source PCM")
		}
	}
	if _, err := LogMel(bytes.NewReader(pcm(100, 440)), 99, func([]float32) error { return nil }); err == nil {
		t.Fatal("read beyond source sample budget")
	}
	stop := errors.New("consumer stopped")
	if _, err := LogMel(bytes.NewReader(pcm(100, 440)), 100, func([]float32) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("lost consumer cancellation")
	}
}

func TestOriginalPowerQuantizationRoundsHalfToEven(t *testing.T) {
	for _, test := range []struct{ in, want float32 }{{1, 1}, {1 + 1./2048, 1}, {1 + 3./2048, 1 + 2./1024}, {1. / (1 << 25), 0}, {3. / (1 << 25), 2. / (1 << 24)}} {
		if got := halfPower(test.in); got != test.want {
			t.Fatalf("float16(%v) = %v, want %v", test.in, got, test.want)
		}
	}
}

func TestLogMelActualLibrosaReference(t *testing.T) {
	directory := os.Getenv("PORTICO_SONIC_REFERENCE_DIR")
	if directory == "" {
		t.Skip("actual librosa numeric reference is supplied by the model conformance job")
	}
	for _, name := range []string{"short", "boundary", "two-patches"} {
		input, err := os.ReadFile(filepath.Join(directory, name+".pcm"))
		if err != nil {
			t.Fatal(err)
		}
		reference, err := os.ReadFile(filepath.Join(directory, name+".mel"))
		if err != nil || len(reference) == 0 {
			t.Fatalf("missing independent reference %s: %v", name, err)
		}
		r := bytes.NewReader(reference)
		var maximum float64
		patches, err := LogMel(bytes.NewReader(input), int64(len(input)/4), func(patch []float32) error {
			var raw [4]byte
			for _, actual := range patch {
				if _, e := io.ReadFull(r, raw[:]); e != nil {
					return e
				}
				expected := math.Float32frombits(binary.LittleEndian.Uint32(raw[:]))
				maximum = max(maximum, math.Abs(float64(actual)-float64(expected)))
			}
			return nil
		})
		// Float32 FFT/BLAS operation ordering may straddle one float16 power
		// boundary. This bound is one half-power ULP after the logarithm.
		if err != nil || r.Len() != 0 || maximum > .0005 {
			t.Fatalf("%s: %d patches, maximum absolute error %g, remaining %d, %v", name, patches, maximum, r.Len(), err)
		}
		t.Logf("%s actual librosa reference: %d patches; max error %g", name, patches, maximum)
	}
}
