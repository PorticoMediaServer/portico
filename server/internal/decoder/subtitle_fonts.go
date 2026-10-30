package decoder

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strconv"
)

// Font bytes are dumped through stdout, never to an attachment-supplied name.
func RunSubtitleFontExtract(ctx context.Context, sup *storage.Supervisor, key, executable, input string, res *EndpointReservation, index int, files ...string) ([]byte, error) {
	endpoint, e := bridgeEndpoint(input)
	if e != nil || !res.matches(endpoint) || !filepath.IsAbs(executable) || index < 0 || index > 65535 {
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-dump_attachment:" + strconv.Itoa(index), "pipe:1", "-protocol_whitelist", "http,tcp", "-format_whitelist", "matroska", "-i", input, "-map", "0:v:0?", "-t", "0", "-an", "-sn", "-f", "null", "pipe:1"}
	cmd, e := confinedCommand(executable, args, endpoint, files...)
	if e != nil {
		res.Close()
		return nil, e
	}
	return runSubtitleOwned(ctx, sup, key, cmd, input, res, 8<<20)
}
