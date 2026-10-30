package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Episode accepted facts deliberately exclude own/inherited genres. Restricted
// show genre reduction reads raw sources in the maintained BASE builder; the
// unrestricted show holder continues to use only its own genres.
// One quantum spends a shared budget on RAW rows, accepted deltas and cleanup.
// A legal oversized scalar is one O(B) row, never a silently truncated value.
const facetHolderQuantum = 8
const facetScalarBudget = 256 << 10

const (
	facetClear = iota
	facetEpisodeAssets
	facetEpisodeCollections
	facetEpisodeAttributes
	facetHolderGenres
	facetHolderYear
	facetHolderDecade
	facetHolderAssets
	facetHolderCollections
	facetHolderAttributes
	facetHolderShowValues
	facetEpisodeRemove
	facetEpisodeAdd
	facetHolderRemove
	facetHolderAdd
	facetFinishClear
	facetFinish
)

type facetWork struct {
	id, revision, epoch            int64
	phase                          int
	hasAfter                       bool
	afterID                        int64
	provider, source, field, value string
	show, item                     sql.NullInt64
	holder, ownShow                bool
	place                          facetPlace
	placeLoaded                    bool
	recent                         string
	quantumBytes                   int
}

func readFacetWork(ctx context.Context, tx *sql.Tx, key Key, epoch int64) (facetWork, error) {
	w := facetWork{id: key.ID, revision: key.Revision, epoch: epoch}
	var revision, previousEpoch int64
	err := tx.QueryRowContext(ctx, `SELECT revision,source_epoch,phase,has_after,after_id,after_provider,after_source,after_field,after_value,
 show_id,item_id,holder,own_show FROM catalog_facet_work WHERE entity_id=?`, key.ID).
		Scan(&revision, &previousEpoch, &w.phase, &w.hasAfter, &w.afterID, &w.provider, &w.source, &w.field, &w.value,
			&w.show, &w.item, &w.holder, &w.ownShow)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (revision != key.Revision || previousEpoch != epoch) {
		// Reset logically, then reclaim old staged values in bounded PK quanta.
		// Previously accepted members stay untouched until ALL raw families finish.
		w = facetWork{id: key.ID, revision: key.Revision, epoch: epoch}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_work_places WHERE entity_id=?`, key.ID); err != nil {
			return w, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_work(entity_id,revision,source_epoch) VALUES(?,?,?)
   ON CONFLICT(entity_id) DO UPDATE SET revision=excluded.revision,source_epoch=excluded.source_epoch,phase=0,has_after=0,
   after_id=0,after_provider='',after_source='',after_field='',after_value='',show_id=NULL,item_id=NULL,
   holder=0,own_show=0`, key.ID, key.Revision, epoch)
	}
	return w, err
}

func (w *facetWork) advance() {
	w.phase++
	w.hasAfter = false
	w.afterID = 0
	w.provider, w.source, w.field, w.value = "", "", "", ""
}

