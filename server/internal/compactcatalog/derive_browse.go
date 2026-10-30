package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Browse rows (domain 18) and memberships (19), their bucket counts, counted
// rows and Home buckets, and the duration/rating metrics fanned out from items
// to the containers they belong to (25, 26).
//
// A row is a function of its entity's facts. Every kind gets the generic row
// (title, sort key, year; an item's added time, availability, attributes and
// backdrop); a few shipped kinds refine it (a show's added time is its first
// episode's, a season is titled after its show, an author lives while a live
// book names it). An unknown kind always takes the generic path.

type browseRow struct {
	entity, library, kind        int64
	title, sortKey, head         string
	year                         int
	added                        sql.NullString
	item                         sql.NullInt64
	ratingKey, labelKey          string
	available, recent, backdrop  int
	durationBucket, ratingBucket string
}

// browseAttributeKeySQL is the '|'-joined distinct value keys of one attribute
// field of item e.
func browseAttributeKeySQL(field string) string {
	return `COALESCE((SELECT group_concat(value_key,'|') FROM (SELECT DISTINCT t.value_key FROM catalog_item_attribute_edges x
 JOIN catalog_attribute_terms t ON t.id=x.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id
 WHERE x.item_id=e.id AND f.field='` + field + `' ORDER BY t.value_key)),'')`
}

// Each returns title, sort_key, year, added, item, rating_key, label_key,
// available, recent, backdrop for entity ?1, or no row when it has none. An
// item not yet through availability derivation reads its links' flags, so a
// first build need not derive its row again when availability arrives.
const (
	playableRowSQL = `SELECT e.title,e.sort_key,e.year,d.added_text,e.id,` + "%s" + `,
 COALESCE((SELECT v.available FROM catalog_item_availability v WHERE v.entity_id=e.id),EXISTS(SELECT 1 FROM catalog_asset_links l WHERE l.entity_id=e.id AND l.available=1)),d.added_text IS NOT NULL AND e.kind<>11,COALESCE(d.backdrop_url,'')<>''
 FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id
 WHERE e.id=?1 AND e.retired=0 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)`
	containerRowSQL = `SELECT e.title,e.sort_key,e.year,NULL,NULL,'','',0,0,0 FROM catalog_entities e WHERE e.id=?1 AND e.retired=0`
)

// A show's and a season's added date is its earliest dated episode, read by
// index from catalog_container_added (one seek, whatever the episode count).
var refinedRowSQL = map[Kind]string{
	Show: `SELECT e.title,e.sort_key,e.year,
 (SELECT added_text FROM catalog_container_added WHERE container_id=e.id ORDER BY added_text LIMIT 1),NULL,'','',0,0,0
 FROM catalog_entities e WHERE e.id=?1 AND e.retired=0`,
	Season: `SELECT sh.title||' · Season '||c.number,portico_sort_title(sh.title||' · Season '||c.number,'',sh.metadata_language),sh.year,
 (SELECT added_text FROM catalog_container_added WHERE container_id=e.id ORDER BY added_text LIMIT 1),NULL,'','',0,0,0
 FROM catalog_entities e JOIN catalog_seasons c ON c.entity_id=e.id JOIN catalog_entities sh ON sh.id=c.show_id
 WHERE e.id=?1 AND e.retired=0`,
	Collection: `SELECT e.title,e.sort_key,0,c.created_at,NULL,'','',0,0,0
 FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id WHERE e.id=?1 AND e.retired=0`,
	// An author lives while a live book of its library names it.
	Author: `SELECT e.title,e.sort_key,0,NULL,NULL,'','',0,0,0
 FROM catalog_entities e WHERE e.id=?1 AND e.retired=0 AND EXISTS(SELECT 1 FROM catalog_books b INDEXED BY catalog_books_author_seek
  JOIN catalog_entities be ON be.id=b.entity_id AND be.retired=0
  WHERE b.library_id=e.library_id AND b.author=e.title COLLATE NOCASE)`,
}

var playableRow = fmt.Sprintf(playableRowSQL, "?2,?3")

