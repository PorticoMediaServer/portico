package storagepaths

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Mount struct{ Point, Filesystem string }

// ParseMountInfo uses kernel mountinfo, including escaped mountpoints and
// optional fields. A directory's existence is not evidence it is a volume.
func ParseMountInfo(r io.Reader) []Mount {
	var out []Mount
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " - ", 2)
		if len(parts) != 2 {
			continue
		}
		a, b := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(a) < 6 || len(b) < 1 {
			continue
		}
		point := a[4]
		for _, escape := range []string{`\040`, `\011`, `\012`, `\134`} {
			v, _ := strconv.ParseInt(escape[1:], 8, 8)
			point = strings.ReplaceAll(point, escape, string(rune(v)))
		}
		out = append(out, Mount{point, b[0]})
	}
	if scanner.Err() != nil {
		return nil // An incomplete mount table cannot authorize cleanup.
	}
	return out
}
func IsMount(path string, mounts []Mount) bool {
	for _, m := range mounts {
		if filepath.Clean(m.Point) == filepath.Clean(path) {
			return true
		}
	}
	return false
}
func Filesystem(path string, mounts []Mount) string {
	best, kind := "", ""
	for _, m := range mounts {
		rel, e := filepath.Rel(m.Point, path)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && len(m.Point) > len(best) {
			best, kind = m.Point, m.Filesystem
		}
	}
	return kind
}
func SharedFilesystem(kind string) bool {
	switch kind {
	case "fuse.shfs", "nfs", "nfs4", "cifs", "smb3":
		return true
	}
	return false
}

func MemoryFilesystem(kind string) bool { return kind == "tmpfs" || kind == "ramfs" }

// CurrentMounts returns a complete platform mount/volume list or an error.
func CurrentMounts() ([]Mount, error) {
	mounts, err := cleanupMounts()
	if err != nil {
		return nil, err
	}
	if len(mounts) == 0 {
		return nil, ErrCleanupUnavailable
	}
	return mounts, nil
}

// TranscodeDirectories separates bounded Linux tmpfs output from its verified
// disk spill volume. ramfs has no kernel size bound and is never writable output.
func TranscodeDirectories(requested, fallback string, mounts []Mount) (disk, memory string, err error) {
	verifiedDisk := func(path string) bool {
		return filepath.IsAbs(path) && canonicalParents(path) == nil && Filesystem(path, mounts) != "" && !MemoryFilesystem(Filesystem(path, mounts))
	}
	// Classifying an alias by its textual mount prefix can turn an apparently
	// disk-backed folder into unbounded tmpfs output. Refuse aliased parents and
	// use only a separately verified disk fallback.
	if !filepath.IsAbs(requested) || canonicalParents(requested) != nil {
		if verifiedDisk(fallback) {
			return fallback, "", nil
		}
		return "", "", errors.New("conversion directories require verified real disk parents")
	}
	kind := Filesystem(requested, mounts)
	if kind == "" {
		return "", "", errors.New("conversion filesystem could not be verified; choose a verified disk directory")
	}
	if !MemoryFilesystem(kind) {
		return requested, "", nil
	}
	if !verifiedDisk(fallback) {
		return "", "", errors.New("RAM-backed conversion storage requires a verified disk spill directory")
	}
	if runtime.GOOS == "linux" && kind == "tmpfs" {
		memory = requested
	}
	return fallback, memory, nil
}

// MemoryTranscodeDirectory never owns a shared tmpfs root. Each disk output
// root gets its own private child; playback verifies its ownership before any
// adoption or cleanup. Merely finding tmpfs does not authorize RAM placement:
// playback also checks its kernel ceiling, the cap and the memory floor.
func MemoryTranscodeDirectory(disk string, mounts []Mount) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	canonical, err := filepath.EvalSymlinks(disk)
	if err != nil {
		return ""
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return ""
	}
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))[:16]
	for _, base := range []string{"/memory-transcode", "/dev/shm"} {
		if Filesystem(base, mounts) == "tmpfs" {
			return filepath.Join(base, "portico-hls-"+identity)
		}
	}
	return ""
}

var ErrCleanupUnavailable = errors.New("destructive cleanup cannot be confined to the owned filesystem")

func refuseMountedChildren(directory string) error {
	mounts, err := cleanupMounts()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCleanupUnavailable, err)
	}
	return refuseMountsWithin(directory, mounts)
}

func refuseMountsWithin(directory string, mounts []Mount) error {
	if len(mounts) == 0 {
		return ErrCleanupUnavailable
	}
	for _, mount := range mounts {
		rel, err := filepath.Rel(directory, mount.Point)
		if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return fmt.Errorf("%w: mounted filesystem at %s", ErrCleanupUnavailable, mount.Point)
		}
	}
	return nil
}
