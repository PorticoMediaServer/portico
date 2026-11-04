package subtitlevideo

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type analysisProbeInput struct {
	fixtureCachedSource
	failAt   int
	conflict error
}

type retainedProbeMedia struct {
	data        []byte
	validations int
}

func (i *retainedProbeMedia) Evidence() string               { return "exact-generated-dv-clip" }
func (i *retainedProbeMedia) Size() int64                    { return int64(len(i.data)) }
func (i *retainedProbeMedia) Validate(context.Context) error { i.validations++; return nil }
func (i *retainedProbeMedia) Close() error                   { return nil }
func (i *retainedProbeMedia) ReadExtent(_ context.Context, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 1 || offset+length > int64(len(i.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	return append([]byte(nil), i.data[offset:offset+length]...), nil
}

func TestAnalysisProbeReadsRealFramesAndPacketsThroughRetainedBridge(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "video", "dovi-probe-r40", "source-MEL.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	input := &retainedProbeMedia{data: raw}
	sup := &storage.Supervisor{Limit: 1}
	r := &Runtime{ffprobe: decodertest.QualifiedFFprobe(t), storage: &storage.Client{Supervisor: sup}}
	probe := r.AnalysisProbe(input)
	detail := assets.StreamDetail{DynamicRange: assets.RangeDolbyVision, DolbyVisionProfile: 7, EnhancementLayer: true}
	frames, err := probe(context.Background(), "real-cadence", decoder.AnalysisProbeSpec{Kind: decoder.ProbeFrameCadence})
	if err != nil {
		t.Fatal(err)
	}
	if err = assets.ApplyFrameCadence(frames, &detail); err != nil {
		t.Fatal(err)
	}
	hdr, err := probe(context.Background(), "real-hdr", decoder.AnalysisProbeSpec{Kind: decoder.ProbeFrameHDR})
	if err != nil {
		t.Fatal(err)
	}
	if err = assets.ApplyFrameHDR(hdr, &detail); err != nil {
		t.Fatal(err)
	}
	packets, err := probe(context.Background(), "real-packets", decoder.AnalysisProbeSpec{Kind: decoder.ProbePacketPeak})
	if err != nil {
		t.Fatal(err)
	}
	if !detail.DolbyVisionMetadataKnown || !detail.DolbyVisionRPU || detail.DolbyVisionEnhancementType != "mel" || assets.PacketPeak(packets) <= 0 || input.validations < 6 || sup.Active() != 0 {
		t.Fatal("retained real observations lost", detail, input.validations, sup.Active())
	}
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
