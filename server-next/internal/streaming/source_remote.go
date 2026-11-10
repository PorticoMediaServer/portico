package streaming

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// RemoteFile is a file on a remote source as the scanner recorded it: its path within the
// source (the library folder's path there, then the file's place beneath it, never text from
// a request), size and modified time. storage is the source behind its throttle, the same
// one its scans use, so playing and scanning share the source's limits.
func RemoteFile(storage remote.Storage, path string, size int64, modified time.Time) Source {
	return remoteFile{storage: storage, path: path, size: size, modified: modified}
}

type remoteFile struct {
	storage  remote.Storage
	path     string
	size     int64
	modified time.Time
}

func (f remoteFile) open(ctx context.Context) (remote.File, error) {
	file, err := f.storage.Open(ctx, f.path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrSourceUnavailable
	}
	if f.size > 0 && file.Size() != f.size {
		file.Close()
		return nil, ErrSourceChanged
	}
	return file, nil
}

// Open gives the player the file: each place it starts reading from is one request for the
// rest of the file, read for as long as the player reads.
func (f remoteFile) Open(ctx context.Context) (io.ReadSeekCloser, int64, time.Time, error) {
	file, err := f.open(ctx)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	return &remoteReader{file: file}, file.Size(), f.modified, nil
}

// remoteReader reads a remote file from where it was last sought to.
type remoteReader struct {
	file remote.File
	pos  int64
	body io.ReadCloser
}

func (r *remoteReader) Read(p []byte) (int, error) {
	if r.pos >= r.file.Size() {
		return 0, io.EOF
	}
	if r.body == nil {
		body, err := remote.StreamFrom(r.file, r.pos)
		if err != nil {
			return 0, err
		}
		r.body = body
	}
	n, err := r.body.Read(p)
	r.pos += int64(n)
	if errors.Is(err, io.EOF) && r.pos < r.file.Size() {
		// The source stopped sending early: the next read asks again from here.
		r.body.Close()
		r.body, err = nil, nil
	}
	return n, err
}

func (r *remoteReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		offset += r.file.Size()
	}
	if offset < 0 {
		return 0, errors.New("seek before the start of the file")
	}
	if offset != r.pos && r.body != nil {
		r.body.Close()
		r.body = nil
	}
	r.pos = offset
	return offset, nil
}

func (r *remoteReader) Close() error {
	if r.body != nil {
		r.body.Close()
	}
	return r.file.Close()
}

// windowSource is a source the converter cannot be handed as a file. It is served to the
// converter on a private loopback address instead (package inputbridge).
type windowSource interface {
	openWindow(ctx context.Context, ffmpeg, ffprobe string) (*PlaybackInput, subtitles.WindowInput, error)
}

func (f remoteFile) openWindow(ctx context.Context, ffmpeg, ffprobe string) (*PlaybackInput, subtitles.WindowInput, error) {
	file, err := f.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	reader := &sequentialReaderAt{file: file}
	bridge, err := inputbridge.Open(ctx, reader, file.Size())
	if err != nil {
		reader.Close()
		return nil, nil, ErrSourceUnavailable
	}
	return &PlaybackInput{ReleaseSource: reader.Close}, &bridgeWindow{bridge: bridge, ffmpeg: ffmpeg, ffprobe: ffprobe}, nil
}

// sequentialReaderAt keeps one request open for a converter reading straight through the
// file, and starts a new one only where it jumps.
type sequentialReaderAt struct {
	mu   sync.Mutex
	file remote.File
	body io.ReadCloser
	pos  int64
}

func (r *sequentialReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := r.file.Size()
	if off >= size {
		return 0, io.EOF
	}
	if r.body == nil || off != r.pos {
		if r.body != nil {
			r.body.Close()
		}
		body, err := remote.StreamFrom(r.file, off)
		if err != nil {
			r.body = nil
			return 0, err
		}
		r.body, r.pos = body, off
	}
	n, err := io.ReadFull(r.body, p[:min(int64(len(p)), size-off)])
	r.pos += int64(n)
	if err != nil {
		r.body.Close()
		r.body = nil
		return n, err
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *sequentialReaderAt) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.body != nil {
		r.body.Close()
		r.body = nil
	}
	return r.file.Close()
}

// bridgeWindow runs the converter and its index probe on the bridge's address, the only
// place they may connect to.
type bridgeWindow struct {
	bridge          *inputbridge.Bridge
	ffmpeg, ffprobe string
}

func (w *bridgeWindow) loopback() string {
	u, _ := url.Parse(w.bridge.URL())
	return u.Host
}

func (w *bridgeWindow) Probe(ctx context.Context, args []string) ([]byte, error) {
	full := append([]string{"-v", "error", "-protocol_whitelist", "http,tcp", "-format_whitelist", mediaexec.LocalInputFormats}, args...)
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: w.ffprobe, Args: append(full, w.bridge.URL()), Loopback: w.loopback()})
	if err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{buffer: &out, limit: 1 << 20}
	if err := childprocess.Run(ctx, cmd); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Run starts the converter and returns once it has exited.
func (w *bridgeWindow) Run(ctx context.Context, dir string, build func(string) (subtitles.WindowCommand, error)) error {
	spec, err := build(w.bridge.URL())
	if err != nil {
		return err
	}
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: w.ffmpeg, Args: spec.Args, Loopback: w.loopback(), WriteDirs: []string{dir},
		Fonts: true, Hardware: spec.Hardware})
	if err != nil {
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	tail := &tailWriter{limit: 4096}
	cmd.Stderr = tail
	if spec.Started != nil {
		ctx = childprocess.WithStarted(ctx, spec.Started)
	}
	if err := childprocess.Run(ctx, cmd); err != nil {
		return errors.Join(err, errors.New(tail.String()))
	}
	return nil
}

func (w *bridgeWindow) Close() error { return w.bridge.Close() }