// readBrowseRow computes an entity's browse row; sql.ErrNoRows when it has none.
func readBrowseRow(ctx context.Context, tx *sql.Tx, id int64, ratingKey, labelKey string) (browseRow, error) {
	row := browseRow{entity: id}
	var playable, browsable bool
	err := tx.QueryRowContext(ctx, `SELECT e.library_id,e.kind,k.playable,k.browsable FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?`, id).Scan(&row.library, &row.kind, &playable, &browsable)
	if err != nil {
		return row, err
	}
	if !browsable {
		return row, sql.ErrNoRows
	}
	query, refined := refinedRowSQL[Kind(row.kind)]
	switch {
	case refined:
	case playable || Kind(row.kind) == Ebook || Kind(row.kind) == Comic:
		// Reading is a leaf without acquiring playback/listening flags.
		query = playableRow
	default:
		query = containerRowSQL
	}
	args := []any{id}
	if query == playableRow {
		args = append(args, ratingKey, labelKey)
	}
	err = tx.QueryRowContext(ctx, query, args...).Scan(&row.title, &row.sortKey, &row.year, &row.added, &row.item, &row.ratingKey, &row.labelKey, &row.available, &row.recent, &row.backdrop)
	if err != nil {
		return row, err
	}
	if len(row.sortKey) > 0 {
		row.head = string([]rune(row.sortKey)[:1])
	}
	return row, nil
}

// Per-item statements that must stay primary-key or index seeks: each once
// walked a whole partition per item, which made derivation quadratic in the
// library. TestPerItemDerivationStatementsSeek holds their query plans.
const (
	browseBucketEmptyDelete = `DELETE FROM catalog_browse_buckets WHERE library_id=? AND kind=? AND container=? AND axis=? AND value=? AND rating_key=? AND label_key=? AND total=0`
	memberMetricDelete      = `DELETE FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_item WHERE item_id=? AND entity_id=? AND source=?`
)

// browseStaticAxes: a row contributes one count to each static axis.
func browseStaticAxes(row browseRow) [7]string {
	year, decade := "", ""
	if row.year > 0 {
		year = strconv.Itoa(row.year)
		decade = strconv.Itoa((row.year/10)*10) + "s"
	}
	month, addedYear := "", ""
	if row.added.Valid && len(row.added.String) >= 7 {
		addedYear = row.added.String[:4]
		month = row.added.String[:7]
	}
	return [7]string{row.head, year, decade, month, addedYear, row.ratingBucket, row.durationBucket}
}

func rowContainer(row browseRow) int {
	if row.item.Valid {
		return 0
	}
	return 1
}

func adjustBrowseBucketAxis(ctx context.Context, tx *sql.Tx, row browseRow, axis int, value string, delta int) error {
	container := rowContainer(row)
	if delta > 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_buckets(library_id,kind,container,axis,value,rating_key,label_key,total) VALUES(?,?,?,?,?,?,?,1) ON CONFLICT(library_id,kind,container,axis,value,rating_key,label_key) DO UPDATE SET total=total+1`, row.library, row.kind, container, axis, value, row.ratingKey, row.labelKey)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_browse_buckets SET total=total-1 WHERE library_id=? AND kind=? AND container=? AND axis=? AND value=? AND rating_key=? AND label_key=?`, row.library, row.kind, container, axis, value, row.ratingKey, row.labelKey); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, browseBucketEmptyDelete, row.library, row.kind, container, axis, value, row.ratingKey, row.labelKey)
	return err
}
func adjustBrowseBuckets(ctx context.Context, tx *sql.Tx, row browseRow, delta int) error {
	for axis, value := range browseStaticAxes(row) {
		if err := adjustBrowseBucketAxis(ctx, tx, row, axis, value, delta); err != nil {
			return err
		}
	}
	return nil
}
func browseBucketScopeEqual(a, b browseRow) bool {
	return a.library == b.library && a.kind == b.kind && rowContainer(a) == rowContainer(b) && a.ratingKey == b.ratingKey && a.labelKey == b.labelKey
}
func moveBrowseBuckets(ctx context.Context, tx *sql.Tx, old, next browseRow) error {
	sameScope := browseBucketScopeEqual(old, next)
	before, after := browseStaticAxes(old), browseStaticAxes(next)
	for axis, value := range before {
		if sameScope && value == after[axis] {
			continue
		}
		if err := adjustBrowseBucketAxis(ctx, tx, old, axis, value, -1); err != nil {
			return err
		}
		if err := adjustBrowseBucketAxis(ctx, tx, next, axis, after[axis], 1); err != nil {
			return err
		}
	}
	return nil
}

