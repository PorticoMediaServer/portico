package reading

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// DiscoverLimits bounds one Discover response. Zero values take the default.
type DiscoverLimits struct {
	Recent int
	// Series opts into the ready, single-library series composition.
	Series     int
	ShortReads int
	NewIssues  int
	Newspaper  int
	// Row composes exactly one row, continued from Cursor when one is given;
	// every other row is absent. Its own limit field bounds the page.
	Row, Cursor string
}

// The Discover rows that continue. The newspaper row is one archive slot.
const (
	DiscoverRecent     = "recent"
	DiscoverSeriesRow  = "series"
	DiscoverShortReads = "short-reads"
	DiscoverNewIssues  = "new-issues"
)

// Discover composes real title rows and, when requested, exact series summaries
// from the same authorized ready projection. Classification and reading-length
// rows require their own real producers and are not inferred here.
type Discover struct {
	Recent     []LibraryRow
	Series     []DiscoverSeries
	ShortReads []ShortRead
	NewIssues  []LibraryRow
	Newspaper  *PeriodicalGroup
	// Next continues each row by key; an absent key means the row is complete.
	Next    map[string]string
	cursors map[string]func(int) (string, error)
}

// Truncate keeps the first n entries of one row and continues after the last.
func (d *Discover) Truncate(row string, n int) error {
	next := d.Next[row]
	var err error
	switch row {
	case DiscoverRecent:
		err = truncatePage(&d.Recent, &next, d.cursors[row], n)
	case DiscoverSeriesRow:
		err = truncatePage(&d.Series, &next, d.cursors[row], n)
	case DiscoverShortReads:
		err = truncatePage(&d.ShortReads, &next, d.cursors[row], n)
	case DiscoverNewIssues:
		err = truncatePage(&d.NewIssues, &next, d.cursors[row], n)
	default:
		return ErrInvalid
	}
	if err == nil && next != "" {
		d.Next[row] = next
	}
	return err
}

// Recently added rows continue after one (added, item) position. The cursor is
// a position only; every page reapplies the viewer's whole visibility predicate.
// A title with no addition time sorts last, at -1.
type addedCursor struct{ added, item int64 }

func (c addedCursor) encode() string {
	return strconv.FormatInt(c.added, 10) + "." + strconv.FormatInt(c.item, 10)
}

func parseAddedCursor(raw string) (addedCursor, error) {
	added, item, ok := strings.Cut(raw, ".")
	a, e1 := strconv.ParseInt(added, 10, 64)
	i, e2 := strconv.ParseInt(item, 10, 64)
	if !ok || e1 != nil || e2 != nil || a < -1 || i < 1 || strconv.FormatInt(a, 10) != added || strconv.FormatInt(i, 10) != item {
		return addedCursor{}, ErrInvalid
	}
	return addedCursor{a, i}, nil
}

// addedPage trims a limit+1 read to its page and records the row's continuation.
func addedPage[T any](d *Discover, row string, entries []T, limit int, position func(T) addedCursor) []T {
	d.cursors[row] = func(i int) (string, error) { return position(entries[i]).encode(), nil }
	if len(entries) > limit {
		entries = entries[:limit]
		d.Next[row] = position(entries[limit-1]).encode()
	}
	return entries
}

