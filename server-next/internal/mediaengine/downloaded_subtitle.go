package mediaengine

import (
	"context"
	"encoding/hex"
	"io"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// A kept subtitle is a server-owned file, not an embedded stream or a library sidecar.
// Its database hash is the only name accepted; the provider is never asked during playback.
type downloadedSubtitle struct{ dir, hash string }

func (s downloadedSubtitle) path() string {
	if s.dir == "" || len(s.hash) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(s.hash); err != nil {
		return ""
	}
	return filepath.Join(s.dir, s.hash[:2], s.hash)
}

func (s downloadedSubtitle) Open(ctx context.Context) (io.ReadSeekCloser, int64, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, time.Time{}, err
	}
	file, err := libraryfile.Open(s.path(), []string{s.dir})
	if err != nil {
		return nil, 0, time.Time{}, streaming.ErrSourceUnavailable
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, time.Time{}, err
	}
	return file, info.Size(), info.ModTime(), nil
}
