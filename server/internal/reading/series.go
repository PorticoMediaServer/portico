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
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// SeriesScope is supplied by the current-authority HTTP composition, resolved
// in the caller's snapshot. None of these facts may come from a request body.
// Formats is the runtime admitted source-format domain, so a cursor computed by
// a build with a different domain is refused rather than silently reused.
type SeriesScope struct {
	Profile, Fence, RestrictionFence, Formats string
	Catalog, Viewer                           int64
}
type SeriesScopeResolver func(context.Context, *sql.Tx, identity.Principal, string) (SeriesScope, error)
type SeriesPage struct {
	Summary    *SeriesSummary
	Items      []LibraryRow
	NextCursor string
	cursorAt   func(int) (string, error)
}

// Truncate keeps the first n items and continues after the last one kept.
func (p *SeriesPage) Truncate(n int) error {
	return truncatePage(&p.Items, &p.NextCursor, p.cursorAt, n)
}

// seriesCursorAt continues after one page member, in the page's own scope.
func seriesCursorAt(current seriesCursor, key []byte, tuples []seriesTuple) func(int) (string, error) {
	return func(i int) (string, error) {
		next := current
		next.Anchor = tuples[i].Item
		tuple, err := tuples[i].digest()
		if err != nil {
			return "", err
		}
		next.Tuple = tuple
		return next.encode(key)
	}
}

// Series reads only a complete immutable attribution generation. Its anchor
// names an indexed member, then verifies the full ordering tuple digest in this
// same authorized snapshot. The cursor never carries a truncated sort key.
func (s *Service) Series(ctx context.Context, p identity.Principal, library, series, cursor string, limit int) (SeriesPage, error) {
	out := SeriesPage{Items: []LibraryRow{}}
	if s == nil || s.DB == nil || s.Visible == nil || s.SeriesScope == nil {
		return out, identity.ErrForbidden
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
	if err = tx.QueryRowContext(ctx, `SELECT cl.id,lib.kind FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind IN('book','comic')`, library).Scan(&handle, &kind); err != nil {
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
	digest, err := seriesScopeDigest(library, series, kind, scope, limit)
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
 AND ((lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)) OR (lib.kind='comic' AND e.kind=21 AND p.format IN(` + readingcap.ComicFormatsSQL() + `)))
 AND (` + filter + `)`
	args := append([]any{handle, generation.Ready, series}, visibleArgs...)
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
		if err = tx.QueryRowContext(ctx, `SELECT m.numbered_order,m.number_value,m.sort_key,m.item_id`+base+` AND m.item_id=?`, anchorArgs...).Scan(&anchor.Numbered, &anchor.Number, &anchor.Sort, &anchor.Item); err != nil {
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
	out.Summary, err = seriesSummaryTx(ctx, tx, scope.Profile, series, base, args)
	if err != nil {
		return out, err
	}
	query := `SELECT e.id,pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1),
 m.numbered_order,m.number_value,m.sort_key` + base
	if after != nil {
		query += ` AND (m.numbered_order,m.number_value,m.sort_key COLLATE BINARY,m.item_id)>(?,?,?,?)`
		args = append(args, after.Numbered, after.Number, after.Sort, after.Item)
	}
	query += ` ORDER BY m.numbered_order,m.number_value,m.sort_key COLLATE BINARY,m.item_id LIMIT ?`
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
	if len(out.Items) == 0 && cursor == "" {
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
		return SeriesPage{}, err
	}
	if !time.Unix(current.Expiry, 0).After(time.Now()) {
		return SeriesPage{}, ErrChanged
	}
	return out, nil
}
