package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// ViewerMarkers projects detected segments onto the viewer path. It is the only
// read of analysis markers that is not owner-only, so it applies rules the
// owner view does not:
//
//   - the caller's library authorization runs inside this transaction, exactly
//     as it does for the rest of the viewer's item reads;
//   - dismissed markers, owner-only kinds and weak guesses
//     (segmentmarkers.ViewerVisible) are never projected, and neither are
//     detections in a library whose detection the owner turned off;
//   - overlapping markers of one kind become the strongest one, so a chapter
//     title and a measured intro make one button;
//   - each projected marker carries the server's skip decision, so no client
//     has to interpret confidence, provenance or approval.
//
// Source selection, the episode boundary rule and the strm probe binding are the
// existing analysis authority, not a second one. A source whose clock analysis
// refuses to establish simply has no viewer markers.
func ViewerMarkers(ctx context.Context, db *sql.DB, a Access, sourceID string) (segmentmarkers.Set, error) {
	return viewerMarkers(ctx, db, a, sourceID, true)
}

// ViewerMarkerRevision reads the same source, marker and policy fence as the
// viewer projection without reading or composing the individual markers.
func ViewerMarkerRevision(ctx context.Context, db *sql.DB, a Access, sourceID string) (string, error) {
	out, err := viewerMarkers(ctx, db, a, sourceID, false)
	return out.Revision, err
}

func viewerMarkers(ctx context.Context, db *sql.DB, a Access, sourceID string, includeMarkers bool) (segmentmarkers.Set, error) {
	out := segmentmarkers.Set{Markers: []segmentmarkers.Marker{}}
	if db == nil {
		return out, ErrInput
	}
	gated, e := dbwork.BeginSnapshot(ctx, db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	out, e = viewerMarkersTx(ctx, tx, a, sourceID, includeMarkers)
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// ViewerMarkersTx runs inside a caller's transaction so item detail and playback
// offers can project markers under the same read they already authorized.
func ViewerMarkersTx(ctx context.Context, tx *sql.Tx, a Access, sourceID string) (segmentmarkers.Set, error) {
	return viewerMarkersTx(ctx, tx, a, sourceID, true)
}

func viewerMarkersTx(ctx context.Context, tx *sql.Tx, a Access, sourceID string, includeMarkers bool) (segmentmarkers.Set, error) {
	out := segmentmarkers.Set{Markers: []segmentmarkers.Marker{}}
	if tx == nil || a.Authorize == nil || a.ItemID == "" || a.AccountID == "" || a.ProfileID == "" || a.Authority == "" {
		return out, identity.ErrUnauthorized
	}
	if e := a.Authorize(tx); e != nil {
		return out, e
	}
	var library string
	if e := tx.QueryRowContext(ctx, `SELECT l.library_id FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?)`, a.ItemID).Scan(&library); e != nil {
		return out, e
	}
	// The authorization above is a library decision. An item that has moved to
	// another library is not the item the caller was authorized for.
	if a.LibraryID != "" && library != a.LibraryID {
		return out, identity.ErrUnauthorized
	}
	a.LibraryID = library
	// A server that has never installed analysis has no markers; that is an
	// answer, not a failure of the viewer's item read.
	installed, e := markersInstalledTx(ctx, tx)
	if e != nil || !installed {
		return out, e
	}
	v, e := resolveSource(ctx, tx, a, Target{SourceID: sourceID})
	if errors.Is(e, sql.ErrNoRows) || errors.Is(e, ErrClock) || errors.Is(e, ErrConflict) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if v.NeedsProbe {
		return out, nil
	}
	out.SourceID = v.ID
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM analysis_marker_sets WHERE object_id=? AND source_revision=?`, v.ObjectID, v.Revision).Scan(&revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	mode, e := SegmentLibraryModeTx(ctx, tx, a.LibraryID)
	if e != nil {
		return out, e
	}
	policy, e := segmentmarkers.ReadConfidencePolicy(ctx, tx)
	if e != nil {
		return out, e
	}
	// The fence is the played source, the marker revision (detection and
	// owner edits both move it) and whether the library's detection is on.
	out.Revision = token(v.ID, v.Revision, v.MappingRevision, strconv.FormatInt(revision, 10), strconv.FormatBool(mode == SegmentModeOff), strconv.Itoa(policy.ViewerPercent), strconv.Itoa(policy.AutomaticPercent), strconv.FormatBool(policy.UseChapterNames))
	if !includeMarkers {
		return out, nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,kind,start_us,end_us,confidence,provenance,approved FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0 AND end_us>? AND start_us<? ORDER BY start_us,id LIMIT 513`, v.ObjectID, v.Revision, v.SourceBinding, v.Start, v.End)
	if e != nil {
		return out, e
	}
	var candidates []segmentmarkers.Candidate
	for rows.Next() {
		var c segmentmarkers.Candidate
		var start, end int64
		var provenance string
		if e = rows.Scan(&c.ID, &c.Kind, &start, &end, &c.Confidence, &provenance, &c.Approved); e != nil {
			break
		}
		if !segmentmarkers.ViewerKind(c.Kind) || !policy.ViewerVisible(c.Approved, c.Confidence, provenance) {
			continue
		}
		// Turning a library's detection off takes its detections away from
		// viewers; markers an owner made or approved stay.
		if mode == SegmentModeOff && !c.Approved {
			continue
		}
		c.StartSeconds = float64(max(start, v.Start)-v.Start) / 1e6
		c.EndSeconds = float64(min(end, v.End)-v.Start) / 1e6
		c.AutomaticSafe = policy.AutomaticSafe(c.Approved, c.Confidence, provenance)
		candidates = append(candidates, c)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return segmentmarkers.Set{Markers: []segmentmarkers.Marker{}}, e
	}
	if len(candidates) > 512 {
		return segmentmarkers.Set{Markers: []segmentmarkers.Marker{}}, ErrBudget
	}
	out.Markers = segmentmarkers.Strongest(candidates)
	if len(out.Markers) > 512 {
		return segmentmarkers.Set{Markers: []segmentmarkers.Marker{}}, ErrBudget
	}
	return out, nil
}

// markersInstalledTx reports whether analysis storage exists in this database.
// mediaanalysis.New installs it; a composition without the analysis service
// never does.
func markersInstalledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var installed bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='analysis_markers')`).Scan(&installed)
	return installed, e
}
