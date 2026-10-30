package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// O-003 browse-row restriction keys. A playable item's browse row carries the
// exact '|'-joined, distinct, ordered value_keys of its contentRating and label
// attributes. The old join built both scalars inside the row read, so one item
// with a very large restriction set widened every derivation. This stages the
// same keys from bounded raw pages first; only when the raw input is exhausted
// does one final ordered group_concat per field run, exactly once.
//
// catalog_browse_row_work holds at most ONE holder per domain (18 browse rows,
// 35 container refresh), and catalog_browse_row_keys is that holder's staged
// PK-distinct keys. A holder in both domains can therefore stage at most two
// small key sets, never the catalogue. Interrupted work is restarted by row or
// revision drift; the old staged keys are reclaimed in bounded PK pages and the
// work row is reset only after the last page.

const (
	browseRowWorkScan    = 0 // raw item_attribute_edges -> staged keys
	browseRowWorkFinal   = 1 // one exact ordered scalar per field, then derive
	browseRowWorkClear   = 2 // bounded staged-key cleanup, then acknowledge
	browseRowWorkQuantum = 8
)

const (
	browseRowRatingField = 0
	browseRowLabelField  = 1
)

type browseRowWork struct {
	id, revision int64
	phase        int
	after        int64
	exists       bool
}

func readBrowseRowWork(ctx context.Context, tx *sql.Tx, domain int) (browseRowWork, error) {
	var w browseRowWork
	err := tx.QueryRowContext(ctx, `SELECT entity_id,revision,phase,after_term FROM catalog_browse_row_work WHERE domain=?`, domain).
		Scan(&w.id, &w.revision, &w.phase, &w.after)
	if errors.Is(err, sql.ErrNoRows) {
		return w, nil
	}
	if err != nil {
		return w, err
	}
	w.exists = true
	return w, nil
}

// clearBrowseRowKeysPage deletes at most one bounded page of this domain's
// staged keys, in complete PK order. It never issues an unbounded DELETE.
func clearBrowseRowKeysPage(ctx context.Context, tx *sql.Tx, domain, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT field,value_key FROM catalog_browse_row_keys WHERE domain=? ORDER BY field,value_key LIMIT ?`, domain, limit)
	if err != nil {
		return 0, err
	}
	type key struct {
		field int
		value string
	}
	var page []key
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.field, &k.value); err != nil {
			rows.Close()
			return 0, err
		}
		page = append(page, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, k := range page {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_browse_row_keys WHERE domain=? AND field=? AND value_key=?`, domain, k.field, k.value); err != nil {
			return 0, err
		}
	}
	return len(page), nil
}

// browseRowKeyScalar is the original row expression's exact output: distinct
// value_keys ordered by value_key, '|'-joined, and ” when the field has none.
// An empty value_key is a real key and sorts first; COALESCE only changes the
// no-row case. This is the approved unavoidable one-shot O(B) output scalar.
func browseRowKeyScalar(ctx context.Context, tx *sql.Tx, domain, field int) (string, error) {
	var out string
	err := tx.QueryRowContext(ctx, `WITH keys AS MATERIALIZED (SELECT value_key FROM catalog_browse_row_keys WHERE domain=? AND field=? ORDER BY value_key) SELECT COALESCE(group_concat(value_key,'|'),'') FROM keys`, domain, field).Scan(&out)
	return out, err
}

