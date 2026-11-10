// Package scan turns the folders of a library into titles. It lists every folder, compares
// what is there with what the last scan stored, and hands the differences to the database in
// small batches: new files become titles under the names they have, a file at a new place
// with the same size and modified time is the same file, and a file a complete listing no
// longer has is marked missing. Reading the inside of files is a second pass. It runs no SQL.
package scan

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Entry is one name in a folder listing.
type Entry struct {
	Name       string
	Dir        bool
	Size       int64
	ModifiedNS int64
	// Device is, for a folder, the number of the disk it is on, or 0 when that cannot be told.
	Device int64
}

// Source is the tree of files one library folder points at. A local folder is one; remote
// storage is another. Paths are relative to the folder, with forward slashes; "" is the folder.
type Source interface {
	// List returns everything in a folder, or an error: never part of a listing. A scan marks
	// files missing only from a listing that was returned whole.
	List(ctx context.Context, rel string) ([]Entry, error)
	// Exists reports whether a file is there now.
	Exists(ctx context.Context, rel string) (bool, error)
	// Open opens a file for reading small parts of it: tags, a sidecar, an ignore file.
	Open(ctx context.Context, rel string) (File, error)
	// LocalPath is the file's path on this machine, or "" for storage that is not a disk here.
	LocalPath(rel string) string
}

// File is an open file.
type File interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// Local is a folder on a disk the server can read.
type Local struct{ Root string }

func (l Local) LocalPath(rel string) string { return filepath.Join(l.Root, filepath.FromSlash(rel)) }

func (l Local) List(ctx context.Context, rel string) ([]Entry, error) {
	dir := l.LocalPath(rel)
	if rel == "" {
		// A root that is a file, or a link to nowhere, is not a listing of nothing.
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("not a folder")
		}
	}
	listed, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(listed))
	for _, e := range listed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := e.Info()
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			info, err = os.Stat(filepath.Join(dir, e.Name()))
		}
		if err != nil {
			// Gone between the listing and the look, or a link to nowhere: not there.
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		entry := Entry{Name: e.Name(), Dir: info.IsDir(), Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
		if entry.Dir {
			entry.Device = device(info)
		}
		out = append(out, entry)
	}
	return out, nil
}

// Device is the number of the disk a folder is on, or 0 when it cannot be told.
func (l Local) Device(rel string) int64 {
	info, err := os.Stat(l.LocalPath(rel))
	if err != nil {
		return 0
	}
	return device(info)
}

func (l Local) Exists(ctx context.Context, rel string) (bool, error) {
	_, err := os.Stat(l.LocalPath(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

type localFile struct {
	*os.File
	size int64
}

func (f localFile) Size() int64 { return f.size }

func (l Local) Open(ctx context.Context, rel string) (File, error) {
	f, err := os.Open(l.LocalPath(rel))
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return localFile{f, info.Size()}, nil
}

// readSmall reads a whole small file, or fails if it is larger than limit.
func readSmall(ctx context.Context, src Source, rel string, limit int64) ([]byte, error) {
	f, err := src.Open(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Size() > limit {
		return nil, errors.New("file is larger than a sidecar may be")
	}
	raw := make([]byte, f.Size())
	n, err := f.ReadAt(raw, 0)
	if err != nil && !(errors.Is(err, io.EOF) && int64(n) == f.Size()) {
		return nil, err
	}
	return raw, nil
}
