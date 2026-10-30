package reading

import (
	"context"
	"database/sql"
	"time"
)

// The persisted Reading text index is EPUB-first private derived text. Its
// bounds are payload bytes, not the physical size SQLite or FTS happens to use:
// one document may contribute at most maxTextIndexDocument bytes across at most
// maxTextIndexSections sections, and all indexed documents together may occupy
// at most maxTextIndexPayload. Eviction and capacity exclusion are determined
// outcomes on the same exact observation, so a background pass never retries a
// document it already decided to leave out.
const (
	readingIndexRuleVersion = 1
	maxTextIndexDocument    = 8 << 20
	maxTextIndexSections    = 4096
	maxTextIndexPayload     = 1 << 30
	maxTextIndexSource      = 64 << 20
	maxTextIndexVictims     = 64
	maxTextIndexScan        = 2048
)

// textIndexSection is one admitted, normalized spine document's publication
// text. ordinal is the document's position in the publication spine (gaps are
// textless spine documents), so a hit's SectionIndex is the real spine position
// rather than a compacted position over non-empty sections. utf16 is the exact
// number of UTF-16 code units the client anchors in.
type textIndexSection struct {
	ordinal int
	path    string
	text    string
	utf16   int
}

// textIndexState is the one determined outcome for an exact observation.
// Zero payload accompanies evicted, excluded and unsupported outcomes.
type textIndexState struct {
	outcome  string
	reason   string
	bytes    int64
	sections int
}

// textIndexEpochTx reads the current global index revision. A cursor minted
// against any other revision is refused rather than continued over changed hits.
func textIndexEpochTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM reading_text_index_epoch WHERE singleton=1),0)`).Scan(&revision)
	return revision, err
}

// clearTextIndexSectionsTx removes this document's private text. The AFTER
// DELETE trigger on reading_text_sections is the only place that removes the
// matching external-content posting, so this path cannot double-delete it.
func clearTextIndexSectionsTx(ctx context.Context, tx *sql.Tx, item, asset int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM reading_text_sections WHERE item_id=? AND asset_id=?`, item, asset)
	return err
}

// writeTextIndexStateTx records one determined outcome for this exact
// observation. indexed_at is the deterministic FIFO order for eviction
// candidates; no read path updates it.
func writeTextIndexStateTx(ctx context.Context, tx *sql.Tx, current Selection, state textIndexState) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_text_index_state(item_id,asset_id,inventory_object_id,source_revision,index_rule_version,outcome,reason,text_bytes,section_count,indexed_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(item_id,asset_id) DO UPDATE SET inventory_object_id=excluded.inventory_object_id,source_revision=excluded.source_revision,index_rule_version=excluded.index_rule_version,outcome=excluded.outcome,reason=excluded.reason,text_bytes=excluded.text_bytes,section_count=excluded.section_count,indexed_at=excluded.indexed_at`,
		current.Entity, current.Asset, current.inventoryRow, current.inventory, readingIndexRuleVersion, state.outcome, state.reason, state.bytes, state.sections, time.Now().UnixMilli())
	return err
}

// evictTextIndexTx turns one indexed document into a zero-payload evicted
// outcome. Its indexed_at is retained: evicted rows are never eviction
// candidates, and only a changed observation or rule makes the document pending
// again.
func evictTextIndexTx(ctx context.Context, tx *sql.Tx, item, asset int64) error {
	if err := clearTextIndexSectionsTx(ctx, tx, item, asset); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE reading_text_index_state SET outcome='evicted',reason='',text_bytes=0,section_count=0
 WHERE item_id=? AND asset_id=? AND outcome='indexed'`, item, asset)
	return err
}

// adoptTextIndexTx publishes one whole-document determination in the caller's
// accepting transaction. A document is indexed only as a complete unit: every
// admitted spine section either publishes or the whole document takes a
// determined zero-payload outcome. When the payload budget is short it may evict
// at most maxTextIndexVictims whole documents in indexed_at FIFO order, but only
// if those candidates' measured bytes cover the shortfall; otherwise the
// existing indices are kept and this document is recorded as capacity-excluded.
func adoptTextIndexTx(ctx context.Context, tx *sql.Tx, current Selection, sections []textIndexSection, reason string) error {
	var need int64
	for _, section := range sections {
		need += int64(len(section.text))
	}
	switch {
	case reason == "" && (len(sections) == 0 || need == 0):
		reason = "empty"
	case reason == "" && (len(sections) > maxTextIndexSections || need > maxTextIndexDocument):
		reason = "oversized"
	case reason == "" && need > maxTextIndexPayload:
		reason = "payload"
	}
	if reason != "" {
		if err := clearTextIndexSectionsTx(ctx, tx, current.Entity, current.Asset); err != nil {
			return err
		}
		outcome := "unsupported"
		if reason == "payload" {
			outcome = "excluded"
		}
		return writeTextIndexStateTx(ctx, tx, current, textIndexState{outcome: outcome, reason: reason})
	}

	// The budget row is maintained by the state triggers, so this is O(1).
	// The current document's old payload already sits inside the budget and is
	// replaced by this adoption, so subtract it before judging the free space.
	var budget, old int64
	if err := tx.QueryRowContext(ctx, `SELECT payload_bytes FROM reading_text_index_budget WHERE singleton=1`).Scan(&budget); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT text_bytes FROM reading_text_index_state WHERE item_id=? AND asset_id=?),0)`, current.Entity, current.Asset).Scan(&old); err != nil {
		return err
	}
	if free := int64(maxTextIndexPayload) - (budget - old); need > free {
		shortfall := need - free
		type victim struct {
			item, asset, bytes int64
		}
		var candidates []victim
		var available int64
		rows, err := tx.QueryContext(ctx, `SELECT item_id,asset_id,text_bytes FROM reading_text_index_state
 WHERE outcome='indexed' AND NOT(item_id=? AND asset_id=?)
 ORDER BY indexed_at,item_id,asset_id LIMIT ?`, current.Entity, current.Asset, maxTextIndexVictims)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate victim
			if err = rows.Scan(&candidate.item, &candidate.asset, &candidate.bytes); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, candidate)
			available += candidate.bytes
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if available < shortfall {
			// The bounded victim window cannot pay for this document. Keep every
			// existing index and record the truthful capacity outcome instead of
			// starting a polling or incremental-eviction loop.
			if err = clearTextIndexSectionsTx(ctx, tx, current.Entity, current.Asset); err != nil {
				return err
			}
			return writeTextIndexStateTx(ctx, tx, current, textIndexState{outcome: "excluded", reason: "capacity"})
		}
		var freed int64
		for _, candidate := range candidates {
			if freed >= shortfall {
				break
			}
			if err = evictTextIndexTx(ctx, tx, candidate.item, candidate.asset); err != nil {
				return err
			}
			freed += candidate.bytes
		}
	}

	if err := clearTextIndexSectionsTx(ctx, tx, current.Entity, current.Asset); err != nil {
		return err
	}
	if err := writeTextIndexStateTx(ctx, tx, current, textIndexState{outcome: "indexed", bytes: need, sections: len(sections)}); err != nil {
		return err
	}
	for _, section := range sections {
		result, err := tx.ExecContext(ctx, `INSERT INTO reading_text_sections(item_id,asset_id,section_index,section_path,text,utf16_length)
 VALUES(?,?,?,?,?,?)`, current.Entity, current.Asset, section.ordinal, section.path, section.text, section.utf16)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO reading_text_fts(rowid,text) VALUES(?,?)`, id, section.text); err != nil {
			return err
		}
	}
	return nil
}
