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

// PeriodicalIssues reads dated issues from one accepted typed series in a complete ready generation. Its anchor
// names an indexed member, then verifies the full ordering tuple digest in this
// same authorized snapshot. The cursor never carries a truncated sort key.
func (s *Service) PeriodicalIssues(ctx context.Context, p identity.Principal, library, series, publicationType, cursor string, limit int) (SeriesPage, error) {
	return s.PeriodicalIssuesAt(ctx, p, library, series, publicationType, "", cursor, limit)
}

// PeriodicalIssuesAt optionally restricts membership to one actual accepted date.
// A month value is exact month precision, never an inferred day or date range.
func (s *Service) PeriodicalIssuesAt(ctx context.Context, p identity.Principal, library, series, publicationType, issueDate, cursor string, limit int) (SeriesPage, error) {
	page, err := s.PeriodicalIssuesInYear(ctx, p, library, series, publicationType, issueDate, "", cursor, limit)
	return page.SeriesPage, err
}

// Periodical issue membership publishes either actual order over the same total
// issue-date/item tuple, so one direction never repeats or skips a member.
const (
	PeriodicalIssuesNewest = "newest"
	PeriodicalIssuesOldest = "oldest"
)

type PeriodicalYear struct {
	Year       string
	IssueCount int64
}
type PeriodicalDay struct {
	Date       string
	IssueCount int64
}
type PeriodicalIssuePage struct {
	SeriesPage
	Years []PeriodicalYear
	Days  []PeriodicalDay
}

// Year buckets count the complete eligible series, before any date/year window.
func (s *Service) PeriodicalIssuesInYear(ctx context.Context, p identity.Principal, library, series, publicationType, issueDate, year, cursor string, limit int) (PeriodicalIssuePage, error) {
	return s.PeriodicalIssuesInMonth(ctx, p, library, series, publicationType, issueDate, year, "", PeriodicalIssuesNewest, cursor, limit)
}