func readCountedRow(ctx context.Context, tx *sql.Tx, id int64) (browseRow, error) {
	row := browseRow{entity: id}
	var container int
	err := tx.QueryRowContext(ctx, `SELECT library_id,kind,container,head,year,added_text,rating_key,label_key,duration_bucket,rating_bucket FROM catalog_browse_counted_rows WHERE entity_id=?`, id).Scan(&row.library, &row.kind, &container, &row.head, &row.year, &row.added, &row.ratingKey, &row.labelKey, &row.durationBucket, &row.ratingBucket)
	if err == nil && container == 0 {
		row.item = sql.NullInt64{Int64: id, Valid: true}
	}
	return row, err
}

// deriveBrowseRow brings one entity's browse row, its bucket and Home
// contributions and its metrics up to date (or removes them). Its sort facts
// are refreshed once, by refreshEntityMetrics; a removed row has none.
func deriveBrowseRowWithKeys(ctx context.Context, tx *sql.Tx, id int64, ratingKey, labelKey string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET sort_key=portico_sort_title(title,sort_title,metadata_language),sort_head=substr(portico_sort_title(title,sort_title,metadata_language),1,1) WHERE id=? AND sort_key IS NOT portico_sort_title(title,sort_title,metadata_language)`, id); err != nil {
		return err
	}
	old, err := readCountedRow(ctx, tx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	counted := err == nil
	row, err := readBrowseRow(ctx, tx, id, ratingKey, labelKey)
	if errors.Is(err, sql.ErrNoRows) {
		if counted {
			if err = adjustBrowseBuckets(ctx, tx, old, -1); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_browse_counted_rows WHERE entity_id=?`, id); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_browse_rows WHERE entity_id=?`, id); err != nil {
			return err
		}
		return deriveHomeBuckets(ctx, tx, id)
	}
	if err != nil {
		return err
	}
	if counted {
		// Retain metric buckets while changing only the static keys that moved.
		row.durationBucket, row.ratingBucket = old.durationBucket, old.ratingBucket
		if err = moveBrowseBuckets(ctx, tx, old, row); err != nil {
			return err
		}
	} else if err = adjustBrowseBuckets(ctx, tx, row, 1); err != nil {
		return err
	}

	var item any
	if row.item.Valid {
		item = row.item.Int64
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,added_text,item_id,rating_key,label_key,available,recent,backdrop)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(entity_id) DO UPDATE SET library_id=excluded.library_id,kind=excluded.kind,title=excluded.title,sort_key=excluded.sort_key,head=excluded.head,year=excluded.year,added_text=excluded.added_text,item_id=excluded.item_id,rating_key=excluded.rating_key,label_key=excluded.label_key,available=excluded.available,recent=excluded.recent,backdrop=excluded.backdrop
 WHERE (library_id,kind,title,sort_key,head,year,added_text,item_id,rating_key,label_key,available,recent,backdrop)
 IS NOT (excluded.library_id,excluded.kind,excluded.title,excluded.sort_key,excluded.head,excluded.year,excluded.added_text,excluded.item_id,excluded.rating_key,excluded.label_key,excluded.available,excluded.recent,excluded.backdrop)`,
		id, row.library, row.kind, row.title, row.sortKey, row.head, row.year, row.added, item, row.ratingKey, row.labelKey, row.available, row.recent, row.backdrop); err != nil {
		return err
	}
	if err = deriveRecentText(ctx, tx, row); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_browse_counted_rows(entity_id,library_id,kind,container,head,year,added_text,rating_key,label_key,duration_bucket,rating_bucket) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(entity_id) DO UPDATE SET library_id=excluded.library_id,kind=excluded.kind,container=excluded.container,head=excluded.head,year=excluded.year,added_text=excluded.added_text,rating_key=excluded.rating_key,label_key=excluded.label_key,duration_bucket=excluded.duration_bucket,rating_bucket=excluded.rating_bucket
 WHERE (library_id,kind,container,head,year,added_text,rating_key,label_key,duration_bucket,rating_bucket) IS NOT (excluded.library_id,excluded.kind,excluded.container,excluded.head,excluded.year,excluded.added_text,excluded.rating_key,excluded.label_key,excluded.duration_bucket,excluded.rating_bucket)`,
		id, row.library, row.kind, rowContainer(row), row.head, row.year, row.added, row.ratingKey, row.labelKey, row.durationBucket, row.ratingBucket); err != nil {
		return err
	}
	if err = refreshEntityMetrics(ctx, tx, id); err != nil {
		return err
	}
	return deriveHomeBuckets(ctx, tx, id)
}

// deriveBrowseRow refreshes an already accepted row's ordinary scalar facts.
// Restriction-source changes go through the queued raw reducer; the only
// synchronous product caller creates an artist (which has no leaf keys).
func deriveBrowseRow(ctx context.Context, tx *sql.Tx, id int64) error {
	var rating, label string
	err := tx.QueryRowContext(ctx, `SELECT rating_key,label_key FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&rating, &label)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return deriveBrowseRowWithKeys(ctx, tx, id, rating, label)
}

