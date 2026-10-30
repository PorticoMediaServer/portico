// Package mediaprobe composes source retention, confined physical probing, and
// typed observations. Publication stays in the coordinator's guarded callback.
package mediaprobe

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/probefacts"
	"github.com/PorticoMediaServer/portico/server/internal/producerinput"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type Tool struct {
	Executable string
	Libraries  []string
	Identity   probefacts.ToolIdentity
}

// Run transfers both listeners on every path. The caller must invoke it inside
// WithPreparedInput and keep that borrow active through subsequent b.Publish.
// All source IO and actual decoder exit complete before this returns facts.
func Run(ctx context.Context, supervisor *storage.Supervisor, key string, tool Tool, source producerinput.BorrowedInput, cache string, ipv4, ipv6 *net.TCPListener) (result probefacts.AnalyzedInput, err error) {
	defer func() {
		if ipv4 != nil {
			ipv4.Close()
		}
		if ipv6 != nil {
			ipv6.Close()
		}
	}()
	digest, e := hex.DecodeString(tool.Identity.ExecutableSHA256)
	if ctx == nil || e != nil || len(digest) != 32 || tool.Identity.ExecutableSHA256 != strings.ToLower(tool.Identity.ExecutableSHA256) || tool.Identity.Version == "" || len(tool.Identity.Version) > probefacts.MaxVersionBytes || !utf8.ValidString(tool.Identity.Version) {
		return result, decoder.ErrInvalidConfiguration
	}
	reservation, err := decoder.ReserveEndpoints(ipv4, ipv6)
	if err != nil {
		return result, err
	}
	defer reservation.Close()
	bridge, err := producerinput.NewPair(ctx, source, cache, ipv4, ipv6)
	if err != nil {
		return result, err
	}
	// Physical handler drain is part of source borrowing. A canceled caller cannot
	// abandon this cleanup; a containing worker owns any outstanding operation.
	defer func() { err = errors.Join(err, bridge.Shutdown(context.Background())) }()
	data, err := decoder.RunProbe(ctx, supervisor, key, tool.Executable, bridge.URL(), reservation, tool.Libraries...)
	if err != nil {
		return result, err
	}
	facts, err := probefacts.Parse(data)
	if err != nil {
		return result, err
	}
	evidence, err := bridge.Finalize(ctx)
	if err != nil {
		return result, err
	}
	return probefacts.AnalyzedInput{Version: probefacts.SchemaVersion, Tool: tool.Identity, Facts: facts, Source: evidence}, nil
}
