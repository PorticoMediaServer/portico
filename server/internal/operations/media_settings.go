package operations

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
)

var startupMediaSettings atomic.Pointer[Settings]

// StartupMediaSettings adopts restart-applied paths before constructing any
// media services. A later save reports requested and effective paths separately.
func StartupMediaSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	doc, err := readSettings(tx)
	if err != nil {
		return Settings{}, err
	}
	settings := doc.Requested
	startupMediaSettings.Store(&settings)
	return settings, nil
}
func effectiveMediaSettings(v Settings) (Settings, []string) {
	out, fields := v, []string{}
	if started := startupMediaSettings.Load(); started != nil {
		if v.ConnectionLimit != started.ConnectionLimit {
			fields = append(fields, "connectionLimit")
			out.ConnectionLimit = started.ConnectionLimit
		}
		if v.TemporaryDirectory != started.TemporaryDirectory {
			fields = append(fields, "temporaryDirectory")
			out.TemporaryDirectory = started.TemporaryDirectory
		}
		if v.FFmpegPath != started.FFmpegPath {
			fields = append(fields, "ffmpegPath")
			out.FFmpegPath = started.FFmpegPath
		}
	}
	return out, fields
}
func ConversionDirectory(state, configured string) string {
	if configured != "" {
		return filepath.Join(configured, "portico-hls")
	}
	return filepath.Join(state, "hls")
}

// UseBundledFFmpeg records the effective fallback without erasing the owner's
// requested path. The console remains available to correct a rejected override.
func UseBundledFFmpeg() {
	if current := startupMediaSettings.Load(); current != nil {
		copy := *current
		copy.FFmpegPath = ""
		startupMediaSettings.Store(&copy)
	}
}
