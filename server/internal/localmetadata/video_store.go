package localmetadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// PersistVideo is called only inside the scanner's existing source/policy-fenced
// commit. Triggered work captures the same immutable local evidence, not raw XML.
func PersistVideo(ctx context.Context, tx *sql.Tx, library, asset string, f assets.Facts) error {
	if f.VideoMetadataStatus == "observed" && (f.LocalMetadataIssue == "" || f.LocalMetadataIssue == "video_nfo_fields_skipped") {
		// The scanner confirmed the complete sidecar set. Missing files release evidence.
		if _, e := tx.ExecContext(ctx, `DELETE FROM video_nfo_evidence WHERE library_id=? AND asset_id=?`, library, asset); e != nil {
			return e
		}
	}
	replaced := map[string]bool{}
	for _, v := range f.VideoMetadata {
		if replaced[v.Locator] {
			continue
		}
		replaced[v.Locator] = true
		if _, e := tx.ExecContext(ctx, `DELETE FROM video_nfo_evidence WHERE library_id=? AND asset_id=? AND locator=? AND digest<>?`, library, asset, v.Locator, v.Digest); e != nil {
			return e
		}
	}
	for _, v := range f.VideoMetadata {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		coordinate, e := json.Marshal([]*int{v.Season, v.Episode, v.Absolute})
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO video_nfo_evidence(library_id,asset_id,locator,kind,coordinate,digest,payload,source_size,source_modified,observed_at) VALUES(?,?,?,?,?,?,?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now')) ON CONFLICT(library_id,asset_id,locator,kind,coordinate) DO UPDATE SET digest=excluded.digest,payload=excluded.payload,source_size=excluded.source_size,source_modified=excluded.source_modified,observed_at=excluded.observed_at WHERE video_nfo_evidence.digest<>excluded.digest OR video_nfo_evidence.source_size<>excluded.source_size OR video_nfo_evidence.source_modified<>excluded.source_modified`, library, asset, v.Locator, v.Kind, string(coordinate), v.Digest, string(raw), f.ObservedSize, f.ObservedModifiedNS)
		if e != nil {
			return e
		}
	}
	if f.VideoMetadataStatus != "" {
		_, e := tx.ExecContext(ctx, `INSERT INTO video_nfo_status(library_id,asset_id,status,issue) VALUES(?,?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET status=excluded.status,issue=excluded.issue`, library, asset, f.VideoMetadataStatus, f.LocalMetadataIssue)
		return e
	}
	return nil
}
