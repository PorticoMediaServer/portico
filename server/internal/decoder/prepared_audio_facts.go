package decoder

import (
	"context"
	"io"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Streaming FLAC has no total-samples field until a seekable writer closes it.
// Measure actual decoded frames through the same confined, custody-bound input;
// never substitute a recipe duration for an observed output duration.
func RunPreparedAudioFacts(ctx context.Context, s *storage.Supervisor, key, executable, input string, res *EndpointReservation, custody *os.File, libraries ...string) (audiofacts.Facts, error) {
	var facts audiofacts.Facts
	cmd, err := confinedProbeCommand(executable, input, res, audiofacts.Args(input), libraries...)
	if err != nil {
		return facts, err
	}
	endpoint, err := bridgeEndpoint(input)
	if err != nil {
		return facts, err
	}
	err = runPrepared(ctx, s, key, cmd, endpoint, res, custody, func(r io.Reader) error {
		var e error
		facts, e = audiofacts.Parse(r)
		return e
	})
	return facts, err
}

func RunPreparedFileAudioFacts(ctx context.Context, s *storage.Supervisor, key, sandbox, executable string, input *os.File, custody *PreparedFileCustody, libraries ...string) (audiofacts.Facts, error) {
	var facts audiofacts.Facts
	args := audiofacts.Args("/input.media")
	for i := range args {
		if args[i] == "-protocol_whitelist" {
			args[i+1] = "file"
		}
	}
	err := runPreparedFile(ctx, s, key, sandbox, executable, input, custody, args, func(r io.Reader) error {
		var e error
		facts, e = audiofacts.Parse(r)
		return e
	}, libraries...)
	return facts, err
}
