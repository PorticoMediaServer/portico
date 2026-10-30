package lyrics

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A configured nil resource is an explicit owner-wide choice of no lyrics.
// The source tuple and resource's library scope are checked again on every read.
func librarySelection(ctx context.Context, tx *sql.Tx, a Access, entity int64, src *Source) (Selection, bool, error) {
	var out Selection
	var id sql.NullString
	var revision sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT revision,resource_id,lyric_revision FROM lyric_library_selections WHERE item_id=? AND asset_id=? AND source_version=?`, entity, src.ID, src.Version).Scan(&out.Revision, &id, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if id.Valid {
		selected, err := readRevision(ctx, tx, a, entity, src, id.String, revision.Int64, true)
		if err != nil {
			return out, true, err
		}
		if selected.Scope != "library" || selected.Deleted {
			return out, true, ErrSource
		}
		out.Resource = &selected
	}
	return out, true, nil
}

func (s *Service) chooseLibrary(ctx context.Context, a Access, in Choose) (Selection, error) {
	if a.Actor.Authority != "local" || !a.Actor.Owner {
		return Selection{}, identity.ErrForbidden
	}
	if in.SessionID != "" || in.SourceID == "" || in.SourceVersion == "" || in.ExpectedSelectionRevision < 0 || in.ExpectedSelectionRevision >= 1<<52 || in.ResourceID == "" && in.Revision != 0 || in.ResourceID != "" && in.Revision < 1 {
		return Selection{}, ErrInput
	}
	gated, entity, err := s.begin(ctx, a)
	if err != nil {
		return Selection{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	_, src, _, err := resolve(ctx, tx, a, entity, in.Target)
	if err != nil {
		return Selection{}, err
	}
	if src == nil {
		return Selection{}, ErrSource
	}
	before, _, err := librarySelection(ctx, tx, a, entity, src)
	if err != nil {
		return Selection{}, err
	}
	if before.Revision != in.ExpectedSelectionRevision {
		return Selection{}, ErrConflict
	}
	if in.ResourceID != "" {
		var head int64
		var deleted bool
		var scope string
		err = tx.QueryRowContext(ctx, `SELECT revision,deleted,scope FROM lyric_resources WHERE id=? AND item_id=? AND asset_id=? AND source_version=?`, in.ResourceID, entity, src.ID, src.Version).Scan(&head, &deleted, &scope)
		if err != nil {
			return Selection{}, err
		}
		if head != in.Revision || deleted || scope != "library" {
			return Selection{}, ErrConflict
		}
		if _, err = readRevision(ctx, tx, a, entity, src, in.ResourceID, in.Revision, true); err != nil {
			return Selection{}, err
		}
	}
	var resource, revision any
	if in.ResourceID != "" {
		resource, revision = in.ResourceID, in.Revision
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO lyric_library_selections(item_id,asset_id,source_version,revision,resource_id,lyric_revision,actor) VALUES(?,?,?,?,?,?,?) ON CONFLICT(item_id,asset_id,source_version) DO UPDATE SET revision=excluded.revision,resource_id=excluded.resource_id,lyric_revision=excluded.lyric_revision,actor=excluded.actor WHERE lyric_library_selections.revision=?`, entity, src.ID, src.Version, before.Revision+1, resource, revision, a.Actor.key(), before.Revision)
	if err != nil {
		return Selection{}, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return Selection{}, err
		}
		return Selection{}, ErrConflict
	}
	out, _, err := librarySelection(ctx, tx, a, entity, src)
	if err != nil {
		return out, err
	}
	return out, gated.Commit()
}
