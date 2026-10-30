//go:build linux

package mediaexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

type identityReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r identityReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// RuntimeIdentity binds native ELF code to the exact library closure used by
// software mediaexec jobs. Scripts have an executable identity but no complete
// loader closure, so their inventories must not be memoized by this result.
func RuntimeIdentity(ctx context.Context, executable string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", false, ErrInvalidJob
	}
	file, err := os.Open(executable)
	if err != nil {
		return "", false, err
	}
	var magic [4]byte
	_, readErr := io.ReadFull(file, magic[:])
	file.Close()
	native := readErr == nil && string(magic[:]) == "\x7fELF"
	paths := []string{executable}
	if native {
		libraries, err := ResolveLibraries(executable, executable, append([]string(nil), configuredLibraries()...))
		if err != nil {
			// Keep custom/unmatched tools on fresh ordinary inventories when
			// their loader closure cannot be represented by our sandbox.
			native = false
		} else {
			paths = append(paths, libraries...)
		}
	}
	sort.Strings(paths)
	sum := sha256.New()
	last := ""
	var total int64
	for _, path := range paths {
		if path == last {
			continue
		}
		last = path
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", false, err
		}
		source, err := os.Open(path)
		if err != nil {
			return "", false, err
		}
		before, err := source.Stat()
		if err != nil || !before.Mode().IsRegular() || before.Size() > 2<<30 || total+before.Size() > 4<<30 {
			source.Close()
			return "", false, ErrInvalidJob
		}
		// File generation is part of the identity even for byte-identical
		// replacement. The loader origin and every alias are explicit too.
		entry, err := os.Lstat(path)
		if err != nil {
			source.Close()
			return "", false, err
		}
		fileStat, ok := before.Sys().(*syscall.Stat_t)
		entryStat, entryOK := entry.Sys().(*syscall.Stat_t)
		if !ok || !entryOK {
			source.Close()
			return "", false, ErrInvalidJob
		}
		// Access time changes while hashing; it is not a code generation.
		fmt.Fprintf(sum, "%s\x00%s\x00%d:%d:%d:%d:%v:%v\x00%d:%d:%d:%d:%v:%v\x00", path, resolved,
			fileStat.Dev, fileStat.Ino, fileStat.Mode, before.Size(), fileStat.Mtim, fileStat.Ctim,
			entryStat.Dev, entryStat.Ino, entryStat.Mode, entry.Size(), entryStat.Mtim, entryStat.Ctim)
		count, readErr := io.Copy(sum, identityReader{ctx, io.LimitReader(source, before.Size()+1)})
		after, statErr := source.Stat()
		source.Close()
		current, currentErr := os.Stat(path)
		currentEntry, entryErr := os.Lstat(path)
		currentResolved, resolveErr := filepath.EvalSymlinks(path)
		if readErr != nil {
			return "", false, readErr
		}
		if count != before.Size() || !sameLibraryInfo(before, after, statErr) ||
			!sameLibraryInfo(before, current, currentErr) || !sameLibraryInfo(entry, currentEntry, entryErr) || resolveErr != nil || currentResolved != resolved {
			return "", false, ErrInvalidJob
		}
		total += count
	}
	// The child receives only this existing explicit environment allowlist.
	// No LD_PRELOAD/LD_LIBRARY_PATH from the server is inherited.
	for _, entry := range environment() {
		fmt.Fprintf(sum, "%s\x00", entry)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(sum.Sum(nil)), native, nil
}
