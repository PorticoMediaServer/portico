package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"sync/atomic"
	"time"
)

var ErrPlaybackSource = errors.New("playback source unavailable or changed")
var ErrPlaybackTimeout = errors.New("playback source read timed out")

const playbackBlock = 256 << 10

var readerID atomic.Uint64

type streamCommand struct {
	Offset int64
	Length int
}
type streamResult struct {
	data []byte
	err  error
}
type streamCall struct {
	command streamCommand
	done    chan streamResult
}

type playbackFailure struct{ err error }

// PlaybackReader never opens or stats a source in the server process. Its child
// owns the descriptor until exit; Supervisor retains admission until Wait returns.
type PlaybackReader struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	input   *io.PipeWriter
	calls   chan streamCall
	timeout time.Duration
	size    int64
	pos     int64
	failure atomic.Pointer[playbackFailure]
}

func (c *Client) OpenPlayback(ctx context.Context, path string, size, modified int64, bindings ...*PlaybackBinding) (*PlaybackReader, error) {
	return c.openPlayback(ctx, path, size, modified, nil, bindings...)
}

func (c *Client) openPlayback(ctx context.Context, path string, size, modified int64, version *mediasource.Evidence, bindings ...*PlaybackBinding) (*PlaybackReader, error) {
	if size < 0 {
		return nil, ErrPlaybackSource
	}
	req, err := c.bindPlaybackRequest(request{Operation: "playback-stream", Path: path, Size: size, ModifiedNS: modified, Version: version}, bindings)
	if err != nil {
		return nil, err
	}
	if c.Guard != nil {
		if e := c.Guard(path); e != nil {
			return nil, e
		}
	}
	managed, err := c.managedRecording(ctx, path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	input, writer := io.Pipe()
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	f := &PlaybackReader{ctx: ctx, cancel: cancel, input: writer, calls: make(chan streamCall), timeout: timeout, size: size}
	req.ManagedArtifact = managed
	if req.MountedRoot == "" && c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	ready := make(chan error, 1)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = input
	supervise.Go("storage.playback-stream", func() {
		defer input.Close()
		defer writer.Close()
		err := c.Supervisor.Run(ctx, "playback:reader:"+fmtReaderID(), cmd, func(out io.Reader) error {
			defer writer.Close() // unblock exec stdin copying before Supervisor waits for exit
			encoder := json.NewEncoder(writer)
			if e := encoder.Encode(req); e != nil {
				return e
			}
			if _, e := readPlaybackFrame(out, 0); e != nil {
				return e
			}
			ready <- nil
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case call := <-f.calls:
					e := encoder.Encode(call.command)
					var b []byte
					if e == nil {
						b, e = readPlaybackFrame(out, call.command.Length)
					}
					call.done <- streamResult{b, e}
					if e != nil {
						return e
					}
				}
			}
		})
		if err != nil {
			f.failure.Store(&playbackFailure{err: err})
			select {
			case ready <- err:
			default:
			}
		}
		cancel()
	})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case e := <-ready:
		if e == nil {
			return f, nil
		}
		f.Close()
		return nil, playbackOpenError(e)
	case <-ctx.Done():
		f.Close()
		return nil, playbackOpenError(f.currentError())
	case <-timer.C:
		f.Close()
		return nil, ErrPlaybackTimeout
	}
}

