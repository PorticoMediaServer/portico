package reading

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// DiscoverSeries names a library-scoped accepted series, with exact visible
// membership/read counts and at most three canonical members for its cover fan.
// These facts confer neither source admission nor a person/work identity.
type DiscoverSeries struct {
	ID, LibraryID, Name string
	Count, ReadCount    int
	Preview             []LibraryRow
}

// discoverSeriesRow is one page of the Discover series row and its continuation.
type discoverSeriesRow struct {
	series   []DiscoverSeries
	next     string
	cursorAt func(int) (string, error)
}

func (s *Service) discoverSeriesTx(ctx context.Context, tx *sql.Tx, p identity.Principal, library, cursor string, limit int, visible string, visibleArgs []any) (discoverSeriesRow, error) {
	out := discoverSeriesRow{series: []DiscoverSeries{}}
	if s.SeriesScope == nil {
		return out, identity.ErrForbidden
	}
	scope, err := s.SeriesScope(ctx, tx, p, library)
	if err != nil {
		return out, err
	}
	var handle int64
	if err = tx.QueryRowContext(ctx, `SELECT cl.id FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind IN('book','comic')`, library).Scan(&handle); err != nil {
		return out, err
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return out, ErrChanged
	}
	if err != nil {
		return out, err
	}
	// The cursor binds the row and the viewer's scope, not its page size: a
	// shelf may continue with a different page than it opened with.
	digest, err := seriesScopeDigest(library, "", "discover-series-v1", scope, 20)
	if err != nil {
		return out, err
	}
	key, err := catalogCursorKeyTx(ctx, tx)
	if err != nil {
		return out, err
	}
	current := seriesCursor{Namespace: generation.Namespace, Input: generation.Input, Ready: generation.Ready, Catalog: scope.Catalog, Viewer: scope.Viewer, Expiry: time.Now().Add(30 * time.Minute).Unix(), Scope: digest}
	var after *string
	if cursor != "" {
		captured, e := decodeSeriesCursor(cursor, key, time.Now())
		if e != nil {
			return out, e
		}
		if captured.Namespace != current.Namespace || captured.Input != current.Input || captured.Ready != current.Ready || captured.Catalog != current.Catalog || captured.Viewer != current.Viewer || captured.Scope != current.Scope {
			return out, ErrChanged
		}
		anchor, e := seriesAnchorTx(ctx, tx, handle, generation.Ready, captured.Anchor)
		if e != nil {
			return out, e
		}
		if tuple, e := (seriesTuple{Sort: anchor.Key + "\x00" + anchor.ID, Item: captured.Anchor}).digest(); e != nil || tuple != captured.Tuple {
			return out, ErrChanged
		}
		after = &anchor.Key
		current.Expiry = captured.Expiry
	}
	from := func(series string) string {
		return ` FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
 WHERE m.library_id=? AND m.generation=? AND ` + series + ` AND e.retired=0 AND cl.retired=0
 AND ((lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)) OR (lib.kind='comic' AND e.kind=21 AND p.format IN(` + readingcap.ComicFormatsSQL() + `)))
 AND (` + visible + `)`
	}
	next, closeWalk, err := librarySeriesWalk(ctx, tx, handle, generation.Ready, after)
	if err != nil {
		return out, err
	}
	present, closePresent, err := presentSeries(ctx, tx, from("m.series_id=?"), handle, generation.Ready, visibleArgs)
	if err != nil {
		closeWalk()
		return out, err
	}
	selected, err := selectSeries(next, present, limit+1)
	closeWalk()
	closePresent()
	if err != nil || len(selected) == 0 {
		return out, err
	}
	ids, err := seriesIDsJSON(selected)
	if err != nil {
		return out, err
	}
	// Label choice uses owner spelling, then frequency and binary ties, over the
	// selected series' entire eligible membership. The optional mark is personal
	// state, so it is read only for a viewer this server lets use personal state at all.
	// A guest or grant context keeps the columns absent even when a personal key
	// exists; the aggregate read count keeps its own unchanged query.
	personal := identity.CanUsePersonalState(p)
	markReadSQL, markRevisionSQL := "NULL", "NULL"
	if personal {
		markReadSQL = "(SELECT wi.watched FROM personal_watched_intents wi WHERE wi.profile_id=? AND wi.item_id=e.id)"
		markRevisionSQL = "(SELECT revision FROM personal_items WHERE profile_id=? AND item_id=e.id)"
	}
	query := `WITH eligible AS MATERIALIZED(
 SELECT m.series_id,m.series_key,m.series_value,m.series_source,m.numbered_order,m.number_value,m.sort_key,
 e.id AS entity,pid(e.public_id) AS public_id,cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1) AS available,
 ` + personalstate.CompactSQL("?", "e.id") + ` AS read,
 ` + markReadSQL + ` AS mark_read,
 ` + markRevisionSQL + ` AS mark_revision` + from("m.series_id IN(SELECT value FROM json_each(?))") + `),
 spellings AS(SELECT series_id,series_key,series_value,count(*) AS frequency,sum(series_source='owner') AS owners FROM eligible GROUP BY series_id,series_key,series_value),
 labels AS(SELECT series_id,series_key,series_value,row_number() OVER(PARTITION BY series_id ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,series_value COLLATE BINARY) AS label_rank FROM spellings),
 selected AS(SELECT labels.series_id,labels.series_key,labels.series_value,count(*) AS total,sum(e.read) AS read_count FROM labels JOIN eligible e ON e.series_id=labels.series_id WHERE labels.label_rank=1 GROUP BY labels.series_id,labels.series_key,labels.series_value),
 members AS(SELECT e.*,row_number() OVER(PARTITION BY e.series_id ORDER BY e.numbered_order,e.number_value,e.sort_key COLLATE BINARY,e.entity) AS member_rank FROM eligible e)
 SELECT s.series_id,s.series_key,s.series_value,s.total,s.read_count,m.entity,m.public_id,m.library_id,m.title,m.format,m.token,m.available,m.mark_read,m.mark_revision
 FROM selected s JOIN members m ON m.series_id=s.series_id AND m.member_rank<=3
 ORDER BY s.series_key COLLATE BINARY,s.series_id,m.member_rank`
	args := []any{scope.Profile}
	if personal {
		args = append(args, scope.Profile, scope.Profile)
	}
	args = append(args, handle, generation.Ready, ids)
	args = append(args, visibleArgs...)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	var preview []LibraryRow
	series := []DiscoverSeries{}
	for rows.Next() {
		var id, sortKey, name string
		var total, read int
		var item LibraryRow
		var markRead sql.NullBool
		var markRevision sql.NullInt64
		if err = rows.Scan(&id, &sortKey, &name, &total, &read, &item.entity, &item.ID, &item.LibraryID, &item.Title, &item.Format, &item.SourceID, &item.InventoryAvailable, &markRead, &markRevision); err != nil {
			break
		}
		// A mark needs both its explicit intent and the revision that carries it.
		item.MarkRead, item.MarkRevision, item.MarkKnown = markRead.Bool, markRevision.Int64, markRead.Valid && markRevision.Valid
		if total < 1 || read < 0 || read > total || item.LibraryID != library {
			err = ErrChanged
			break
		}
		if len(series) == 0 || series[len(series)-1].ID != id {
			if at := len(series); at >= len(selected) || selected[at].ID != id || selected[at].Key != sortKey {
				err = ErrChanged
				break
			}
			series = append(series, DiscoverSeries{ID: id, LibraryID: library, Name: name, Count: total, ReadCount: read, Preview: []LibraryRow{}})
		}
		series[len(series)-1].Preview = append(series[len(series)-1].Preview, item)
		preview = append(preview, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err == nil && len(series) != len(selected) {
		err = ErrChanged
	}
	if err != nil {
		return out, err
	}
	// This is metadata-only projection for the bounded, already visible preview.
	if err = readingAttributionProjectionTx(ctx, tx, preview); err != nil {
		return out, err
	}
	at := 0
	for i := range series {
		for j := range series[i].Preview {
			series[i].Preview[j] = preview[at]
			at++
		}
	}
	out.series = series
	out.cursorAt = func(i int) (string, error) {
		c := current
		c.Anchor = series[i].Preview[0].entity
		tuple, err := (seriesTuple{Sort: selected[i].Key + "\x00" + selected[i].ID, Item: c.Anchor}).digest()
		if err != nil {
			return "", err
		}
		c.Tuple = tuple
		return c.encode(key)
	}
	if len(series) > limit {
		out.series = series[:limit]
		if out.next, err = out.cursorAt(limit - 1); err != nil {
			return out, err
		}
	}
	return out, nil
}

func catalogCursorKeyTx(ctx context.Context, tx *sql.Tx) ([]byte, error) {
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='catalog_cursor_key'`).Scan(&encoded); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, ErrChanged
	}
	return key, nil
}
