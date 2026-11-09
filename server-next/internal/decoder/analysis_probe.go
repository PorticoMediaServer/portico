package decoder

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// AnalysisProbeKind selects a package-owned observation. These are internal
// decoder recipes, not scan permissions: the durable stage proves its own grant.
type AnalysisProbeKind string

const (
	ProbeFrameCadence AnalysisProbeKind = "frame_cadence"
	ProbeFrameHDR     AnalysisProbeKind = "frame_hdr"
	ProbePacketPeak   AnalysisProbeKind = "packet_peak"
)

type AnalysisProbeSpec struct {
	Kind         AnalysisProbeKind
	PhysicalLock *os.File
}

type probeRecipe struct {
	query    []string
	limit    int64
	deadline time.Duration
}

func analysisProbeRecipe(kind AnalysisProbeKind) (probeRecipe, error) {
	switch kind {
	case ProbeFrameCadence:
		return probeRecipe{
			[]string{"-read_intervals", "%+#240", "-show_frames", "-show_entries", "frame=best_effort_timestamp_time,interlaced_frame,repeat_pict", "-of", "json"},
			4 << 20,
			12 * time.Second,
		}, nil
	case ProbeFrameHDR:
		return probeRecipe{[]string{"-read_intervals", "%+#1", "-show_frames", "-show_entries", "frame=media_type,width,height,side_data_list", "-of", "json"}, 1 << 20, 8 * time.Second}, nil
	case ProbePacketPeak:
		return probeRecipe{[]string{"-show_packets", "-show_entries", "packet=dts_time,size", "-of", "csv=p=0"}, 32 << 20, 90 * time.Second}, nil
	default:
		return probeRecipe{}, ErrInvalidConfiguration
	}
}

func (r probeRecipe) args(input string) []string {
	args := []string{
		"-v",
		"error",
		"-threads",
		"1",
		"-protocol_whitelist",
		"http,tcp",
		"-format_whitelist",
		mediaexec.LocalInputFormats,
		"-probesize",
		"8388608",
		"-analyzeduration",
		"5000000",
		"-select_streams",
		"v:0",
	}
	args = append(args, r.query...)
	return append(args, input)
}

// RunAnalysisProbe runs a fixed observation through the retained-input bridge.
// Failed, cancelled or oversized output is never returned as usable evidence.
// The bridge and optional physical lock stay borrowed until process retirement.
func RunAnalysisProbe(
	ctx context.Context,
	supervisor *processpool.Supervisor,
	key, executable, input string,
	reservation *EndpointReservation,
	spec AnalysisProbeSpec,
	libraries ...string,
) ([]byte, error) {
	recipe, err := analysisProbeRecipe(spec.Kind)
	if err != nil || supervisor == nil || key == "" {
		if reservation != nil {
			_ = reservation.Close()
		}
		return nil, ErrInvalidConfiguration
	}
	cmd, err := probeJobCommand(executable, input, reservation, recipe.args(input), true, libraries...)
	if err != nil {
		if reservation != nil {
			_ = reservation.Close()
		}
		return nil, err
	}
	if spec.PhysicalLock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, spec.PhysicalLock)
	}
	endpoint, _ := bridgeEndpoint(input) // proved by probeJobCommand
	ctx, cancel := context.WithTimeout(ctx, recipe.deadline)
	defer cancel()
	return runAnalysisProbeOwned(ctx, supervisor, key, cmd, endpoint, reservation, recipe.limit)
}

func runAnalysisProbeOwned(ctx context.Context, supervisor *processpool.Supervisor, key string, cmd *exec.Cmd, endpoint string, reservation *EndpointReservation, limit int64) ([]byte, error) {
	if !reservation.claim(endpoint, cmd) {
		return nil, ErrReservationInUse
	}
	retired := make(chan struct{})
	var data []byte
	err := supervisor.RunOwnedCompletion(ctx, "analysis:probe:"+key, cmd, func(reader io.Reader) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > limit {
			return ErrProbeOutput
		}
		return nil
	}, nil, func() { reservation.retire(); close(retired) })
	<-retired
	if err != nil {
		return nil, err
	}
	return data, nil
}
