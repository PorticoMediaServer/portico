package reading

import (
	"archive/zip"
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

// copyName asks the worker for the whole comic as a CBZ: its pages and ComicInfo.xml, stored, in
// the archive's own order. It is not a name an entry can have.
const copyName = ":cbz"
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
	deadline, staged, cleanup, err := stageArchive(ctx, f, 90*time.Second)
	if err != nil {
		return nil, err
	}
	run := func(name string) ([]byte, error) {
		limit := int64(maxDirectory)
		if name != "" {
			limit = maxPartSize
		}
		var body bytes.Buffer
		err := runArchive(deadline, staged, format, name, &body, limit)
		return body.Bytes(), err
	}
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

// comicCopy writes a CBR or CB7 as a CBZ of the same pages, unpacking it once in the worker.
func comicCopy(ctx context.Context, f *bookFile, format string, out io.Writer) error {
	if !comicFormat(format) || format == FormatCBZ {
		return ErrFormatLater
	}
	if f.size <= 0 || f.size > maxArchiveSize {
		return errDamaged
	}
	deadline, staged, cleanup, err := stageArchive(ctx, f, 10*time.Minute)
	if err != nil {
		return err
	}
	defer cleanup()
	return runArchive(deadline, staged, format, copyName, out, maxArchiveSize+maxEntries<<10)
}

// stageArchive copies a book's file where the worker can read it, once a request, holding one
// of the worker slots until cleanup.
func stageArchive(ctx context.Context, f *bookFile, timeout time.Duration) (context.Context, *os.File, func(), error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	select {
	case archiveSlots <- struct{}{}:
	case <-deadline.Done():
		cancel()
		return nil, nil, nil, deadline.Err()
	}
	done := func() { <-archiveSlots; cancel() }
	staged, err := os.CreateTemp("", "portico-comic-*")
	if err != nil {
		done()
		return nil, nil, nil, errDamaged
	}
	cleanup := func() { staged.Close(); os.Remove(staged.Name()); done() }
	buffer := make([]byte, 1<<20)
	for at := int64(0); at < f.size; {
		if err := deadline.Err(); err != nil {
			cleanup()
			return nil, nil, nil, err
		}
		n, err := f.at.ReadAt(buffer[:min(int64(len(buffer)), f.size-at)], at)
		if n > 0 {
			if _, werr := staged.Write(buffer[:n]); werr != nil {
				cleanup()
				return nil, nil, nil, errDamaged
			}
			at += int64(n)
		}
		if err != nil && !(err == io.EOF && at == f.size) || n == 0 {
			cleanup()
			return nil, nil, nil, f.explain(errDamaged)
		}
	}
	return deadline, staged, cleanup, nil
}

// runArchive runs the worker on a staged file and writes what it answers to out, at most limit
// bytes.
func runArchive(ctx context.Context, input *os.File, format, name string, out io.Writer, limit int64) error {
	executable, err := os.Executable()
	if err != nil {
		return errDamaged
	}
	// The worker uses ReaderAt from offset zero; duplicated stdin offsets do not matter.
	command := exec.CommandContext(ctx, executable, archiveMode, format, name)
	command.Stdin = input
	command.WaitDelay = 5 * time.Second
	pipe, err := command.StdoutPipe()
	if err != nil {
		return errDamaged
	}
	if err = command.Start(); err != nil {
		return errDamaged
	}
	n, copyErr := io.Copy(out, io.LimitReader(pipe, limit+1))
	if copyErr != nil || n > limit {
		command.Process.Kill()
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil || waitErr != nil || n > limit {
		return errDamaged
	}
	return nil
}

// copyMember stores one entry in a CBZ being written, exactly size bytes of it.
func copyMember(all *zip.Writer, name string, body io.Reader, size int64) error {
	w, err := all.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	if n, err := io.Copy(w, io.LimitReader(body, size+1)); err != nil || n != size {
		return errDamaged
	}
	return nil
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
	name := args[2]
	var all *zip.Writer
	if name == copyName {
		all, name = zip.NewWriter(os.Stdout), ""
	} else if name != "" && !safeName(name) {
		return true, errDamaged
	}
	var members []comicMember
	switch args[1] {
	case FormatCBR:
		members, err = rarMembers(os.Stdin, info.Size(), name, os.Stdout, all)
	case FormatCB7:
		members, err = sevenMembers(os.Stdin, info.Size(), name, os.Stdout, all)
	default:
		err = errDamaged
	}
	if all != nil {
		if err == nil && len(members) == 0 {
			err = errDamaged
		}
		if err == nil {
			err = all.Close()
		}
		return true, err
	}
	if err == nil && name == "" {
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
