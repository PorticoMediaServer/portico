package decoder

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// RunLinearCopy records compatible elementary streams without a delivery-quality
// conversion. HLS descendants have already been rewritten by the private input
// gateway; raw MPEG-TS cannot turn into an HLS demuxer with arbitrary fetches.
// No provider locator, secret or writable filesystem path reaches FFmpeg.
func RunLinearCopy(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, hls bool, reservation *EndpointReservation, physicalLock *os.File, consume func(io.Reader) error, libraries ...string) error {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || supervisor == nil || key == "" || consume == nil {
		return ErrInvalidConfiguration
	}
	for _, file := range libraries {
		if !filepath.IsAbs(file) || filepath.Clean(file) != file {
			return ErrInvalidConfiguration
		}
	}
	endpoint, e := bridgeEndpoint(input)
	if e != nil {
		return e
	}
	if !reservation.matches(endpoint) {
		return ErrInvalidConfiguration
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-protocol_whitelist", "http,tcp", "-format_whitelist", "mpegts", "-probesize", "8388608", "-analyzeduration", "10000000"}
	if hls {
		args = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-protocol_whitelist", "http,tcp,crypto", "-format_whitelist", "hls,mpegts,mov,aac,mp3", "-allowed_extensions", "ALL", "-probesize", "8388608", "-analyzeduration", "10000000"}
	}
	args = append(args, "-i", input, "-map", "0:v?", "-map", "0:a?", "-map", "0:s?", "-c", "copy", "-copyts", "-start_at_zero", "-mpegts_flags", "+resend_headers", "-f", "mpegts", "pipe:1")
	cmd, e := confinedCommand(executable, args, endpoint, libraries...)
	if e != nil {
		return e
	}
	if physicalLock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, physicalLock)
	}
	if !reservation.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	retired := make(chan struct{})
	e = supervisor.RunOwnedLinear(ctx, "playback:linear:"+key, cmd, consume, nil, func() { reservation.retire(); close(retired) })
	<-retired
	return e
}
