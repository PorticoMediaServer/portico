package downloads

import (
	"context"
	"database/sql"
	"errors"
	"io"
)

// An original download is the library file itself. Nothing is copied: the file
// is read once, in place, to learn its SHA-256, and from then on it is served
// straight from the library for as long as it keeps the size and modified time
// it was read at. Those two facts are checked on the opened file, by the same
// confined opener playback uses, every time a transfer begins; a file that has
// changed ends the claim with source_changed instead of sending bytes the
// published digest no longer describes. The device still verifies the digest of
// what it received before it calls the file ready, so a file altered in place
// without a change of size or time is caught there rather than played.
//
// The digest belongs to the file, not to a viewer. It is remembered per asset
// with the size and time it was computed for, so the second person to download
// a film waits for nothing.

// pinned is the identity of the file a claim's digest describes.
type pinned struct {
	asset      string
	path       string
	size       int64
	modifiedNS int64
}

// knownDigest answers a file's remembered SHA-256 while the file still has the
// identity it was hashed at.
func knownDigest(ctx context.Context, tx *sql.Tx, f pinned) (string, bool, error) {
	var digest string
	e := tx.QueryRowContext(ctx, `SELECT sha256 FROM download_source_digests WHERE asset_id=? AND size=? AND modified_ns=?`, f.asset, f.size, f.modifiedNS).Scan(&digest)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	return digest, validDigest.MatchString(digest), nil
}

func rememberDigest(ctx context.Context, tx *sql.Tx, f pinned, digest string, now int64) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO download_source_digests(asset_id,size,modified_ns,sha256,hashed_ms) VALUES(?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,sha256=excluded.sha256,hashed_ms=excluded.hashed_ms`, f.asset, f.size, f.modifiedNS, digest, now)
	return e
}

// currentFile reads what the library holds today for the asset a claim pinned,
// as long as that asset is still a file of the claim's title.
func currentFile(ctx context.Context, tx *sql.Tx, item, asset string) (pinned, bool, error) {
	out := pinned{asset: asset}
	var available int
	e := tx.QueryRowContext(ctx, `SELECT a.path,a.size,a.modified_ns,link.available FROM catalog_entities e JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id WHERE e.public_id=pid_blob(?) AND a.token=?`, item, asset).Scan(&out.path, &out.size, &out.modifiedNS, &available)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	return out, e == nil && available != 0, e
}

// openSource opens the library file, refusing one whose size or modified time
// is not the pinned one. The reader stays on the file that was checked.
func (s *Service) openSource(ctx context.Context, f pinned) (io.ReadSeekCloser, error) {
	open := s.openOriginal
	if open == nil {
		open = directSource
	}
	return open(ctx, f.path, f.size, f.modifiedNS)
}
