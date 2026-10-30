//go:build linux || darwin

package storagepaths

import (
	"fmt"
	"os"
	"syscall"
)

func recordingDirectoryIdentity(_ *os.File, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok { return "", os.ErrInvalid }
	return fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), nil
}