// Discover returns the bounded server-composed rows for one authorized library
// or for every library the viewer may read. It shares one snapshot and the same
// profile/library/restriction predicate as List.
func (s *Service) Discover(ctx context.Context, p identity.Principal, library string, limits DiscoverLimits) (Discover, error) {
	out := Discover{Recent: []LibraryRow{}, Next: map[string]string{}, cursors: map[string]func(int) (string, error){}}
	switch limits.Row {
	case "":
		if limits.Cursor != "" {
			return out, ErrInvalid
		}
	case DiscoverRecent, DiscoverSeriesRow, DiscoverShortReads, DiscoverNewIssues:
		// One row alone: only its own limit may be set.
		set := map[string]int{DiscoverRecent: limits.Recent, DiscoverSeriesRow: limits.Series, DiscoverShortReads: limits.ShortReads, DiscoverNewIssues: limits.NewIssues}
		for row, limit := range set {
			if row != limits.Row && limit != 0 || row == limits.Row && limit == 0 && row != DiscoverRecent {
				return out, ErrInvalid
			}
		}
		if limits.Newspaper != 0 || len(limits.Cursor) > 4096 {
			return out, ErrInvalid
		}
	default:
		return out, ErrInvalid
	}
	if limits.Newspaper < 0 || limits.Newspaper > 1 || limits.Newspaper > 0 && library == "" {
		return out, ErrInvalid
	}
	if limits.NewIssues < 0 || limits.NewIssues > 20 || limits.NewIssues > 0 && library == "" {
		return out, ErrInvalid
	}
	if limits.ShortReads < 0 || limits.ShortReads > 20 || limits.ShortReads > 0 && library == "" {
		return out, ErrInvalid
	}
	if limits.Series < 0 || limits.Series > 20 || limits.Series > 0 && library == "" {
		return out, ErrInvalid
	}
	if s.Visible == nil {
		return out, identity.ErrForbidden
	}
	limit := limits.Recent
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer g.Rollback()
	tx := g.Tx()
	filter, args, err := s.Visible(ctx, tx, p, "e.id")
	if err != nil {
		return out, err
	}
	if filter == "" {
		return out, identity.ErrForbidden
	}
	only := func(row string) bool { return limits.Row == "" || limits.Row == row }
	cursor := func(row string) string {
		if limits.Row == row {
			return limits.Cursor
		}
		return ""
	}
	if only(DiscoverRecent) {
		if out.Recent, err = s.discoverRecentTx(ctx, tx, library, cursor(DiscoverRecent), limit, filter, args); err != nil {
			return out, err
		}
		out.Recent = addedPage(&out, DiscoverRecent, out.Recent, limit, LibraryRow.position)
		if err = readingAttributionProjectionTx(ctx, tx, out.Recent); err != nil {
			return out, err
		}
	}
	if limits.Series > 0 && only(DiscoverSeriesRow) {
		var row discoverSeriesRow
		if row, err = s.discoverSeriesTx(ctx, tx, p, library, cursor(DiscoverSeriesRow), limits.Series, filter, args); err != nil {
			return out, err
		}
		out.Series, out.cursors[DiscoverSeriesRow] = row.series, row.cursorAt
		if row.next != "" {
			out.Next[DiscoverSeriesRow] = row.next
		}
	}
	if limits.ShortReads > 0 && only(DiscoverShortReads) {
		if out.ShortReads, err = s.discoverShortReadsTx(ctx, tx, library, cursor(DiscoverShortReads), limits.ShortReads, filter, args); err != nil {
			return out, err
		}
		out.ShortReads = addedPage(&out, DiscoverShortReads, out.ShortReads, limits.ShortReads, func(r ShortRead) addedCursor { return r.Item.position() })
	}
	if limits.NewIssues > 0 && only(DiscoverNewIssues) {
		if out.NewIssues, err = s.discoverNewIssuesTx(ctx, tx, p, library, cursor(DiscoverNewIssues), limits.NewIssues, filter, args); err != nil {
			return out, err
		}
		out.NewIssues = addedPage(&out, DiscoverNewIssues, out.NewIssues, limits.NewIssues, LibraryRow.position)
	}
	if limits.Newspaper > 0 {
		out.Newspaper, err = s.discoverNewspaperTx(ctx, tx, p, library, filter, args)
	}
	return out, err
}

func (r LibraryRow) position() addedCursor { return addedCursor{r.added, r.entity} }

// discoverRecentTx reads limit+1 recently added titles after an optional position.
func (s *Service) discoverRecentTx(ctx context.Context, tx *sql.Tx, library, cursor string, limit int, filter string, args []any) ([]LibraryRow, error) {
	out := []LibraryRow{}
	seek, values, err := addedSeek("e.added_ms", cursor)
	if err != nil {
		return out, err
	}
	query := `SELECT e.id,e.added_ms,pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id
 WHERE e.retired=0 AND cl.retired=0
 AND ((lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)) OR (lib.kind='comic' AND e.kind=21 AND p.format IN(` + readingcap.ComicFormatsSQL() + `)))
 AND e.added_ms IS NOT NULL AND (?='' OR cl.library_id=?) AND (` + filter + `)` + seek + `
 ORDER BY e.added_ms DESC,e.id DESC LIMIT ?`
	values = append(append([]any{library, library}, args...), values...)
	values = append(values, limit+1)
	rows, err := tx.QueryContext(ctx, query, values...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var row LibraryRow
		if err = rows.Scan(&row.entity, &row.added, &row.ID, &row.LibraryID, &row.Title, &row.Format, &row.SourceID, &row.InventoryAvailable); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, rows.Close()
}

// addedSeek continues strictly after a recently-added position, newest first.
// added is the row's ordering expression for its addition time.
func addedSeek(added, cursor string) (string, []any, error) {
	if cursor == "" {
		return "", nil, nil
	}
	after, err := parseAddedCursor(cursor)
	if err != nil {
		return "", nil, err
	}
	return ` AND (` + added + `,e.id)<(?,?)`, []any{after.added, after.item}, nil
}
