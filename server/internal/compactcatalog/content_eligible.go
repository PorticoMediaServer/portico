package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
)

// StepContentEligible advances one native hierarchy eligibility key. A parent
// with arbitrarily many children is walked across transactions, and its count
// changes only when the complete replacement result is known.
func StepContentEligible(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 {
		limit = 1
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_content_eligible_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	for n := 0; n < 3; n++ {
		stage := (turn + n) % 3
		var worked bool
		var err error
		switch stage {
		case 0:
			worked, err = stepContentEligibleParent(ctx, tx, limit)
		case 1:
			worked, err = stepContentEligibleChild(ctx, tx, limit)
		case 2:
			worked, err = stepContentEligibleBackfill(ctx, tx)
		}
		if err != nil {
			return 0, err
		}
		if worked {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_state SET turn=? WHERE id=1`, (stage+1)%3)
			return 1, err
		}
	}
	return 0, nil
}

func stepContentEligibleParent(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	var entityID, sequence, after, found int
	err := tx.QueryRowContext(ctx, `SELECT entity_id,sequence,after_item_id,found FROM catalog_content_eligible_jobs ORDER BY entity_id LIMIT 1`).Scan(&entityID, &sequence, &after, &found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var libraryID, kind int
	err = tx.QueryRowContext(ctx, `SELECT library_id,kind FROM catalog_entities WHERE id=?`, entityID).Scan(&libraryID, &kind)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows || (kind != 2 && kind != 5 && kind != 8 && kind != 10) {
		return true, publishContentEligible(ctx, tx, entityID, sequence, 0, 0, false)
	}
	var active bool
	switch kind {
	case 2:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_shows s JOIN catalog_entities e ON e.id=s.entity_id AND e.retired=0 WHERE s.entity_id=?)`, entityID).Scan(&active)
	case 5:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_artists a JOIN catalog_entities e ON e.id=a.entity_id AND e.retired=0 WHERE a.entity_id=?)`, entityID).Scan(&active)
	case 8:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_books b JOIN catalog_entities e ON e.id=b.entity_id AND e.retired=0 WHERE b.entity_id=?)`, entityID).Scan(&active)
	case 10:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id AND e.retired=0 WHERE c.entity_id=?)`, entityID).Scan(&active)
	}
	if err != nil {
		return false, err
	}
	if !active || kind == 10 {
		return true, publishContentEligible(ctx, tx, entityID, sequence, libraryID, kind, active)
	}
	if found != 0 {
		return true, publishContentEligible(ctx, tx, entityID, sequence, libraryID, kind, true)
	}
	var sources string
	switch kind {
	case 2:
		sources = `1`
	case 5:
		sources = `4,5`
	case 8:
		sources = `6`
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT item_id FROM catalog_browse_memberships WHERE entity_id=? AND item_id>? AND source IN(`+sources+`) ORDER BY item_id LIMIT ?`, entityID, after, limit+1)
	if err != nil {
		return false, err
	}
	items := []int{}
	for rows.Next() {
		var item int
		if err = rows.Scan(&item); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	for _, item := range items {
		var eligible bool
		switch kind {
		case 2:
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_asset_links a ON a.entity_id=ep.entity_id WHERE ep.entity_id=? AND ep.show_id=? LIMIT 1)`, item, entityID).Scan(&eligible)
		case 5:
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_songs WHERE entity_id=?)`, item).Scan(&eligible)
		case 8:
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_files WHERE entity_id=? AND book_id=?)`, item, entityID).Scan(&eligible)
		}
		if err != nil {
			return false, err
		}
		if eligible {
			return true, publishContentEligible(ctx, tx, entityID, sequence, libraryID, kind, true)
		}
	}
	if !more {
		return true, publishContentEligible(ctx, tx, entityID, sequence, libraryID, kind, false)
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_jobs SET after_item_id=? WHERE entity_id=? AND sequence=?`, items[len(items)-1], entityID, sequence)
	return true, err
}

func publishContentEligible(ctx context.Context, tx *sql.Tx, entityID, sequence, libraryID, kind int, eligible bool) error {
	var oldLibrary, oldKind, oldEligible int
	err := tx.QueryRowContext(ctx, `SELECT library_id,kind,eligible FROM catalog_content_eligible_rows WHERE entity_id=?`, entityID).Scan(&oldLibrary, &oldKind, &oldEligible)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && oldEligible != 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_counts SET total=total-1 WHERE library_id=? AND kind=?`, oldLibrary, oldKind); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_content_eligible_counts WHERE library_id=? AND kind=? AND total=0`, oldLibrary, oldKind); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_content_eligible_rows WHERE entity_id=?`, entityID); err != nil {
		return err
	}
	if eligible {
		if libraryID == 0 || kind == 0 {
			return fmt.Errorf("eligible content entity %d has no scope", entityID)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_content_eligible_rows(entity_id,library_id,kind,eligible) VALUES(?,?,?,1)`, entityID, libraryID, kind); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_content_eligible_counts(library_id,kind,total) VALUES(?,?,1) ON CONFLICT(library_id,kind) DO UPDATE SET total=total+1`, libraryID, kind); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_content_eligible_jobs WHERE entity_id=? AND sequence=?`, entityID, sequence)
	return err
}

func stepContentEligibleChild(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	var itemID, after int
	err := tx.QueryRowContext(ctx, `SELECT item_id,after_entity_id FROM catalog_content_eligible_child_jobs ORDER BY item_id LIMIT 1`).Scan(&itemID, &after)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT entity_id FROM catalog_browse_memberships WHERE item_id=? AND entity_id>? AND source IN(4,5) ORDER BY entity_id LIMIT ?`, itemID, after, limit+1)
	if err != nil {
		return false, err
	}
	parents := []int{}
	for rows.Next() {
		var parent int
		if err = rows.Scan(&parent); err != nil {
			rows.Close()
			return false, err
		}
		parents = append(parents, parent)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	more := len(parents) > limit
	if more {
		parents = parents[:limit]
	}
	for _, parent := range parents {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_content_eligible_jobs(entity_id) VALUES(?) ON CONFLICT(entity_id) DO UPDATE SET sequence=sequence+1,after_item_id=0,found=0`, parent); err != nil {
			return false, err
		}
	}
	if more {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_child_jobs SET after_entity_id=? WHERE item_id=?`, parents[len(parents)-1], itemID)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_content_eligible_child_jobs WHERE item_id=?`, itemID)
	}
	return true, err
}

func stepContentEligibleBackfill(ctx context.Context, tx *sql.Tx) (bool, error) {
	var after int
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT after_entity_id,backfill_done FROM catalog_content_eligible_state WHERE id=1`).Scan(&after, &done); err != nil {
		return false, err
	}
	if done {
		return false, nil
	}
	var entityID int
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE id>? AND kind IN(2,5,8,10) ORDER BY id LIMIT 1`, after).Scan(&entityID)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_state SET backfill_done=1 WHERE id=1`)
		return true, err
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_content_eligible_jobs(entity_id) VALUES(?) ON CONFLICT(entity_id) DO NOTHING`, entityID); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_content_eligible_state SET after_entity_id=? WHERE id=1`, entityID)
	return true, err
}
