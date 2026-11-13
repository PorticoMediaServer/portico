package reading

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

const archiveMode = "__reading-archive"
const maxArchiveSize = 2 << 30

var archiveSlots = make(chan struct{}, 2)

type comicMember struct {
	Name string
	Size uint64
}

func comicFormat(format string) bool {
	return format == FormatCBZ || format == FormatCBR && rarEnabled || format == FormatCB7 && sevenEnabled
}

// RAR and 7z parse untrusted counts and decoder windows in a separate, limited
// invocation of this executable. Stage once per request, including remote files;
// no library path, external decoder or extracted member touches the filesystem.
func openBookArchive(ctx context.Context, f *bookFile, format string) (*archive, error) {
	if format != FormatCBR && format != FormatCB7 {
		return openArchive(f.at, f.size)
	}
	if !comicFormat(format) {
		return nil, ErrFormatLater
	}
	if f.size <= 0 || f.size > maxArchiveSize {
		return nil, errDamaged
	}
	deadline, cancel := context.WithTimeout(ctx, 90*time.Second)
	select {
	case archiveSlots <- struct{}{}:
	case <-deadline.Done():
		cancel()
		return nil, deadline.Err()
	}
	done := func() { <-archiveSlots; cancel() }
	staged, err := os.CreateTemp("", "portico-comic-*")
	if err != nil {
		done()
		return nil, errDamaged
	}
	cleanup := func() { staged.Close(); os.Remove(staged.Name()); done() }
	buffer := make([]byte, 1<<20)
	for at := int64(0); at < f.size; {
		if err := deadline.Err(); err != nil {
			cleanup()
			return nil, err
		}
		n, err := f.at.ReadAt(buffer[:min(int64(len(buffer)), f.size-at)], at)
		if n > 0 {
			if _, werr := staged.Write(buffer[:n]); werr != nil {
				cleanup()
				return nil, errDamaged
			}
			at += int64(n)
		}
		if err != nil && !(err == io.EOF && at == f.size) || n == 0 {
			cleanup()
			return nil, f.explain(errDamaged)
		}
	}
	run := func(name string) ([]byte, error) { return runArchive(deadline, staged, format, name) }
	raw, err := run("")
	if err != nil {
		cleanup()
		return nil, err
	}
	var members []comicMember
	if json.Unmarshal(raw, &members) != nil || len(members) == 0 || len(members) > maxEntries {
		cleanup()
		return nil, errDamaged
	}
	a := &archive{entries: map[string]*archiveEntry{}, close: cleanup}
	var total uint64
	for _, m := range members {
		if !safeName(m.Name) || a.entries[m.Name] != nil || m.Size > maxArchiveSize-total {
			cleanup()
			return nil, errDamaged
		}
		total += m.Size
		a.names = append(a.names, m.Name)
		a.entries[m.Name] = &archiveEntry{UncompressedSize64: m.Size, Open: func() (io.ReadCloser, error) {
			body, err := run(m.Name)
			if err != nil || uint64(len(body)) != m.Size {
				return nil, errDamaged
			}
			return io.NopCloser(bytes.NewReader(body)), nil
		}}
	}
	return a, nil
}

func runArchive(ctx context.Context, input *os.File, format, name string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errDamaged
	}
	// The worker uses ReaderAt from offset zero; duplicated stdin offsets do not matter.
	command := exec.CommandContext(ctx, executable, archiveMode, format, name)
	command.Stdin = input
	command.WaitDelay = 5 * time.Second
	pipe, err := command.StdoutPipe()
	if err != nil {
		return nil, errDamaged
	}
	if err = command.Start(); err != nil {
		return nil, errDamaged
	}
	limit := int64(maxDirectory)
	if name != "" {
		limit = maxPartSize
	}
	body, readErr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if readErr != nil || int64(len(body)) > limit {
		command.Process.Kill()
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil || waitErr != nil || int64(len(body)) > limit {
		return nil, errDamaged
	}
	return body, nil
}

// RunHelper is dispatched before normal server startup, so the worker has no
// database, HTTP listener or provider credentials open.
func RunHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != archiveMode {
		return false, nil
	}
	if len(args) != 3 {
		return true, errDamaged
	}
	if err := archiveLimits(); err != nil {
		return true, err
	}
	info, err := os.Stdin.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxArchiveSize {
		return true, errDamaged
	}
	if args[2] != "" && !safeName(args[2]) {
		return true, errDamaged
	}
	var members []comicMember
	switch args[1] {
	case FormatCBR:
		members, err = rarMembers(os.Stdin, info.Size(), args[2], os.Stdout)
	case FormatCB7:
		members, err = sevenMembers(os.Stdin, info.Size(), args[2], os.Stdout)
	default:
		err = errDamaged
	}
	if err == nil && args[2] == "" {
		err = json.NewEncoder(os.Stdout).Encode(members)
	}
	return true, err
}

// Only pictures and root ComicInfo.xml become reader entries. All headers count
// against the archive budget, even those that are not pages.
func comicEntry(name string) bool {
	if !safeName(name) || hidden(name) {
		return false
	}
	if name == "ComicInfo.xml" {
		return true
	}
	_, ok := pageTypes[strings.ToLower(path.Ext(name))]
	return ok
}
