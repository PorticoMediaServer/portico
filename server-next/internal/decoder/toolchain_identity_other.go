//go:build !linux

package decoder

import "context"

// Preserve the existing non-Linux detector behavior. Qualified bundle feature
// grants remain disabled there until that target has a checked loader binding.
func inventoryIdentity(ctx context.Context, executable string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	identity, err := ffmpegIdentity(executable)
	return identity, true, err
}

func cachedHardwareIdentity(ctx context.Context, executable string) (string, bool) {
	identity, _, err := inventoryIdentity(ctx, executable)
	return identity, err == nil
}