func (w *facetWork) checkpoint(ctx context.Context, tx *sql.Tx) error {
	// Captured placement is immutable within one staged revision. Rewriting its
	// potentially large bucket strings with EVERY raw cursor would amplify a
	// million-attribute scan by the whole bucket width.
	_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_work SET phase=?,has_after=?,after_id=?,after_provider=?,after_source=?,after_field=?,after_value=?
 WHERE entity_id=? AND revision=? AND source_epoch=?`, w.phase, w.hasAfter, w.afterID, w.provider, w.source, w.field, w.value, w.id, w.revision, w.epoch)
	return err
}

func (w *facetWork) loadPlace(ctx context.Context, tx *sql.Tx) error {
	if w.placeLoaded {
		return nil
	}
	err := tx.QueryRowContext(ctx, `SELECT library_id,rating_key,label_key,recent_key FROM catalog_facet_work_places WHERE entity_id=?`, w.id).
		Scan(&w.place.library, &w.place.ratingKey, &w.place.labelKey, &w.recent)
	if err == nil {
		w.placeLoaded = true
	}
	return err
}

func (w *facetWork) capture(ctx context.Context, tx *sql.Tx) error {
	var kind int64
	err := tx.QueryRowContext(ctx, `SELECT library_id,rating_key,label_key,item_id,kind,COALESCE(recent_text,added_text,'') FROM catalog_browse_rows WHERE entity_id=?`, w.id).
		Scan(&w.place.library, &w.place.ratingKey, &w.place.labelKey, &w.item, &kind, &w.recent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	w.ownShow = err == nil && kind == int64(Show)
	w.holder = err == nil && (w.ownShow || w.item.Valid && kind != int64(Episode))
	err = tx.QueryRowContext(ctx, `SELECT show_id FROM catalog_episodes WHERE entity_id=?`, w.id).Scan(&w.show)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return err
	}
	w.placeLoaded = true
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_work_places(entity_id,library_id,rating_key,label_key,recent_key) VALUES(?,?,?,?,?)
 ON CONFLICT(entity_id) DO UPDATE SET library_id=excluded.library_id,rating_key=excluded.rating_key,label_key=excluded.label_key,recent_key=excluded.recent_key`, w.id, w.place.library, w.place.ratingKey, w.place.labelKey, w.recent); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_facet_work SET show_id=?,item_id=?,holder=?,own_show=? WHERE entity_id=? AND revision=? AND source_epoch=?`, w.show, w.item, w.holder, w.ownShow, w.id, w.revision, w.epoch)
	return err
}

func facetAttribute(field string) bool {
	switch field {
	case "contentRating", "audioLanguage", "studio", "network", "tag", "label", "series":
		return true
	}
	return false
}

// rawFacetRow retains the full source PK even for rows that produce no value.
// Only the fixed descriptive field vocabulary is tested in Go, AFTER raw LIMIT.
type rawFacetRow struct {
	id                      int64
	provider, source, field string
	value                   sql.NullString
}

func (r rawFacetRow) bytes() int {
	return len(r.provider) + len(r.source) + len(r.field) + len(r.value.String)
}

func readRawFacetRows(rows *sql.Rows, phase int) ([]rawFacetRow, error) {
	defer rows.Close()
	var out []rawFacetRow
	bytes := 0
	for rows.Next() {
		var r rawFacetRow
		var err error
		switch phase {
		case facetHolderGenres:
			err = rows.Scan(&r.provider, &r.source, &r.field, &r.value)
		default:
			err = rows.Scan(&r.id, &r.field, &r.value)
		}
		if err != nil {
			return nil, err
		}
		if len(out) > 0 && bytes+r.bytes() > facetScalarBudget {
			break
		}
		out = append(out, r)
		bytes += r.bytes()
		if bytes >= facetScalarBudget {
			break
		}
	}
	return out, rows.Err()
}

func (w *facetWork) raw(ctx context.Context, tx *sql.Tx, budget int) ([]rawFacetRow, error) {
	subject := w.id
	if w.phase >= facetHolderGenres {
		if !w.holder {
			return nil, nil
		}
		if !w.ownShow {
			subject = w.item.Int64
		}
	} else if !w.show.Valid {
		return nil, nil
	}
	var rows *sql.Rows
	var err error
	switch w.phase {
	case facetEpisodeAssets, facetHolderAssets:
		if w.phase == facetHolderAssets && w.ownShow {
			return nil, nil
		}
		q := `SELECT l.asset_id,'resolution',CASE WHEN a.height>=2000 THEN '4k' WHEN a.height>=1000 THEN '1080p' WHEN a.height>=700 THEN '720p' ELSE 'sd' END
   FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`
		args := []any{subject}
		if w.hasAfter {
			q += ` AND l.asset_id>?`
			args = append(args, w.afterID)
		}
		q += ` ORDER BY l.asset_id LIMIT ?`
		args = append(args, budget)
		rows, err = tx.QueryContext(ctx, q, args...)
	case facetEpisodeCollections, facetHolderCollections:
		q := `SELECT collection_id,'collection',CAST(collection_id AS TEXT) FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=?`
		args := []any{subject}
		if w.hasAfter {
			q += ` AND collection_id>?`
			args = append(args, w.afterID)
		}
		q += ` ORDER BY collection_id LIMIT ?`
		args = append(args, budget)
		rows, err = tx.QueryContext(ctx, q, args...)
	case facetEpisodeAttributes, facetHolderAttributes:
		if w.phase == facetHolderAttributes && w.ownShow {
			return nil, nil
		}
		q := `SELECT e.term_id,f.field,e.source_value FROM catalog_item_attribute_edges e
   CROSS JOIN catalog_attribute_terms t ON t.id=e.term_id CROSS JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=?`
		args := []any{subject}
		if w.hasAfter {
			q += ` AND e.term_id>?`
			args = append(args, w.afterID)
		}
		q += ` ORDER BY e.term_id LIMIT ?`
		args = append(args, budget)
		rows, err = tx.QueryContext(ctx, q, args...)
	case facetHolderGenres:
		q := `SELECT s.provider,s.source_id,CASE WHEN t.vocab=1 THEN 'genre' ELSE '' END,COALESCE(s.label_override,t.label)
   FROM catalog_term_sources s CROSS JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id=?`
		args := []any{subject}
		if w.hasAfter {
			q += ` AND (s.provider,s.source_id)>(?,?)`
			args = append(args, w.provider, w.source)
		}
		q += ` ORDER BY s.provider,s.source_id LIMIT ?`
		args = append(args, budget)
		rows, err = tx.QueryContext(ctx, q, args...)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return readRawFacetRows(rows, w.phase)
}

func (w *facetWork) stageRaw(ctx context.Context, tx *sql.Tx, budget int) (int, error) {
	rows, err := w.raw(ctx, tx, budget)
	if err != nil {
		return 0, err
	}
	scope := 1
	if w.phase < facetHolderGenres {
		scope = 0
	}
	for _, r := range rows {
		w.quantumBytes += r.bytes()
		accept := r.field != "" && r.value.Valid && r.value.String != ""
		if w.phase == facetEpisodeAttributes || w.phase == facetHolderAttributes {
			accept = accept && facetAttribute(r.field)
		}
		if accept {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_stage(entity_id,scope,field,value) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, w.id, scope, r.field, r.value.String); err != nil {
				return 0, err
			}
		}
		// Advance past rejected fields, empty source values and duplicate values.
		w.afterID, w.provider, w.source, w.hasAfter = r.id, r.provider, r.source, true
	}
	// Only an EMPTY raw result proves the source exhausted. Byte stops and a
	// short full-PK page are not mistaken for completion.
	if len(rows) == 0 {
		w.advance()
	}
	return len(rows), nil
}

type facetAcceptedRow struct {
	scope        int
	field, value string
	show         int64
	place        facetPlace
	recent       string
}

func readFacetAccepted(rows *sql.Rows, phase int) ([]facetAcceptedRow, error) {
	defer rows.Close()
	var out []facetAcceptedRow
	bytes := 0
	for rows.Next() {
		var r facetAcceptedRow
		var err error
		switch phase {
		case facetClear, facetFinishClear:
			err = rows.Scan(&r.scope, &r.field, &r.value)
		case facetEpisodeRemove:
			err = rows.Scan(&r.field, &r.value, &r.show)
		case facetHolderRemove:
			err = rows.Scan(&r.field, &r.value, &r.place.library, &r.place.ratingKey, &r.place.labelKey, &r.recent)
		default:
			err = rows.Scan(&r.field, &r.value)
		}
		if err != nil {
			return nil, err
		}
		size := len(r.field) + len(r.value) + len(r.place.ratingKey) + len(r.place.labelKey) + len(r.recent)
		if len(out) > 0 && bytes+size > facetScalarBudget {
			break
		}
		out = append(out, r)
		bytes += size
		if bytes >= facetScalarBudget {
			break
		}
	}
	return out, rows.Err()
}

func (w *facetWork) accepted(ctx context.Context, tx *sql.Tx, budget int) ([]facetAcceptedRow, error) {
	var q string
	args := []any{w.id}
	switch w.phase {
	case facetClear, facetFinishClear:
		q = `SELECT scope,field,value FROM catalog_facet_stage WHERE entity_id=? ORDER BY scope,field,value LIMIT ?`
	case facetEpisodeRemove:
		q = `SELECT field,value,show_id FROM catalog_show_facet_episodes WHERE episode_id=?`
	case facetHolderRemove:
		q = `SELECT field,value,library_id,rating_key,label_key,recent_key FROM catalog_facet_members WHERE entity_id=?`
	case facetHolderShowValues:
		if !w.ownShow {
			return nil, nil
		}
		q = `SELECT field,value FROM catalog_show_facet_values WHERE show_id=?`
	default:
		q = `SELECT field,value FROM catalog_facet_stage WHERE entity_id=? AND scope=?`
		scope := 1
		if w.phase == facetEpisodeAdd {
			scope = 0
		}
		args = append(args, scope)
	}
	if w.phase != facetClear && w.phase != facetFinishClear {
		if w.hasAfter {
			q += ` AND (field,value)>(?,?)`
			args = append(args, w.field, w.value)
		}
		q += ` ORDER BY field,value LIMIT ?`
	}
	args = append(args, budget)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return readFacetAccepted(rows, w.phase)
}

func adjustShowFacet(ctx context.Context, tx *sql.Tx, show int64, k facetKey, delta int) error {
	if delta > 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_show_facet_values(show_id,field,value,total) VALUES(?,?,?,1)
   ON CONFLICT(show_id,field,value) DO UPDATE SET total=total+1`, show, k.field, k.value)
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM catalog_show_facet_values WHERE show_id=? AND field=? AND value=? AND total=1`, show, k.field, k.value)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_show_facet_values SET total=total-1 WHERE show_id=? AND field=? AND value=?`, show, k.field, k.value)
	}
	return err
}

