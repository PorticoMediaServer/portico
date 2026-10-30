package disc

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

const remoteDiscRangeBytes = 2 << 20

// remoteImageCommand exposes only a caller-retained immutable RemoteObject,
// through two inherited pipes. The helper receives neither a locator nor any
// network, source-directory or credential authority. OpenRange retains the
// captured object's existing conditional reads and configuration guards.
//
// This does not admit remote discs into scanning/playback: those routes remain
// closed until the complete stream-mode path is qualified.
func (t Tool) remoteImageCommand(ctx context.Context, object storage.RemoteObject, mode string, arguments []string, maxRequests, maxBytes int64, current func(context.Context) error) (*exec.Cmd, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if current != nil {
		if err := current(ctx); err != nil {
			return nil, nil, err
		}
	}
	if runtime.GOOS == "windows" || object == nil || object.Version().ID() == "" || object.Access() != "finite_random" {
		return nil, nil, ErrRemoteUnqualified
	}
	size := object.Snapshot().Size
	if object.Snapshot().Directory || object.Snapshot().ErrorCode != "" || size <= 0 || size%2048 != 0 || maxRequests <= 0 || maxBytes <= 0 {
		return nil, nil, ErrProtocol
	}
	requests, helperRequests, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	helperData, data, err := os.Pipe()
	if err != nil {
		requests.Close()
		helperRequests.Close()
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var once sync.Once
	closePipes := func() {
		once.Do(func() {
			requests.Close()
			helperRequests.Close()
			helperData.Close()
			data.Close()
		})
	}
	stop := context.AfterFunc(ctx, closePipes)
	finish := func() { cancel(); stop(); closePipes() }
	binaryPath := t.Binary
	if binaryPath == "" {
		binaryPath = mediatools.Resolve("portico-disc")
	}
	args := append([]string{mode, strconv.FormatInt(size, 10)}, arguments...)
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binaryPath, Args: args, Files: []*os.File{helperRequests, helperData}, MaxFileBytes: mediaexec.NoOutputFiles})
	if err != nil {
		finish()
		return nil, nil, err
	}
	done := make(chan struct{})
	supervise.Go("disc.remote-image-ranges", func() {
		defer close(done)
		defer closePipes()
		serveRemoteDiscRanges(ctx, requests, data, object, size, maxRequests, maxBytes, current)
	})
	return cmd, func() { finish(); <-done }, nil
}

// Each request is PDR1, byte offset (uint64 BE), length (uint32 BE). A reply
// is PDA1 and the exact length (uint32 BE), then complete bytes. Refusal closes
// the channel; no partial range is published as a successful response.
func serveRemoteDiscRanges(ctx context.Context, requests io.Reader, data io.Writer, object storage.RemoteObject, size, maxRequests, maxBytes int64, current func(context.Context) error) {
	buffer := make([]byte, remoteDiscRangeBytes)
	var header [16]byte
	for count, used := int64(0), int64(0); count < maxRequests; count++ {
		if _, err := io.ReadFull(requests, header[:]); err != nil || ctx.Err() != nil || string(header[:4]) != "PDR1" {
			return
		}
		offset, length := binary.BigEndian.Uint64(header[4:12]), int64(binary.BigEndian.Uint32(header[12:]))
		if offset >= uint64(size) || length <= 0 || length > remoteDiscRangeBytes || length > size-int64(offset) || length > maxBytes-used {
			return
		}
		used += length
		readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if current != nil && current(readCtx) != nil {
			cancel()
			return
		}
		body, err := object.OpenRange(readCtx, int64(offset), length)
		if err != nil || body == nil {
			if body != nil {
				body.Close()
			}
			cancel()
			return
		}
		_, err = io.ReadFull(body, buffer[:length])
		closeErr := body.Close()
		live := err == nil && closeErr == nil && readCtx.Err() == nil && ctx.Err() == nil && (current == nil || current(readCtx) == nil)
		cancel()
		if !live || ctx.Err() != nil {
			return
		}
		var reply [8]byte
		copy(reply[:4], "PDA1")
		binary.BigEndian.PutUint32(reply[4:], uint32(length))
		if n, e := data.Write(reply[:]); e != nil || n != len(reply) {
			return
		}
		if n, e := data.Write(buffer[:length]); e != nil || int64(n) != length {
			return
		}
	}
}

// ProbeRemoteImage runs the bounded reader against one original captured ISO.
// The caller owns and closes that object after this invocation. A size/mtime
// observation alone is deliberately insufficient authority to issue this job.
func (t Tool) ProbeRemoteImage(ctx context.Context, object storage.RemoteObject) (Probe, error) {
	return t.probeRemoteImage(ctx, object, nil)
}

// ProbeRemoteImageForPlay narrows the existing bounded metadata helper to the
// original Play invocation. The mandatory check runs around each served range
// and after the final observation; it does not acquire or authorize an object.
func (t Tool) ProbeRemoteImageForPlay(ctx context.Context, object storage.RemoteObject, current func(context.Context) error) (Probe, error) {
	if ctx == nil || current == nil || dbwork.Snapshot(ctx) != nil {
		return Probe{}, ErrRemoteUnqualified
	}
	return t.probeRemoteImage(ctx, object, current)
}

func (t Tool) probeRemoteImage(ctx context.Context, object storage.RemoteObject, current func(context.Context) error) (Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := t.remoteImageMetadata(ctx, object, "probe-image-stream", nil, current)
	if err != nil {
		return Probe{}, err
	}
	probe, err := DecodeProbe(raw)
	if ctx.Err() != nil {
		return Probe{}, ctx.Err()
	}
	return probe, err
}

func (t Tool) remoteImageMetadata(ctx context.Context, object storage.RemoteObject, mode string, args []string, current func(context.Context) error) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, finish, err := t.remoteImageCommand(ctx, object, mode, args, 1024, 16<<20, current)
	if err != nil {
		return nil, err
	}
	defer finish()
	before := object.Snapshot()
	out := &outputBuffer{}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, io.Discard, time.Second
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrProtocol
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	after, err := object.Observe(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || after != before {
		return nil, storage.ErrRemoteChanged
	}
	if current != nil {
		if err = current(ctx); err != nil {
			return nil, err
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out.data, nil
}

// SeekRemoteImage uses the original captured image and the same libbluray
// landing facts as local seeking. current must retain the original admitted
// source/title/plan/occurrence and caller lifetime; this method grants none.
func (t Tool) SeekRemoteImage(ctx context.Context, object storage.RemoteObject, playlist int, ms int64, current func(context.Context) error) (Seek, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if current == nil {
		return Seek{}, ErrRemoteUnqualified
	}
	if playlist < 0 || playlist > 99999 || ms < 0 {
		return Seek{}, ErrProtocol
	}
	raw, err := t.remoteImageMetadata(ctx, object, "seek-image-stream", []string{strconv.Itoa(playlist), strconv.FormatInt(ms, 10)}, current)
	if err != nil {
		return Seek{}, err
	}
	seek, err := DecodeSeek(raw, playlist, ms)
	if ctx.Err() != nil {
		return Seek{}, ctx.Err()
	}
	return seek, err
}
