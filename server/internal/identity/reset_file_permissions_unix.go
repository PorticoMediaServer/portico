//go:build !windows

package identity

import "os"

func prepareResetDirectory(path string) error                 { return os.MkdirAll(path, 0700) }
func resetPermissionsPrivate(_ string, info os.FileInfo) bool { return info.Mode().Perm()&0077 == 0 }