func (w *facetWork) reconcile(ctx context.Context, tx *sql.Tx, budget int) (int, error) {
	if w.phase == facetHolderRemove || w.phase == facetHolderAdd {
		if err := w.loadPlace(ctx, tx); err != nil {
			return 0, err
		}
		if w.phase == facetHolderAdd && len(w.place.ratingKey)+len(w.place.labelKey)+len(w.recent) >= facetScalarBudget {
			budget = 1
		}
	}
	rows, err := w.accepted(ctx, tx, budget)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		w.quantumBytes += len(r.field) + len(r.value) + len(r.place.ratingKey) + len(r.place.labelKey) + len(r.recent)
		k := facetKey{r.field, r.value}
		switch w.phase {
		case facetClear, facetFinishClear:
			_, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_stage WHERE entity_id=? AND scope=? AND field=? AND value=?`, w.id, r.scope, r.field, r.value)
		case facetEpisodeRemove, facetHolderRemove:
			scope := 1
			if w.phase == facetEpisodeRemove {
				scope = 0
			}
			var keep bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_facet_stage WHERE entity_id=? AND scope=? AND field=? AND value=?)`, w.id, scope, r.field, r.value).Scan(&keep)
			if err != nil {
				return 0, err
			}
			if w.phase == facetEpisodeRemove {
				keep = keep && w.show.Valid && w.show.Int64 == r.show
				if !keep {
					if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_show_facet_episodes WHERE episode_id=? AND field=? AND value=?`, w.id, r.field, r.value); err != nil {
						return 0, err
					}
					if err = adjustShowFacet(ctx, tx, r.show, k, -1); err != nil {
						return 0, err
					}
					err = TouchTx(ctx, tx, DomainFacetCounts, r.show)
				}
			} else {
				keep = keep && r.place == w.place
				if !keep {
					if err = adjustFacetCount(ctx, tx, r.place, k, -1); err != nil {
						return 0, err
					}
					_, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_members WHERE entity_id=? AND field=? AND value=?`, w.id, r.field, r.value)
				} else if r.recent != w.recent {
					_, err = tx.ExecContext(ctx, `UPDATE catalog_facet_members SET recent_key=? WHERE entity_id=? AND field=? AND value=?`, w.recent, w.id, r.field, r.value)
				}
			}
		case facetEpisodeAdd:
			if !w.show.Valid {
				break
			}
			var res sql.Result
			res, err = tx.ExecContext(ctx, `INSERT INTO catalog_show_facet_episodes(episode_id,show_id,field,value) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, w.id, w.show.Int64, r.field, r.value)
			if err != nil {
				return 0, err
			}
			n, e := res.RowsAffected()
			if e != nil {
				return 0, e
			}
			if n > 0 {
				if err = adjustShowFacet(ctx, tx, w.show.Int64, k, 1); err != nil {
					return 0, err
				}
				err = TouchTx(ctx, tx, DomainFacetCounts, w.show.Int64)
			}
		case facetHolderAdd:
			var res sql.Result
			res, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_members(entity_id,field,value,library_id,rating_key,label_key,recent_key) VALUES(?,?,?,?,?,?,?)
    ON CONFLICT DO NOTHING`, w.id, r.field, r.value, w.place.library, w.place.ratingKey, w.place.labelKey, w.recent)
			if err != nil {
				return 0, err
			}
			n, e := res.RowsAffected()
			if e != nil {
				return 0, e
			}
			if n > 0 {
				err = adjustFacetCount(ctx, tx, w.place, k, 1)
			}
		case facetHolderShowValues:
			// Keep exactly the old show semantics: own genres/year/collection plus
			// episode-derived resolution/collections/descriptive attributes, NOT its
			// own descriptive attributes and NOT episode genre/year.
			if r.field != "genre" && r.value != "" {
				_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_stage(entity_id,scope,field,value) VALUES(?,1,?,?) ON CONFLICT DO NOTHING`, w.id, r.field, r.value)
			}
		}
		if err != nil {
			return 0, err
		}
		w.field, w.value, w.hasAfter = r.field, r.value, true
	}
	if len(rows) == 0 {
		if w.phase == facetClear {
			if err = w.capture(ctx, tx); err != nil {
				return 0, err
			}
		}
		w.advance()
	}
	return len(rows), nil
}

func drainFacetHolders(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
	if len(keys) == 0 || limit <= 0 {
		return nil, nil
	}
	// A source dictionary edit must become visible to ALL holders before any
	// candidate can capture/commit against the corresponding global source stamp.
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(36,37,38,39,40,41,42,43))
 OR EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(36,37,38,39,40,41,42,43))`).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, nil
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return nil, err
	}
	w, err := readFacetWork(ctx, tx, keys[0], epoch)
	if err != nil {
		return nil, err
	}
	remaining := min(limit, facetHolderQuantum)
	changed := false
	for remaining > 0 {
		// Share the same eight raw rows across families. After the first batch,
		// read one row at a time so a wide next scalar yields before another.
		batch := remaining
		if w.quantumBytes > 0 {
			batch = 1
		}
		var n int
		switch w.phase {
		case facetEpisodeAssets, facetEpisodeCollections, facetEpisodeAttributes, facetHolderGenres, facetHolderAssets, facetHolderCollections, facetHolderAttributes:
			n, err = w.stageRaw(ctx, tx, batch)
		case facetHolderYear, facetHolderDecade:
			if w.holder {
				subject := w.id
				if !w.ownShow {
					subject = w.item.Int64
				}
				var year int
				err = tx.QueryRowContext(ctx, `SELECT year FROM catalog_browse_rows WHERE entity_id=?`, subject).Scan(&year)
				if errors.Is(err, sql.ErrNoRows) {
					err = nil
				}
				if err != nil {
					return nil, err
				}
				if year >= 1800 && year <= 2199 {
					field := "year"
					if w.phase == facetHolderDecade {
						field = "decade"
						year = (year / 10) * 10
					}
					_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_stage(entity_id,scope,field,value) VALUES(?,1,?,?) ON CONFLICT DO NOTHING`, w.id, field, strconv.Itoa(year))
					n = 1
				}
			}
			w.advance()
		case facetFinish:
			if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_work_places WHERE entity_id=?`, w.id); err != nil {
				return nil, err
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_work WHERE entity_id=? AND revision=? AND source_epoch=?`, w.id, w.revision, w.epoch)
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET projection_epoch=projection_epoch+1 WHERE singleton=1`); err != nil {
				return nil, err
			}
			return []int64{w.id}, nil
		default:
			phase := w.phase
			n, err = w.reconcile(ctx, tx, batch)
			changed = changed || n > 0 && (phase == facetEpisodeRemove || phase == facetEpisodeAdd || phase == facetHolderRemove || phase == facetHolderAdd)
		}
		if err != nil {
			return nil, err
		}
		remaining -= n
		// A byte-limited/oversized scalar ends this quantum. An ordinary short
		// family can use the remaining RAW-row budget without a new admission;
		// only its next EMPTY read proves EOF, as before.
		if w.quantumBytes >= facetScalarBudget {
			break
		}
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET projection_epoch=projection_epoch+1 WHERE singleton=1`); err != nil {
			return nil, err
		}
	}
	return nil, w.checkpoint(ctx, tx)
}
