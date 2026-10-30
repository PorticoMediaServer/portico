package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Bounded item metrics (P2-64, domain 25).
//
// The item's duration, rating, air date, critic score, file size and
// resolution used to be four aggregates over every one of its asset links and
// metadata_ratings rows, all in one statement. That made one item's metric
// derivation grow with the item's own file/provider count. The replacement
// keeps one in-flight job row per item (catalog_item_metric_jobs) and walks
// the raw inputs in three phases:
//
//	phase 0  catalog_asset_links rows (the item's links), ordered by the full
//	         (entity_id,asset_id) primary key. The raw link page is materialized
//	         first, then catalog_assets is probed by primary key. The link's
//	         available flag is captured here as well as the asset's scalar
//	         evidence, so the item's actual file counts survive the rest of
//	         the walk.
//	phase 1  metadata_ratings rows for the item, ordered by (item_id,provider).
//	         Every provider row is scanned, accepted or not: the scan is a
//	         bounded PK range and the cursor must not jump over rows the
//	         acceptance set would hide.
//	phase 2  publish the scalars once, then merge the item's recorded member
//	         metrics with its current memberships (the existing metricEdges
//	         two-stream merge) and apply maxima plus the captured link totals
//	         to one bounded page of member rows.
//
// A call spends at most min(limit, upstreamQuantum) raw rows in total, plus
// the existing 256KiB byte stop. A scalar output (the air/added text) is read
// whole even when it is larger than the byte budget: it is one legal row, not
// a truncated stream. One call serves one holder; a newer catalog_dirty
// revision for the item resets the job and re-walks it, because maxima are not
// monotone across a repeated key.
//
// The raw maxima are Go comparisons, not SQL aggregates, so SQLite's MAX
// semantics are kept explicitly: NULL is ignored until the first value, a
// value may be negative, and a NULL accumulator stays NULL (duration/rating)
// or becomes the published zero default (critic/size/resolution).

const (
	// metricRawBudget is the same producer byte stop used by the facet holder:
	// an oversized raw row is processed alone and the quantum ends.
	metricRawBudget = 256 << 10
)

type itemMetricJob struct {
	sequence        int64
	phase           int
	linkAvailable   int64
	linkUnavailable int64
	afterAsset      int64
	afterProvider   string
	maxDuration     sql.NullFloat64
	maxSize         int64
	maxHeight       int64
	critic          sql.NullFloat64
	rating          sql.NullFloat64
	afterEntity     int64
	afterSource     int
}

// itemMetricScalars are the values published to catalog_browse_item_metrics
// and copied to every current member metric row.
type itemMetricScalars struct {
	duration sql.NullFloat64
	rating   sql.NullFloat64
	added    sql.NullString
	air      string
	critic   float64
	size     int64
	height   int64
}

const selectItemMetricJob = `SELECT sequence,phase,link_available,link_unavailable,after_asset,after_provider,
 max_duration,max_size,max_height,critic,rating,after_entity,after_source
 FROM catalog_item_metric_jobs WHERE item_id=?`

// resetItemMetricJob both creates a job and restarts a job whose queued
// revision moved. The revision is the fence: every cursor and accumulator is
// part of the same derived value, so none may survive a new input revision.
const resetItemMetricJob = `INSERT INTO catalog_item_metric_jobs(item_id,sequence,phase,link_available,link_unavailable,
 after_asset,after_provider,max_duration,max_size,max_height,critic,rating,after_entity,after_source)
 VALUES(?1,?2,0,0,0,0,'',NULL,0,0,NULL,NULL,0,-1)
 ON CONFLICT(item_id) DO UPDATE SET sequence=excluded.sequence,phase=0,link_available=0,link_unavailable=0,
 after_asset=0,after_provider='',max_duration=NULL,max_size=0,max_height=0,critic=NULL,rating=NULL,
 after_entity=0,after_source=-1`

