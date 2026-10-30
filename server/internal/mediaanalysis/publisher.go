// Package mediaanalysis persists measured source facts separately from selected
// item clock mappings. An observed publication remains local to its acquisition;
// a future playback must reacquire and validate, not promote this to a version.
package mediaanalysis

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/mediatimeline"
)

var ErrEvidence = errors.New("analysis evidence invalid")
var ErrConflict = errors.New("analysis publication identity conflict")
var ErrClock = errors.New("analysis has no selected finite clock")

func digest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func bounded(v string, n int) bool { return len(v) > 0 && len(v) <= n && utf8.ValidString(v) }

// Candidate is historical metadata for planning or schedule eligibility, never
// a source handle. It must be compared to a fresh authorized acquisition before
// use; observed rows cannot authorize a future independently opened file.
type Candidate struct {
	PublicationID, ItemID, AssetID, SourceKind, SourceID, SelectionDigest string
	StreamIndex                                                           int64
	Mapping                                                               mediatimeline.Mapping
}

func ReadCandidate(ctx context.Context, tx *sql.Tx, id, item string) (Candidate, error) {
	c := Candidate{PublicationID: id, ItemID: item}
	entity, err := entityid.Resolve(ctx, tx, item)
	if err != nil {
		return c, sql.ErrNoRows
	}
	err = tx.QueryRowContext(ctx, `SELECT m.asset_id,a.source_kind,a.source_id,m.selection_digest,m.stream_index,m.source_start_num,m.source_start_den,m.source_end_num,m.source_end_den,m.duration_us FROM media_analysis_item_mappings m JOIN media_analyses a USING(publication_id) WHERE m.publication_id=? AND m.item_id=?`, id, entity).Scan(&c.AssetID, &c.SourceKind, &c.SourceID, &c.SelectionDigest, &c.StreamIndex, &c.Mapping.SourceStart.Numerator, &c.Mapping.SourceStart.Denominator, &c.Mapping.SourceEnd.Numerator, &c.Mapping.SourceEnd.Denominator, &c.Mapping.DurationMicroseconds)
	return c, err
}
