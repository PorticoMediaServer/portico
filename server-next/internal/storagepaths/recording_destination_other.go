//go:build !linux && !darwin && !windows

package storagepaths

import "os"

func recordingDirectoryIdentity(*os.File, os.FileInfo) (string, error) { return "", os.ErrInvalid }
