package playback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// conditionalDiscInput consumes this original conditional object after its
// outside-SQL context check; invalid context leaves cleanup with the caller.
// The helper's guarded reader is copied through a private bounded OS pipe; its
// raw stdout descriptor is never handed to FFmpeg, which would bypass Read's
// original authority and known-helper-failure checks. This adapter grants no
// source, title or presentation authority and cannot serve weak/provider guesses.
type conditionalDiscPlaybackInput struct {
	Input   *assets.PlaybackInput
	Quiesce func(context.Context) error
}

func conditionalDiscInput(ctx context.Context, tool disc.Tool, object storage.RemoteObject, structure disc.Probe, title disc.Title, current func(context.Context) error) (_ *conditionalDiscPlaybackInput, err error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || object == nil {
		return nil, disc.ErrProtocol
	}
	defer func() {
		if err != nil {
			object.Close()
		}
	}()
	if current == nil || object.Access() != "finite_random" {
		return nil, disc.ErrRemoteUnqualified
	}
	version, snapshot := object.Version(), object.Snapshot()
	if version.ID() == "" || version.Evidence().Kind != mediasource.StrongETag && version.Evidence().Kind != mediasource.ImmutableProviderVersion {
		return nil, disc.ErrRemoteUnqualified
	}
	raw, err := json.Marshal(structure)
	if err != nil {
		return nil, disc.ErrProtocol
	}
	structure, err = disc.DecodeProbe(raw)
	if err != nil || structure.Packaging != "iso" || structure.Kind != "bluray" {
		return nil, disc.ErrRemoteUnqualified
	}
	// Retain the private decoded title rather than the caller's nested slices.
	key, matches := title.Key(structure.Kind), 0
	var selected disc.Title
	for _, candidate := range structure.Titles {
		if candidate.Key(structure.Kind) == key {
			if !reflect.DeepEqual(candidate, title) {
				return nil, disc.ErrProtocol
			}
			selected, matches = candidate, matches+1
		}
	}
	if matches != 1 {
		return nil, disc.ErrProtocol
	}
	// FFmpegInput is also the existing encrypted/title-membership validator;
	// no local pathname from this value is used for the conditional pipe reader.
	if _, err = structure.FFmpegInput(disc.DescriptorPath(false), selected, 0, nil); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	owner := &conditionalDiscPipe{life: life, cancel: cancel, object: object}
	var input *assets.PlaybackInput
	checkSource := func(check context.Context) error {
		if err := check.Err(); err != nil {
			return err
		}
		if err := life.Err(); err != nil {
			return err
		}
		if object.Version().ID() != version.ID() || object.Snapshot() != snapshot {
			cancel()
			return mediasource.ErrSourceChanged
		}
		if err := current(check); err != nil {
			cancel()
			return err
		}
		return life.Err()
	}
	validate := func(check context.Context) error {
		if err := checkSource(check); err != nil {
			return err
		}
		owner.mu.Lock()
		active := owner.active
		fileChanged := active != nil && input.File != active.file
		owner.mu.Unlock()
		if fileChanged {
			cancel()
			return mediasource.ErrSourceChanged
		}
		if active != nil {
			refresh := func(ctx context.Context) error {
				// An early consumer stop has no accepted helper EOF/final
				// observation. Reprove this same conditional object rather
				// than promoting the abandoned stream to successful EOF.
				actual, err := object.Observe(ctx)
				if err != nil {
					return err
				}
				if actual != snapshot {
					return mediasource.ErrSourceChanged
				}
				return checkSource(ctx)
			}
			if err := checkConditionalDiscAttempt(check, active, refresh); err != nil {
				cancel()
				return err
			}
		}
		return life.Err()
	}
	if err = validate(ctx); err != nil {
		cancel()
		return nil, err
	}
	options := []string{"-protocol_whitelist", "pipe", "-format_whitelist", "mpegts", "-f", "mpegts"}
	input = &assets.PlaybackInput{Inherit: true, Argument: "pipe:3", Options: append([]string(nil), options...), Validate: validate, Lifetime: life, ReleaseSource: owner.Close}
	input.InputAt = func(check context.Context, ms int64) ([]string, string, int64, error) {
		if check == nil || dbwork.Snapshot(check) != nil {
			return nil, "", 0, disc.ErrProtocol
		}
		owner.openMu.Lock()
		defer owner.openMu.Unlock()
		if e := validate(check); e != nil {
			return nil, "", 0, e
		}
		owner.mu.Lock()
		previous := owner.active
		owner.active = nil
		owner.mu.Unlock()
		if previous != nil {
			previous.Close()
		}
		// Preparation observes this command's budget. Once the stream has
		// been handed off, its owner is the retained input: a completed probe's
		// temporary timeout context must not retire the still-pending original
		// measurement before its accepting writer has checked it.
		readCtx, stop := context.WithCancel(life)
		stopPreparation := context.AfterFunc(check, stop)
		defer stopPreparation()
		if check.Err() != nil {
			stop()
		}
		release := stop
		stream, landing, e := tool.StreamRemoteBluRay(readCtx, object, structure, selected, ms, checkSource)
		if e != nil {
			release()
			return nil, "", 0, e
		}
		if e = check.Err(); e == nil {
			e = readCtx.Err()
		}
		if e != nil {
			release()
			stream.Close()
			return nil, "", 0, e
		}
		reader, writer, e := os.Pipe()
		if e != nil {
			release()
			stream.Close()
			return nil, "", 0, e
		}
		attempt := &conditionalDiscAttempt{file: reader, writer: writer, source: stream, release: release, readDone: make(chan struct{}), done: make(chan struct{})}
		owner.mu.Lock()
		if life.Err() != nil || owner.closed {
			owner.mu.Unlock()
			reader.Close()
			writer.Close()
			release()
			stream.Close()
			return nil, "", 0, context.Canceled
		}
		owner.active, input.File = attempt, reader
		owner.mu.Unlock()
		supervise.Go("playback.disc.conditional-pipe", func() {
			defer close(attempt.done)
			attempt.complete, attempt.err = copyConditionalDisc(readCtx, writer, stream)
			close(attempt.readDone)
			if attempt.err != nil && !attempt.stopping.Load() {
				cancel()
			}
			writer.Close()
			stream.Close()
			release()
		})
		if e = validate(check); e != nil {
			attempt.Close()
			return nil, "", 0, e
		}
		return append([]string(nil), options...), "pipe:3", landing.TitleTimeMs, nil
	}
	return &conditionalDiscPlaybackInput{Input: input, Quiesce: func(check context.Context) error {
		return quiesceConditionalDiscInput(check, owner, input, snapshot, validate, checkSource)
	}}, nil
}

