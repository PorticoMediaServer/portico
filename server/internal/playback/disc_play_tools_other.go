//go:build !linux

package playback

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// The private remote snapshot has no complete native loader-identity adapter on
// these hosts yet. Existing local-disc playback remains on its original path.
func discPlayToolWitness(string, string, string) (func(context.Context) (playbackdisc.ToolPins, error), error) {
	return nil, disc.ErrRemoteUnqualified
}
