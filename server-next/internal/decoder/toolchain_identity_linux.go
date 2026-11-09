//go:build linux

package decoder

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

func inventoryIdentity(ctx context.Context, executable string) (string, bool, error) {
	return mediaexec.RuntimeIdentity(ctx, executable)
}

func cachedHardwareIdentity(ctx context.Context, executable string) (string, bool) {
	return mediaexec.CachedRuntimeIdentity(ctx, executable)
}
