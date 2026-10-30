//go:build !darwin && !linux

package linearbuffer

import "os"

// Channel decoding is not configured on these targets. Never simulate the
// ownership fence if a future runtime attempts to use this buffer there.
func createOwnedDirectory(parent string) (string, *os.File, error) { return "", nil, ErrMedia }
func Sweep(parent string) error                                    { return nil }
