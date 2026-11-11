package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
)

// Checking a backup whole, before it is restored. A backup that fails any part of the check is
// refused whole: its archive must be whole, hold nothing but a database, key files and
// pictures under the names Portico gives them, and its database must pass db.CheckCopy.

// Contents is what a backup that passed its check holds.
type Contents struct {
	Database    bool
	KeyFiles    []string
	Pictures    int
	Credentials []string
}

// ErrDamaged refuses a backup that failed its check, saying why.
type ErrDamaged struct{ Reason string }

func (e *ErrDamaged) Error() string { return "this backup cannot be restored: " + e.Reason }

// Inspect checks the archive at path whole. scratch is a folder it may write the database
// to while checking it; nothing is left there.
func Inspect(ctx context.Context, archivePath, scratch string) (Contents, error) {
	var out Contents
	f, err := os.Open(archivePath)
	if err != nil {
		return out, &ErrDamaged{"it cannot be opened"}
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return out, &ErrDamaged{"it is not a file"}
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return out, &ErrDamaged{"it is not a Portico backup"}
	}
	work, err := os.MkdirTemp(scratch, ".checking-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(work)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, &ErrDamaged{"it is cut short or damaged"}
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 {
			return out, &ErrDamaged{fmt.Sprintf("it holds %q, which is not a file a backup holds", h.Name)}
		}
		switch kind := entryKind(h.Name); kind {
		case "database":
			// The database is unpacked to be checked: one larger than the free space there
			// is refused rather than filling the disk.
			if free, err := diskspace.Free(scratch); err == nil && h.Size > free {
				return out, fmt.Errorf("the data folder's disk has too little free space to check this backup")
			}
			if out.Database {
				return out, &ErrDamaged{"it holds two databases"}
			}
			dst, err := os.OpenFile(filepath.Join(work, "main.db"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return out, err
			}
			_, err = io.Copy(dst, tr)
			if err = errors.Join(err, dst.Close()); err != nil {
				return out, &ErrDamaged{"its database is cut short"}
			}
			out.Database = true
		case "key":
			out.KeyFiles = append(out.KeyFiles, h.Name)
		case "picture":
			out.Pictures++
		default:
			return out, &ErrDamaged{fmt.Sprintf("it holds %q, which is not a file a backup holds", h.Name)}
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return out, &ErrDamaged{"it is cut short or damaged"}
		}
	}
	if !out.Database {
		return out, &ErrDamaged{"it holds no database"}
	}
	if err := db.CheckCopy(ctx, filepath.Join(work, "main.db")); err != nil {
		return out, &ErrDamaged{err.Error()}
	}
	out.Credentials = []string{CredentialServerKeys}
	if len(out.KeyFiles) > 0 {
		out.Credentials = append(out.Credentials, CredentialKeyFile)
	}
	return out, nil
}

// entryKind says what an archive entry is by its name, "" for anything a backup never holds.
func entryKind(name string) string {
	switch {
	case name == "main.db":
		return "database"
	case slices.Contains(keyFiles, name):
		return "key"
	}
	// artwork/ab/<id>.<size>: the picture store's own names, which nothing can climb out of.
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != "artwork" {
		return ""
	}
	id, size, ok := strings.Cut(parts[2], ".")
	if !ok || !artwork.ValidID(id) || parts[1] != id[:2] || strings.ContainsAny(size, "./\\") || size == "" {
		return ""
	}
	return "picture"
}
