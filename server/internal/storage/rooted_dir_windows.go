//go:build windows

package storage

import "errors"

// ErrRootedUnsupported says this platform cannot pin a directory by descriptor.
var ErrRootedUnsupported = errors.New("rooted directory browsing is unavailable on this platform")

// RootedDir is refused on Windows: there is no relative no-follow directory
// open here, and checking a name and then reopening an absolute path is exactly
// the race a confined walk must not have. Callers report browsing as
// unsupported rather than walking with weaker confinement.
type RootedDir struct{}

func OpenRootedDir(string) (*RootedDir, error)         { return nil, ErrRootedUnsupported }
func (d *RootedDir) Child(string) (*RootedDir, error)  { return nil, ErrRootedUnsupported }
func (d *RootedDir) Close() error                      { return nil }
func (d *RootedDir) Names(int) ([]string, bool, error) { return nil, false, ErrRootedUnsupported }
func (d *RootedDir) IsDirectoryEntry(string) bool      { return false }
