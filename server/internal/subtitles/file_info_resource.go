package subtitles

import (
	"context"
	"database/sql"
)

// The FileInfo head lanes need one offered subtitle resource at a time. They
// cannot call listTx: it lists a whole item's sources and resources, which is
// exactly the unbounded resident array the FileInfo surface forbids. These
// point helpers therefore re-use the same resourceColumns, scanResource and
// eligibility predicate as listTx, addressed by one numeric ROWID and one exact
// parent asset.
//
// The predicate is the exact listTx resource predicate with two deliberate
// differences that keep a FileInfo head stable:
//
//   - Visibility is only the resource owner fence (shared or the caller's
//     keyed actor). It is not an item-list traversal and invents no personal
//     approval.
//   - There is no library-source health/enabled join. listTx applies that to
//     the asset list it also exposes; a FileInfo head already admitted one
//     exact asset and must not flap a personal offer because inventory health
//     changed underneath it.
//
// The disc half is copied by reference from the same private constants listTx
// uses, so a raw default-disc resource is offered or refused exactly as the
// playback options query would. A disc-scoped row still needs the extractor's
// runtime capability; that is a composition fact, not a row fact, so the
// caller applies it when it offers or hydrates the exact row.
const fileInfoHeadResourceEligible = `r.item_id=? AND r.deleted=0 AND (r.scope='shared' OR r.owner=?)
 AND EXISTS(SELECT 1 FROM subtitle_revisions cv WHERE cv.resource_id=r.id AND cv.revision=r.current_revision AND cv.source_size=? AND cv.source_modified_ns=?)
 AND ` + discDefaultResource

// rowQueryer is the one-row read surface these point helpers need. Both *sql.DB
// (usually a dbwork read handle) and *sql.Tx satisfy it, so a caller can keep
// its admitted snapshot.
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// resourceEligibleScanner appends the point predicate's boolean to the ordinary
// resourceColumns scan without a second definition of the column order.
type resourceEligibleScanner struct {
	row      rowScanner
	eligible *bool
}

func (s resourceEligibleScanner) Scan(dest ...any) error {
	return s.row.Scan(append(dest, s.eligible)...)
}

// FileInfoHeadResource is the exact point projection for one offered subtitle
// row: the same Resource listTx would return, plus the disc binding that a
// point reader cannot otherwise see. A caller uses DiscScoped together with
// its configured extractor (and the storage guard for the parent asset) when it
// offers the hydrated row; the data predicate itself deliberately does not
// guess whether those runtime capabilities are installed.
type FileInfoHeadResource struct {
	Resource
	DiscScoped bool
}

// FileInfoHeadResourceRow hydrates exactly one raw subtitle resource row by its
// numeric ROWID for one exact parent asset, item and accepted source stat. It
// returns the same Resource shape listTx would for that row, including the
// current revision, render fields and deliveries. A row that is deleted, owned
// by another viewer, bound to another item or source, missing its current
// revision, carrying a different accepted source stat, or not the current
// default disc resource is sql.ErrNoRows: the caller's occurrence is stale, not
// an empty resource.
//
// resourceColumns carries every text field of the row (identifier, title,
// language, digest, attribution and source evidence), so a caller must reserve
// the bytes reported by FileInfoHeadResourceTextBytes before calling this.
func FileInfoHeadResourceRow(ctx context.Context, q rowQueryer, rowid, item, asset, size, modified int64, actor string) (FileInfoHeadResource, error) {
	if q == nil || rowid <= 0 || item <= 0 || asset <= 0 || actor == "" {
		return FileInfoHeadResource{}, ErrInput
	}
	var eligible bool
	r, err := scanResource(resourceEligibleScanner{
		row: q.QueryRowContext(ctx, `SELECT `+resourceColumns+`,(`+fileInfoHeadResourceEligible+`)
 FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision
 WHERE r.rowid=? AND r.source_id=(SELECT token FROM catalog_assets WHERE id=?)`,
			item, actor, size, modified, rowid, asset),
		eligible: &eligible,
	})
	if err != nil {
		return FileInfoHeadResource{}, err
	}
	if !eligible {
		return FileInfoHeadResource{}, sql.ErrNoRows
	}
	return FileInfoHeadResource{Resource: r, DiscScoped: r.discScoped}, nil
}

// FileInfoHeadResourceMatch decides the same point eligibility without reading
// the row's text. It is one primary-key point statement; the caller walks raw
// ROWIDs and applies this decision after its LIMIT, so a rejected row still
// consumes its lane event.
func FileInfoHeadResourceMatch(ctx context.Context, q rowQueryer, rowid, item, asset, size, modified int64, actor string) (bool, error) {
	if q == nil || rowid <= 0 || item <= 0 || asset <= 0 || actor == "" {
		return false, ErrInput
	}
	var eligible bool
	err := q.QueryRowContext(ctx, `SELECT (`+fileInfoHeadResourceEligible+`)
 FROM subtitle_resources r
 WHERE r.rowid=? AND r.source_id=(SELECT token FROM catalog_assets WHERE id=?)`,
		item, actor, size, modified, rowid, asset).Scan(&eligible)
	return eligible, err
}

// FileInfoHeadResourceTextBytes reports the total UTF-8 byte length of every
// text scalar FileInfoHeadResourceRow would scan for the same point row. It is
// the cost point the FileInfo head lane reserves before it loads a resource's
// identifier, title, language, digest or evidence, so a legal long scalar is
// charged instead of entering the payload for free. A row that is no longer
// offered is sql.ErrNoRows.
func FileInfoHeadResourceTextBytes(ctx context.Context, q rowQueryer, rowid, item, asset, size, modified int64, actor string) (int64, error) {
	if q == nil || rowid <= 0 || item <= 0 || asset <= 0 || actor == "" {
		return 0, ErrInput
	}
	var bytes int64
	err := q.QueryRowContext(ctx, `SELECT
 length(r.id)+length(r.source_id)+length(r.scope)+length(r.owner)+length(r.discovery_id)
 +length(v.language)+length(v.title)+length(v.format)+length(v.origin)
 +length(v.provider)+length(v.attribution)+length(v.rights)+length(v.digest)
 +length(COALESCE(rr.source_evidence,''))+length(COALESCE(rr.renderer,''))
 FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision
 LEFT JOIN subtitle_render_revisions rr ON rr.resource_id=r.id AND rr.revision=v.revision
 WHERE r.rowid=? AND r.source_id=(SELECT token FROM catalog_assets WHERE id=?) AND `+fileInfoHeadResourceEligible,
		rowid, asset, item, actor, size, modified).Scan(&bytes)
	return bytes, err
}