const checkpointItemMetricJob = `UPDATE catalog_item_metric_jobs SET phase=?,link_available=?,link_unavailable=?,after_asset=?,
 after_provider=?,max_duration=?,max_size=?,max_height=?,critic=?,rating=?,after_entity=?,after_source=?
 WHERE item_id=? AND sequence=?`

const finishItemMetricJob = `DELETE FROM catalog_item_metric_jobs WHERE item_id=? AND sequence=?`

// deleteMemberMetricSQL is this walk's own copy of the per-edge delete: a
// departed membership or an unpublished container must leave no applied row.
// The full (item_id,entity_id,source) triple is the covering index key.
const deleteMemberMetricSQL = `DELETE FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_item WHERE item_id=? AND entity_id=? AND source=?`

const captureMetricLinkPage = `WITH l AS MATERIALIZED (
 SELECT asset_id,available FROM catalog_asset_links
 WHERE entity_id=?1 AND asset_id>?2 ORDER BY asset_id LIMIT ?3)
 SELECT l.asset_id,l.available,a.duration,a.size,a.height
 FROM l LEFT JOIN catalog_assets a ON a.id=l.asset_id ORDER BY l.asset_id`

const captureMetricRatingPage = `SELECT provider,value FROM metadata_ratings
 WHERE item_id=?1 AND provider>?2 ORDER BY provider LIMIT ?3`

const publishItemMetricScalarsSQL = `INSERT INTO catalog_browse_item_metrics(item_id,available_links,unavailable_links,duration,rating,added)
 VALUES(?1,?2,?3,?4,?5,?6)
 ON CONFLICT(item_id) DO UPDATE SET available_links=excluded.available_links,
 unavailable_links=excluded.unavailable_links,duration=excluded.duration,rating=excluded.rating,added=excluded.added`

// The INSERT selects from the current membership: a
// membership that departed between the edge page and this write inserts no
// row, and the caller's explicit existence check removes the old metric. The
// applied link totals come from the item's published row, not from the
// possibly-drifted trigger counter.
const upsertMemberMetricSQL = `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,
 duration,rating,added,air_key,critic,file_size,resolution,available_links,unavailable_links)
 SELECT m.entity_id,m.item_id,m.source,?4,?5,?6,?7,?8,?9,?10,?11,?12
 FROM catalog_browse_memberships m
 WHERE m.entity_id=?1 AND m.item_id=?2 AND m.source=?3
 ON CONFLICT(entity_id,item_id,source) DO UPDATE SET duration=excluded.duration,rating=excluded.rating,
 added=excluded.added,air_key=excluded.air_key,critic=excluded.critic,file_size=excluded.file_size,
 resolution=excluded.resolution,available_links=excluded.available_links,unavailable_links=excluded.unavailable_links
 WHERE (duration,rating,added,air_key,critic,file_size,resolution,available_links,unavailable_links)
 IS NOT(excluded.duration,excluded.rating,excluded.added,excluded.air_key,excluded.critic,excluded.file_size,excluded.resolution,excluded.available_links,excluded.unavailable_links)`

