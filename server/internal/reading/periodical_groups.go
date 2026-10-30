package reading

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type PeriodicalGroup struct {
	ID, Name   string
	IssueCount int64
	Latest     LibraryRow
}
type PeriodicalGroupsPage struct {
	Groups     []PeriodicalGroup
	NextCursor string
	cursorAt   func(int) (string, error)
}

// Truncate keeps the first n groups and continues after the last one kept.
func (p *PeriodicalGroupsPage) Truncate(n int) error {
	return truncatePage(&p.Groups, &p.NextCursor, p.cursorAt, n)
}

// PeriodicalGroups counts the entire current visible typed+dated membership
// before selecting groups or their latest issue. A series alone is insufficient.
func (s *Service) PeriodicalGroups(ctx context.Context, p identity.Principal, library, publicationType, cursor string, limit int) (PeriodicalGroupsPage, error) {
	out := PeriodicalGroupsPage{Groups: []PeriodicalGroup{}}
	if s == nil || s.DB == nil || s.Visible == nil || s.SeriesScope == nil {
		return out, identity.ErrForbidden
	}
	if library == "" || len(library) > 128 || limit < 1 || limit > 100 || len(cursor) > 4096 || (publicationType != "magazine" && publicationType != "newspaper") {
		return out, ErrInvalid
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer g.Rollback()
	tx := g.Tx()
	scope, err := s.SeriesScope(ctx, tx, p, library)
	if err != nil {
		return out, err
	}
	var handle int64
	if err = tx.QueryRowContext(ctx, `SELECT cl.id FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind='book'`, library).Scan(&handle); err != nil {
		return out, err
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return out, ErrChanged
	}
	if err != nil {
		return out, err
	}
	visible, visibleArgs, err := s.Visible(ctx, tx, p, "e.id")
	if err != nil {
		return out, err
	}
	if visible == "" {
		return out, identity.ErrForbidden
	}
	digest, err := seriesScopeDigest(library, "", "periodical-groups-v1:"+publicationType, scope, limit)
	if err != nil {
		return out, err
	}
	var encoded string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='catalog_cursor_key'`).Scan(&encoded); err != nil {
		return out, err
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return out, ErrChanged
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
		tuple, e := (seriesTuple{Sort: anchor.Key + "\x00" + anchor.ID, Item: captured.Anchor}).digest()
		if e != nil || tuple != captured.Tuple {
			return out, ErrChanged
		}
		after = &anchor.Key
		current.Expiry = captured.Expiry
	}
	groups, selected, err := s.periodicalGroupsTx(ctx, tx, handle, generation.Ready, publicationType, visible, visibleArgs, after, limit+1)
	if err != nil {
		return out, err
	}
	out.Groups = groups
	if len(out.Groups) > limit {
		out.Groups = out.Groups[:limit]
	}
	out.cursorAt = func(i int) (string, error) {
		next := current
		next.Anchor = groups[i].Latest.entity
		tuple, err := (seriesTuple{Sort: selected[i].Key + "\x00" + selected[i].ID, Item: next.Anchor}).digest()
		if err != nil {
			return "", err
		}
		next.Tuple = tuple
		return next.encode(key)
	}
	if len(groups) > limit {
		if out.NextCursor, err = out.cursorAt(limit - 1); err != nil {
			return out, err
		}
	}
	latest := make([]LibraryRow, len(out.Groups))
	for i := range out.Groups {
		latest[i] = out.Groups[i].Latest
	}
	if err = readingAttributionProjectionTx(ctx, tx, latest); err != nil {
		return out, err
	}
	for i := range out.Groups {
		out.Groups[i].Latest = latest[i]
	}
	if !time.Unix(current.Expiry, 0).After(time.Now()) {
		return PeriodicalGroupsPage{}, ErrChanged
	}
	return out, nil
}

// periodicalFrom is the eligible, visible membership of one publication type.
// series is the series condition and takes the third parameter.
func (s *Service) periodicalFrom(visible, series string) string {
	return ` FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
 WHERE m.library_id=? AND m.generation=? AND ` + series + ` AND m.publication_type=? AND m.issue_date IS NOT NULL
 AND (?='magazine' OR length(m.issue_date)=10) AND e.retired=0 AND cl.retired=0 AND lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `) AND (` + visible + `)`
}

// periodicalGroupsTx selects at most want groups after the given key, then
// counts, labels and finds the latest issue of the selected groups only, over
// their entire current visible typed and dated membership.
func (s *Service) periodicalGroupsTx(ctx context.Context, tx *sql.Tx, handle, generation int64, publicationType, visible string, visibleArgs []any, after *string, want int) ([]PeriodicalGroup, []seriesCandidate, error) {
	next, err := periodicalSeriesWalk(ctx, tx, handle, generation, publicationType, after)
	if err != nil {
		return nil, nil, err
	}
	present, done, err := presentSeries(ctx, tx, s.periodicalFrom(visible, "m.series_id=?"), handle, generation, append([]any{publicationType, publicationType}, visibleArgs...))
	if err != nil {
		return nil, nil, err
	}
	selected, err := selectSeries(next, present, want)
	done()
	if err != nil || len(selected) == 0 {
		return []PeriodicalGroup{}, selected, err
	}
	ids, err := seriesIDsJSON(selected)
	if err != nil {
		return nil, nil, err
	}
	query := `WITH eligible AS MATERIALIZED(
 SELECT m.series_id,m.series_key,m.series_value,m.series_source,m.issue_date,e.id AS entity,pid(e.public_id) AS public_id,cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1) AS available` + s.periodicalFrom(visible, "m.series_id IN(SELECT value FROM json_each(?))") + `),
 spellings AS(SELECT series_id,series_key,series_value,count(*) AS frequency,sum(series_source='owner') AS owners FROM eligible GROUP BY series_id,series_key,series_value),
 labels AS(SELECT series_id,series_key,series_value,row_number() OVER(PARTITION BY series_id ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,series_value COLLATE BINARY) AS label_rank FROM spellings),
 members AS(SELECT e.*,count(*) OVER(PARTITION BY series_id) AS issue_count,row_number() OVER(PARTITION BY series_id ORDER BY issue_date DESC,entity DESC) AS latest_rank FROM eligible e)
 SELECT l.series_id,l.series_key,l.series_value,m.issue_count,m.entity,m.public_id,m.library_id,m.title,m.format,m.token,m.available,m.issue_date
 FROM labels l JOIN members m ON m.series_id=l.series_id WHERE l.label_rank=1 AND m.latest_rank=1 ORDER BY l.series_key COLLATE BINARY,l.series_id`
	args := append([]any{handle, generation, ids, publicationType, publicationType}, visibleArgs...)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	groups := []PeriodicalGroup{}
	for rows.Next() {
		var group PeriodicalGroup
		var sort string
		item := &group.Latest
		if err = rows.Scan(&group.ID, &sort, &group.Name, &group.IssueCount, &item.entity, &item.ID, &item.LibraryID, &item.Title, &item.Format, &item.SourceID, &item.InventoryAvailable, &item.IssueDate); err != nil {
			break
		}
		at := len(groups)
		if at >= len(selected) || selected[at].ID != group.ID || selected[at].Key != sort || group.IssueCount < 1 || !compactcatalog.ValidReadingIssueDate(item.IssueDate) || publicationType == "newspaper" && len(item.IssueDate) != 10 {
			err = ErrChanged
			break
		}
		groups = append(groups, group)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err == nil && len(groups) != len(selected) {
		err = ErrChanged
	}
	if err != nil {
		return nil, nil, err
	}
	return groups, selected, nil
}
