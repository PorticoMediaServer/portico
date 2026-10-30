package reading

import (
	"context"
	"database/sql"
	"io"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

func (s *Session) liveFor(p identity.Principal, v Selection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.life.Err() != nil || s.actor != p.Hash || s.owner != identity.PersonalKey(p.Viewer) || s.selection.Revision != v.Revision || s.selection.ItemID != v.ItemID || s.selection.SourceID != v.SourceID {
		return identity.ErrForbidden
	}
	return nil
}

// A provisional session already owns its real actor/publication tuple and
// lifetime during inspection. It becomes public only after successful parsing
// and final authority checks; failure closes its lease instead of orphaning it.
func (s *Service) openRemote(ctx context.Context, p identity.Principal, v Selection, session *Session) (*Source, error) {
	// Local-only archive staging must refuse before any remote controller,
	// provider acquisition or version/manifest I/O, including session reopen.
	if v.Format == "cb7" || v.Format == "cbr" {
		return nil, ErrArchiveFormat
	}
	if s.Remote == nil || p.ExpiresAt.IsZero() || !p.ExpiresAt.After(time.Now()) {
		return nil, ErrUnavailable
	}
	provisional := session == nil
	if provisional {
		life, cancel := context.WithDeadline(context.Background(), p.ExpiresAt)
		session = &Session{ID: identity.Token(), selection: v, actor: p.Hash, owner: identity.PersonalKey(p.Viewer), life: life, cancel: cancel}
	}
	good := false
	defer func() {
		if !good && provisional {
			session.close()
		}
	}()
	if err := session.liveFor(p, v); err != nil {
		return nil, err
	}
	if provisional {
		g, err := dbwork.BeginSnapshot(ctx, s.DB)
		if err != nil {
			return nil, err
		}
		defer g.Rollback()
		current, err := s.selectTx(ctx, g.Tx(), p, v.ItemID, v.SourceID)
		if err != nil {
			return nil, err
		}
		if current.Revision != v.Revision {
			return nil, ErrChanged
		}
		recorded, _, err := s.Remote.ScanSnapshotTx(ctx, g.Tx(), v.path)
		if err != nil {
			return nil, err
		}
		lease, err := s.Remote.CaptureReadSourceTx(session.life, g.Tx(), remotesources.SourceSessionAdmission{
			Purpose: remotesources.SourceReading, Observation: recorded.Ref, OnChanged: func() {
				session.close()
				if s.Changed != nil {
					s.Changed(v.library, v.rootID)
				}
			},
			CheckTx: func(ctx context.Context, tx *sql.Tx, binding remotesources.SourceReadBinding) error {
				return s.checkRemoteTx(ctx, tx, p, session, v, binding)
			},
		})
		if err != nil {
			return nil, err
		}
		session.mu.Lock()
		if session.closed || session.life.Err() != nil {
			session.mu.Unlock()
			_ = lease.Close()
			return nil, ErrChanged
		}
		session.remote = lease
		session.mu.Unlock()
		g.Rollback()
	}
	if err := session.remote.Check(ctx); err != nil {
		return nil, err
	}
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(session.life, cancel)
	good = true
	return &Source{Selection: v, ctx: request, p: p, service: s, remoteSession: session, remoteRetained: !provisional, validateCurrent: func(ctx context.Context) error { return s.validate(ctx, p, v) }, remoteCancel: func() { stop(); cancel() }}, nil
}

// CheckTx is pure current DB/session authority. The Remote controller supplies
// caps/profile/process/normalized-observation fences around it; neither a pool
// label, scan tier nor a heavy-work window can authorize or block this read.
func (s *Service) checkRemoteTx(ctx context.Context, tx *sql.Tx, p identity.Principal, session *Session, selected Selection, binding remotesources.SourceReadBinding) error {
	if err := session.liveFor(p, selected); err != nil {
		return err
	}
	now, err := s.selectTx(ctx, tx, p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if now.Revision != selected.Revision || binding.Purpose != remotesources.SourceReading || binding.Path != now.path || binding.Size != now.Size {
		return ErrChanged
	}
	recorded, snapshot, err := s.Remote.ScanSnapshotTx(ctx, tx, now.path)
	if err != nil {
		return err
	}
	if recorded.Ref != binding.Observation || snapshot.Path != now.path || snapshot.ChangeToken != binding.ScanRevision || snapshot.Size != now.Size {
		return ErrChanged
	}
	// The folder source identity is separate from the immutable asset token.
	// Transport ScanRevision is not the catalogue's inventory-object revision.
	var folder catalog.LibrarySource
	err = tx.QueryRowContext(ctx, `SELECT id,library_id,kind,root,generation,incarnation,root_identity,identity_confirmed FROM library_sources WHERE id=? AND library_id=? AND enabled=1`, now.rootID, now.library).Scan(&folder.ID, &folder.LibraryID, &folder.Kind, &folder.ResolvedPath, &folder.Generation, &folder.Incarnation, &folder.RootIdentity, &folder.IdentityConfirmed)
	if err != nil {
		return err
	}
	if folder.Kind != "remote" || !folder.IdentityConfirmed || folder.ResolvedPath != now.root || folder.Generation != now.generation || folder.Incarnation != now.incarnation || folder.RootIdentity != now.rootIdentity {
		return ErrChanged
	}
	revision, evidence, err := catalog.ObservationRevision(folder, snapshot)
	if err != nil {
		return err
	}
	if revision != now.inventory || evidence != now.evidence {
		return ErrChanged
	}
	return session.liveFor(p, selected)
}

func (f *Source) Sequential() bool {
	return f.remoteSession != nil && f.remoteSession.remote.Binding().Sequential
}

// OpenFromStart never seeks, replays a prefix or manufactures ReaderAt. Each
// call is a new genuine upstream stream bound to the same retained revision.
func (f *Source) OpenFromStart() (io.ReadCloser, error) {
	if !f.Sequential() {
		return nil, ErrInvalid
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	stream, err := f.remoteSession.remote.OpenSequential(f.ctx)
	if err != nil {
		return nil, err
	}
	return &readingSequential{source: f, stream: stream}, nil
}

type readingSequential struct {
	source *Source
	stream *remotesources.SourceSequential
}

func (s *readingSequential) Read(p []byte) (int, error) {
	// The transport rechecks the actual session before and after every bounded
	// extent, discarding an extent if it loses authority before publication.
	return s.stream.Read(p[:min(len(p), 1<<20)])
}
func (s *readingSequential) Close() error { return s.stream.Close() }

func (f *Source) epub() (*EPUB, error) {
	if f.derived != nil {
		return f.derived.EPUB()
	}
	if f.Sequential() {
		return OpenSequentialEPUB(f.ctx, SequentialSource{Size: f.Size, Open: func(ctx context.Context) (io.ReadCloser, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return f.OpenFromStart()
		}})
	}
	return OpenEPUB(f.ctx, f, f.Size)
}
func (f *Source) comic() (*Comic, error) {
	if f.Format == "cb7" || f.Format == "cbr" {
		if !f.LocalArchiveStaging() {
			return nil, ErrArchiveFormat
		}
		return OpenClassicComic(f.ctx, f.Format, f, f.Size)
	}
	if f.Sequential() {
		return OpenSequentialComic(f.ctx, SequentialSource{Size: f.Size, Open: func(ctx context.Context) (io.ReadCloser, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return f.OpenFromStart()
		}})
	}
	return OpenComic(f.ctx, f, f.Size)
}

func (f *Source) EPUB() (*EPUB, error)   { return f.epub() }
func (f *Source) Comic() (*Comic, error) { return f.comic() }

func (f *Source) CheckPDF() error {
	var header [5]byte
	if f.Sequential() {
		body, err := f.OpenFromStart()
		if err != nil {
			return err
		}
		_, err = io.ReadFull(body, header[:])
		_ = body.Close()
		if err != nil {
			return err
		}
	} else if _, err := f.ReadAt(header[:], 0); err != nil {
		return err
	}
	if string(header[:]) != "%PDF-" {
		return ErrInvalid
	}
	return f.Validate()
}

var _ io.ReadCloser = (*readingSequential)(nil)
