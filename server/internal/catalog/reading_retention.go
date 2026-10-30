package catalog

import (
	"context"
	"database/sql"
)

func retainReadingTx(ctx context.Context, tx *sql.Tx, entity int64, owner string, state int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO retained_reading_progress(state_id,source_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at)
 SELECT ?,a.token,p.source_revision,p.format,p.cfi,p.section,p.page_index,p.progression,p.revision,p.observed_at
 FROM reading_progress p JOIN catalog_assets a ON a.id=p.asset_id WHERE p.owner_key=? AND p.item_id=?
 ON CONFLICT(state_id,source_id) DO UPDATE SET source_revision=excluded.source_revision,format=excluded.format,cfi=excluded.cfi,section=excluded.section,page_index=excluded.page_index,progression=excluded.progression,revision=excluded.revision,observed_at=excluded.observed_at WHERE excluded.observed_at>=retained_reading_progress.observed_at`, state, owner, entity)
	return err
}

func mergeRetainedReadingTx(ctx context.Context, tx *sql.Tx, state int64, identity, owner string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO retained_reading_progress(state_id,source_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at)
 SELECT ?,r.source_id,r.source_revision,r.format,r.cfi,r.section,r.page_index,r.progression,r.revision,r.observed_at
 FROM retained_reading_progress r JOIN retained_title_identities i ON i.state_id=r.state_id WHERE i.identity=? AND i.profile_id=? AND r.state_id<>?
 ON CONFLICT(state_id,source_id) DO UPDATE SET source_revision=excluded.source_revision,format=excluded.format,cfi=excluded.cfi,section=excluded.section,page_index=excluded.page_index,progression=excluded.progression,revision=excluded.revision,observed_at=excluded.observed_at WHERE excluded.observed_at>retained_reading_progress.observed_at`, state, identity, owner, state)
	return err
}

// Restore only the exact retained asset identity. A new file/version with the
// same title is not proof of interchangeable CFI/pages. Unmatched versions stay
// retained; they are never discarded when a different version reappears.
func restoreReadingTx(ctx context.Context, tx *sql.Tx, entity int64, owner string, state int64) (bool, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at)
 SELECT ?,?,p.asset_id,r.source_revision,r.format,r.cfi,r.section,r.page_index,r.progression,r.revision,r.observed_at
 FROM retained_reading_progress r JOIN catalog_assets a ON a.token=r.source_id JOIN reading_publications p ON p.asset_id=a.id AND p.item_id=? AND p.format=r.format
 WHERE r.state_id=?
 ON CONFLICT(owner_key,item_id,asset_id) DO NOTHING`, owner, entity, entity, state)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM retained_reading_progress WHERE state_id=? AND EXISTS(SELECT 1 FROM reading_publications p JOIN catalog_assets a ON a.id=p.asset_id WHERE p.item_id=? AND a.token=retained_reading_progress.source_id AND p.format=retained_reading_progress.format)`, state, entity)
	if err != nil {
		return false, err
	}
	var remaining bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM retained_reading_progress WHERE state_id=?)`, state).Scan(&remaining)
	return remaining, err
}
