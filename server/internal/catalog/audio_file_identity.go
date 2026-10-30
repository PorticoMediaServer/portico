package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A tag change describes this physical copy. It cannot reidentify other
// versions of the old title when the new release evidence contradicts them.
func identifyAudioFileTx(ctx context.Context, tx *sql.Tx, item int64, key, kind string, file TrackFile, tags map[string]string) (int64, error) {
	if key == "" {
		return item, nil
	}
	keys, err := compactcatalog.IdentitiesTx(ctx, tx, item)
	if err != nil {
		return 0, err
	}
	known := false
	for _, old := range keys {
		if old == key {
			return item, restoreRetainedTx(ctx, tx, item)
		}
		known = known || strings.HasPrefix(old, kind+":") && !compatibleAudioKeys(old, key, kind, tags)
	}
	if known {
		var others bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=? AND asset_id<>?)`, item, file.Asset).Scan(&others); err != nil {
			return 0, err
		}
		if others {
			if err = compactcatalog.UnlinkAssetTx(ctx, tx, item, file.Asset); err != nil {
				return 0, err
			}
			return AttachTrackTx(ctx, tx, file)
		}
		// Corrected tags no longer authorize another file to join by the old name.
		for _, old := range keys {
			if strings.HasPrefix(old, kind+":") {
				if err = compactcatalog.DropIdentityTx(ctx, tx, item, old); err != nil {
					return 0, err
				}
			}
		}
	}
	return IdentifyTrackTx(ctx, tx, item, key)
}

// Keep the identity ledger in step with the album's enriched grouping key;
// the former weak key must not point silently at one of several known releases.
func recordAlbumGroupTx(ctx context.Context, tx *sql.Tx, album int64, key string) error {
	var old string
	if err := tx.QueryRowContext(ctx, `SELECT local_key FROM catalog_albums WHERE entity_id=?`, album).Scan(&old); err != nil {
		return err
	}
	if key == old {
		return nil
	}
	held, err := compactcatalog.AddIdentityTx(ctx, tx, album, compactcatalog.AlbumKey(key))
	if err != nil {
		return err
	}
	if held != 0 {
		return compactcatalog.ErrIdentityConflict
	}
	return compactcatalog.DropIdentityTx(ctx, tx, album, compactcatalog.AlbumKey(old))
}

// Incomplete release tags may be enriched; only contradictory evidence splits
// a physical copy away. The same explicit release ID is authoritative.
func compatibleAudioKeys(old, next, kind string, tags map[string]string) bool {
	var a, b []json.RawMessage
	if json.Unmarshal([]byte(strings.TrimPrefix(old, kind+":")), &a) != nil || json.Unmarshal([]byte(strings.TrimPrefix(next, kind+":")), &b) != nil || len(a) != 4 || len(b) != 4 {
		return false
	}
	for i := 1; i < 4; i++ {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	var left, right string
	if json.Unmarshal(a[0], &left) != nil || json.Unmarshal(b[0], &right) != nil {
		return false
	}
	if left == right {
		return true
	}
	if kind == "audiobook" {
		var x, y []string
		if json.Unmarshal([]byte(left), &x) != nil || json.Unmarshal([]byte(right), &y) != nil || len(x) != 6 || len(y) != 6 {
			return false
		}
		for i := range x {
			if x[i] != "" && y[i] != "" && x[i] != y[i] {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(left, "musicbrainz:") {
		return false
	}
	if strings.HasPrefix(right, "musicbrainz:") {
		copy := make(map[string]string, len(tags))
		for k, v := range tags {
			if k != "musicbrainz_albumid" {
				copy[k] = v
			}
		}
		artist := firstTag(copy, "album_artist", "albumartist", "artist", "author")
		year := 0
		value := firstTag(copy, "date", "year")
		if len(value) >= 4 {
			year, _ = strconv.Atoi(value[:4])
		}
		right = albumIdentityKey(copy, artist, firstTag(copy, "album"), year)
	}
	var x, y []json.RawMessage
	if json.Unmarshal([]byte(left), &x) != nil || json.Unmarshal([]byte(right), &y) != nil || len(x) != 4 || len(y) != 4 {
		return false
	}
	if string(x[0]) != string(y[0]) || string(x[1]) != string(y[1]) {
		return false
	}
	var yearX, yearY int
	var editionX, editionY string
	if json.Unmarshal(x[2], &yearX) != nil || json.Unmarshal(y[2], &yearY) != nil || json.Unmarshal(x[3], &editionX) != nil || json.Unmarshal(y[3], &editionY) != nil {
		return false
	}
	if yearX != 0 && yearY != 0 && yearX != yearY {
		return false
	}
	_, ok := mergeLocalEdition(editionX, editionY)
	return ok
}
