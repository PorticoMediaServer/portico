package decoder

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var ErrReservationInUse = errors.New("decoder endpoint reservation is owned by a physical process")
var ErrProbeOutput = errors.New("decoder output exceeds limit")

const MaxProbeOutputBytes = 1 << 20

// RunProbe consumes a fresh endpoint reservation. It never returns while a
// started process still owns that reservation: cancellation kills the process,
// and actual supervisor retirement releases the listening descriptors. The
// caller must keep its preparation borrow and bridge until this function ends.
func RunProbe(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, reservation *EndpointReservation, libraries ...string) ([]byte, error) {
	cmd, err := ProbeCommand(executable, input, reservation, libraries...)
	if err != nil {
		if reservation != nil {
			reservation.Close()
		}
		return nil, err
	}
	endpoint, err := bridgeEndpoint(input)
	if err != nil {
		return nil, err
	}
	return runOwned(ctx, supervisor, key, cmd, endpoint, reservation)
}

// RunProbeQuery runs a closed timeline query with RunProbe's reservation,
// physical retirement supervision, and MaxProbeOutputBytes output bound.
func RunProbeQuery(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, reservation *EndpointReservation, query []string, libraries ...string) ([]byte, error) {
	cmd, err := ProbeQueryCommand(executable, input, reservation, query, libraries...)
	if err != nil {
		if reservation != nil {
			_ = reservation.Close()
		}
		return nil, err
	}
	endpoint, err := bridgeEndpoint(input)
	if err != nil {
		return nil, err
	}
	return runOwned(ctx, supervisor, key, cmd, endpoint, reservation)
}

// RunAllocatedProbe validates bytes owned by an already-admitted recording.
// Source capacity/worker claims govern admission; output and deadlines remain bounded.
func RunAllocatedProbe(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, reservation *EndpointReservation, libraries ...string) ([]byte, error) {
	cmd, e := ProbeCommand(executable, input, reservation, libraries...)
	if e != nil {
		if reservation != nil {
			_ = reservation.Close()
		}
		return nil, e
	}
	endpoint, e := bridgeEndpoint(input)
	if e != nil {
		return nil, e
	}
	return runOwnedAdmission(ctx, supervisor, key, cmd, endpoint, reservation, true)
}

func runOwned(ctx context.Context, supervisor *storage.Supervisor, key string, cmd *exec.Cmd, endpoint string, reservation *EndpointReservation) ([]byte, error) {
	return runOwnedAdmission(ctx, supervisor, key, cmd, endpoint, reservation, false)
}
func runOwnedAdmission(ctx context.Context, supervisor *storage.Supervisor, key string, cmd *exec.Cmd, endpoint string, reservation *EndpointReservation, allocated bool) ([]byte, error) {
	if supervisor == nil || key == "" || cmd == nil {
		if reservation != nil {
			reservation.Close()
		}
		return nil, ErrInvalidConfiguration
	}
	if !reservation.claim(endpoint, cmd) {
		return nil, ErrReservationInUse
	}
	ctx, progress := progressio.NewDormant(ctx, 2*time.Minute)
	defer progress.Close()
	retired := make(chan struct{})
	var data []byte
	run := supervisor.RunOwnedCompletion
	if allocated {
		run = supervisor.RunOwnedLinear
	}
	err := run(ctx, "playback:probe:"+key, cmd, func(reader io.Reader) error {
		var e error
		progress.Ping()
		data, e = io.ReadAll(io.LimitReader(progress.Reader(reader), MaxProbeOutputBytes+1))
		if e != nil {
			return e
		}
		if len(data) > MaxProbeOutputBytes {
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

// RunStreamingProbe runs ffprobe with caller-built arguments (the last one must
// be the private bridge input) and streams its stdout to consume, which bounds
// what it keeps (a whole-file packet and frame listing is large). Same
// confinement, reservation and retirement rules as RunProbe.
func RunStreamingProbe(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, args []string, reservation *EndpointReservation, consume func(io.Reader) error, libraries ...string) error {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || supervisor == nil || key == "" || consume == nil || len(args) == 0 || args[len(args)-1] != input || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || len(libraries) > maxLibraryFiles || !reservation.matches(endpoint) {
		if reservation != nil {
			reservation.Close()
		}
		return ErrInvalidConfiguration
	}
	for _, path := range libraries {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			reservation.Close()
			return ErrInvalidConfiguration
		}
	}
	cmd, err := confinedCommand(executable, args, endpoint, libraries...)
	if err != nil {
		reservation.Close()
		return err
	}
	if !reservation.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	ctx, progress := progressio.NewDormant(ctx, 2*time.Minute)
	defer progress.Close()
	retired := make(chan struct{})
	err = supervisor.RunOwnedCompletion(ctx, "playback:probe:"+key, cmd, func(reader io.Reader) error { progress.Ping(); return consume(progress.Reader(reader)) }, nil, func() { reservation.retire(); close(retired) })
	<-retired
	return err
}
