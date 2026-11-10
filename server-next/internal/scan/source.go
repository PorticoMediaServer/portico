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
	"path"
	"path/filepath"
	"syscall"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// Entry is one name in a folder listing.
type Entry struct {
	Name       string
	Dir        bool
	Size       int64
	ModifiedNS int64
	// FolderID is, for a folder, a name for the folder itself however it was reached (through
	// a link or not), or "" when that cannot be told. A scan reads each folder once, so a
	// link back to a folder above it cannot send the scan round in circles.
	FolderID string
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
			entry.FolderID = folderID(info)
		}
		out = append(out, entry)
	}
	return out, nil
}

// FolderID names a folder itself, however it was reached, or is "" when it cannot be told.
func (l Local) FolderID(rel string) string {
	info, err := os.Stat(l.LocalPath(rel))
	if err != nil {
		return ""
	}
	return folderID(info)
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
	// O_NONBLOCK: a named pipe put where a file was opens at once and is refused,
	// where a plain open would wait for ever for somebody to write to it.
	f, err := os.OpenFile(l.LocalPath(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New(rel + " is not a regular file")
	}
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

// Remote is a folder on remote storage: Root is the folder's path within the source. Every
// request it makes goes through the source's throttle, which Storage already carries.
type Remote struct {
	Storage remote.Storage
	Root    string
}

// readsFiles says whether a scan reads inside the folder's files. A source on Just list the
// files says no.
func (r Remote) readsFiles() bool {
	choice, ok := r.Storage.(interface{ ReadsFiles() bool })
	return !ok || choice.ReadsFiles()
}

// LocalPath is always "": remote storage is never mounted.
func (Remote) LocalPath(string) string { return "" }

func (r Remote) at(rel string) string { return path.Join(r.Root, rel) }

func (r Remote) List(ctx context.Context, rel string) ([]Entry, error) {
	listed, err := r.Storage.List(ctx, r.at(rel))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(listed))
	for i, e := range listed {
		out[i] = Entry{Name: e.Name, Dir: e.Dir, Size: e.Size, ModifiedNS: e.ModifiedNS}
	}
	return out, nil
}

// Exists lists the file's folder, which is the one question every kind of storage answers.
func (r Remote) Exists(ctx context.Context, rel string) (bool, error) {
	listed, err := r.Storage.List(ctx, r.at(path.Dir(rel)))
	if err != nil {
		return false, err
	}
	for _, e := range listed {
		if e.Name == path.Base(rel) {
			return true, nil
		}
	}
	return false, nil
}

func (r Remote) Open(ctx context.Context, rel string) (File, error) {
	return r.Storage.Open(ctx, r.at(rel))
}

// unreachable is a folder whose source could not be opened. It lists as an error, so the
// folder is reported unavailable and nothing in it is marked missing.
type unreachable struct{ err error }

func (u unreachable) List(context.Context, string) ([]Entry, error) { return nil, u.err }
func (u unreachable) Exists(context.Context, string) (bool, error)  { return false, u.err }
func (u unreachable) Open(context.Context, string) (File, error)    { return nil, u.err }
func (unreachable) LocalPath(string) string                         { return "" }
