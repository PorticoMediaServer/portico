package mediaanalysis

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"testing"
)

type fragmentedPCM struct{ input *bytes.Reader }

func (r fragmentedPCM) Read(b []byte) (int, error) { return r.input.Read(b[:min(len(b), 7)]) }
func pcmTone(hz float64, seconds int) []byte {
	data := make([]byte, 11025*seconds*2)
	for i := 0; i < len(data)/2; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(math.Sin(2*math.Pi*hz*float64(i)/11025)*16000)))
	}
	return data
}
func TestAnalysisPCMBoundedAndFragmentIndependent(t *testing.T) {
	raw := pcmTone(440, 2)
	for _, stage := range []string{"waveform", "fingerprint"} {
		whole, err := analyzePCM(bytes.NewReader(raw), 2000000, stage)
		if err != nil {
			t.Fatal(err)
		}
		fragmented, err := analyzePCM(fragmentedPCM{bytes.NewReader(raw)}, 2000000, stage)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(whole)
		b, _ := json.Marshal(fragmented)
		if !bytes.Equal(a, b) {
			t.Fatalf("%s depends on read boundaries", stage)
		}
		if len(a) > 200000 {
			t.Fatalf("unbounded %s output", stage)
		}
	}
	view, err := analyzePCM(bytes.NewReader(raw), 2000000, "waveform")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	var wave struct {
		Duration string                        `json:"decodedDurationUS"`
		Points   []struct{ Peak, RMS float64 } `json:"points"`
	}
	if err = json.Unmarshal(encoded, &wave); err != nil {
		t.Fatal(err)
	}
	if wave.Duration != "2000000" || len(wave.Points) > 2048 || len(wave.Points) == 0 {
		t.Fatalf("invalid waveform: %s", encoded)
	}
	for _, p := range wave.Points {
		if p.Peak < 0 || p.Peak > 1 || p.RMS < 0 || p.RMS > p.Peak {
			t.Fatal("invalid amplitudes")
		}
	}
	a, _ := analyzePCM(bytes.NewReader(pcmTone(220, 2)), 2000000, "fingerprint")
	b, _ := analyzePCM(bytes.NewReader(pcmTone(1800, 2)), 2000000, "fingerprint")
	ea, _ := json.Marshal(a)
	eb, _ := json.Marshal(b)
	if bytes.Equal(ea, eb) {
		t.Fatal("fingerprints ignore spectral content")
	}
}
func TestAnalysisPCMRejectsMalformedAndExcessiveInput(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, {1, 2, 3}} {
		if _, err := analyzePCM(bytes.NewReader(data), 1000000, "waveform"); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("malformed PCM: %v", err)
		}
	}
	if _, err := analyzePCM(bytes.NewReader(make([]byte, 11025*62*2)), 1000000, "fingerprint"); !errors.Is(err, ErrBudget) {
		t.Fatalf("duration budget: %v", err)
	}
	if _, err := analyzePCM(bytes.NewReader([]byte{0, 0}), 0, "waveform"); !errors.Is(err, ErrBudget) {
		t.Fatalf("zero duration: %v", err)
	}
}
