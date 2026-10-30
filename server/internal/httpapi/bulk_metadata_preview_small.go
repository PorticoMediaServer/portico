package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// Retain the released P2-47b conversion allowance independently from the small
// aggregate's TOTAL additional allowance, through actual response delivery.
const bulkMetadataConversionBytes int64 = 8 << 20

// A qualified complete <=200 local-search conversion is an existing fast path,
// not a new foreground census. Keep its exact proof and lease; both its field
// response and general preparation now use the same capped aggregate semantics.
func (d Dependencies) smallBulkMetadataPreview(ctx context.Context, r *http.Request, in catalog.BulkMetadataPreviewRequest, p identity.Principal, expectedFence string, hold func(func()) bool) (out bulkMetadataPreviewResponse, handled bool, err error) {
	if in.Selector == nil || in.Selector.Query == nil {
		return out, false, nil
	}
	query := *in.Selector.Query
	var node *catalog.BrowseNode
	if len(query.Filter) > 0 {
		if err = json.Unmarshal(query.Filter, &node); err != nil {
			return out, true, err
		}
	}
	if !catalog.BrowseNeedsLocalSearch(node) {
		return out, false, nil
	}
	// The general legacy compiler cannot bound its local-search MATCH branch.
	// Only the existing complete, proof-bearing <=200 conversion is qualified;
	// an omitted proof must never fall through to foreground query replay.
	if query.SelectionBasis == "" {
		return out, true, catalog.ErrSelectionBatchUnsupported
	}
	handled = true
	revision := in.Expected.CatalogRevision
	var conversion catalog.BrowseSelectionConversion
	err = dbwork.WithReadSnapshot(ctx, d.DB, func(snapshot context.Context) error {
		request := r.WithContext(snapshot)
		libraries, fence, e := d.homeScopeFor(request, p)
		if e != nil {
			return e
		}
		if fence != expectedFence {
			return catalog.ErrStaleContinuation
		}
		viewer, e := d.catalogViewer(request, p, libraries, fence)
		if e != nil {
			return e
		}
		value, e := d.Catalog.WithContext(snapshot).ResolvePreparedBulkMetadataSelection(viewer, query, revision)
		if e != nil {
			return e
		}
		conversion = value
		if !hold(value.Release) {
			value.Release()
			return catalog.ErrStaleContinuation
		}
		if e = value.Reserve(bulkMetadataConversionBytes); e != nil {
			return e
		}
		raw, e := json.Marshal(value.IDs)
		if e != nil {
			return e
		}
		rows, e := dbwork.Snapshot(snapshot).QueryContext(snapshot, `SELECT k.name,pid(i.public_id),l.library_id FROM json_each(?) picked CROSS JOIN catalog_entities i ON i.id=picked.value JOIN catalog_kinds k ON k.id=i.kind JOIN catalog_libraries l ON l.id=i.library_id`, string(raw))
		if e != nil {
			return e
		}
		targets := make([]catalog.BulkMetadataTarget, 0, len(value.IDs))
		for rows.Next() {
			var t catalog.BulkMetadataTarget
			if e = rows.Scan(&t.ContentKind, &t.ID, &t.LibraryID); e != nil {
				break
			}
			t.Kind = "item"
			targets = append(targets, t)
		}
		if e == nil {
			e = rows.Err()
		}
		closeErr := rows.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		if len(targets) != len(value.IDs) {
			return catalog.ErrStaleContinuation
		}
		in = catalog.BulkMetadataPreviewRequest{Targets: targets}
		return nil
	})
	if errors.Is(err, catalog.ErrBulkMetadataLargeSelection) {
		return out, false, nil
	}
	if err != nil {
		return out, true, err
	}
	source := func(snapshot context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		request := r.WithContext(snapshot)
		libraries, fence, e := d.homeScopeFor(request, p)
		if e != nil {
			return catalog.BulkMetadataSelectionPage{}, e
		}
		if fence != expectedFence {
			return catalog.BulkMetadataSelectionPage{}, catalog.ErrStaleContinuation
		}
		viewer, e := d.catalogViewer(request, p, libraries, fence)
		if e != nil {
			return catalog.BulkMetadataSelectionPage{}, e
		}
		if e = d.Catalog.CheckBrowseSelectionBasisTx(snapshot, tx, viewer, query.LibraryID, query.SelectionBasis); e != nil {
			return catalog.BulkMetadataSelectionPage{}, e
		}
		return d.Catalog.BulkMetadataSelectionPageTx(snapshot, tx, viewer, in, after)
	}
	value, err := d.Metadata.BulkPreviewMemory(ctx, source, func(snapshot context.Context, tx *sql.Tx) error { return d.manualMetadataAuthorize(snapshot, tx, p) }, func(bytes int64) error {
		// Reserve is a TOTAL setter, not a delta. Keep the conversion charge.
		return conversion.Reserve(bulkMetadataConversionBytes + bytes)
	})
	if err != nil {
		return out, true, err
	}
	if _, err = d.manualMetadataAdministrator(ctx, r); err != nil {
		return out, true, err
	}
	_, fence, err := d.homeScopeFor(r, p)
	if err != nil {
		return out, true, err
	}
	if fence != expectedFence {
		return out, true, catalog.ErrStaleContinuation
	}
	// The original input was a whole-query preview, whose ready contract
	// has no explicit target census. Keep CAS data inside the verified reducer;
	// per-target edits still receive their complete ordered CAS targets.
	value.Targets = []metadata.BulkPreviewTarget{}
	value.ServerID, value.ViewerFence, value.SelectionRevision = d.Identity.ID(), fence, revision
	out.Ready = &value
	return out, true, nil
}
