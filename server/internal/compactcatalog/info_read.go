package compactcatalog

import (
	"context"
	"database/sql"
)

type InfoQuery interface {
	ReadQuery
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// InfoAsset is the accepted informational scalar frame, not a byte grant.
// Physical presence, size/revision, source health and roots remain live checks
// at playback/destructive boundaries. Owner path disclosure is explicit.
type InfoAsset struct {
	ID, Epoch, Size, ModifiedNS       int64
	Token, Path                       string
	Container, VideoCodec, AudioCodec string
	Width, Height                     int64
	Duration                          float64
	OrderAvailable                    bool
}

type InfoSummary struct {
	Head      InfoHead
	Files     int64
	Versions  int64
	Available bool
	Duration  float64
}

// ReadInfoSummary uses two endpoint seeks and point counters. Unavailable links
// still contribute raw duration, with the original zero floor. It never
// aggregates all links or consults global dirty/rebuild state.
func ReadInfoSummary(ctx context.Context, q ReadQuery, item int64) (InfoSummary, error) {
	var out InfoSummary
	err := q.QueryRowContext(ctx, `SELECT e.info_desired_epoch,e.info_applied_epoch,e.info_source_blockers,e.info_availability_pending,e.info_blocked_children,
 e.asset_links,COALESCE((SELECT versions FROM catalog_item_version_counts WHERE item_id=e.id),0),
 COALESCE((SELECT available FROM catalog_asset_links INDEXED BY catalog_asset_links_available_seek WHERE entity_id=e.id AND available=1 LIMIT 1),0),
 COALESCE((SELECT info_duration FROM catalog_asset_links INDEXED BY catalog_info_duration WHERE entity_id=e.id ORDER BY info_duration DESC LIMIT 1),0)
 FROM catalog_entities e WHERE e.id=? AND e.retired=0`, item).
		Scan(&out.Head.Desired, &out.Head.Applied, &out.Head.SourceBlockers, &out.Head.AvailabilityPending, &out.Head.BlockedChildren,
			&out.Files, &out.Versions, &out.Available, &out.Duration)
	if err != nil {
		return InfoSummary{}, err
	}
	if !out.Head.Ready() {
		return out, ErrInfoPreparing
	}
	if out.Duration < 0 {
		out.Duration = 0
	}
	return out, nil
}

// ReadInfoAsset reads one exact lifetime. It overlays first OLD deltas while
// a source is pre-fencing, and queued OLD deltas after active NEW1 is accepted.
// It never reads a partial technical-family replacement as ready. Text remains
// transient: hydration adapters reserve ordinary driver/encoder capacity first,
// or take their explicit uncached single-O(B)-scalar path.
func ReadInfoAsset(ctx context.Context, q InfoQuery, asset int64, token string, ownerPath bool) (InfoAsset, error) {
	var a InfoAsset
	var materializing bool
	err := q.QueryRowContext(ctx, `SELECT a.id,a.info_epoch,a.token,CASE WHEN ? THEN a.path ELSE '' END,a.size,a.modified_ns,
 a.container,a.video_codec,a.audio_codec,a.width,a.height,a.duration,a.info_order_available,
 EXISTS(SELECT 1 FROM catalog_info_changes c WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.slot=0 AND c.phase IN(2,3))
 FROM catalog_assets a WHERE a.id=? AND a.token=?`, ownerPath, asset, token).
		Scan(&a.ID, &a.Epoch, &a.Token, &a.Path, &a.Size, &a.ModifiedNS, &a.Container, &a.VideoCodec, &a.AudioCodec, &a.Width, &a.Height, &a.Duration, &a.OrderAvailable, &materializing)
	if err != nil {
		return a, err
	}
	if materializing {
		return InfoAsset{}, ErrInfoPreparing
	}
	rows, err := q.QueryContext(ctx, `SELECT o.field,o.kind,o.old_integer,o.old_real,o.old_text
 FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id
 WHERE c.asset_id=? AND c.asset_token=? AND c.phase<3 ORDER BY c.slot,o.field`, asset, token)
	if err != nil {
		return InfoAsset{}, err
	}
	defer rows.Close()
	var seen [10]bool
	for rows.Next() {
		var field, kind int
		var integer sql.NullInt64
		var real sql.NullFloat64
		var text sql.NullString
		if err = rows.Scan(&field, &kind, &integer, &real, &text); err != nil {
			return InfoAsset{}, err
		}
		if field < 1 || field > 9 {
			return InfoAsset{}, ErrInfoInvariant
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		switch field {
		case 1:
			// Ordering always uses the accepted head, never raw OLD or NEW.
			if kind != 1 || !integer.Valid || (integer.Int64 != 0 && integer.Int64 != 1) {
				return InfoAsset{}, ErrInfoInvariant
			}
		case 2, 3, 7, 8:
			if kind != 1 || !integer.Valid {
				return InfoAsset{}, ErrInfoInvariant
			}
			switch field {
			case 2:
				a.Size = integer.Int64
			case 3:
				a.ModifiedNS = integer.Int64
			case 7:
				a.Width = integer.Int64
			case 8:
				a.Height = integer.Int64
			}
		case 4, 5, 6:
			if kind != 3 || !text.Valid {
				return InfoAsset{}, ErrInfoInvariant
			}
			switch field {
			case 4:
				a.Container = text.String
			case 5:
				a.VideoCodec = text.String
			case 6:
				a.AudioCodec = text.String
			}
		case 9:
			if kind != 2 || !real.Valid {
				return InfoAsset{}, ErrInfoInvariant
			}
			a.Duration = real.Float64
		}
	}
	if err = rows.Err(); err != nil {
		return InfoAsset{}, err
	}
	rows.Close()
	if ownerPath {
		var path sql.NullString
		if err = q.QueryRowContext(ctx, `SELECT o.old_path FROM catalog_info_changes c JOIN catalog_info_path_old o ON o.change_id=c.id
 WHERE c.asset_id=? AND c.asset_token=? AND c.phase<3 ORDER BY c.slot LIMIT 1`, asset, token).Scan(&path); err != nil && err != sql.ErrNoRows {
			return InfoAsset{}, err
		}
		if path.Valid {
			a.Path = path.String
		}
	}
	return a, nil
}
