package remotesources

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// sourceMetadataCapture records source facts for the existing storage Stat
// caller. It grants no content read or decode; those still require a retained
// purpose-specific SourceReadLease. Root/library admission belongs to the caller.
type sourceMetadataCapture struct {
	source     binding
	generation string
	profile    SourceReadProfile
}

func captureSourceMetadataTx(ctx context.Context, tx *sql.Tx, b binding) (sourceMetadataCapture, error) {
	if tx == nil || b.ID == "" || b.Removed || !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root {
		return sourceMetadataCapture{}, storage.ErrRemoteConfig
	}
	var kind, root string
	if err := tx.QueryRowContext(ctx, `SELECT kind,root FROM remote_sources WHERE id=? AND removed=0`, b.ID).Scan(&kind, &root); err != nil {
		return sourceMetadataCapture{}, err
	}
	if root != b.Root || kind != b.Kind {
		return sourceMetadataCapture{}, storage.ErrRemoteChanged
	}
	generation, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return sourceMetadataCapture{}, err
	}
	profile, err := SourceReadProfileTx(ctx, tx, b.ID)
	if err != nil {
		return sourceMetadataCapture{}, err
	}
	return sourceMetadataCapture{source: b, generation: generation, profile: profile}, nil
}

func (s *Service) checkSourceMetadata(ctx context.Context, captured sourceMetadataCapture) error {
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	current, err := captureSourceMetadataTx(ctx, view.Tx(), captured.source)
	if err != nil {
		return err
	}
	if current != captured {
		return storage.ErrRemoteChanged
	}
	return nil
}

// statSourceMetadata replaces per-call native helpers and direct DAV requests
// with the same admitted process used by listings and retained readers. Every
// metadata/observer request draws from its single source-wide operation budget.
// Equal size/time/hash facts remain observations, never an immutable Version.
func (s *Service) statSourceMetadata(ctx context.Context, b binding, object string) (value storage.Snapshot, err error) {
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return value, err
	}
	captured, err := captureSourceMetadataTx(ctx, view.Tx(), b)
	view.Rollback()
	if err != nil {
		return value, err
	}
	var process *rcloneprogram.ServeProcess
	defer func() {
		err = mapError(err)
		// The process lease is the original custody: a retired process proves
		// no provider answer, so neither state nor contact is recorded.
		s.health(process != nil && process.CheckLocal(ctx) == nil, b, captured.generation, err)
		if err != nil {
			value = storage.Snapshot{}
		}
	}()
	process, err = s.ensureSourceServe(ctx, SourceReadBinding{Root: b.Root, Profile: captured.profile, Observation: ObservationRef{SourceID: b.ID, Generation: captured.generation}}, "")
	if err != nil {
		return value, err
	}
	check := func(ctx context.Context) error {
		if err := s.checkSourceMetadata(ctx, captured); err != nil {
			return err
		}
		return process.Check(ctx)
	}
	if err = check(ctx); err != nil {
		return value, err
	}
	observer, err := process.ObservationEndpoint(ctx)
	if err != nil {
		return value, err
	}
	served, err := observer.Stat(ctx, object, check)
	if err != nil {
		return value, err
	}
	if served.Directory {
		// Directory identity is scoped to source/path/generation. Deletion
		// proof still requires complete, fenced directory enumeration.
		value = inventorySnapshot(b, captured.generation, davSnapshot(served, b.Root))
	} else {
		facts, e := observeProcessFile(ctx, process, object, check)
		if e != nil {
			return value, e
		}
		value, err = observationSnapshot(b, captured.generation, facts)
		if err != nil {
			return value, err
		}
	}
	return value, check(ctx)
}
