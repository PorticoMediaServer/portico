package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// This is provenance, not a second catalog identity. In this baseline editions
// have no resolved catalog table. Keep the exact item/asset/part association; do
// not fabricate an edition, map another cut, or insert a generated library item.
type selection struct {
	Item, Asset, Library, Kind, Container, Path string
	Size, Modified                              int64
	Part                                        int
	Start                                       float64
	End                                         *float64
	Boundary                                    string
	Cue                                         bool `json:",omitempty"`
	AssetInc, ItemInc, AssociationInc, RootInc  string
	AssetRev, ItemRev, AssociationRev, RootRev  int64
	Physical, PhysicalInc, PhysicalRoot         string
	PhysicalRev, NetworkRevision                int64
}

func selectSource(ctx context.Context, tx *sql.Tx, item, asset string, requireAvailable bool) (selection, error) {
	var s selection
	var available bool
	e := tx.QueryRowContext(ctx, `SELECT pid(e.public_id),a.token,cl.library_id,CASE e.kind WHEN 1 THEN 'movie' WHEN 4 THEN 'episode' WHEN 7 THEN 'song' WHEN 9 THEN 'audiobook_file' WHEN 11 THEN 'extra' ELSE '' END,a.container,a.path,a.size,a.modified_ns,link.part_index,link.start_seconds,link.end_seconds,COALESCE(b.status,'whole_source'),
 ah.incarnation,ih.incarnation,lh.incarnation,r.incarnation,pr.revision,ih.revision,lh.revision,r.revision,
 COALESCE(n.revision,0),EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=e.id AND av.available=1)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id
 JOIN playback_origin_assets ah ON ah.id=a.token JOIN prepared_media_source_revisions pr ON pr.asset_id=a.token JOIN playback_origin_items ih ON ih.id=e.id
 JOIN playback_origin_associations lh ON lh.item_id=e.id AND lh.asset_id=a.token JOIN playback_origin_roots r ON r.id=cl.library_id
 LEFT JOIN episode_asset_boundaries b ON b.item_id=e.id AND b.asset_id=a.token LEFT JOIN library_network_policy n ON n.library_id=cl.library_id
 WHERE e.public_id=pid_blob(?) AND a.token=?`, item, asset).Scan(&s.Item, &s.Asset, &s.Library, &s.Kind, &s.Container, &s.Path, &s.Size, &s.Modified, &s.Part, &s.Start, &s.End, &s.Boundary, &s.AssetInc, &s.ItemInc, &s.AssociationInc, &s.RootInc, &s.AssetRev, &s.ItemRev, &s.AssociationRev, &s.RootRev, &s.NetworkRevision, &available)
	if e != nil {
		return s, e
	}
	var managed bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.asset_id=? AND src.library_id=?)`, asset, s.Library).Scan(&managed); e != nil {
		return s, e
	}
	if managed {
		e = tx.QueryRowContext(ctx, `SELECT src.id,src.incarnation,src.generation,src.root FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.asset_id=? AND src.library_id=? AND o.retired=0 AND o.root_incarnation=src.incarnation ORDER BY src.id,o.id LIMIT 1`, asset, s.Library).Scan(&s.Physical, &s.PhysicalInc, &s.PhysicalRev, &s.PhysicalRoot)
		if e != nil {
			return s, e
		}
	}
	if requireAvailable && !available {
		return s, ErrUnavailable
	}
	if s.Kind == "song" {
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM music_virtual_tracks v JOIN catalog_assets a ON a.token=v.asset_id JOIN catalog_entities e ON e.id=v.entity_id WHERE a.token=? AND e.public_id=pid_blob(?) AND v.start_seconds=? AND v.end_seconds=?)`, asset, item, s.Start, s.End).Scan(&s.Cue); e != nil {
			return s, e
		}
	}
	if !s.Cue && (s.Start != 0 || s.Boundary != "whole_source") {
		return s, ErrUnsupported
	}
	if s.Cue {
		if s.End == nil || s.Start < 0 || *s.End <= s.Start || math.IsNaN(*s.End) || math.IsInf(*s.End, 0) {
			return s, ErrUnsupported
		}
	}
	if s.End != nil && !s.Cue {
		var duration float64
		if e = tx.QueryRowContext(ctx, `SELECT duration FROM catalog_assets WHERE token=?`, asset).Scan(&duration); e != nil {
			return s, e
		}
		// P13 records the exact full sealed-media end explicitly. This is not
		// permission to infer a clip, a programme clock, or a different cut.
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || *s.End != duration {
			return s, ErrUnsupported
		}
	}
	single, e := DownloadSinglePartTx(ctx, tx, item)
	if e != nil {
		return s, e
	}
	if !single {
		return s, ErrUnsupported
	}
	return s, nil
}
func validateSelection(ctx context.Context, tx *sql.Tx, expected selection, available bool) error {
	current, e := selectSource(ctx, tx, expected.Item, expected.Asset, available)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrSourceChanged
	}
	if e != nil {
		return e
	}
	if hash(current) != hash(expected) {
		return ErrSourceChanged
	}
	return nil
}