// deriveItemMetricsBounded recomputes one item's metrics and applies them to
// one bounded page of its containers. It reports whether the item's job is
// complete. The caller runs refreshEntityMetrics for touched items and queues
// the remaining touched containers exactly as the old drain did.
func deriveItemMetricsBounded(ctx context.Context, tx *sql.Tx, k Key, limit int, touched map[int64]bool) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	budget := min(limit, upstreamQuantum)
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=?)`, k.ID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		// The entity and its memberships are gone. Its item metric row has no
		// foreign key, so drop it, then reclaim stale member metric rows in
		// bounded primary-key pages. Deleting as we go is the cursor.
		return deriveGoneItemMetrics(ctx, tx, k.ID, budget, touched)
	}
	job, err := readItemMetricJob(ctx, tx, k.ID, k.Revision)
	if err != nil {
		return false, err
	}
	bytes := 0
	for budget > 0 {
		switch job.phase {
		case 0:
			n, eof, stopped, err := captureItemMetricLinks(ctx, tx, k.ID, &job, budget, &bytes)
			if err != nil {
				return false, err
			}
			budget -= n
			if eof {
				job.phase = 1
				job.afterProvider = ""
			}
			if stopped {
				budget = 0
			}
		case 1:
			n, eof, stopped, err := captureItemMetricRatings(ctx, tx, k.ID, &job, budget, &bytes)
			if err != nil {
				return false, err
			}
			budget -= n
			if eof {
				job.phase = 2
				job.afterEntity, job.afterSource = 0, -1
				// Publish at the phase boundary, inside the same dirty window
				// as the member fanout. A resumed phase 2 already published.
				if _, err = publishItemMetricScalars(ctx, tx, k.ID, &job); err != nil {
					return false, err
				}
			}
			if stopped {
				budget = 0
			}
		case 2:
			// The two-stream merge reads one row per stream at the floor, so a
			// one-row quantum still makes progress instead of starving.
			per := max(1, budget/2)
			edges, err := metricEdges(ctx, tx, k.ID, job.afterEntity, job.afterSource, per)
			if err != nil {
				return false, err
			}
			if err = applyMemberMetricPage(ctx, tx, k.ID, &job, edges, touched); err != nil {
				return false, err
			}
			// metricEdges reads at most per rows from each stream. If it
			// returns fewer distinct edges than per, both streams reached
			// end-of-input; otherwise the last edge is the full-key cursor.
			if len(edges) < per {
				_, err = tx.ExecContext(ctx, finishItemMetricJob, k.ID, k.Revision)
				return true, err
			}
			last := edges[len(edges)-1]
			job.afterEntity, job.afterSource = last.entity, last.source
			budget = 0 // one bounded edge page per call
		default:
			return false, fmt.Errorf("item metric job %d has phase %d", k.ID, job.phase)
		}
	}
	return false, checkpointItemMetricJobRow(ctx, tx, k.ID, &job)
}

func readItemMetricJob(ctx context.Context, tx *sql.Tx, item, revision int64) (itemMetricJob, error) {
	job := itemMetricJob{sequence: revision, afterSource: -1}
	var sequence int64
	err := tx.QueryRowContext(ctx, selectItemMetricJob, item).Scan(&sequence, &job.phase, &job.linkAvailable,
		&job.linkUnavailable, &job.afterAsset, &job.afterProvider, &job.maxDuration, &job.maxSize,
		&job.maxHeight, &job.critic, &job.rating, &job.afterEntity, &job.afterSource)
	if err == nil && sequence == revision {
		job.sequence = sequence
		return job, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	job = itemMetricJob{sequence: revision, afterSource: -1}
	_, err = tx.ExecContext(ctx, resetItemMetricJob, item, revision)
	return job, err
}

func checkpointItemMetricJobRow(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob) error {
	_, err := tx.ExecContext(ctx, checkpointItemMetricJob, job.phase, job.linkAvailable, job.linkUnavailable,
		job.afterAsset, job.afterProvider, job.maxDuration, job.maxSize, job.maxHeight, job.critic,
		job.rating, job.afterEntity, job.afterSource, item, job.sequence)
	return err
}

func captureItemMetricLinks(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob, limit int, bytes *int) (int, bool, bool, error) {
	rows, err := tx.QueryContext(ctx, captureMetricLinkPage, item, job.afterAsset, limit)
	if err != nil {
		return 0, false, false, err
	}
	defer rows.Close()
	// catalog_assets.duration/size/height are NOT NULL, and links reference
	// assets with ON DELETE CASCADE, so the LEFT JOIN probe is a normal
	// one-row PK lookup. Null types keep a pathological missing asset from
	// turning a raw link into an error while still advancing the cursor.
	sized := job.afterAsset != 0
	heights := job.afterAsset != 0
	processed := 0
	stopped := false
	for rows.Next() {
		var asset, available int64
		var duration sql.NullFloat64
		var size, height sql.NullInt64
		if err = rows.Scan(&asset, &available, &duration, &size, &height); err != nil {
			return 0, false, false, err
		}
		if available == 1 {
			job.linkAvailable++
		} else {
			job.linkUnavailable++
		}
		if duration.Valid && (!job.maxDuration.Valid || duration.Float64 > job.maxDuration.Float64) {
			job.maxDuration = duration
		}
		if size.Valid {
			if !sized || size.Int64 > job.maxSize {
				job.maxSize = size.Int64
			}
			sized = true
		}
		if height.Valid {
			if !heights || height.Int64 > job.maxHeight {
				job.maxHeight = height.Int64
			}
			heights = true
		}
		job.afterAsset = asset
		processed++
		*bytes += 40 // asset id, flag and three scalar columns, admission estimate
		if *bytes >= metricRawBudget {
			stopped = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return 0, false, false, err
	}
	eof := processed < limit && !stopped
	return processed, eof, stopped, nil
}

func captureItemMetricRatings(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob, limit int, bytes *int) (int, bool, bool, error) {
	rows, err := tx.QueryContext(ctx, captureMetricRatingPage, item, job.afterProvider, limit)
	if err != nil {
		return 0, false, false, err
	}
	defer rows.Close()
	processed := 0
	stopped := false
	for rows.Next() {
		var provider string
		var value float64
		if err = rows.Scan(&provider, &value); err != nil {
			return 0, false, false, err
		}
		switch provider {
		case "metacritic", "rottentomatoes":
			if !job.critic.Valid || value > job.critic.Float64 {
				job.critic = sql.NullFloat64{Float64: value, Valid: true}
			}
		case "tmdb", "imdb", "musicbrainz", "audible":
			if !job.rating.Valid || value > job.rating.Float64 {
				job.rating = sql.NullFloat64{Float64: value, Valid: true}
			}
		}
		job.afterProvider = provider
		processed++
		*bytes += len(provider) + 16
		if *bytes >= metricRawBudget {
			stopped = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return 0, false, false, err
	}
	eof := processed < limit && !stopped
	return processed, eof, stopped, nil
}

// publishItemMetricScalars reads the two independent text facts and writes the
// item's scalar row. Air comes from catalog_item_details directly, so an item
// with zero links still publishes its release date (the old aggregate query
// returned one row for an empty link set and evaluated the scalar subquery).
func publishItemMetricScalars(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob) (itemMetricScalars, error) {
	scalars, err := itemMetricScalarsFromJob(ctx, tx, item, job)
	if err != nil {
		return scalars, err
	}
	// Overwrite the maintained counters with the raw capture. This is the
	// drift repair for item counters; the trigger deltas are the lead's.
	_, err = tx.ExecContext(ctx, publishItemMetricScalarsSQL, item, job.linkAvailable, job.linkUnavailable,
		scalars.duration, scalars.rating, scalars.added)
	return scalars, err
}

func applyMemberMetricPage(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob, edges []metricEdge, touched map[int64]bool) error {
	if len(edges) == 0 {
		return nil
	}
	scalars, err := readPublishedItemMetricScalars(ctx, tx, item, job)
	if err != nil {
		return err
	}
	available, unavailable, err := readItemMetricCounts(ctx, tx, item, job)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		// Check the current membership first: a missing
		// membership must remove the old applied row, and a no-op UPSERT may
		// report zero changed rows on some drivers.
		var present bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_browse_memberships m
 WHERE m.entity_id=?1 AND m.item_id=?2 AND m.source=?3)`, edge.entity, item, edge.source).Scan(&present)
		if err != nil {
			return err
		}
		if !present {
			if _, err = tx.ExecContext(ctx, deleteMemberMetricSQL, item, edge.entity, edge.source); err != nil {
				return err
			}
		} else if _, err = tx.ExecContext(ctx, upsertMemberMetricSQL, edge.entity, item, edge.source,
			scalars.duration, scalars.rating, scalars.added, scalars.air, scalars.critic,
			scalars.size, scalars.height, available, unavailable); err != nil {
			return err
		}
		if touched != nil {
			touched[edge.entity] = true
		}
	}
	return nil
}

