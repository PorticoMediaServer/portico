package compactcatalog

import (
	"context"
	"database/sql"
	"log"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// retentionRawTurn bounds the rows one production prune turn may examine. The
// background quantum stays the compat path's default; the long-running walk
// keeps each writer lease to a single small family instead.
const retentionRawTurn = 8

// retentionDictionaries are the per-table cursor walks. The cursor name is the
// table name; the first row after that durable cursor is the raw frontier.
var retentionDictionaries = []struct{ table, predicate string }{
	{"catalog_search_prefix_terms", `NOT EXISTS(SELECT 1 FROM catalog_search_prefix_rows r WHERE r.term_id=d.id) AND NOT EXISTS(SELECT 1 FROM catalog_search_prefix_rows_next r WHERE r.term_id=d.id)`},
	{"catalog_people", `NOT EXISTS(SELECT 1 FROM catalog_credits c WHERE c.person_id=d.id) AND NOT EXISTS(SELECT 1 FROM show_people_credits c WHERE c.identity_key=d.identity_key)`},
	{"catalog_terms", `NOT EXISTS(SELECT 1 FROM catalog_entity_terms c WHERE c.term_id=d.id)`},
	{"catalog_attribute_terms", `NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges c WHERE c.term_id=d.id)`},
	{"catalog_credit_labels", `d.label<>'' AND NOT EXISTS(SELECT 1 FROM catalog_credits c WHERE c.role_id=d.id) AND NOT EXISTS(SELECT 1 FROM catalog_credits c WHERE c.department_id=d.id)`},
}

// clearRetentionCursors names the six cursors this package owns so clearing is
// bounded and an unrelated cursor row is never touched.
const clearRetentionCursors = `DELETE FROM catalog_retention_cursors WHERE name IN('catalog_search_prefix_terms','catalog_people','catalog_terms','catalog_attribute_terms','catalog_credit_labels','identities')`

// RunRetention owns both the daily relational walk and periodic native-FTS
// upkeep. It uses the existing goroutine and one timer; each writer is released
// after one pruning family and retains the maintenance class below scan/user
// work.
func RunRetention(ctx context.Context, db *sql.DB) {
	nextPrune := time.Now()
	nextMerge := nextPrune.Add(titleGramsMergeInterval)
	// The owner phase is process-local: the durable per-table cursors keep each
	// raw frontier, so a restart only re-walks idle heads before reaching work.
	turn := retentionTurn{}
	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(nextMerge) {
			if e := maintainTitleGrams(ctx, db); e != nil && ctx.Err() == nil {
				log.Printf("Catalogue title index maintenance will retry: %v", e)
			}
			nextMerge = time.Now().Add(titleGramsMergeInterval)
		}
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(nextPrune) {
			if !dbwork.WaitBackground(ctx) {
				return
			}
			workCtx := dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, db))
			started := time.Now()
			limit := min(32*dbwork.BackgroundConcurrency(workCtx), retentionRawTurn)
			var closed bool
			var heldFrom time.Time
			phase := turn.phase
			// Advance the process-local phase only after the transaction commits.
			nextTurn := turn
			e := dbwork.WithWriteTx(workCtx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				heldFrom = time.Now()
				var e error
				_, closed, e = nextTurn.step(workCtx, tx, limit)
				return e
			})
			if !heldFrom.IsZero() && time.Since(heldFrom) > dbwork.SlowReadThreshold {
				log.Printf("Catalogue retention turn completed after %s: family=%d rawLimit=%d", time.Since(heldFrom).Round(time.Millisecond), phase, limit)
			}
			if e == nil {
				turn = nextTurn
				if !closed {
					if !dbwork.PaceBackground(workCtx, started) {
						return
					}
					continue
				}
			}
			nextPrune = time.Now().Add(24 * time.Hour)
		}
		next := nextPrune
		if nextMerge.Before(next) {
			next = nextMerge
		}
		timer := time.NewTimer(max(time.Until(next), 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// PruneTx keeps the one-transaction API used by existing callers. It walks the
// same point families in order and stops at the first family with work, which
// is the historical behaviour for callers that loop until it returns false.
func PruneTx(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	turn := retentionTurn{}
	for {
		worked, closed, err := turn.step(ctx, tx, limit)
		if err != nil {
			return false, err
		}
		if worked {
			return true, nil
		}
		if closed {
			return false, nil
		}
	}
}

// retentionTurn is the process-local owner phase of the daily walk. One step
// runs exactly one family in the caller's transaction; a cycle that finishes
// without any family finding work clears the owned cursors and reports closure.
type retentionTurn struct {
	phase int
	idle  int
}

func retentionFamilyCount() int {
	return len(retentionDictionaries) + 1 + len(ServingTables) + 2 + 1
}

func (t *retentionTurn) step(ctx context.Context, tx *sql.Tx, limit int) (worked, closed bool, err error) {
	total := retentionFamilyCount()
	dicts := len(retentionDictionaries)
	switch {
	case t.phase < dicts:
		d := retentionDictionaries[t.phase]
		worked, err = pruneDictionary(ctx, tx, d.table, d.predicate, limit)
	case t.phase == dicts:
		worked, err = pruneIdentities(ctx, tx, limit)
	case t.phase < dicts+1+len(ServingTables):
		worked, err = prunePrevious(ctx, tx, ServingTables[t.phase-dicts-1], limit)
	case t.phase == dicts+1+len(ServingTables):
		worked, err = pruneAgedEvents(ctx, tx, limit)
	case t.phase == dicts+2+len(ServingTables):
		worked, err = trimEventRing(ctx, tx, limit)
	default:
		// Closure runs only after a whole cycle found no work, so the six
		// cursors restart the next daily walk at the raw frontier.
		if t.idle == total-1 {
			_, err = tx.ExecContext(ctx, clearRetentionCursors)
			closed = err == nil
		}
		t.idle = 0
	}
	if err != nil {
		return false, false, err
	}
	if worked {
		t.idle = 0
	} else if t.phase < total-1 {
		t.idle++
	}
	t.phase++
	if t.phase == total {
		t.phase = 0
	}
	return worked, closed, nil
}

func pruneDictionary(ctx context.Context, tx *sql.Tx, table, predicate string, limit int) (bool, error) {
	var after int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT after_id FROM catalog_retention_cursors WHERE name=?),0)`, table).Scan(&after); err != nil {
		return false, err
	}
	ids, err := scanIDs(ctx, tx, `SELECT id FROM `+table+` WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` AS d WHERE id IN(SELECT value FROM json_each(?)) AND `+predicate, mustJSON(ids)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_retention_cursors(name,after_id) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET after_id=excluded.after_id`, table, ids[len(ids)-1]); err != nil {
		return false, err
	}
	return true, nil
}

func pruneIdentities(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	var root, key string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT after_root FROM catalog_retention_cursors WHERE name='identities'),''),COALESCE((SELECT after_key FROM catalog_retention_cursors WHERE name='identities'),'')`).Scan(&root, &key); err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT root,source_key FROM catalog_identities WHERE (root,source_key)>(?,?) ORDER BY root,source_key LIMIT ?`, root, key, limit)
	if err != nil {
		return false, err
	}
	var pairs [][2]string
	for rows.Next() {
		var pair [2]string
		if err = rows.Scan(&pair[0], &pair[1]); err != nil {
			break
		}
		pairs = append(pairs, pair)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(pairs) == 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_identities WHERE (root,source_key) IN(SELECT json_extract(value,'$[0]'),json_extract(value,'$[1]') FROM json_each(?)) AND NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.public_id=catalog_identities.public_id)`, mustJSON(pairs)); err != nil {
		return false, err
	}
	last := pairs[len(pairs)-1]
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_retention_cursors(name,after_id,after_root,after_key) VALUES('identities',0,?,?) ON CONFLICT(name) DO UPDATE SET after_root=excluded.after_root,after_key=excluded.after_key`, last[0], last[1]); err != nil {
		return false, err
	}
	return true, nil
}

// prunePrevious removes rows only from older generations or from the current
// generation once it stopped retaining. The raw LIMIT reads the generation
// frontier first; the outer delete still applies the full eligibility check, so
// a retained generation is never removed.
func prunePrevious(ctx context.Context, tx *sql.Tx, table string, limit int) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+`_previous WHERE (generation,`+previousKeys[table]+`) IN(SELECT generation,`+previousKeys[table]+` FROM `+table+`_previous ORDER BY generation,`+previousKeys[table]+` LIMIT ?) AND (generation<(SELECT generation FROM catalog_generations) OR (generation=(SELECT generation FROM catalog_generations) AND (SELECT retaining FROM catalog_generations)=0))`, limit)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func pruneAgedEvents(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM api_events WHERE id IN(SELECT id FROM api_events INDEXED BY api_events_expiry WHERE at_ms<? ORDER BY at_ms,id LIMIT ?)`, time.Now().Add(-7*24*time.Hour).UnixMilli(), limit)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// trimEventRing bounds the ring by raw primary key so the production walk never
// runs the unbounded Trim on the shared event table.
func trimEventRing(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM api_events WHERE id IN(SELECT id FROM api_events WHERE id<=(SELECT COALESCE(max(id),0) FROM api_events)-? ORDER BY id LIMIT ?)`, apievents.RingSize, limit)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

var previousKeys = map[string]string{
	"catalog_browse_availability_counts": "library_id,kind,availability_state,rating_key,label_key,container",
	"catalog_movie_category_summaries":   "library_id,kind,value",
	"catalog_movie_category_members":     "library_id,kind,value,item_id",
	"catalog_browse_rows":                "entity_id",
	"catalog_browse_blocks":              "library_id,kind,ordering,sort_value,entity_id",
	"catalog_browse_buckets":             "library_id,kind,container,axis,value,rating_key,label_key",
	"catalog_browse_counted_rows":        "entity_id",
	"catalog_browse_memberships":         "entity_id,item_id,source",
	"catalog_browse_member_metrics":      "entity_id,item_id,source",
	"catalog_home_buckets":               "library_id,kind,available,recent,backdrop,rating_key,label_key,dated,retired",
	"catalog_item_availability":          "entity_id",
	"catalog_facet_counts":               "library_id,field,value,rating_key,label_key",
	"catalog_related_facets":             "entity_id,relation,provider,facet_id",
	"catalog_browse_groups":              "library_id,field,value",
	"catalog_facet_members":              "entity_id,field,value",
}