// DeriveBrowseRowTx derives one entity's browse row now, in the caller's
// transaction, for an owner's action whose next read must already find it: a
// container made by that action is otherwise unseen by every page until the
// derivation worker reaches it. The queued derivation still runs and finds the
// row current.
func DeriveBrowseRowTx(ctx context.Context, tx *sql.Tx, id int64) error {
	return deriveBrowseRowWithKeys(ctx, tx, id, "", "")
}

// recentWorkKinds are the containers a Recently Added shelf lists in place of
// their members; foldedKinds are those members.
var (
	recentWorkKinds = map[int64]bool{int64(Show): true, int64(Album): true, int64(Book): true}
	foldedKinds     = map[int64]bool{int64(Episode): true, int64(Track): true, int64(Part): true}
)

// deriveRecentText keeps an item's side of Recently Added. The item's newest
// available added time is its metric (fanned out to its show, album or book,
// whose recent_text is their members' maximum: refreshEntityMetrics); a
// standalone item is a work of its own and carries it as recent_text.
func deriveRecentText(ctx context.Context, tx *sql.Tx, row browseRow) error {
	if !row.item.Valid {
		if recentWorkKinds[row.kind] {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_browse_rows SET recent_text=NULL WHERE entity_id=? AND recent_text IS NOT NULL`, row.entity)
		return err
	}
	var added any
	if row.added.Valid && row.available == 1 && row.recent == 1 {
		added = row.added.String
	}
	own := added
	if foldedKinds[row.kind] {
		own = nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_browse_rows SET recent_text=? WHERE entity_id=? AND recent_text IS NOT ?`, own, row.entity, own); err != nil {
		return err
	}
	var changed bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM catalog_browse_item_metrics WHERE item_id=?1 AND added IS ?2)`, row.entity, added).Scan(&changed); err != nil || !changed {
		return err
	}
	return TouchTx(ctx, tx, DomainItemMetrics, row.entity)
}

// deriveHomeBuckets moves one row's Home bucket contribution.
func deriveHomeBuckets(ctx context.Context, tx *sql.Tx, id int64) error {
	var changed bool
	const same = `WITH next AS (SELECT library_id,kind,available,recent,backdrop,rating_key,label_key,added_text IS NOT NULL AS dated,EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=entity_id) AS retired FROM catalog_browse_rows WHERE entity_id=?1 AND item_id IS NOT NULL),old AS(SELECT library_id,kind,available,recent,backdrop,rating_key,label_key,dated,retired FROM catalog_home_counted_rows WHERE entity_id=?1)
 SELECT EXISTS(SELECT * FROM next EXCEPT SELECT * FROM old) OR EXISTS(SELECT * FROM old EXCEPT SELECT * FROM next)`
	if err := tx.QueryRowContext(ctx, same, id).Scan(&changed); err != nil || !changed {
		return err
	}
	const columns = `library_id,kind,available,recent,backdrop,rating_key,label_key,dated,retired`
	for _, q := range []string{
		`UPDATE catalog_home_buckets AS b SET total=total-1 FROM catalog_home_counted_rows old WHERE old.entity_id=?1 AND (b.library_id,b.kind,b.available,b.recent,b.backdrop,b.rating_key,b.label_key,b.dated,b.retired)=(old.library_id,old.kind,old.available,old.recent,old.backdrop,old.rating_key,old.label_key,old.dated,old.retired)`,
		`DELETE FROM catalog_home_buckets WHERE total=0 AND (` + columns + `) IN(SELECT ` + columns + ` FROM catalog_home_counted_rows WHERE entity_id=?1)`,
		`DELETE FROM catalog_home_counted_rows WHERE entity_id=?1`,
		`INSERT INTO catalog_home_counted_rows SELECT entity_id,library_id,kind,available,recent,backdrop,rating_key,label_key,added_text IS NOT NULL,EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=entity_id) FROM catalog_browse_rows WHERE entity_id=?1 AND item_id IS NOT NULL`,
		`INSERT INTO catalog_home_buckets SELECT ` + columns + `,1 FROM catalog_home_counted_rows WHERE entity_id=?1 ON CONFLICT DO UPDATE SET total=total+1`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

type browseEdge struct {
	entity int64
	source int
}

// touchContainers queues the containers' browse rows for a refresh.
func touchContainers(ctx context.Context, tx *sql.Tx, containers map[int64]bool) error {
	ordered := make([]int64, 0, len(containers))
	for id := range containers {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return TouchTx(ctx, tx, DomainContainerRefresh, ordered...)
}

// Metrics. An item's duration (its longest file) and rating (its best
// provider rating) are fanned out to every container it belongs to; a
// container's row shows the max over its members. The fanout is bounded per
// step and resumes from a cursor; the cursor restarts when the item is queued
// again (its catalog_dirty revision moved).

func metricCursor(ctx context.Context, tx *sql.Tx, domain int, k Key) (afterEntity int64, afterSource int, afterItem int64, err error) {
	var sequence, version int64
	err = tx.QueryRowContext(ctx, `SELECT sequence,projection_version,after_entity,after_source,after_item FROM catalog_browse_metric_cursors WHERE domain=? AND entity_id=?`, domain, k.ID).Scan(&sequence, &version, &afterEntity, &afterSource, &afterItem)
	if err == nil && sequence == k.Revision {
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	afterEntity, afterSource, afterItem = 0, -1, 0
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_browse_metric_cursors(domain,entity_id,sequence,projection_version,after_entity,after_source,after_item) VALUES(?,?,?,1,0,-1,0)
 ON CONFLICT(domain,entity_id) DO UPDATE SET sequence=excluded.sequence,after_entity=0,after_source=-1,after_item=0,after_item_started=0`, domain, k.ID, k.Revision)
	return
}