// readItemMetricCounts reads the just-published applied totals. The job's
// capture is the fallback only if the item row disappeared in this same
// transaction (which the existence check above makes unreachable in practice).
func readItemMetricCounts(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob) (int64, int64, error) {
	var available, unavailable int64
	err := tx.QueryRowContext(ctx, `SELECT available_links,unavailable_links
 FROM catalog_browse_item_metrics WHERE item_id=?`, item).Scan(&available, &unavailable)
	if errors.Is(err, sql.ErrNoRows) {
		return job.linkAvailable, job.linkUnavailable, nil
	}
	return available, unavailable, err
}

// readPublishedItemMetricScalars uses the job's maxima and re-reads the two
// text facts for this edge page. A change to either fact re-queues domain 25,
// which resets the job before the next page, so pages cannot mix revisions.
func readPublishedItemMetricScalars(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob) (itemMetricScalars, error) {
	return itemMetricScalarsFromJob(ctx, tx, item, job)
}

const itemMetricAirSQL = `SELECT COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ',release_date),'')
 FROM catalog_item_details WHERE entity_id=?`

const itemMetricAddedSQL = `SELECT added_text FROM catalog_browse_rows
 WHERE entity_id=? AND available=1 AND recent=1`

// itemMetricScalarsFromJob reads the two text facts independently of the link
// set: the old aggregate query returned one row for an empty link set and
// evaluated the release-date subquery, so an item with zero files still has an
// air key. Both strings are read whole; an oversized value is one legal scalar,
// not a truncated stream.
func itemMetricScalarsFromJob(ctx context.Context, tx *sql.Tx, item int64, job *itemMetricJob) (itemMetricScalars, error) {
	scalars := itemMetricScalars{
		duration: job.maxDuration,
		rating:   job.rating,
		size:     job.maxSize,
		height:   job.maxHeight,
	}
	err := tx.QueryRowContext(ctx, itemMetricAirSQL, item).Scan(&scalars.air)
	if errors.Is(err, sql.ErrNoRows) {
		scalars.air, err = "", nil
	}
	if err != nil {
		return scalars, err
	}
	err = tx.QueryRowContext(ctx, itemMetricAddedSQL, item).Scan(&scalars.added)
	if errors.Is(err, sql.ErrNoRows) {
		scalars.added, err = sql.NullString{}, nil
	}
	if err != nil {
		return scalars, err
	}
	if job.critic.Valid {
		scalars.critic = job.critic.Float64
	}
	return scalars, nil
}

// deriveGoneItemMetrics removes a deleted item's item row and at most limit
// stale member metric rows. The member metrics table has no foreign key, so
// the rows are reclaimed by primary-key page; deleting each page is the
// cursor and no separate job row is needed for a holder that no longer exists.
func deriveGoneItemMetrics(ctx context.Context, tx *sql.Tx, item int64, limit int, touched map[int64]bool) (bool, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_browse_item_metrics WHERE item_id=?`, item); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_item_metric_jobs WHERE item_id=?`, item); err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT entity_id,source FROM catalog_browse_member_metrics
 INDEXED BY catalog_browse_metric_item WHERE item_id=? ORDER BY entity_id,source LIMIT ?`, item, limit)
	if err != nil {
		return false, err
	}
	var edges []metricEdge
	for rows.Next() {
		var edge metricEdge
		if err = rows.Scan(&edge.entity, &edge.source); err != nil {
			rows.Close()
			return false, err
		}
		edges = append(edges, edge)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, edge := range edges {
		if _, err = tx.ExecContext(ctx, deleteMemberMetricSQL, item, edge.entity, edge.source); err != nil {
			return false, err
		}
		if touched != nil {
			touched[edge.entity] = true
		}
	}
	return len(edges) < limit, nil
}
