package reading

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type ContinueRow struct {
	Conversion *MobiProvenance
	Item       LibraryRow
	Progress   Progress
}

type ContinuePage struct {
	Items      []ContinueRow
	NextCursor string
	positions  []continueCursor
}

// Truncate keeps the first n items and continues after the last one kept.
func (p *ContinuePage) Truncate(n int) error {
	return truncatePage(&p.Items, &p.NextCursor, func(i int) (string, error) { return p.positions[i].encode(), nil }, n)
}

type continueCursor struct{ observed, item, asset int64 }

func (c continueCursor) encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.observed, 10) + ":" + strconv.FormatInt(c.item, 10) + ":" + strconv.FormatInt(c.asset, 10)))
}

func parseContinueCursor(raw string) (continueCursor, error) {
	var out continueCursor
	if len(raw) > 128 {
		return out, ErrInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return out, ErrInvalid
	}
	parts := strings.Split(string(b), ":")
	if len(parts) != 3 {
		return out, ErrInvalid
	}
	for i, value := range []*int64{&out.observed, &out.item, &out.asset} {
		*value, err = strconv.ParseInt(parts[i], 10, 64)
		if err != nil || *value < 0 || i > 0 && *value == 0 {
			return continueCursor{}, ErrInvalid
		}
	}
	if out.encode() != raw {
		return continueCursor{}, ErrInvalid
	}
	return out, nil
}

// Continue projects the viewer's latest saved version of each unfinished
// Reading title. One current-authority snapshot filters before pagination;
// there is no session preparation, source open, provider call or AV history.
// The revision comparison is a catalogue observation, never a bytes grant.
func (s *Service) Continue(ctx context.Context, p identity.Principal, library, cursor string, limit int) (ContinuePage, error) {
	out := ContinuePage{Items: []ContinueRow{}}
	if s == nil || s.DB == nil || s.Visible == nil {
		return out, identity.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	var after continueCursor
	if cursor != "" {
		var err error
		if after, err = parseContinueCursor(cursor); err != nil {
			return out, err
		}
	}
	conversions := s.mobiSessionCandidates()
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer g.Rollback()
	filter, args, err := s.Visible(ctx, g.Tx(), p, "e.id")
	if err != nil {
		return out, err
	}
	if filter == "" {
		return out, identity.ErrForbidden
	}
	// Walk only this owner's recent-progress index. The two indexed absence
	// checks select its latest saved version and the same lowest eligible
	// inventory-object id as selectPublication, without a whole-library walk
	// or a window that sorts the owner's entire history before LIMIT.
	priorEligible := strings.NewReplacer("o.", "earlier.", "s.", "earlier_source.").Replace(publicationObjectEligible)
	query := `SELECT ` + publicationFields + `,pid(e.public_id),rp.source_revision,rp.format,rp.cfi,rp.section,rp.page_index,rp.progression,rp.revision,rp.observed_at
 FROM reading_progress rp CROSS JOIN catalog_entities e ON e.id=rp.item_id` + publicationJoins + `
 WHERE rp.owner_key=? AND rp.asset_id=p.asset_id AND rp.progression<1
 AND NOT EXISTS(SELECT 1 FROM reading_progress newer WHERE newer.owner_key=rp.owner_key AND newer.item_id=rp.item_id
 AND (newer.observed_at>rp.observed_at OR (newer.observed_at=rp.observed_at AND newer.asset_id>rp.asset_id)))
 AND (?='' OR cl.library_id=?) AND ` + publicationEligible + ` AND (p.format NOT IN('mobi','azw3') OR ?=1) AND (` + filter + `)
 AND NOT EXISTS(SELECT 1 FROM inventory_objects earlier INDEXED BY inventory_objects_asset
 JOIN library_sources earlier_source ON earlier_source.id=earlier.source_id AND earlier_source.library_id=cl.library_id
 WHERE earlier.asset_id=a.token AND earlier.id<o.id AND ` + priorEligible + `)`
	values := append([]any{identity.PersonalKey(p.Viewer), library, library, s.Mobi != nil}, args...)
	if cursor != "" {
		query += ` AND (rp.observed_at,rp.item_id,rp.asset_id)<(?,?,?)`
		values = append(values, after.observed, after.item, after.asset)
	}
	query += ` ORDER BY rp.observed_at DESC,rp.item_id DESC,rp.asset_id DESC LIMIT ?`
	values = append(values, limit+1)
	rows, err := g.Tx().QueryContext(ctx, query, values...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last continueCursor
	for rows.Next() {
		var selected Selection
		var progress Progress
		var cfi, section sql.NullString
		var page sql.NullInt64
		columns := append(selectionColumns(&selected), &selected.ItemID, &progress.SourceRevision, &progress.Location.Format, &cfi, &section, &page, &progress.Location.Progression, &progress.Revision, &progress.ObservedAt)
		if err = rows.Scan(columns...); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.NextCursor = last.encode()
			break
		}
		progress.SourceID = selected.token
		progress.Location.CFI, progress.Location.Section = cfi.String, section.String
		if page.Valid {
			progress.Location.PageIndex = &page.Int64
		}
		selected.SourceID = selected.token
		selected.Revision = selected.fence()
		presentation := selected
		var conversion *MobiProvenance
		if (selected.Format == "mobi" || selected.Format == "azw3") && progress.Location.Format == "epub" {
			if actual, ok := s.currentMobiProvenance(conversions, p, selected, progress.SourceRevision); ok {
				conversion = &actual
				presentation.Format = "epub"
				presentation.Revision = actual.Revision
			}
		}
		progress.Stale = progress.SourceRevision != presentation.Revision || progress.Location.Format != presentation.Format
		out.Items = append(out.Items, ContinueRow{Conversion: conversion, Item: LibraryRow{ID: selected.ItemID, LibraryID: selected.library, Title: selected.Title, Format: selected.Format, SourceID: selected.token, InventoryAvailable: true, entity: selected.Entity}, Progress: progress})
		last = continueCursor{progress.ObservedAt, selected.Entity, selected.Asset}
		out.positions = append(out.positions, last)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	items := make([]LibraryRow, len(out.Items))
	for i := range items {
		items[i] = out.Items[i].Item
	}
	if err = readingAttributionProjectionTx(ctx, g.Tx(), items); err != nil {
		return out, err
	}
	for i := range items {
		out.Items[i].Item = items[i]
	}
	return out, nil
}
