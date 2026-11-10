package mediaanalysis

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// A 997 Hz sine at -20 dBFS on both channels measures -20 LUFS (BS.1770's
// reference), its true peak is its amplitude, and the one decode also yields
// the waveform.
func TestSharedAudioMeasuresReferenceLoudness(t *testing.T) {
	var raw []byte
	for i := 0; i < sharedRate*10; i++ {
		v := int16(math.Round(0.1 * math.Sin(2*math.Pi*997*float64(i)/sharedRate) * 32767))
		raw = binary.LittleEndian.AppendUint16(raw, uint16(v))
		raw = binary.LittleEndian.AppendUint16(raw, uint16(v))
	}
	out, err := analyzeSharedAudio(bytes.NewReader(raw), 10000000, []string{"waveform", "loudness"})
	if err != nil {
		t.Fatal(err)
	}
	loudness := out["loudness"].(map[string]any)
	if got := loudness["integratedLUFS"].(float64); math.Abs(got+20) > 0.3 {
		t.Fatalf("integrated loudness %v, want -20", got)
	}
	if peak := loudness["truePeakLinear"].(float64); peak < 0.099 || peak > 0.103 {
		t.Fatalf("true peak %v, want 0.1", peak)
	}
	if out["waveform"] == nil || out["fingerprint"] != nil {
		t.Fatalf("stages: %v", out)
	}
}