func (f *PlaybackReader) currentError() error {
	if failure := f.failure.Load(); failure != nil {
		return failure.err
	}
	return f.ctx.Err()
}
func fmtReaderID() string { // internal admission identity, never a path or credential
	n := readerID.Add(1)
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte(n%10) + '0'
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}
func readPlaybackFrame(r io.Reader, limit int) ([]byte, error) {
	var header [5]byte
	if _, e := io.ReadFull(r, header[:]); e != nil {
		return nil, ErrPlaybackSource
	}
	n := binary.BigEndian.Uint32(header[1:])
	if header[0] == 1 && n == 0 {
		return nil, mediasource.ErrSourceChanged
	}
	if header[0] == 2 && n == 0 {
		return nil, mediasource.ErrIdentityRequired
	}
	if header[0] != 0 || n > uint32(limit) || n > playbackBlock {
		return nil, ErrPlaybackSource
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(r, b); e != nil {
		return nil, ErrPlaybackSource
	}
	return b, nil
}
func (f *PlaybackReader) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if f.pos >= f.size {
		return 0, io.EOF
	}
	n := min(len(p), playbackBlock)
	if int64(n) > f.size-f.pos {
		n = int(f.size - f.pos)
	}
	call := streamCall{streamCommand{f.pos, n}, make(chan streamResult, 1)}
	timer := time.NewTimer(f.timeout)
	defer timer.Stop()
	select {
	case f.calls <- call:
	case <-f.ctx.Done():
		return 0, f.currentError()
	case <-timer.C:
		f.Close()
		return 0, ErrPlaybackTimeout
	}
	select {
	case result := <-call.done:
		if result.err != nil {
			return 0, result.err
		}
		if len(result.data) != n {
			return 0, ErrPlaybackSource
		}
		copy(p, result.data)
		f.pos += int64(n)
		return n, nil
	case <-f.ctx.Done():
		return 0, f.currentError()
	case <-timer.C:
		f.Close()
		return 0, ErrPlaybackTimeout
	}
}
func (f *PlaybackReader) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.pos
	case io.SeekEnd:
		base = f.size
	default:
		return 0, os.ErrInvalid
	}
	next := base + offset
	if next < 0 || (offset > 0 && next < base) {
		return 0, os.ErrInvalid
	}
	f.pos = next
	return next, nil
}
func (f *PlaybackReader) Close() error { f.cancel(); return f.input.Close() }

func playbackStreamHelper(req request, in io.Reader, out io.Writer) (result error) {
	defer func() {
		if req.Version == nil || result == nil {
			return
		}
		var code byte
		if errors.Is(result, mediasource.ErrSourceChanged) {
			code = 1
		}
		if errors.Is(result, mediasource.ErrIdentityRequired) {
			code = 2
		}
		if code != 0 {
			_, _ = out.Write([]byte{code, 0, 0, 0, 0})
		}
	}()
	f, e := openPlaybackRequestFile(req)
	if e != nil {
		return ErrPlaybackSource
	}
	defer f.Close()
	if e = lockRecordingFile(req, f); e != nil {
		return e
	}
	check := func() error {
		if req.MountedRoot != "" {
			mounted, e := isMount(req.MountedRoot)
			if e != nil || !mounted {
				return ErrPlaybackSource
			}
		}
		if req.Version != nil {
			if err := matchLocalVersion(f, *req.Version); err != nil {
				return err
			}
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != req.Size || info.ModTime().UnixNano() != req.ModifiedNS {
			return ErrPlaybackSource
		}
		current, e := playbackCurrentInfo(req)
		if e != nil || !os.SameFile(info, current) {
			if req.Version != nil && e == nil {
				return mediasource.ErrSourceChanged
			}
			return ErrPlaybackSource
		}
		return nil
	}
	frame := func(b []byte) error {
		var h [5]byte
		binary.BigEndian.PutUint32(h[1:], uint32(len(b)))
		if _, e := out.Write(h[:]); e != nil {
			return e
		}
		_, e := out.Write(b)
		return e
	}
	if e = check(); e != nil {
		return e
	}
	if e = frame(nil); e != nil {
		return e
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024), 1024)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var command streamCommand
		if e = json.Unmarshal(scanner.Bytes(), &command); e != nil {
			return e
		}
		if command.Length < 1 || command.Length > playbackBlock || command.Offset < 0 || command.Offset > req.Size || int64(command.Length) > req.Size-command.Offset {
			return ErrPlaybackSource
		}
		b, e := checkedPlaybackBlock(f, command, check)
		if e != nil {
			return e
		}
		if e = frame(b); e != nil {
			return e
		}
	}
	return scanner.Err()
}

func checkedPlaybackBlock(reader io.ReaderAt, command streamCommand, check func() error) ([]byte, error) {
	if err := check(); err != nil {
		return nil, err
	}
	b := make([]byte, command.Length)
	n, err := reader.ReadAt(b, command.Offset)
	if err != nil || n != len(b) {
		// A truncate/rewrite may occur after precheck and make ReadAt short.
		// Preserve source_changed when actual post-error evidence establishes it.
		if changed := check(); changed != nil {
			return nil, changed
		}
		return nil, ErrPlaybackSource
	}
	if err := check(); err != nil {
		return nil, err
	}
	return b, nil
}

func playbackOpenError(e error) error {
	if errors.Is(e, ErrBusy) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || errors.Is(e, mediasource.ErrSourceChanged) || errors.Is(e, mediasource.ErrIdentityRequired) {
		return e
	}
	return ErrPlaybackSource
}
