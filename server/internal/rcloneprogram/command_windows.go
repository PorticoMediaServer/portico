//go:build windows

package rcloneprogram

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func versionCommand(ctx context.Context, path string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		// Use an environment expansion inside quotes, with delayed expansion disabled.
		// This permits spaces and shell metacharacters in a chosen wrapper path.
		cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/D", "/V:OFF", "/S", "/C", `""%PORTICO_RCLONE_VERSION_PROGRAM%" version"`)
		cmd.Env = append(os.Environ(), "PORTICO_RCLONE_VERSION_PROGRAM="+path)
		return cmd
	default:
		return exec.CommandContext(ctx, path, "version")
	}
}
