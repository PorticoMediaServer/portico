//go:build !linux && !darwin

package storage

// mountIsLocal keeps every operation in a helper process where the mount
// table can't be read without risk (Windows keeps its locked-handle helper).
func mountIsLocal(string) bool { return false }
