package subtitlevideo

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type analysisProbeInput struct {
	fixtureCachedSource
	failAt   int
	conflict error
}

func (i *analysisProbeInput) Validate(context.Context) error {
	i.validations++
	if i.validations == i.failAt {
		return i.conflict
	}
	return nil
}

func TestAnalysisProbeReprovesRetainedInput(t *testing.T) {
	tool, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1) here")
	}
	tool, err = filepath.EvalSymlinks(tool)
	if err != nil {
		t.Fatal(err)
	}
	conflict := errors.New("retained source changed")
	for _, failAt := range []int{0, 1, 2} {
		t.Run(map[int]string{0: "valid", 1: "before", 2: "after"}[failAt], func(t *testing.T) {
			input := &analysisProbeInput{failAt: failAt, conflict: conflict}
			sup := &storage.Supervisor{Limit: 1}
			r := &Runtime{ffprobe: tool, storage: &storage.Client{Supervisor: sup}}
			probe := r.AnalysisProbe(input)
			data, err := probe(context.Background(), "retained-fixture", decoder.AnalysisProbeSpec{Kind: decoder.ProbeFrameCadence})
			if failAt > 0 {
				if !errors.Is(err, conflict) || data != nil {
					t.Fatalf("accepted changed source: %q %v", data, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := 2
			if failAt == 1 {
				want = 1
			}
			if input.validations != want || input.closes != 0 || sup.Active() != 0 {
				t.Fatalf("retained source validations=%d closes=%d active=%d", input.validations, input.closes, sup.Active())
			}
			if failAt == 0 {
				if _, err := probe(context.Background(), "retained-fixture-hdr", decoder.AnalysisProbeSpec{Kind: decoder.ProbeFrameHDR}); err != nil {
					t.Fatal(err)
				}
				if input.validations != 4 || input.closes != 0 {
					t.Fatal("did not reuse retained input", input)
				}
			}
		})
	}
}
