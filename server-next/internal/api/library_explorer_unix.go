//go:build unix

package api

import "golang.org/x/sys/unix"

// canWrite reports whether the server may make files in a folder.
func canWrite(folder string) bool { return unix.Access(folder, unix.W_OK|unix.X_OK) == nil }