func finishMetricCursor(ctx context.Context, tx *sql.Tx, domain int, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_browse_metric_cursors WHERE domain=? AND entity_id=?`, domain, id)
	return err
}

// deriveAssetFanout queues the metrics of the items an asset backs.
func deriveAssetFanout(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, error) {
	_, _, afterItem, err := metricCursor(ctx, tx, DomainAssetMetrics, k)
	if err != nil {
		return false, err
	}
	var started bool
	if err = tx.QueryRowContext(ctx, `SELECT after_item_started FROM catalog_browse_metric_cursors WHERE domain=? AND entity_id=?`, DomainAssetMetrics, k.ID).Scan(&started); err != nil {
		return false, err
	}
	q := `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?`
	args := []any{k.ID}
	if started {
		q += ` AND entity_id>?`
		args = append(args, afterItem)
	}
	q += ` ORDER BY entity_id LIMIT ?`
	args = append(args, limit)
	items, err := scanIDs(ctx, tx, q, args...)
	if err != nil {
		return false, err
	}
	if err = TouchTx(ctx, tx, DomainItemMetrics, items...); err != nil {
		return false, err
	}
	if len(items) < limit {
		return true, finishMetricCursor(ctx, tx, DomainAssetMetrics, k.ID)
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_metric_cursors SET after_item=?,after_item_started=1 WHERE domain=? AND entity_id=?`, items[len(items)-1], DomainAssetMetrics, k.ID)
	return false, err
}