func quiesceConditionalDiscInput(check context.Context, owner *conditionalDiscPipe, input *assets.PlaybackInput, snapshot storage.Snapshot, validate, checkSource func(context.Context) error) error {
	if check == nil || dbwork.Snapshot(check) != nil {
		return disc.ErrProtocol
	}
	// No child may retain the worker's old claim across the prepared CAS.
	// Keep the original conditional object, but stop/join its one probe run.
	owner.openMu.Lock()
	defer owner.openMu.Unlock()
	if err := validate(check); err != nil {
		return err
	}
	owner.mu.Lock()
	active := owner.active
	owner.mu.Unlock()
	if active != nil {
		active.Close()
		// Close-induced cancellation is not a successful EOF. Preserve any
		// actual non-cancel helper failure, then freshly check the SAME object.
		if active.err != nil && !errors.Is(active.err, context.Canceled) {
			owner.cancel()
			return active.err
		}
	}
	if err := checkSource(check); err != nil {
		return err
	}
	actual, err := owner.object.Observe(check)
	if err != nil {
		owner.cancel()
		return err
	}
	if actual != snapshot {
		owner.cancel()
		return mediasource.ErrSourceChanged
	}
	if err = checkSource(check); err != nil {
		return err
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.active != active || owner.closed {
		owner.cancel()
		return context.Canceled
	}
	owner.active, input.File = nil, nil
	return owner.life.Err()
}

type conditionalDiscPipe struct {
	life     context.Context
	cancel   context.CancelFunc
	object   storage.RemoteObject
	openMu   sync.Mutex // InputAt serializes source replacement; no child overlap.
	mu       sync.Mutex
	active   *conditionalDiscAttempt
	closed   bool
	once     sync.Once
	closeErr error
}

func (p *conditionalDiscPipe) Close() error {
	p.once.Do(func() {
		p.cancel()
		p.mu.Lock()
		p.closed = true
		active := p.active
		p.mu.Unlock()
		if active != nil {
			active.Close()
		}
		// Join any original in-flight preparation before releasing its object.
		p.openMu.Lock()
		p.openMu.Unlock()
		p.closeErr = p.object.Close()
	})
	return p.closeErr
}

type conditionalDiscAttempt struct {
	file, writer *os.File
	source       disc.RemoteTitleReader
	release      func()
	readDone     chan struct{}
	done         chan struct{}
	err          error // published by closing readDone
	complete     bool  // true only for fully checked EOF, never pipe abandonment
	once         sync.Once
	stopping     atomic.Bool
}

func copyConditionalDisc(ctx context.Context, writer io.Writer, source disc.RemoteTitleReader) (bool, error) {
	buffer := make([]byte, 64<<10)
	for {
		n, err := source.Read(buffer)
		if err != nil && err != io.EOF {
			return false, err
		}
		if n > 0 {
			if e := source.Check(ctx); e != nil {
				return false, e
			}
			written, e := writer.Write(buffer[:n])
			if e != nil || written != n {
				// The converter may stop or refuse an encoder without proving
				// source failure. Preserve any failure that became known while
				// Write was blocked BEFORE closing/cancelling the helper can
				// obscure that outcome. A benign stop still is not checked EOF.
				return false, source.Check(ctx)
			}
		}
		if err == io.EOF {
			return true, nil // Read checked helper exit and final observation.
		}
		if err = ctx.Err(); err != nil {
			return false, err
		}
	}
}

// checkConditionalDiscAttempt distinguishes a fully checked helper EOF from an
// early consumer stop. In the latter case the closed helper cannot be checked
// as live: a fresh check of the same retained conditional object is mandatory.
func checkConditionalDiscAttempt(ctx context.Context, active *conditionalDiscAttempt, refresh func(context.Context) error) error {
	completed := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if active.err != nil {
			return active.err
		}
		if !active.complete {
			if refresh == nil {
				return disc.ErrRemoteUnqualified
			}
			return refresh(ctx)
		}
		return ctx.Err()
	}
	select {
	case <-active.readDone:
		return completed()
	default:
	}
	err := active.source.Check(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		// In particular, a known helper/final-observation failure must not be
		// replaced by a concurrent benign consumer-stop completion.
		return err
	}
	// Completion may have closed the original helper during Check; consult
	// its actual result rather than treating cancellation as source failure.
	select {
	case <-active.readDone:
		return completed()
	default:
		return err
	}
}

func (a *conditionalDiscAttempt) Close() {
	a.once.Do(func() {
		a.stopping.Store(true)
		a.release()
		a.file.Close() // unblocks the bounded pipe writer before joining it
		a.writer.Close()
		a.source.Close()
		<-a.done
	})
}