const versionColumns = `v.id,COALESCE(pid(e.public_id),''),v.asset_id,v.profile_id,v.target_id,v.source_revision,v.part_index,v.edition_id,v.digest,v.size,v.facts_json,v.state,v.revision,v.created_ms`

const versionSource = `prepared_media_versions v LEFT JOIN catalog_entities e ON e.id=v.item_id`

func readVersion(row interface{ Scan(...any) error }) (Version, error) {
	var v Version
	var raw string
	e := row.Scan(&v.ID, &v.ItemID, &v.SourceID, &v.ProfileID, &v.TargetID, &v.SourceRevision, &v.PartIndex, &v.EditionID, &v.Digest, &v.Size, &raw, &v.State, &v.Revision, &v.CreatedMS)
	if e == nil {
		e = v.Facts.DecodeStored([]byte(raw))
	}
	return v, e
}

// VersionsTx is consumed by authorized server offers, not by public file routes.
func VersionsTx(ctx context.Context, tx *sql.Tx, item string) ([]Version, error) {
	out := []Version{}
	entity, e := entityid.Resolve(ctx, tx, item)
	if e != nil {
		return out, nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+` WHERE v.item_id=? AND v.state!='deleted' ORDER BY v.created_ms,v.id`, entity)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, err := readVersion(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for i := range out {
		v := &out[i]
		v.Reason = "pending_deletion"
		if v.State != "published" {
			continue
		}
		sel, err := selectSource(ctx, tx, item, v.SourceID, false)
		if err != nil || hash(sel) != v.SourceRevision {
			v.Reason = "source_changed"
			continue
		}
		single, err := DownloadSinglePartTx(ctx, tx, item)
		if err != nil {
			return out, err
		}
		if !single {
			v.Reason = "part_mapping_unavailable"
			continue
		}
		v.Selectable = true
		v.Reason = ""
	}
	return out, nil
}

// SelectTx fences admission to a specific prepared artifact. New readers cannot
// enter once deleting; existing pinned sessions may finish against closed bytes.
func SelectTx(ctx context.Context, tx *sql.Tx, item, id string) (Version, error) {
	vs, e := VersionsTx(ctx, tx, item)
	if e != nil {
		return Version{}, e
	}
	for _, v := range vs {
		if v.ID == id {
			if !v.Selectable {
				return v, ErrConflict
			}
			return v, nil
		}
	}
	return Version{}, ErrUnavailable
}

func SelectChoiceTx(ctx context.Context, tx *sql.Tx, item string, c *Choice) (Version, error) {
	if c == nil || !ValidChoice(c) {
		return Version{}, ErrInput
	}
	vs, e := VersionsTx(ctx, tx, item)
	if e != nil {
		return Version{}, e
	}
	if OffersRevision(item, vs) != c.OffersRevision {
		return Version{}, ErrConflict
	}
	for _, v := range vs {
		if v.ID == c.VersionID {
			if !v.Selectable || v.Revision != c.ExpectedRevision {
				return Version{}, ErrConflict
			}
			return v, nil
		}
	}
	return Version{}, ErrUnavailable
}

// sourceAudioChannels is the channel count of every audio track of the
// selected source, in stream order. A recipe version keeps or folds them per
// track, so the admission check needs the real values, not a profile guess.
func sourceAudioChannels(ctx context.Context, tx *sql.Tx, asset string) ([]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT channels FROM asset_streams WHERE asset_id=? AND type='audio' ORDER BY stream_index LIMIT 64`, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var channels int
		if err = rows.Scan(&channels); err != nil {
			return nil, err
		}
		out = append(out, channels)
	}
	return out, rows.Err()
}
