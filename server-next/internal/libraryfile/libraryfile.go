// Package libraryfile is the one rule for reading a file from a library: the
// server opens it itself, and only if its real location, with every symbolic
// link resolved, is inside a folder the owner configured as a library. A link
// placed in a library therefore cannot make the server read anything else.
//
// Media tools never get this authority. ffmpeg and ffprobe run in their sandbox
// and are handed the one file: as an open descriptor, or bound read-only at its
// real path.
package libraryfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutside says the path is not a readable file inside a library folder: it
// does not exist, cannot be resolved, or resolves to somewhere else.
var ErrOutside = errors.New("not a file inside a library folder")

// Resolve returns the real path of a file if it is inside one of the folders.
// A folder that cannot itself be resolved is skipped.
func Resolve(path string, folders []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrOutside
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", ErrOutside
	}
	for _, folder := range folders {
		realFolder, err := filepath.EvalSymlinks(folder)
		if err != nil || !filepath.IsAbs(realFolder) {
			continue
		}
		relative, err := filepath.Rel(realFolder, real)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
			strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		return real, nil
	}
	return "", ErrOutside
}

// Open opens a regular file inside one of the folders for reading.
func Open(path string, folders []string) (*os.File, error) {
	real, err := Resolve(path, folders)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(real)
	if err != nil {
		return nil, ErrOutside
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrOutside
	}
	return file, nil
}
