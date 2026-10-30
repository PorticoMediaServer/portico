package mediaanalysis

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"testing"
	"time"
)

func TestMusicFullResolutionLoudnessR29(t *testing.T) {
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	for _, tc := range []struct {
		name, expr string
		rate       int
		want       float64
	}{{"stereo96k", "0.1*sin(2*PI*1000*t)|0.1*sin(2*PI*1000*t)", 96000, -20.0}, {"lfe_only", "0|0|0|0.1*sin(2*PI*1000*t)|0|0", 48000, -70}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tone.wav")
			_, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=%s:s=%d:d=4", tc.expr, tc.rate), "-c:a", "pcm_f64le", "-f", "wav", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.ServeContent(w, r, "tone.wav", time.Time{}, bytes.NewReader(source))
			}))
			defer srv.Close()
			args, err := decoder.AnalysisArgs(srv.URL, decoder.AnalysisSpec{Kind: "loudness_full", MaxDurationUS: 4_000_000})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(ff, args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v %s", err, stderr.String())
			}
			m, err := readFullLoudness(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(m["integratedLUFS"].(float64)-tc.want) > .3 {
				t.Fatal(m)
			}
			if m["truePeakLinear"].(float64) < .099 || len(m["startEnvelopeLUFS"].([]float64)) != 30 || len(m["endEnvelopeLUFS"].([]float64)) != 30 {
				t.Fatal(m)
			}
		})
	}
	if _, e := readFullLoudness(bytes.NewBufferString("lavfi.r128.M=NaN")); e == nil {
		t.Fatal("nonfinite metadata accepted")
	}
	if _, e := readFullLoudness(io.LimitReader(bytes.NewReader(nil), 0)); e == nil {
		t.Fatal("empty output accepted")
	}
}
