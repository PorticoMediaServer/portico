package decoder

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// RunHLSWindow executes a server-built ordinary playback graph against one
// reserved observed-source endpoint. The only writable mount is this session's
// private output directory, which also holds its sealed subtitle input.
func RunHLSWindow(ctx context.Context, supervisor *storage.Supervisor, key, executable, input, output string, res *EndpointReservation, args []string, hardware bool, libraries ...string) error {
	defer res.Close()
	endpoint, e := bridgeEndpoint(input)
	if e != nil || supervisor == nil || key == "" || !filepath.IsAbs(output) || filepath.Clean(output) != output || !filepath.IsAbs(executable) || !res.matches(endpoint) {
		return ErrInvalidConfiguration
	}
	canonical, e := filepath.EvalSymlinks(output)
	if e != nil || canonical != output {
		return ErrInvalidConfiguration
	}
	stat, e := os.Lstat(output)
	if e != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidConfiguration
	}
	cmd, e := confinedHLSCommand(executable, args, endpoint, output, hardware, libraries...)
	if e != nil {
		return e
	}
	if !res.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	retired := make(chan struct{})
	e = supervisor.RunOwnedCompletion(ctx, "playback:hls:"+key, cmd, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e }, nil, func() { res.retire(); close(retired) })
	<-retired
	return e
}
