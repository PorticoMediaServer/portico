package scan

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxLibraryRoots     = 64
	MaxLibraryRootBytes = 8192
)

// ErrRootUnrepresentable is a configuration refusal, never a scan poison-name
// disposition. An invalid root cannot open a run or authorize cursor progress.
var ErrRootUnrepresentable = errors.New("library root must be valid UTF-8")

// LibraryRoots owns exact configured path values. Preparation neither resolves
// symlinks nor proves source identity, containment, availability or authority.
// The source driver must validate these before starting any inspection IO.
type LibraryRoots struct {
	roots []string
	bytes int64
}

func (r LibraryRoots) Len() int     { return len(r.roots) }
func (r LibraryRoots) Bytes() int64 { return r.bytes }

func (r LibraryRoots) Root(i int) (string, bool) {
	if i < 0 || i >= len(r.roots) {
		return "", false
	}
	return r.roots[i], true
}

// PrepareLibraryRoots validates the COMPLETE raw input before copying any path.
// byteBudget counts retained string payload, not heap/RSS or source IO memory.
// Native paths are not cleaned, escaped, Unicode-normalized or made relative:
// a display replacement must never become a persisted identity namespace.
// Errors identify an input ordinal but never include native path bytes.
func PrepareLibraryRoots(raw [][]byte, byteBudget int64) (LibraryRoots, error) {
	if len(raw) == 0 {
		return LibraryRoots{}, fmt.Errorf("%w: at least one library root is required", ErrInvalid)
	}
	if len(raw) > MaxLibraryRoots || byteBudget <= 0 {
		return LibraryRoots{}, ErrCapacity
	}
	remaining := byteBudget
	for _, root := range raw {
		if len(root) > MaxLibraryRootBytes || int64(len(root)) > remaining {
			return LibraryRoots{}, ErrCapacity
		}
		remaining -= int64(len(root))
	}
	// NUL is invalid input even when the same batch also has invalid UTF-8.
	for i, root := range raw {
		if bytes.IndexByte(root, 0) >= 0 {
			return LibraryRoots{}, fmt.Errorf("%w: library root %d contains NUL", ErrInvalid, i)
		}
	}
	for i, root := range raw {
		if !utf8.Valid(root) {
			return LibraryRoots{}, fmt.Errorf("%w: library root %d", ErrRootUnrepresentable, i)
		}
	}
	for i, root := range raw {
		value := string(root)
		if !filepath.IsAbs(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return LibraryRoots{}, fmt.Errorf("%w: library root %d must be an absolute path without control characters", ErrInvalid, i)
		}
	}
	roots := make([]string, len(raw))
	for i, root := range raw {
		roots[i] = string(root)
	}
	return LibraryRoots{roots: roots, bytes: byteBudget - remaining}, nil
}