type metricEdge struct {
	entity int64
	source int
}

// metricEdges merges the item's recorded member metrics with its current
// memberships after the cursor: two indexed streams, each bounded by limit.
func metricEdges(ctx context.Context, tx *sql.Tx, item, afterEntity int64, afterSource, limit int) ([]metricEdge, error) {
	var all []metricEdge
	for _, query := range []string{
		`SELECT entity_id,source FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_item WHERE item_id=? AND (entity_id,source)>(?,?) ORDER BY entity_id,source LIMIT ?`,
		`SELECT entity_id,source FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=? AND (entity_id,source)>(?,?) ORDER BY entity_id,source LIMIT ?`,
	} {
		rows, err := tx.QueryContext(ctx, query, item, afterEntity, afterSource, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e metricEdge
			if err = rows.Scan(&e.entity, &e.source); err != nil {
				rows.Close()
				return nil, err
			}
			all = append(all, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].entity != all[j].entity {
			return all[i].entity < all[j].entity
		}
		return all[i].source < all[j].source
	})
	unique := all[:0]
	for _, e := range all {
		if len(unique) > 0 && unique[len(unique)-1] == e {
			continue
		}
		unique = append(unique, e)
		if len(unique) == limit {
			break
		}
	}
	return unique, nil
}

// metricEdgesPerUnit is the fewest containers one item's metrics reach per
// unit, whatever the batch size.
const metricEdgesPerUnit = 16

// deriveItemMetrics recomputes one item's metrics and fans them out to its
// containers, at most limit containers per step.
func browseDurationBucket(value sql.NullFloat64) string {
	if !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) {
		return ""
	}
	return strconv.FormatInt(int64(math.Trunc(value.Float64/1800))*30, 10)
}

func browseRatingBucket(value sql.NullFloat64) string {
	if !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) {
		return ""
	}
	return strconv.FormatInt(int64(math.Trunc(value.Float64)), 10)
}

