package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// The unique(object_id,title_number) index is a numeric keyset over the exact
// accepted owner observation. No title key/object id string becomes a cursor.
type fileInfoDiscSource struct {
	db           *sql.DB
	asset, bytes int64
	check        fileInfoBasisCheck
}

func (s *fileInfoDiscSource) AccountedBytes() int64 { return 512 + s.bytes }
func (s *fileInfoDiscSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}
func (s *fileInfoDiscSource) Next(ctx context.Context, has bool, after int64) (int64, bool, bool, error) {
	query := `SELECT title_number FROM inventory_disc_titles WHERE object_id=(SELECT a.info_owner_object FROM catalog_assets a NOT INDEXED CROSS JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.source_revision=a.info_owner_revision AND d.asset_id=a.token WHERE a.id=? AND d.encryption='none')`
	args := []any{s.asset}
	if has {
		query += ` AND title_number>?`
		args = append(args, after)
	}
	query += ` ORDER BY title_number LIMIT 1`
	var id int64
	e := dbwork.ReadHandle(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db).QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	return id, e == nil, e == nil, e
}
func (r *FileInfoRuntime) hydrateDiscTitle(ctx context.Context, asset, number int64) (FileInfoDiscTitle, FileInfoDiscKind, error) {
	var out FileInfoDiscTitle
	var kind FileInfoDiscKind
	e := dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT d.kind,t.title_number,t.duration_ms,t.angle_count,
 COALESCE((SELECT c.chapter_order+1 FROM inventory_disc_chapters c WHERE c.object_id=t.object_id AND c.title_key=t.title_key ORDER BY c.chapter_order DESC LIMIT 1),0)
 FROM catalog_assets a NOT INDEXED CROSS JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.source_revision=a.info_owner_revision AND d.asset_id=a.token
 JOIN inventory_disc_titles t ON t.object_id=d.object_id AND t.title_number=?
 WHERE a.id=? AND d.encryption='none'`, number, asset).Scan(&kind, &out.Number, &out.DurationMs, &out.Angles, &out.Chapters)
	if errors.Is(e, sql.ErrNoRows) {
		e = readprep.ErrStale
	}
	return out, kind, e
}