// deriveBrowseRowBounded advances one holder of a browse-row stage by one
// bounded quantum and reports whether the unit is finished (and may leave the
// queue). A call reads at most min(limit,8) raw edges and always moves the
// complete (item_id,term_id) cursor past rejected or duplicate edges. Only an
// EMPTY raw page proves the input complete; a short or byte-stopped page does
// not. Finalization calls the lead-owned deriveBrowseRowWithKeys, then the
// staged keys are reclaimed in bounded phase-2 pages before acknowledgement.
func deriveBrowseRowBounded(ctx context.Context, tx *sql.Tx, domain int, k Key, limit int) (bool, error) {
	if domain != DomainBrowseRows && domain != DomainContainerRefresh {
		return false, nil
	}
	w, err := readBrowseRowWork(ctx, tx, domain)
	if err != nil {
		return false, err
	}
	page := max(1, min(limit, browseRowWorkQuantum))

	// A different holder or revision owns different staged keys. Keep the old
	// work row until every old key page is gone, so a crash cannot forget keys
	// that the reset row no longer points at.
	if !w.exists {
		// Empty restriction input needs no stage or continuation. This indexed
		// empty-prefix proof keeps the common row a single publication unit.
		var raw, staged bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_item_attribute_edges WHERE item_id=?),EXISTS(SELECT 1 FROM catalog_browse_row_keys WHERE domain=?)`, k.ID, domain).Scan(&raw, &staged); err != nil {
			return false, err
		}
		if !raw && !staged {
			return true, deriveBrowseRowWithKeys(ctx, tx, k.ID, "", "")
		}
		// No holder: only an orphaned staged key set can remain. Reclaim it in
		// pages; a fresh unit may start scanning now that the keys are gone.
		n, err := clearBrowseRowKeysPage(ctx, tx, domain, page)
		if err != nil {
			return false, err
		}
		if n >= page {
			return false, nil
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_browse_row_work(domain,entity_id,revision,phase,after_term) VALUES(?,?,?,0,0)`, domain, k.ID, k.Revision); err != nil {
			return false, err
		}
		w = browseRowWork{id: k.ID, revision: k.Revision, exists: true}
	} else if w.id != k.ID || w.revision != k.Revision {
		n, err := clearBrowseRowKeysPage(ctx, tx, domain, page)
		if err != nil {
			return false, err
		}
		if n >= page {
			return false, nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_browse_row_work SET entity_id=?,revision=?,phase=0,after_term=0 WHERE domain=?`, k.ID, k.Revision, domain); err != nil {
			return false, err
		}
		// Reset only; the next call starts the new holder's raw scan.
		return false, nil
	}

	switch w.phase {
	case browseRowWorkScan:
		// Raw pages carry only the complete-PK cursor; term/field hydration is
		// one point seek per visited edge.
		rows, err := tx.QueryContext(ctx, `SELECT term_id FROM catalog_item_attribute_edges WHERE item_id=? AND term_id>? ORDER BY term_id LIMIT ?`, k.ID, w.after, page)
		if err != nil {
			return false, err
		}
		var raw []int64
		for rows.Next() {
			var term int64
			if err = rows.Scan(&term); err != nil {
				rows.Close()
				return false, err
			}
			raw = append(raw, term)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		if len(raw) == 0 {
			// Only an empty page proves the raw input exhausted.
			_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_row_work SET phase=? WHERE domain=? AND entity_id=? AND revision=? AND phase=?`, browseRowWorkFinal, domain, k.ID, k.Revision, browseRowWorkScan)
			return false, err
		}
		bytes, consumed := 0, 0
		for _, term := range raw {
			var field, valueKey string
			err = tx.QueryRowContext(ctx, `SELECT f.field,t.value_key FROM catalog_attribute_terms t JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE t.id=?`, term).Scan(&field, &valueKey)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			if err == nil {
				// A legal oversized scalar is staged alone, exactly as the
				// existing 256 KiB producer precedent does.
				if consumed > 0 && bytes+len(valueKey) > facetScalarBudget {
					break
				}
				bytes += len(valueKey)
				switch field {
				case "contentRating":
					if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_browse_row_keys(domain,field,value_key) VALUES(?,?,?)`, domain, browseRowRatingField, valueKey); err != nil {
						return false, err
					}
				case "label":
					if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_browse_row_keys(domain,field,value_key) VALUES(?,?,?)`, domain, browseRowLabelField, valueKey); err != nil {
						return false, err
					}
				}
			}
			// Rejected fields (and a term that vanished between the page and
			// its point hydration) still consume the cursor.
			w.after = term
			consumed++
		}
		if consumed > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_row_work SET after_term=? WHERE domain=? AND entity_id=? AND revision=? AND phase=?`, w.after, domain, k.ID, k.Revision, browseRowWorkScan)
		}
		return false, err
	case browseRowWorkFinal:
		rating, err := browseRowKeyScalar(ctx, tx, domain, browseRowRatingField)
		if err != nil {
			return false, err
		}
		label, err := browseRowKeyScalar(ctx, tx, domain, browseRowLabelField)
		if err != nil {
			return false, err
		}
		if err = deriveBrowseRowWithKeys(ctx, tx, k.ID, rating, label); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_row_work SET phase=? WHERE domain=? AND entity_id=? AND revision=? AND phase=?`, browseRowWorkClear, domain, k.ID, k.Revision, browseRowWorkFinal)
		return false, err
	case browseRowWorkClear:
		n, err := clearBrowseRowKeysPage(ctx, tx, domain, page)
		if err != nil {
			return false, err
		}
		if n >= page {
			return false, nil
		}
		// The last bounded page (or no keys at all) completes the unit; the
		// work row goes only now, after the staged input is gone.
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_browse_row_work WHERE domain=? AND entity_id=? AND revision=? AND phase=?`, domain, k.ID, k.Revision, browseRowWorkClear)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