// refreshEntityMetrics moves a row's metric buckets to its members' current
// maxima (two index starts, never a member scan).
func refreshEntityMetrics(ctx context.Context, tx *sql.Tx, entity int64) (errOut error) {
	defer func() {
		if errOut == nil {
			errOut = refreshBrowseSortFacts(ctx, tx, entity)
		}
	}()
	var library, kind int64
	var container int
	var ratingKey, labelKey, oldDuration, oldRating string
	err := tx.QueryRowContext(ctx, `SELECT library_id,kind,container,rating_key,label_key,duration_bucket,rating_bucket FROM catalog_browse_counted_rows WHERE entity_id=?`, entity).Scan(&library, &kind, &container, &ratingKey, &labelKey, &oldDuration, &oldRating)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var duration, rating sql.NullFloat64
	if err = tx.QueryRowContext(ctx, `SELECT duration FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_duration WHERE entity_id=? AND duration IS NOT NULL ORDER BY duration DESC LIMIT 1`, entity).Scan(&duration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT rating FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_rating WHERE entity_id=? AND rating IS NOT NULL ORDER BY rating DESC LIMIT 1`, entity).Scan(&rating); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	newDuration, newRating := browseDurationBucket(duration), browseRatingBucket(rating)
	for _, axis := range []struct {
		n         int
		old, next string
	}{{5, oldRating, newRating}, {6, oldDuration, newDuration}} {
		if axis.old == axis.next {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_browse_buckets SET total=total-1 WHERE library_id=? AND kind=? AND container=? AND axis=? AND value=? AND rating_key=? AND label_key=?`, library, kind, container, axis.n, axis.old, ratingKey, labelKey); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, browseBucketEmptyDelete, library, kind, container, axis.n, axis.old, ratingKey, labelKey); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_browse_buckets(library_id,kind,container,axis,value,rating_key,label_key,total) VALUES(?,?,?,?,?,?,?,1) ON CONFLICT(library_id,kind,container,axis,value,rating_key,label_key) DO UPDATE SET total=total+1`, library, kind, container, axis.n, axis.next, ratingKey, labelKey); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_browse_counted_rows SET duration_bucket=?,rating_bucket=? WHERE entity_id=? AND (duration_bucket,rating_bucket) IS NOT (?,?)`, newDuration, newRating, entity, newDuration, newRating); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_browse_rows SET duration_max=?,rating_max=? WHERE entity_id=? AND (duration_max,rating_max) IS NOT (?,?)`, duration, rating, entity, duration, rating); err != nil {
		return err
	}
	if !recentWorkKinds[kind] {
		return nil
	}
	var added sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT added FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_added WHERE entity_id=? AND added IS NOT NULL ORDER BY added DESC LIMIT 1`, entity).Scan(&added); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_rows SET recent_text=? WHERE entity_id=? AND recent_text IS NOT ?`, added, entity, added)
	return err
}

func init() {
	Register(DomainBrowseRows, Derivation{
		Version: 6,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			if len(keys) == 0 {
				return nil, nil
			}
			done, err := deriveBrowseRowBounded(ctx, tx, DomainBrowseRows, keys[0], limit)
			if err != nil {
				return nil, err
			}
			if done {
				return []int64{keys[0].ID}, nil
			}
			return nil, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
	Register(DomainBrowseEdges, Derivation{
		Version: 3,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			if len(keys) == 0 {
				return nil, nil
			}
			done, err := deriveBrowseMembersBounded(ctx, tx, keys[0], limit)
			if err != nil {
				return nil, err
			}
			if done {
				return []int64{keys[0].ID}, nil
			}
			return nil, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE (k.playable=1 OR k.id IN(20,21)) AND e.id>? ORDER BY e.id LIMIT ?`, after, limit)
		},
	})
	Register(DomainAssetMetrics, Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			var done []int64
			for _, k := range keys[:min(1, len(keys))] {
				finished, err := deriveAssetFanout(ctx, tx, k, min(limit, upstreamQuantum))
				if err != nil {
					return nil, err
				}
				if finished {
					done = append(done, k.ID)
				}
			}
			return done, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return nil, nil // item metrics rebuild from items
		},
	})
	Register(DomainItemMetrics, Derivation{
		Version: 4,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			touched := map[int64]bool{}
			var done []int64
			// An item's fanout is bounded per unit; a song's few containers
			// (album, its artist, credited artists, collections) fit in one.
			edges := max(1, min(limit, upstreamQuantum))
			for _, k := range keys[:min(1, len(keys))] {
				finished, err := deriveItemMetricsBounded(ctx, tx, k, edges, touched)
				if err != nil {
					return nil, err
				}
				if finished {
					done = append(done, k.ID)
				}
			}
			// An item's own row takes its metrics now; its containers are
			// queued, so each refreshes once however many batches its members
			// arrive in.
			ordered := make([]int64, 0, len(keys))
			for _, k := range keys[:min(1, len(keys))] {
				if touched[k.ID] {
					ordered = append(ordered, k.ID)
					delete(touched, k.ID)
				}
			}
			sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
			for _, id := range ordered {
				if err := refreshEntityMetrics(ctx, tx, id); err != nil {
					return nil, err
				}
			}
			return done, touchContainers(ctx, tx, touched)
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE (k.playable=1 OR k.id IN(20,21)) AND e.id>? ORDER BY e.id LIMIT ?`, after, limit)
		},
	})
	Register(DomainContainerRefresh, Derivation{
		Version: 2,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			if len(keys) == 0 {
				return nil, nil
			}
			pending, err := RepairBrowseCountersTx(ctx, tx, keys[0], limit)
			if err != nil || pending {
				return nil, err
			}
			done, err := deriveBrowseRowBounded(ctx, tx, DomainContainerRefresh, keys[0], limit)
			if err != nil {
				return nil, err
			}
			if done {
				return []int64{keys[0].ID}, nil
			}
			return nil, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return nil, nil // containers rebuild with every row (DomainBrowseRows)
		},
	})
}
