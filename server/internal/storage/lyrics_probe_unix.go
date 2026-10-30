//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// The descriptor is confined by os.Root before FFprobe receives it as
// descriptor 3. argv was built by mediaexec in the server (sandboxed where the
// platform can); it carries no provider URL, secret or untrusted path.
func probeLyricTags(source *os.File, argv []string) ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if len(argv) < 2 || argv[len(argv)-1] != descriptorInput {
		return nil, errors.New("invalid lyric probe")
	}
	cmd, err := mediaexec.CommandArgv(ctx, argv, source)
	if err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	cmd.Stderr = io.Discard
	buf := &lyricProbeBuffer{}
	cmd.Stdout = buf
	if e := cmd.Run(); e != nil {
		return nil, e
	}
	return parseLyricProbe(buf.data)
}
