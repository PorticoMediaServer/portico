package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// BuildInfoInput stages raw inventory evidence separately from offered-resource
// projection. The latter is published per holder after activation; no all-holder
// scan or resource mutation occurs in this accepting writer.
func BuildInfoInput(ctx context.Context, tx *sql.Tx, asset int64, token string, size, modified int64, f *Inventory) ([]compactcatalog.InfoSourceFamily, error) {
	if f == nil {
		return nil, nil
	}
	if len(f.Tracks) > MaxTracks {
		return nil, ErrCapacity
	}
	if f.Status != "known" {
		var oldSize, oldModified int64
		if err := tx.QueryRowContext(ctx, `SELECT size,modified_ns FROM asset_subtitle_facts WHERE asset_id=?`, token).Scan(&oldSize, &oldModified); err == nil && oldSize == size && oldModified == modified {
			return nil, nil
		}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	fingerprint := stable(string(raw))
	revision, changed, err := compactcatalog.NextInfoFactRevisionTx(ctx, tx, asset, token, 6, fingerprint, size, modified)
	if err != nil || !changed {
		return nil, err
	}
	boolean := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	// A sidecar has global path identity. Repeated tracks name one NEW source
	// row; the final observation wins exactly as Persist's sequential upserts do.
	var sidecars []Track
	positions := map[string]int{}
	for _, t := range f.Tracks {
		if t.Origin != "embedded" {
			if pos, ok := positions[t.Locator]; ok {
				sidecars[pos] = t
			} else {
				positions[t.Locator] = len(sidecars)
				sidecars = append(sidecars, t)
			}
		}
	}
	families := []compactcatalog.InfoSourceFamily{
		{Number: 6, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoSubtitleFactRow{InputOrdinal: i, AssetID: token, Revision: revision, Size: size, ModifiedNS: modified, OriginUS: f.OriginUS, TimingKnown: boolean(f.TimingKnown), Status: f.Status, Fingerprint: fingerprint}, nil
		}},
	}
	if len(sidecars) > 0 {
		families = append(families, compactcatalog.InfoSourceFamily{Number: 7, Count: int64(len(sidecars)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			t := sidecars[i]
			return compactcatalog.InfoSubtitleSidecarRow{InputOrdinal: i, ID: stable("sidecar:" + t.Locator), Path: t.Locator, Size: t.Size, ModifiedNS: t.ModifiedNS, Digest: t.Digest}, nil
		}})
	}
	families = append(families,
		compactcatalog.InfoSourceFamily{Number: 8, Count: int64(len(f.Tracks)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			t := f.Tracks[i]
			locator := t.Locator
			var sidecar *string
			if t.Origin == "embedded" {
				locator = strconv.Itoa(t.StreamIndex)
			} else {
				id := stable("sidecar:" + locator)
				sidecar = &id
			}
			return compactcatalog.InfoSubtitleRow{InputOrdinal: i, ID: stable(token + ":" + t.Origin + ":" + locator), AssetID: token, Origin: t.Origin, Locator: locator, StreamIndex: int64(t.StreamIndex), SidecarID: sidecar, Format: t.Format, Language: t.Language, Title: t.Title, IsDefault: boolean(t.Default), IsForced: boolean(t.Forced), Reason: t.Reason, HearingImpaired: boolean(t.HearingImpaired)}, nil
		}},
		compactcatalog.InfoSourceFamily{Number: 9, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoSubtitleEvidenceRow{InputOrdinal: i, AssetID: token, Revision: revision, Evidence: f.SourceEvidence}, nil
		}},
	)
	return families, nil
}
