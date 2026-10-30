package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// EpisodeReadSource retains the real typed policy authority and source lease
// through publication. It is internal, never an engine-availability switch.
type EpisodeReadSource struct {
	authority *EpisodeAuthority
	remote    *SourceReadLease
	roots     *sourceaccess.Registry
	life      context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	opened    bool
	closed    bool
}

// ResolveEpisodeReadSourceTx is DB-only. Its context is the actual retained
// invocation lifetime, not a setup/transaction timeout or detached background.
// The only nil success is a typed, positively known automatic tier exclusion.
func (r EpisodeAuthorityResolver) ResolveEpisodeReadSourceTx(ctx context.Context, tx *sql.Tx, request EpisodeAuthorityRequest, onChanged func()) (*EpisodeReadSource, error) {
	if onChanged == nil {
		return nil, ErrEpisodeAuthority
	}
	policy, err := r.ResolveEpisodeAuthorityTx(ctx, tx, request)
	if request.Purpose == EpisodeAutomatic && (err == ErrEpisodeAutomaticSourceTier || errors.Is(err, storage.ErrRemoteAnalysisDisabled) || errors.Is(err, mediaanalysis.ErrUnsupported)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	source := &EpisodeReadSource{authority: policy, life: life, cancel: cancel}
	captured := policy.captured
	switch captured.Source.Kind {
	case "remote":
		purpose := SourceEpisodeAutomatic
		if request.Purpose == EpisodeExplicitOneEpisode {
			purpose = SourceEpisodeExplicit
		}
		source.remote, err = r.Sources.CaptureReadSourceTx(life, tx, SourceSessionAdmission{Purpose: purpose, Observation: captured.Observation, OnChanged: onChanged,
			CheckTx: func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
				if binding.Purpose != purpose || binding.Observation != captured.Observation || binding.Path != captured.File || binding.Profile != captured.Profile || binding.Size != captured.Size || binding.Sequential {
					return ErrEpisodeAuthority
				}
				return policy.CheckTx(ctx, tx)
			}})
	case "local":
		if r.LocalStorage == nil || r.Sources.mounts == nil {
			err = ErrEpisodeAuthority
		} else {
			source.roots = sourceaccess.New(r.LocalStorage, r.Sources.mounts)
		}
	default:
		err = ErrEpisodeAuthority
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return source, nil
}

func (s *EpisodeReadSource) Authorize(ctx context.Context, operation, cost string, manual bool) error {
	if err := s.checkLife(ctx); err != nil {
		return err
	}
	if err := s.authority.Authorize(ctx, operation, cost, manual); err != nil {
		s.cancel()
		return err
	}
	if s.remote != nil {
		return s.remote.Check(ctx)
	}
	return nil
}

func (s *EpisodeReadSource) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if err := s.checkLife(ctx); err != nil {
		return err
	}
	if err := s.authority.CheckTx(ctx, tx); err != nil {
		s.cancel()
		return err
	}
	if s.remote != nil {
		return s.remote.CheckTx(ctx, tx)
	}
	return nil
}

func (s *EpisodeReadSource) checkLife(ctx context.Context) error {
	if s == nil || s.authority == nil || s.life == nil {
		return ErrEpisodeAuthority
	}
	if err := ctx.Err(); err != nil {
		s.cancel()
		return err
	}
	return s.life.Err()
}

