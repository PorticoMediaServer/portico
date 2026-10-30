package assets

import (
	"context"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
)

// Remote probes only see a capability-bound loopback byte bridge. Select a
// finite media demuxer explicitly: never permit playlists, external references,
// URL credentials or arbitrary local-file protocols through ffprobe.
func (p Probe) InspectRemote(ctx context.Context, object storage.RemoteObject, extension string) (Facts, error) {
	ext := strings.TrimPrefix(strings.ToLower(extension), ".")
	format := ""
	switch ext {
	case "mp4", "m4v", "m4a", "m4b", "mov":
		format = "mov"
	case "mkv", "webm":
		format = "matroska"
	case "mp3", "flac", "wav", "aac", "ogg", "avi":
		format = ext
	case "ts", "m2ts":
		format = "mpegts"
	default:
		return Facts{}, storage.ErrRemoteRange
	}
	bridge, closeBridge, err := storage.RemoteProbeBridge(ctx, object)
	if err != nil {
		return Facts{}, err
	}
	defer closeBridge()
	options := []string{"-f", format, "-protocol_whitelist", "http,tcp", "-probesize", "8388608", "-analyzeduration", "5000000"}
	if format == "mov" {
		options = append(options, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	facts, err := p.inspect(ctx, bridge, options)
	if err != nil || facts.Container != "matroska" {
		return facts, err
	}
	// Read only the admitted object's prefix, under its existing range and
	// version checks. The filename must not turn generic Matroska into WebM.
	body, err := object.OpenRange(ctx, 0, min(int64(containerHeaderLimit), object.Snapshot().Size))
	if err != nil {
		return Facts{}, err
	}
	prefix, err := io.ReadAll(io.LimitReader(body, containerHeaderLimit))
	closeErr := body.Close()
	if err != nil {
		return Facts{}, err
	}
	if closeErr != nil {
		return Facts{}, closeErr
	}
	facts.Container = containerFromHeader(facts.Container, prefix)
	return facts, nil
}
