package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

const (
	DomainAssetSources   = 41
	DomainEntitySources  = 42
	DomainLibrarySources = 43
	DomainMissingSources = 44
	sourceAvailability   = 1
	sourceMissing        = 2
	sourceMetadata       = 4
	sourceRevision       = 8
	sourceScreen         = 16
	sourceInfo           = 32
	sourceTVDB           = 64
	upstreamQuantum      = 8
)

// queueSource is an accepting source marker, never a query job. Source41
// combines actions while preserving its finite same-token pass; other domains
// reset their cursor immediately. A captured
// source lifetime prevents a later reused integer ID from continuing old work.
func queueSource(ctx context.Context, tx *sql.Tx, domain int, id int64, token string, flags int64) error {
	query := `INSERT INTO catalog_upstream_sources(domain,source_id,token,flags) VALUES(?,?,?,?)
 ON CONFLICT(domain,source_id) DO UPDATE SET token=excluded.token,flags=flags|excluded.flags,revision=revision+1,source_epoch=0,
 phase=0,has_after=0,after_id=0,after_aux=0,after_key='',active_id=NULL,active_key='',active_available=0,seen_inventory=0,inner_has=0,inner_id=0,inner_aux=0,inner_key=''`
	if domain == DomainAssetSources {
		query = `INSERT INTO catalog_upstream_sources(domain,source_id,token,flags) VALUES(?,?,?,?)
 ON CONFLICT(domain,source_id) DO UPDATE SET token=excluded.token,flags=flags|excluded.flags,revision=revision+1,source_epoch=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN source_epoch ELSE 0 END,
 phase=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN phase ELSE 0 END,has_after=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN has_after ELSE 0 END,after_id=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN after_id ELSE 0 END,after_aux=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN after_aux ELSE 0 END,after_key=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN after_key ELSE '' END,active_id=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN active_id ELSE NULL END,active_key=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN active_key ELSE '' END,active_available=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN active_available ELSE 0 END,seen_inventory=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN seen_inventory ELSE 0 END,inner_has=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN inner_has ELSE 0 END,inner_id=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN inner_id ELSE 0 END,inner_aux=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN inner_aux ELSE 0 END,inner_key=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN inner_key ELSE '' END,pass_revision=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN pass_revision ELSE 0 END,fence_entity=CASE WHEN catalog_upstream_sources.token IS excluded.token THEN fence_entity ELSE NULL END`
	}
	if _, err := tx.ExecContext(ctx, query, domain, id, token, flags); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`); err != nil {
		return err
	}
	return TouchTx(ctx, tx, domain, id)
}

// An unlinked asset has no source consumers. Link ingress queues its current
// owners independently, so earlier unlinked changes need no speculative marker.
func queueAssetSource(ctx context.Context, tx *sql.Tx, asset int64, flags int64) error {
	var token string
	err := tx.QueryRowContext(ctx, `SELECT a.token FROM catalog_assets a WHERE a.id=?
 AND EXISTS(SELECT 1 FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=a.id)`, asset).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return queueSource(ctx, tx, DomainAssetSources, asset, token, flags)
}

type upstreamSource struct {
	domain                    int
	id, revision, flags       int64
	token                     string
	epoch                     int64
	passRevision              int64
	fenceEntity               sql.NullInt64
	phase                     int
	has                       bool
	after, aux                int64
	key                       string
	active                    sql.NullInt64
	activeKey                 string
	available, seen, innerHas bool
	inner, innerAux           int64
	innerKey                  string
}

func readUpstreamSource(ctx context.Context, tx *sql.Tx, domain int, id int64) (upstreamSource, error) {
	w := upstreamSource{domain: domain, id: id}
	err := tx.QueryRowContext(ctx, `SELECT token,flags,revision,source_epoch,phase,has_after,after_id,after_aux,after_key,active_id,active_key,active_available,seen_inventory,inner_has,inner_id,inner_aux,inner_key,pass_revision,fence_entity
 FROM catalog_upstream_sources WHERE domain=? AND source_id=?`, domain, id).
		Scan(&w.token, &w.flags, &w.revision, &w.epoch, &w.phase, &w.has, &w.after, &w.aux, &w.key, &w.active, &w.activeKey, &w.available, &w.seen, &w.innerHas, &w.inner, &w.innerAux, &w.innerKey, &w.passRevision, &w.fenceEntity)
	return w, err
}
func (w *upstreamSource) advance() {
	w.phase++
	w.has = false
	w.after = 0
	w.aux = 0
	w.key = ""
	w.clearInner()
}
func (w *upstreamSource) clearInner() {
	w.active = sql.NullInt64{}
	w.innerHas = false
	w.inner = 0
	w.innerAux = 0
	w.innerKey = ""
	w.activeKey = ""
	w.available = false
	w.seen = false
}
func (w *upstreamSource) checkpoint(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_upstream_sources SET source_epoch=?,phase=?,has_after=?,after_id=?,after_aux=?,after_key=?,active_id=?,active_key=?,active_available=?,seen_inventory=?,inner_has=?,inner_id=?,inner_aux=?,inner_key=?,pass_revision=?,fence_entity=?
 WHERE domain=? AND source_id=? AND revision=?`, w.epoch, w.phase, w.has, w.after, w.aux, w.key, w.active, w.activeKey, w.available, w.seen, w.innerHas, w.inner, w.innerAux, w.innerKey, w.passRevision, w.fenceEntity, w.domain, w.id, w.revision)
	return err
}
func (w *upstreamSource) finish(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_upstream_sources WHERE domain=? AND source_id=? AND revision=?`, w.domain, w.id, w.revision)
	return err
}

type sourceEdge struct {
	id, aux int64
	key     string
	flag    bool
}

func sourceEdges(rows *sql.Rows, shape int) ([]sourceEdge, error) {
	defer rows.Close()
	out := make([]sourceEdge, 0, upstreamQuantum)
	bytes := 0
	for rows.Next() {
		var r sourceEdge
		var err error
		switch shape {
		case 1:
			err = rows.Scan(&r.id, &r.key)
		case 2:
			err = rows.Scan(&r.id, &r.aux)
		case 3:
			err = rows.Scan(&r.id, &r.flag)
		case 4:
			err = rows.Scan(&r.key, &r.flag)
		case 5:
			err = rows.Scan(&r.id, &r.key, &r.aux)
		default:
			err = rows.Scan(&r.id)
		}
		if err != nil {
			return nil, err
		}
		if len(out) > 0 && bytes+len(r.key) > facetScalarBudget {
			break
		}
		out = append(out, r)
		bytes += len(r.key)
		if bytes >= facetScalarBudget {
			break
		}
	}
	return out, rows.Err()
}
func querySourceEdges(ctx context.Context, tx *sql.Tx, shape int, q string, args ...any) ([]sourceEdge, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sourceEdges(rows, shape)
}

// queryShowEpisodeEdges uses the existing (show,season,number,entity) index.
// NULL seasons sort first but cannot participate in SQL tuple comparisons. Read
// their range separately, then seek the non-NULL tuple. Every raw row advances,
// even when its downstream semantic consumer rejects it. The nullable season
// is encoded as "n" or its exact decimal integer in existing generic cursors.
func queryShowEpisodeEdges(ctx context.Context, tx *sql.Tx, show int64, has bool, entity, number int64, season string, limit int) ([]sourceEdge, error) {
	const prefix = `SELECT entity_id,COALESCE(CAST(season_id AS TEXT),'n'),number FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=?`
	// Empty is the previous entity-only cursor format. Restart that source once;
	// duplicate dirty marks are safe, skipping an episode is not.
	if season == "" {
		has = false
	}
	if !has || season == "n" {
		q := prefix + ` AND season_id IS NULL`
		args := []any{show}
		if has {
			q += ` AND (number,entity_id)>(?,?)`
			args = append(args, number, entity)
		}
		q += ` ORDER BY number,entity_id LIMIT ?`
		args = append(args, limit)
		edges, err := querySourceEdges(ctx, tx, 5, q, args...)
		if err != nil || len(edges) > 0 {
			return edges, err
		}
		return querySourceEdges(ctx, tx, 5, prefix+` AND season_id IS NOT NULL ORDER BY season_id,number,entity_id LIMIT ?`, show, limit)
	}
	seasonID, err := strconv.ParseInt(season, 10, 64)
	if err != nil {
		return nil, err
	}
	return querySourceEdges(ctx, tx, 5, prefix+` AND (season_id,number,entity_id)>(?,?,?) ORDER BY season_id,number,entity_id LIMIT ?`, show, seasonID, number, entity, limit)
}

func wakeScreenSource(ctx context.Context, tx *sql.Tx, kind string, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind=? AND target_id=?`, kind, id)
	return err
}

