package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
)

// Marker is a chapter or a skippable span of a version.
type Marker struct {
	Kind          MarkerKind
	Source        MarkerSource
	StartMS       int64
	EndMS         int64
	Title         string
	Confidence    float64
	Provenance    string
	AutomaticSafe bool
}

// Project inside the same item snapshot, so playback and downloaded bundles
// receive the same visibility and automatic-skip decision. The write path has
// already validated these settings; defaults cover a server with none saved.
func projectSegmentMarkers(ctx context.Context, tx *sql.Tx, itemID int64, versions []Version) error {
	hasMarkers := false
	for _, v := range versions {
		for _, m := range v.Markers {
			hasMarkers = hasMarkers || m.Kind != MarkerChapter
		}
	}
	if !hasMarkers {
		return nil
	}
	var raw, choice string
	err := tx.QueryRowContext(ctx, `SELECT ifnull((SELECT value FROM settings WHERE key='segments'),'{}'),
 ifnull(json_extract(l.options,'$.segmentDetection'),'server') FROM items i JOIN libraries l ON l.id=i.library_id WHERE i.id=?`, itemID).Scan(&raw, &choice)
	if err != nil {
		return err
	}
	c := struct {
		Mode string `json:"mode"`
		segmentdetect.ConfidencePolicy
	}{Mode: "standard", ConfidencePolicy: segmentdetect.DefaultConfidencePolicy()}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	if choice != "" && choice != "server" {
		c.Mode = choice
	}
	for i := range versions {
		out := versions[i].Markers[:0]
		for _, m := range versions[i].Markers {
			if m.Kind == MarkerChapter {
				out = append(out, m)
				continue
			}
			if c.Mode == "off" {
				continue
			}
			// Earlier imported markers have no numeric observation. Preserve their
			// existing decision; do not invent a measured confidence for them.
			if m.Provenance == "" {
				m.AutomaticSafe = m.Source.Trusted()
			} else {
				approved := m.Source == MarkerFromPerson
				if !c.Visible(approved, m.Confidence, m.Provenance) {
					continue
				}
				m.AutomaticSafe = c.AutomaticSafe(approved, m.Confidence, m.Provenance)
			}
			out = append(out, m)
		}
		versions[i].Markers = out
	}
	return nil
}
