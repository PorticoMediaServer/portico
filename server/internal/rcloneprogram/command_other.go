//go:build !windows

package rcloneprogram

import (
	"context"
	"os/exec"
)

func versionCommand(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, path, "version")
}