func (w *upstreamSource) asset(ctx context.Context, tx *sql.Tx, limit int) (int, bool, error) {
	var token string
	err := tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, w.id).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) || err == nil && token != w.token {
		// Old-lifetime source bodies outlive the asset deliberately. Drain their
		// bounded retirement before acknowledging this exact source token.
		done, cleanupErr := AdvanceInfoPublicationTx(ctx, tx, w.id, w.token, NewInfoMaterializer())
		return 0, done, cleanupErr
	}
	if err != nil {
		return 0, false, err
	}
	// Empty protocol phases do not consume a separate writer admission. They
	// move only forward (0 -> 1 -> 2 -> 3) inside this captured pass. A raw
	// inventory page, availability publication or FI1 quantum still returns
	// immediately, preserving its existing work budget and yield boundary.
assetPhase:
	switch w.phase {
	case 0:
		if err = tx.QueryRowContext(ctx, `SELECT availability_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&w.epoch); err != nil {
			return 0, false, err
		}
		// Both frontiers are captured once. NULL means no row; empty and negative
		// raw keys remain legal. Same-token accepts preserve this entire pass.
		var fence sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT max(id) FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=?`, w.token).Scan(&fence); err != nil {
			return 0, false, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT max(entity_id) FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?`, w.id).Scan(&w.fenceEntity); err != nil {
			return 0, false, err
		}
		w.clearInner()
		if fence.Valid {
			w.active = sql.NullInt64{Int64: 1, Valid: true}
			w.activeKey = fence.String
		}
		w.has, w.after, w.key = false, 0, ""
		w.passRevision, w.phase = w.revision, 1
		goto assetPhase
	case 1:
		if !w.active.Valid {
			w.phase = 2
			goto assetPhase
		}
		q := `SELECT o.id,o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND o.root_incarnation=s.incarnation
 FROM inventory_objects o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources s ON s.id=o.source_id
 WHERE o.asset_id=? AND o.id<=?`
		args := []any{w.token, w.activeKey}
		if w.innerHas {
			q += ` AND o.id>?`
			args = append(args, w.innerKey)
		}
		q += ` ORDER BY o.id LIMIT ?`
		args = append(args, limit)
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return 0, false, err
		}
		count, bytes := 0, 0
		for rows.Next() {
			var id string
			var available bool
			if err = rows.Scan(&id, &available); err != nil {
				rows.Close()
				return 0, false, err
			}
			if count > 0 && bytes+len(id) > facetScalarBudget {
				break
			}
			count++
			bytes += len(id)
			w.innerHas, w.innerKey, w.seen = true, id, true
			w.available = w.available || available
			// The first eligible raw ID also defines informational owner choice;
			// do not let later rows replace it before testing for a probe.
			if available {
				break
			}
			if bytes >= facetScalarBudget {
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return count, false, err
		}
		if count == 0 || w.available {
			w.phase = 2
		}
		return count, false, nil
	case 2:
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT availability_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); err != nil {
			return 0, false, err
		}
		// This source-config stamp includes enabled/incarnation/root refusal
		// changes, while unrelated imports and transient health leave it alone.
		if epoch != w.epoch {
			w.key = "rerun"
		}
		if w.passRevision == w.revision && w.key != "rerun" {
			// Only a complete revision-consistent reduction publishes. The changed
			// asset's existing point hooks run atomically and may retouch this source.
			if w.seen {
				if err = SetAssetAvailableTx(ctx, tx, w.id, w.available); err != nil {
					return 0, false, err
				}
				if err = tx.QueryRowContext(ctx, `SELECT revision,flags FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, w.id).Scan(&w.revision, &w.flags); err != nil {
					return 0, false, err
				}
				w.passRevision = w.revision // adopt only our same-writer point hooks
			}
		}
		w.phase = 3
		if !w.seen {
			goto assetPhase
		}
		return 0, false, nil
	case 3:
		if w.flags&sourceInfoInvalidated != 0 && w.passRevision == w.revision && w.key != "rerun" {
			handled, accepted, e := advanceInfoInvalidationTx(ctx, tx, w.id, w.token)
			if e != nil {
				return 0, false, e
			}
			if accepted {
				// Whole-input acceptance queues this exact source in the same
				// writer. Adopt only that known point retouch, just as A5 does
				// after SetAssetAvailableTx; no external revision can interleave.
				if e = tx.QueryRowContext(ctx, `SELECT revision,flags FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND token=?`, w.id, w.token).Scan(&w.revision, &w.flags); e != nil {
					return 0, false, e
				}
				w.passRevision = w.revision
				if _, e = tx.ExecContext(ctx, `UPDATE catalog_upstream_sources SET flags=flags&~128 WHERE domain=41 AND source_id=? AND token=? AND revision=?`, w.id, w.token, w.revision); e != nil {
					return 0, false, e
				}
				w.flags &^= sourceInfoInvalidated
			}
			if handled {
				return 0, false, nil
			}
		}
		canAdvanceInfo := true
		if w.flags&sourceInfo != 0 {
			// Finish an already fenced publication even during later churn.
			// An unresolved owner choice needs A5's coherent captured pass;
			// otherwise finish the old owner walk and honor its existing rerun.
			c, e := activeInfoChange(ctx, tx, w.id, w.token)
			unresolved := errors.Is(e, sql.ErrNoRows) || e == nil && c.phase <= 1
			if e == nil && c.phase == 1 {
				var pinned bool
				if e = tx.QueryRowContext(ctx, `SELECT copy_family=1 FROM catalog_info_changes WHERE id=?`, c.id).Scan(&pinned); e != nil {
					return 0, false, e
				}
				unresolved = !pinned
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return 0, false, e
			}
			if unresolved {
				if w.passRevision != w.revision || w.key == "rerun" {
					canAdvanceInfo = false
				} else {
					ready, e := prepareInfoOwnerTargetTx(ctx, tx, w.id, w.token, w.available, w.innerKey)
					if e != nil {
						return 0, false, e
					}
					if !ready {
						return 0, false, nil
					}
				}
			}
		}
		if w.flags&sourceInfo != 0 && canAdvanceInfo {
			done, err := AdvanceInfoPublicationTx(ctx, tx, w.id, w.token, NewInfoMaterializer())
			if err != nil || !done {
				return 0, false, err
			}
			// No other writer can accept between this proof and clearing the bit.
			// Keep A5's revision, cursor and rerun interval exactly as captured.
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_upstream_sources SET flags=flags&~32 WHERE domain=41 AND source_id=? AND token=? AND revision=?`, w.id, w.token, w.revision); err != nil {
				return 0, false, err
			}
			w.flags &^= sourceInfo
		}
		q := `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? AND entity_id<=?`
		args := []any{w.id, w.fenceEntity}
		if w.has {
			q += ` AND entity_id>?`
			args = append(args, w.after)
		}
		q += ` ORDER BY entity_id LIMIT ?`
		args = append(args, limit)
		edges, err := querySourceEdges(ctx, tx, 0, q, args...)
		if err != nil {
			return 0, false, err
		}
		for _, r := range edges {
			if w.flags&sourceAvailability != 0 {
				if err = TouchTx(ctx, tx, DomainAvailability, r.id); err != nil {
					return 0, false, err
				}
			}
			if w.flags&sourceMissing != 0 {
				if err = queueMissingSource(ctx, tx, r.id); err != nil {
					return 0, false, err
				}
			}
			if w.flags&sourceMetadata != 0 {
				if _, err = tx.ExecContext(ctx, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, r.id); err != nil {
					return 0, false, err
				}
			}
			if w.flags&sourceRevision != 0 {
				if _, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=(SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?)`, r.id); err != nil {
					return 0, false, err
				}
			}
			if w.flags&sourceScreen != 0 {
				if err = wakeScreenSource(ctx, tx, "item", r.id); err != nil {
					return 0, false, err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind='show' AND target_id=(SELECT show_id FROM catalog_episodes WHERE entity_id=?)`, r.id); err != nil {
					return 0, false, err
				}
			}
			w.after, w.has = r.id, true
		}

		if len(edges) == 0 {
			if w.passRevision != w.revision || w.key == "rerun" {
				// A finite pass finished. One dirty row owes the whole newer interval;
				// no reset loop can prevent this pass from reaching its captured end.
				w.phase, w.passRevision = 0, 0
				return 0, false, nil
			}
			return 0, true, nil
		}
		return len(edges), false, nil
	default:
		return 0, false, errors.New("unknown asset source phase")
	}
}

// Every parent stream uses its full raw key. The Book stream walks each file's
// membership PK before queueing its membership rebuild, retaining old authors.
func (w *upstreamSource) parent(ctx context.Context, tx *sql.Tx, limit int) (int, bool, error) {
	var lifetime string
	err := tx.QueryRowContext(ctx, `SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, w.id).Scan(&lifetime)
	if errors.Is(err, sql.ErrNoRows) || err == nil && lifetime != w.token {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	for w.phase < 7 {
		kinds := []Kind{Artist, Artist, Collection, Show, Show, Album, Book}
		if w.flags&(1<<uint(kinds[w.phase])) == 0 {
			w.advance()
			continue
		}
		if w.phase == 6 && w.active.Valid {
			q := `SELECT entity_id,source FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=?`
			args := []any{w.active.Int64}
			if w.innerHas {
				q += ` AND (entity_id,source)>(?,?)`
				args = append(args, w.inner, w.innerAux)
			}
			q += ` ORDER BY entity_id,source LIMIT ?`
			args = append(args, limit)
			edges, e := querySourceEdges(ctx, tx, 2, q, args...)
			if e != nil {
				return 0, false, e
			}
			for _, r := range edges {
				if r.aux == 7 {
					if e = TouchTx(ctx, tx, DomainBrowseRows, r.id); e != nil {
						return 0, false, e
					}
				}
				w.inner, w.innerAux, w.innerHas = r.id, r.aux, true
			}
			if len(edges) > 0 {
				return len(edges), false, nil
			}
			if err = TouchTx(ctx, tx, DomainBrowseEdges, w.active.Int64); err != nil {
				return 0, false, err
			}
			w.clearInner()
			// Completing an empty nested stream is one bounded scheduling unit.
			return 1, false, nil
		}
		sources := []string{
			`SELECT entity_id FROM catalog_albums WHERE artist_id=?`,
			`SELECT song_id FROM catalog_song_artists INDEXED BY catalog_song_artists_artist WHERE artist_id=?`,
			`SELECT item_id FROM catalog_collection_members WHERE collection_id=?`,
			`SELECT entity_id,number FROM catalog_seasons WHERE show_id=?`,
			``, // episode stream uses queryShowEpisodeEdges below
			`SELECT entity_id FROM catalog_songs INDEXED BY catalog_songs_album_entity WHERE album_id=?`,
			`SELECT entity_id FROM catalog_book_files INDEXED BY catalog_book_files_book_entity WHERE book_id=?`,
		}
		columns := []string{"entity_id", "song_id", "item_id", "entity_id", "entity_id", "entity_id", "entity_id"}
		var edges []sourceEdge
		var e error
		if w.phase == 4 {
			edges, e = queryShowEpisodeEdges(ctx, tx, w.id, w.has, w.after, w.aux, w.key, limit)
		} else {
			q := sources[w.phase]
			args := []any{w.id}
			shape := 0
			if w.phase == 3 {
				shape = 2
				if w.has {
					q += ` AND (number,entity_id)>(?,?)`
					args = append(args, w.aux, w.after)
				}
				q += ` ORDER BY number,entity_id LIMIT ?`
			} else {
				if w.has {
					q += ` AND ` + columns[w.phase] + `>?`
					args = append(args, w.after)
				}
				q += ` ORDER BY ` + columns[w.phase] + ` LIMIT ?`
			}
			if w.phase == 6 {
				limit = 1
			}
			args = append(args, limit)
			edges, e = querySourceEdges(ctx, tx, shape, q, args...)
		}
		if e != nil {
			return 0, false, e
		}
		if len(edges) == 0 {
			w.advance()
			continue
		}
		for _, r := range edges {
			switch w.phase {
			case 3:
				e = TouchTx(ctx, tx, DomainBrowseRows, r.id)
			case 5:
				if e = TouchTx(ctx, tx, DomainBrowseEdges, r.id); e == nil {
					e = TouchTx(ctx, tx, DomainSearch, r.id)
				}
			case 6:
				w.active = sql.NullInt64{Int64: r.id, Valid: true}
			default:
				e = TouchTx(ctx, tx, DomainSearch, r.id)
			}
			if e != nil {
				return 0, false, e
			}
			w.after, w.aux, w.key, w.has = r.id, r.aux, r.key, true
		}
		return len(edges), false, nil
	}
	return 0, true, nil
}

func (w *upstreamSource) library(ctx context.Context, tx *sql.Tx, limit int) (int, bool, error) {
	var library string
	err := tx.QueryRowContext(ctx, `SELECT library_id FROM catalog_libraries WHERE id=?`, w.id).Scan(&library)
	if errors.Is(err, sql.ErrNoRows) || err == nil && library != w.token {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	// P2-52's screen_metadata_fanout is the sole library-wide screen/artwork
	// owner. Domain43 only invalidates deferred metadata/TVDB source heads.
	if w.flags&(sourceMetadata|sourceTVDB) == 0 {
		return 0, true, nil
	}
	// Keep the metadata stream in its existing phase1. An interrupted old
	// phase0 screen cursor must not become an entity cursor and skip heads.
	if w.phase == 0 {
		w.advance()
	}
	q := `SELECT id,kind FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=?`
	args := []any{w.id}
	if w.has {
		q += ` AND id>?`
		args = append(args, w.after)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	edges, err := querySourceEdges(ctx, tx, 2, q, args...)
	if err != nil {
		return 0, false, err
	}
	for _, r := range edges {
		if w.flags&sourceMetadata != 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, r.id); err != nil {
				return 0, false, err
			}
		}
		if w.flags&sourceTVDB != 0 && r.aux == int64(Show) {
			if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_heads SET source_revision=source_revision+1 WHERE show_id=?`, r.id); err != nil {
				return 0, false, err
			}
		}
		w.after, w.has = r.id, true
	}
	return len(edges), len(edges) == 0, nil
}

func drainUpstreamSources(ctx context.Context, tx *sql.Tx, domain int, keys []Key, limit int) ([]int64, error) {
	if len(keys) == 0 || limit <= 0 {
		return nil, nil
	}
	// One source per transaction; raw/output/empty-stream budget is shared.
	key := keys[0]
	w, err := readUpstreamSource(ctx, tx, domain, key.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return []int64{key.ID}, nil
	}
	if err != nil {
		return nil, err
	}
	budget := min(limit, upstreamQuantum)
	var done bool
	switch domain {
	case DomainAssetSources:
		_, done, err = w.asset(ctx, tx, budget)
	case DomainEntitySources:
		_, done, err = w.parent(ctx, tx, budget)
	case DomainLibrarySources:
		_, done, err = w.library(ctx, tx, budget)
	case DomainMissingSources:
		_, done, err = w.missing(ctx, tx, budget)
	}
	if err != nil {
		return nil, err
	}
	if done {
		if err = w.finish(ctx, tx); err != nil {
			return nil, err
		}
		return []int64{key.ID}, nil
	}
	return nil, w.checkpoint(ctx, tx)
}

// PublicationSourcesReady guards deferred source-head invalidation. Provider
// capture AND validation use it; live grants/root authority remain raw checks.
// P2-52 owns library screen/artwork scheduling, not source-head invalidation.
// Its pending/retry counts are not acceptance epochs: policy/config/scan/root
// checks fence provider publication immediately, and later accepted catalogue
// changes advance facet sources themselves. Domain41 asset-point wakes remain
// distinct and must complete along with deferred metadata-head invalidation.
func PublicationSourcesReady(ctx context.Context, q ReadQuery) (bool, error) {
	var pending, failed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain IN(41,43))
 OR EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(41,43)),EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(41,43))`).Scan(&pending, &failed)
	if err != nil {
		return false, err
	}
	if failed {
		return false, ErrFacetProducerFailed
	}
	return !pending, nil
}

func init() {
	for _, domain := range []int{DomainAssetSources, DomainEntitySources, DomainLibrarySources, DomainMissingSources} {
		version := 1
		if domain == DomainAssetSources {
			version = 2
		}
		Register(domain, Derivation{Version: version,
			Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
				return drainUpstreamSources(ctx, tx, domain, keys, limit)
			},
			Backfill: func(context.Context, *sql.Tx, int64, int) ([]int64, error) { return nil, nil },
		})
	}
}
