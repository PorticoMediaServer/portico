package metadata

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// readWithSnapshot admits the database portion of a metadata operation. Callers
// finish it before provider, filesystem or decoder work, and derive any returned
// long-lived context from their original caller rather than this bounded view.
func (s *Service) readWithSnapshot(ctx context.Context, read func(context.Context) error) error {
	return dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		// This is always a borrow of the admitted snapshot (or carried writer).
		// Preserve the legacy rule that a failed nested read aborts its writer.
		view, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		defer view.Rollback()
		if err := read(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return view.Commit()
	})
}

// Preparation may borrow the request's view, but must release it before media
// opens. Request reuse's ordinary idle hold is not an I/O boundary.
func (s *Service) readBeforeMedia(ctx context.Context, read func(context.Context) error) error {
	return dbwork.WithReadSnapshotOnce(ctx, s.db, func(ctx context.Context) error {
		return s.readWithSnapshot(ctx, read)
	})
}

func metadataReadValue[T any](ctx context.Context, s *Service, read func(context.Context) (T, error)) (out T, err error) {
	err = s.readWithSnapshot(ctx, func(ctx context.Context) error {
		var err error
		out, err = read(ctx)
		return err
	})
	return out, err
}
