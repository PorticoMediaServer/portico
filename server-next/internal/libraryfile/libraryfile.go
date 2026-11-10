// Package libraryfile is the one rule for reading a file from a library: the
// server opens it itself, by the path the scanner recorded for it, and that
// path must be written beneath a folder the owner configured as a library.
//
// Callers pass only a path built from the scanner's own records (library
// folder, folder beneath it, file name), never a path or part of a path that
// came from a request.
//
// Symbolic links are followed wherever they lead, in scanning and here alike,
// so a title that lists also plays. Libraries made of links to other disks and
// to remote mounts are common. The cost: whoever can write into a library
// folder can place a link there and, once a scan has recorded it, have the
// server read any regular file the server's own user can read. The owner
// accepts that, because such a person already controls the library. The one
// exception is the server's own data folder (ServerData): its database, tokens
// and setup code are never read as library files, however they are reached. To go back
// to refusing links that leave the library, this file is the only place to
// change.
//
// Media tools never get this authority. ffmpeg and ffprobe run in their sandbox
// and are handed the one file: as an open descriptor, or bound read-only at its
// real path.
package libraryfile

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrOutside says the path is not a readable file of a library: it is not
// written beneath a library folder, does not exist, or is not a regular file.
var ErrOutside = errors.New("not a file inside a library folder")

// ServerData is the server's own data folder, set once at startup before
// anything is served. A file whose real location is inside it is refused.
var ServerData string

// private reports whether a real path (links already followed) is the server's
// data folder or inside it.
func private(real string) bool {
	if ServerData == "" {
		return false
	}
	data, err := filepath.EvalSymlinks(ServerData)
	if err != nil {
		return false // no data folder there, so nothing is inside it
	}
	relative, err := filepath.Rel(data, real)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

// beneath reports whether a path is written beneath one of the folders. It
// reads the path's text only; "x/../.." steps are worked out first, so they
// cannot climb out.
func beneath(path string, folders []string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, folder := range folders {
		if !filepath.IsAbs(folder) {
			continue
		}
		relative, err := filepath.Rel(filepath.Clean(folder), filepath.Clean(path))
		if err == nil && relative != "." && filepath.IsLocal(relative) {
			return true
		}
	}
	return false
}

// Resolve returns the real path of a library file, with links followed, for
// handing to a sandboxed tool.
func Resolve(path string, folders []string) (string, error) {
	if !beneath(path, folders) {
		return "", ErrOutside
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || private(real) {
		return "", ErrOutside
	}
	return real, nil
}

// Open opens a library file for reading, following links. It must not really
// be inside the server's data folder, and must be a regular file with something
// in it: an empty file plays nothing, and the files Linux
// makes up under /proc (a process's memory and environment among them) all
// report no size.
func Open(path string, folders []string) (*os.File, error) {
	if !beneath(path, folders) {
		return nil, ErrOutside
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrOutside
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		file.Close()
		return nil, ErrOutside
	}
	// Where the file really is gets worked out after it is open, and must name
	// the very file that was opened: a link swapped in between the open and
	// the look then matches nothing and is refused, not trusted.
	real, err := filepath.EvalSymlinks(path)
	if err != nil || private(real) {
		file.Close()
		return nil, ErrOutside
	}
	if at, err := os.Stat(real); err != nil || !os.SameFile(info, at) {
		file.Close()
		return nil, ErrOutside
	}
	return file, nil
}