func (s *EpisodeReadSource) Open(ctx context.Context) (mediaanalysis.Input, error) {
	if err := s.checkLife(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.opened || s.closed {
		s.mu.Unlock()
		return nil, ErrEpisodeAuthority
	}
	s.opened = true
	s.mu.Unlock()
	request := s.authority.request
	if err := s.Authorize(ctx, "episode_thumbnail", "decode", request.Purpose == EpisodeExplicitOneEpisode); err != nil {
		return nil, err
	}
	if s.remote != nil {
		reader, err := s.remote.OpenRange(ctx)
		if err != nil {
			return nil, err
		}
		return &episodeRangeInput{source: s, reader: reader}, nil
	}
	captured := s.authority.captured
	relative, err := filepath.Rel(captured.Source.ResolvedPath, captured.File)
	if err != nil || !filepath.IsLocal(relative) || relative == "." || s.roots == nil {
		return nil, ErrEpisodeAuthority
	}
	lease, err := s.roots.Borrow(ctx, sourceaccess.Authority{OwnerID: identity.Token(), RootID: captured.Source.ID, RootPath: captured.Source.ResolvedPath, RelativePath: relative, Revision: captured.Source.Incarnation + ":" + request.Target.SourceRevision, Lifetime: s.life, Validate: s.authority.Check})
	if err != nil {
		return nil, err
	}
	reader, err := s.authority.resolver.LocalStorage.OpenObservedPlayback(ctx, lease)
	if err != nil {
		return nil, err
	}
	input := &episodeLocalInput{source: s, reader: reader}
	input.observed, err = reader.Observe(ctx)
	if err == nil && (!input.observed.SizeKnown || !input.observed.ModifiedKnown || input.observed.Size != captured.Size || input.observed.ModifiedNS != captured.ModifiedNS) {
		err = storage.ErrRemoteChanged
	}
	if err == nil {
		// The helper reports the identity of the actual borrowed root and file.
		// Matching size/mtime alone must not admit a replacement before Open.
		root, bound := strings.CutPrefix(input.observed.RootBindingID, lease.RegistrationID+":")
		if !bound || !storage.SameRoot(captured.Source.RootIdentity, root) {
			err = storage.ErrRemoteChanged
		} else {
			fresh := storage.Snapshot{Path: captured.File, Size: input.observed.Size, ModifiedNS: input.observed.ModifiedNS, ObjectIdentity: input.observed.ObjectBindingID, ChangeToken: input.observed.ChangeToken}
			revision, _, e := catalog.ObservationRevision(captured.Source, fresh)
			if e != nil || revision != request.Target.SourceRevision {
				err = storage.ErrRemoteChanged
			}
		}
	}
	if err == nil {
		err = input.Validate(ctx)
	}
	if err != nil {
		input.Close()
		return nil, err
	}
	return input, nil
}

func (s *EpisodeReadSource) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.remote != nil {
		return s.remote.Close()
	}
	if s.roots != nil {
		return s.roots.Close()
	}
	return nil
}

type episodeRangeInput struct {
	source *EpisodeReadSource
	reader *SourceRange
}

func (i *episodeRangeInput) Evidence() string {
	return "observed:" + i.source.authority.request.Target.SourceRevision
}
func (i *episodeRangeInput) Size() int64                        { return i.source.authority.captured.Size }
func (i *episodeRangeInput) Validate(ctx context.Context) error { return i.reader.Check(ctx) }
func (i *episodeRangeInput) StopAndRecheck(ctx context.Context) error {
	if i.source == nil || i.source.remote == nil {
		return ErrEpisodeAuthority
	}
	return i.source.remote.StopAndRecheck(ctx)
}
func (i *episodeRangeInput) Close() error { return i.reader.Close() }
func (i *episodeRangeInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if length < 1 || length > 1<<20 || offset < 0 || offset > i.Size()-length {
		return nil, ErrEpisodeAuthority
	}
	p := make([]byte, int(length))
	n, err := i.reader.ReadAt(ctx, p, offset)
	if err != nil || n != len(p) {
		clear(p)
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return p, nil
}

type episodeLocalInput struct {
	source   *EpisodeReadSource
	reader   *storage.ObservedPlaybackReader
	observed storage.ObservedFileObservation
}

func (i *episodeLocalInput) Evidence() string {
	return "observed:" + i.source.authority.request.Target.SourceRevision
}
func (i *episodeLocalInput) Size() int64  { return i.source.authority.captured.Size }
func (i *episodeLocalInput) Close() error { return i.reader.Close() }
func (i *episodeLocalInput) Validate(ctx context.Context) error {
	r := i.source.authority.request
	if err := i.source.Authorize(ctx, "episode_thumbnail", "decode", r.Purpose == EpisodeExplicitOneEpisode); err != nil {
		return err
	}
	now, err := i.reader.Observe(ctx)
	if err != nil {
		i.source.cancel()
		return err
	}
	if now != i.observed {
		i.source.cancel()
		return storage.ErrRemoteChanged
	}
	return nil
}
func (i *episodeLocalInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if length < 1 || length > 1<<20 || offset < 0 || offset > i.Size()-length {
		return nil, ErrEpisodeAuthority
	}
	if err := i.Validate(ctx); err != nil {
		return nil, err
	}
	data := make([]byte, 0, int(length))
	for int64(len(data)) < length {
		n := min(int(length)-len(data), storage.ObservedReadLimit)
		part, err := i.reader.ReadExtent(ctx, offset+int64(len(data)), n)
		if err != nil || len(part) != n {
			clear(data)
			clear(part)
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		data = append(data, part...)
	}
	if err := i.Validate(ctx); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}
