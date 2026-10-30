package catalog

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// separateArtistLimit bounds the albums one "Not this artist" moves.
const separateArtistLimit = 100

// SeparateArtist is the owner's "these albums are not by this artist": the
// given albums, all filed under one artist, move to a new artist of the same
// name that provider identity never joins with another again (the owner can
// still merge it, and rename it). Their songs' credits follow them. Nothing on
// disk changes and everyone's state stays where it is. At least one album must
// stay with the artist they leave. It returns the new artist's public id.
func (s *Service) SeparateArtist(ctx context.Context, albums []string, authorize func(*sql.Tx) error) (string, error) {
	if authorize == nil || len(albums) == 0 || len(albums) > separateArtistLimit {
		return "", ErrAdminQuery
	}
	var public string
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		if err := authorize(tx); err != nil {
			return err
		}
		var artist, handle int64
		chosen := map[int64]bool{}
		var ids []int64
		for _, album := range albums {
			id, err := entityid.Resolve(ctx, tx, album)
			if err != nil {
				return err
			}
			if chosen[id] {
				continue
			}
			var kind compactcatalog.Kind
			var library, owner int64
			if err = tx.QueryRowContext(ctx, `SELECT e.kind,e.library_id,a.artist_id FROM catalog_entities e JOIN catalog_albums a ON a.entity_id=e.id WHERE e.id=? AND e.retired=0`, id).Scan(&kind, &library, &owner); err != nil {
				return err
			}
			if kind != compactcatalog.Album || owner == 0 || artist != 0 && (owner != artist || library != handle) {
				return ErrAdminQuery
			}
			artist, handle = owner, library
			chosen[id] = true
			ids = append(ids, id)
		}
		// Something must stay with the artist, or there is nothing to tell apart.
		var stays bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_albums a INDEXED BY catalog_albums_artist JOIN catalog_entities e ON e.id=a.entity_id AND e.retired=0
 WHERE a.artist_id=? AND a.entity_id NOT IN(SELECT value FROM json_each(?)))`, artist, int64sJSON(ids)).Scan(&stays); err != nil {
			return err
		}
		if !stays {
			return ErrAdminQuery
		}
		var name, localKey, library string
		if err := tx.QueryRowContext(ctx, `SELECT e.title,r.local_key,cl.library_id FROM catalog_entities e JOIN catalog_artists r ON r.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? AND cl.retired=0`, artist).Scan(&name, &localKey, &library); err != nil {
			return err
		}
		first, err := entityid.Public(ctx, tx, ids[0])
		if err != nil {
			return err
		}
		separate, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey(localKey) + "#separate:" + first, Title: name})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, separate, map[string]any{"local_key": localKey + "#separate"}); err != nil {
			return err
		}
		if err = KeepApartTx(ctx, tx, separate); err != nil {
			return err
		}
		for _, album := range ids {
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, separate, album, separate); err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, album, map[string]any{"artist_id": separate}); err != nil {
				return err
			}
			// The album's songs credit the new artist where they credited the old.
			songs, err := scanInt64s(tx.QueryContext(ctx, `SELECT s.entity_id FROM catalog_songs s JOIN catalog_song_artists c ON c.song_id=s.entity_id AND c.artist_id=? WHERE s.album_id=? ORDER BY s.entity_id LIMIT ?`, artist, album, containerMergeLimit+1))
			if err != nil {
				return err
			}
			if len(songs) > containerMergeLimit {
				return ErrMergeTooLarge
			}
			for _, song := range songs {
				credited, err := scanInt64s(tx.QueryContext(ctx, `SELECT artist_id FROM catalog_song_artists WHERE song_id=? ORDER BY artist_id`, song))
				if err != nil {
					return err
				}
				next := make([]int64, 0, len(credited))
				for _, a := range credited {
					if a == artist {
						a = separate
					}
					next = append(next, a)
				}
				if err = compactcatalog.SetSongArtistsTx(ctx, tx, song, next); err != nil {
					return err
				}
				if err = compactcatalog.TouchEntityTx(ctx, tx, song); err != nil {
					return err
				}
			}
			if err = compactcatalog.TouchEntityTx(ctx, tx, album); err != nil {
				return err
			}
		}
		for _, id := range []int64{separate, artist} {
			if err = compactcatalog.TouchEntityTx(ctx, tx, id); err != nil {
				return err
			}
		}
		// The new artist's page opens next: it is visible from the first read,
		// not once the derivation worker has caught up.
		if err = compactcatalog.DeriveBrowseRowTx(ctx, tx, separate); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, library); err != nil {
			return err
		}
		public, err = entityid.Public(ctx, tx, separate)
		return err
	})
	return public, err
}

func int64sJSON(ids []int64) string {
	out := []byte{'['}
	for i, id := range ids {
		if i > 0 {
			out = append(out, ',')
		}
		out = strconv.AppendInt(out, id, 10)
	}
	return string(append(out, ']'))
}