// A newspaper month publishes actual available edition dates, not guessed days.
// The order is the requested actual direction; an empty value keeps the
// canonical newest-first order.
func (s *Service) PeriodicalIssuesInMonth(ctx context.Context, p identity.Principal, library, series, publicationType, issueDate, year, month, order, cursor string, limit int) (PeriodicalIssuePage, error) {
	out := PeriodicalIssuePage{SeriesPage: SeriesPage{Items: []LibraryRow{}}, Years: []PeriodicalYear{}, Days: []PeriodicalDay{}}
	if s == nil || s.DB == nil || s.Visible == nil || s.SeriesScope == nil {
		return out, identity.ErrForbidden
	}
	if publicationType != "magazine" && publicationType != "newspaper" {
		return out, ErrInvalid
	}
	if order == "" {
		order = PeriodicalIssuesNewest
	}
	if order != PeriodicalIssuesNewest && order != PeriodicalIssuesOldest {
		return out, ErrInvalid
	}
	if issueDate != "" && (!compactcatalog.ValidReadingIssueDate(issueDate) || publicationType == "newspaper" && len(issueDate) != 10) {
		return out, ErrInvalid
	}
	if year != "" && (len(year) != 4 || !compactcatalog.ValidReadingIssueDate(year+"-01") || issueDate != "" && issueDate[:4] != year) {
		return out, ErrInvalid
	}
	if month != "" && (publicationType != "newspaper" || len(month) != 7 || !compactcatalog.ValidReadingIssueDate(month) || year != "" && month[:4] != year || issueDate != "" && issueDate[:7] != month) {
		return out, ErrInvalid
	}
	if library == "" || len(library) > 128 || len(series) != 43 || limit < 1 || limit > 100 || len(cursor) > 4096 {
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
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT cl.id,lib.kind FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind='book'`, library).Scan(&handle, &kind); err != nil {
		return out, err
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return out, ErrChanged
	}
	if err != nil {
		return out, err
	}
	filter, visibleArgs, err := s.Visible(ctx, tx, p, "e.id")
	if err != nil {
		return out, err
	}
	if filter == "" {
		return out, identity.ErrForbidden
	}
	cursorDomain := "periodical-issues-v1:" + publicationType + ":order:" + order
	if issueDate != "" {
		cursorDomain += ":date:" + issueDate
	}
	if year != "" {
		cursorDomain += ":year:" + year
	}
	if month != "" {
		cursorDomain += ":month:" + month
	}
	digest, err := seriesScopeDigest(library, series, cursorDomain, scope, limit)
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
	base := ` FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
 WHERE m.library_id=? AND m.generation=? AND m.series_id=? AND e.retired=0 AND cl.retired=0
 AND lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)
 AND m.publication_type=? AND m.issue_date IS NOT NULL AND (?='magazine' OR length(m.issue_date)=10)
 AND (` + filter + `)`
	args := append([]any{handle, generation.Ready, series, publicationType, publicationType}, visibleArgs...)
	yearRows, err := tx.QueryContext(ctx, `SELECT substr(m.issue_date,1,4),count(*)`+base+` GROUP BY substr(m.issue_date,1,4) ORDER BY substr(m.issue_date,1,4) DESC LIMIT 10000`, args...)
	if err != nil {
		return out, err
	}
	for yearRows.Next() {
		var bucket PeriodicalYear
		if err = yearRows.Scan(&bucket.Year, &bucket.IssueCount); err != nil {
			yearRows.Close()
			return out, err
		}
		if len(out.Years) >= 9999 || len(bucket.Year) != 4 || !compactcatalog.ValidReadingIssueDate(bucket.Year+"-01") || bucket.IssueCount < 1 || bucket.IssueCount > 9007199254740991 {
			yearRows.Close()
			return out, ErrChanged
		}
		out.Years = append(out.Years, bucket)
	}
	err = yearRows.Err()
	yearRows.Close()
	if err != nil {
		return out, err
	}
	if month != "" {
		base += ` AND m.issue_date>=? AND m.issue_date<?`
		args = append(args, month+"-", month+".")
		dayRows, e := tx.QueryContext(ctx, `SELECT m.issue_date,count(*)`+base+` GROUP BY m.issue_date ORDER BY m.issue_date LIMIT 32`, args...)
		if e != nil {
			return out, e
		}
		for dayRows.Next() {
			var day PeriodicalDay
			if e = dayRows.Scan(&day.Date, &day.IssueCount); e != nil {
				dayRows.Close()
				return out, e
			}
			if len(out.Days) >= 31 || len(day.Date) != 10 || !compactcatalog.ValidReadingIssueDate(day.Date) || day.Date[:7] != month || day.IssueCount < 1 || day.IssueCount > 9007199254740991 {
				dayRows.Close()
				return out, ErrChanged
			}
			out.Days = append(out.Days, day)
		}
		e = dayRows.Err()
		dayRows.Close()
		if e != nil {
			return out, e
		}
	}
	if year != "" {
		base += ` AND m.issue_date>=? AND m.issue_date<?`
		args = append(args, year+"-", year+".")
	}
	if issueDate != "" {
		base += ` AND m.issue_date=?`
		args = append(args, issueDate)
	}
	var after *seriesTuple
	if cursor != "" {
		captured, e := decodeSeriesCursor(cursor, key, time.Now())
		if e != nil {
			return out, e
		}
		if captured.Namespace != current.Namespace || captured.Input != current.Input || captured.Ready != current.Ready || captured.Catalog != current.Catalog || captured.Viewer != current.Viewer || captured.Scope != current.Scope {
			return out, ErrChanged
		}
		anchor := seriesTuple{}
		anchorArgs := append(append([]any{}, args...), captured.Anchor)
		if err = tx.QueryRowContext(ctx, `SELECT 0,0,m.issue_date,m.item_id`+base+` AND m.item_id=?`, anchorArgs...).Scan(&anchor.Numbered, &anchor.Number, &anchor.Sort, &anchor.Item); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return out, ErrChanged
			}
			return out, err
		}
		tuple, e := anchor.digest()
		if e != nil || tuple != captured.Tuple {
			return out, ErrChanged
		}
		after = &anchor
		current.Expiry = captured.Expiry
	}
	seek, sortOrder := `(m.issue_date,m.item_id)<(?,?)`, `m.issue_date DESC,m.item_id DESC`
	if order == PeriodicalIssuesOldest {
		seek, sortOrder = `(m.issue_date,m.item_id)>(?,?)`, `m.issue_date ASC,m.item_id ASC`
	}
	query := `SELECT e.id,pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1),
 0,0,m.issue_date` + base
	if after != nil {
		query += ` AND ` + seek
		args = append(args, after.Sort, after.Item)
	}
	query += ` ORDER BY ` + sortOrder + ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	var tuples []seriesTuple
	for rows.Next() {
		var item LibraryRow
		var tuple seriesTuple
		if err = rows.Scan(&item.entity, &item.ID, &item.LibraryID, &item.Title, &item.Format, &item.SourceID, &item.InventoryAvailable, &tuple.Numbered, &tuple.Number, &tuple.Sort); err != nil {
			rows.Close()
			return out, err
		}
		if !compactcatalog.ValidReadingIssueDate(tuple.Sort) || publicationType == "newspaper" && len(tuple.Sort) != 10 {
			rows.Close()
			return out, ErrChanged
		}
		item.IssueDate = tuple.Sort
		tuple.Item = item.entity
		if _, err = tuple.digest(); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, item)
		tuples = append(tuples, tuple)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) == 0 && cursor == "" && issueDate == "" && year == "" && month == "" {
		return out, ErrUnavailable
	}
	out.cursorAt = seriesCursorAt(current, key, tuples)
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		if out.NextCursor, err = out.cursorAt(limit - 1); err != nil {
			return out, err
		}
	}
	if err = readingAttributionProjectionTx(ctx, tx, out.Items); err != nil {
		return PeriodicalIssuePage{}, err
	}
	if !time.Unix(current.Expiry, 0).After(time.Now()) {
		return PeriodicalIssuePage{}, ErrChanged
	}
	return out, nil
}
