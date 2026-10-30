package decoder

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strconv"
)

const MaxSubtitleOutput = medialimits.MaxSubtitleFileMiB << 20

func seconds(us int64) string { return strconv.FormatFloat(float64(us)/1e6, 'f', 6, 64) }
func runSubtitleOwned(ctx context.Context, sup *storage.Supervisor, key string, cmd *exec.Cmd, input string, res *EndpointReservation, max int64) ([]byte, error) {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || sup == nil || key == "" {
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	if !claimSubtitleEndpoint(res, endpoint, cmd) {
		return nil, ErrReservationInUse
	}
	retired := make(chan struct{})
	var data []byte
	err = sup.RunOwnedCompletion(ctx, "playback:subtitle:"+key, cmd, func(r io.Reader) error {
		var e error
		data, e = io.ReadAll(io.LimitReader(r, max+1))
		if len(data) > int(max) {
			return ErrProbeOutput
		}
		return e
	}, nil, func() { res.retire(); close(retired) })
	<-retired
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Extract preserves ASS styling and bitmap packets. Matroska capsules normalize
// embedded timestamps to the media demux origin once, never to the first cue.
func RunSubtitleExtract(ctx context.Context, sup *storage.Supervisor, key, executable, input string, res *EndpointReservation, index int, format string, files ...string) ([]byte, error) {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || !res.matches(endpoint) || !filepath.IsAbs(executable) || index < 0 || index > 65535 {
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	codec, mux := "copy", format
	switch format {
	case "srt":
		codec = "srt"
	case "vtt":
		codec = "webvtt"
		mux = "webvtt"
	case "ass", "ssa":
		codec = "copy"
		mux = "ass"
	case "pgs":
		mux = "sup"
	case "vobsub", "dvb", "mks":
		mux = "matroska"
	default:
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-copyts", "-start_at_zero", "-protocol_whitelist", "http,tcp", "-format_whitelist", "mov,matroska,avi,mpegts,mpeg", "-i", input, "-map", "0:" + strconv.Itoa(index), "-vn", "-an", "-dn", "-c:s", codec, "-map_metadata", "-1", "-f", mux, "pipe:1"}
	cmd, err := confinedCommand(executable, args, endpoint, files...)
	if err != nil {
		res.Close()
		return nil, err
	}
	limit := int64(medialimits.SubtitleFileBytes())
	if format == "srt" || format == "vtt" || format == "ass" || format == "ssa" {
		limit = 4 << 20
	}
	return runSubtitleOwned(ctx, sup, key, cmd, input, res, limit)
}

// P01's owned-command reservation additionally binds inherited endpoint handles.
// Use that exact method when assembled with P01; baseline reservations retain
// their original physical-retirement contract. No reflection or unsafe access.
func claimSubtitleEndpoint(res *EndpointReservation, endpoint string, cmd *exec.Cmd) bool {
	switch r := any(res).(type) {
	case interface{ claim(string, *exec.Cmd) bool }:
		return r.claim(endpoint, cmd)
	case interface{ claim(string) bool }:
		return r.claim(endpoint)
	default:
		return false
	}
}
