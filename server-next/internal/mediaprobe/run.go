// Package mediaprobe runs one confined ffprobe over a source the server keeps
// open, and returns typed observations.
package mediaprobe

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/probefacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

type Tool struct {
	Executable string
	Libraries  []string
	Identity   probefacts.ToolIdentity
}

// Run probes a source of a known size. The tool reads it through a private
// loopback bridge and never sees a path. All source reads and the tool's real
// exit complete before this returns facts.
func Run(
	ctx context.Context, supervisor *processpool.Supervisor, key string, tool Tool, source io.ReaderAt, size int64,
) (result probefacts.AnalyzedInput, err error) {
	digest, e := hex.DecodeString(tool.Identity.ExecutableSHA256)
	if ctx == nil || e != nil || len(digest) != 32 || tool.Identity.ExecutableSHA256 != strings.ToLower(tool.Identity.ExecutableSHA256) ||
		tool.Identity.Version == "" || len(tool.Identity.Version) > probefacts.MaxVersionBytes || !utf8.ValidString(tool.Identity.Version) {
		return result, decoder.ErrInvalidConfiguration
	}
	bridge, err := inputbridge.Open(ctx, source, size)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, bridge.Close()) }()
	reservation, err := bridge.Reserve()
	if err != nil {
		return result, err
	}
	// A probe that was refused before its tool started never took the reservation: it is
	// given back here. Once a tool has it, its real exit gives it back and this does nothing.
	defer reservation.Close()
	data, err := decoder.RunProbe(ctx, supervisor, key, tool.Executable, bridge.URL(), reservation, tool.Libraries...)
	if err != nil {
		return result, err
	}
	facts, err := probefacts.Parse(data)
	if err != nil {
		return result, err
	}
	return probefacts.AnalyzedInput{Version: probefacts.SchemaVersion, Tool: tool.Identity, Facts: facts}, nil
}
