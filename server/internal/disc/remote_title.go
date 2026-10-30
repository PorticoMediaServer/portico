package disc

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// StreamRemoteBluRay reads one exact admitted title from a captured ISO. It
// never chooses a title, acquires another source, or grants playback authority.
// current is mandatory and must check the original source/title/plan/session
// and caller lifetime. The caller owns object until the returned stream closes.
// Inventory/playback admission remains closed until the complete remote path
// is composed and qualified. DVD and folder streaming are not supplied here.
func (t Tool) StreamRemoteBluRay(ctx context.Context, object storage.RemoteObject, original Probe, title Title, ms int64, current func(context.Context) error) (RemoteTitleReader, Seek, error) {
	if current == nil || original.Kind != "bluray" || original.Packaging != "iso" {
		return nil, Seek{}, ErrRemoteUnqualified
	}
	// Own the original structure across asynchronous reads, including nested
	// title/clip/stream slices. Never retain a mutable caller-owned inventory.
	captured, err := json.Marshal(original)
	if err != nil {
		return nil, Seek{}, ErrProtocol
	}
	original, err = DecodeProbe(captured)
	if err != nil {
		return nil, Seek{}, err
	}
	if original.checkTitleInput(title, ms) != nil || title.Playlist == nil || len(title.Clips) == 0 || len(title.Clips) > 4096 {
		return nil, Seek{}, ErrProtocol
	}
	playlist, clipCount := *title.Playlist, int64(len(title.Clips))
	check, release := context.WithTimeout(ctx, 15*time.Second)
	err = current(check)
	if err == nil {
		err = check.Err()
	}
	release()
	if err != nil {
		return nil, Seek{}, err
	}
	// Fresh structure is compared with the accepted original, not adopted as a
	// replacement. All reads still use this one immutable source object.
	raw, err := t.remoteImageMetadata(ctx, object, "probe-image-stream", nil, current)
	if err != nil {
		return nil, Seek{}, err
	}
	actual, err := DecodeProbe(raw)
	if err != nil || !reflect.DeepEqual(actual, original) {
		return nil, Seek{}, storage.ErrRemoteChanged
	}
	landing, err := t.SeekRemoteImage(ctx, object, playlist, ms, current)
	if err != nil {
		return nil, Seek{}, err
	}
	before, version := object.Snapshot(), object.Version().ID()
	// Each ordered playitem can consume at most the image extent, including
	// repeated clips. Two further extents bound metadata/reader overhead. This
	// is an I/O work bound, never a measured bitrate or title byte-size claim.
	if before.Size <= 0 || before.Size > math.MaxInt64/(clipCount+2) {
		return nil, Seek{}, ErrRemoteUnqualified
	}
	maxBytes := before.Size * (clipCount + 2)
	maxRequests := maxBytes/2048 + 1024
	guard := func(run context.Context) error {
		if err := run.Err(); err != nil {
			return err
		}
		if version == "" || object.Version().ID() != version || object.Snapshot() != before {
			return storage.ErrRemoteChanged
		}
		check, cancel := context.WithTimeout(run, 15*time.Second)
		defer cancel()
		if err := current(check); err != nil {
			return err
		}
		return check.Err()
	}
	life, cancel := context.WithCancel(ctx)
	args := []string{strconv.Itoa(playlist), strconv.FormatInt(ms, 10), strconv.FormatInt(landing.ByteOffset, 10), strconv.FormatInt(landing.TitleTimeMs, 10)}
	cmd, finish, err := t.remoteImageCommand(life, object, "stream-bluray-image", args, maxRequests, maxBytes, guard)
	if err != nil {
		cancel()
		return nil, Seek{}, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		cancel()
		finish()
		return nil, Seek{}, err
	}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = writer, io.Discard, time.Second
	if err = guard(life); err == nil {
		err = cmd.Start()
	}
	writer.Close()
	if err != nil {
		reader.Close()
		cancel()
		finish()
		if ctx.Err() != nil {
			return nil, Seek{}, ctx.Err()
		}
		return nil, Seek{}, ErrProtocol
	}
	s := &remoteTitleStream{ctx: life, file: reader, guard: guard, done: make(chan struct{}), stop: func() { cancel(); finish() }}
	supervise.Go("disc.remote-title.wait", func() {
		defer close(s.done)
		err := cmd.Wait()
		finish()
		if err == nil {
			check, release := context.WithTimeout(life, 15*time.Second)
			after, e := object.Observe(check)
			if e != nil || after != before {
				err = storage.ErrRemoteChanged
			} else {
				err = guard(check)
			}
			release()
		}
		s.waitErr = err
	})
	return s, landing, nil
}

// RemoteTitleReader keeps the guarded read surface even when the consumer is
// back-pressured. Check reports an already-known helper/final-observation
// failure without requiring a further Read. It never exposes the helper FD.
type RemoteTitleReader interface {
	io.ReadCloser
	Check(context.Context) error
}

type remoteTitleStream struct {
	ctx      context.Context
	file     io.ReadCloser
	guard    func(context.Context) error
	stop     func()
	done     chan struct{}
	waitErr  error // published only by closing done
	readMu   sync.Mutex
	close    sync.Once
	closeErr error
	buffer   [64 << 10]byte
}

func (s *remoteTitleStream) Check(ctx context.Context) error {
	check, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	if s.ctx.Err() != nil {
		cancel()
	}
	if err := s.guard(check); err != nil {
		return err
	}
	select {
	case <-s.done:
		if s.waitErr != nil {
			return ErrProtocol
		}
	default:
	}
	return check.Err()
}

func (s *remoteTitleStream) Read(p []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.guard(s.ctx); err != nil {
		s.Close()
		return 0, err
	}
	select {
	case <-s.done:
		if s.waitErr != nil {
			s.Close()
			return 0, ErrProtocol
		}
	default:
	}
	// Check the original caller again before publishing a read. Retired bytes
	// are never copied into the downstream consumer's buffer.
	buffer := s.buffer[:min(len(p), len(s.buffer))]
	n, err := s.file.Read(buffer)
	if e := s.guard(s.ctx); e != nil {
		s.Close()
		return 0, e
	}
	// Completion can fail while the pipe read is blocked. Do not publish its
	// buffered bytes after that failure is known, even if authority is current.
	select {
	case <-s.done:
		if s.waitErr != nil {
			s.Close()
			return 0, ErrProtocol
		}
	default:
	}
	if err == io.EOF {
		<-s.done
		if e := s.guard(s.ctx); e != nil {
			s.Close()
			return 0, e
		}
		if s.waitErr != nil {
			s.Close()
			return 0, ErrProtocol
		}
	}
	copy(p, buffer[:n])
	if err != nil && err != io.EOF {
		s.Close()
	}
	return n, err
}

func (s *remoteTitleStream) Close() error {
	s.close.Do(func() {
		s.closeErr = s.file.Close()
		s.stop()
		<-s.done
	})
	return s.closeErr
}
