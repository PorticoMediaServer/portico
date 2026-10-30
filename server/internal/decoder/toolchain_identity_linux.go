//go:build linux

package decoder

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func inventoryIdentity(ctx context.Context, executable string) (string, bool, error) {
	return mediaexec.RuntimeIdentity(ctx, executable)
}
