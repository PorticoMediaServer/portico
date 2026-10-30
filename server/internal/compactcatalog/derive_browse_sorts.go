package compactcatalog

import (
	"context"
	"database/sql"
	"hash/fnv"
)

// Sort facts are published with the browse row, and refreshed by the bounded
// item-to-container metric fanout when a member's facts change. Every fact is
// a seek: a container's availability reads its maintained member-link
// counters (catalog_browse_member_links), never its members.
func refreshBrowseSortFacts(ctx context.Context, tx *sql.Tx, entity int64) error {
	var public []byte
	if err := tx.QueryRowContext(ctx, `SELECT public_id FROM catalog_entities WHERE id=?`, entity).Scan(&public); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	hash := fnv.New32a()
	_, _ = hash.Write(public)
	_, err := tx.ExecContext(ctx, `WITH next AS MATERIALIZED (SELECT e.entity_id,
 COALESCE((SELECT strftime('%Y-%m-%dT%H:%M:%fZ',release_date) FROM catalog_item_details WHERE entity_id=e.entity_id),'') AS release_key,
 COALESCE((SELECT air_key FROM catalog_browse_member_metrics WHERE entity_id=e.entity_id ORDER BY air_key DESC LIMIT 1),'') AS latest_aired_key,
 COALESCE((SELECT critic FROM catalog_browse_member_metrics WHERE entity_id=e.entity_id ORDER BY critic DESC LIMIT 1),0) AS critic_key,
 COALESCE((SELECT total FROM catalog_browse_episode_counts WHERE entity_id=e.entity_id),0) AS episode_count,
 COALESCE((SELECT lower(a.sort_key) FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE s.entity_id=e.entity_id),'') AS album_key,
 COALESCE((SELECT lower(ar.sort_key) FROM catalog_songs s JOIN catalog_albums a ON a.entity_id=s.album_id JOIN catalog_entities ar ON ar.id=a.artist_id WHERE s.entity_id=e.entity_id),(SELECT lower(ar.sort_key) FROM catalog_albums a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.entity_id=e.entity_id),'') AS artist_key,
 COALESCE((SELECT COALESCE(disc_number,1)*1000000+COALESCE(track_number,0) FROM catalog_songs WHERE entity_id=e.entity_id),0) AS track_key,
 COALESCE((SELECT file_size FROM catalog_browse_member_metrics WHERE entity_id=e.entity_id ORDER BY file_size DESC LIMIT 1),0) AS file_size,
 COALESCE((SELECT resolution FROM catalog_browse_member_metrics WHERE entity_id=e.entity_id ORDER BY resolution DESC LIMIT 1),0) AS resolution_key,
 ? AS random_key,
 CASE WHEN COALESCE((SELECT available FROM catalog_browse_member_links WHERE entity_id=e.entity_id),0)<=0 THEN 'unavailable'
 WHEN COALESCE((SELECT unavailable FROM catalog_browse_member_links WHERE entity_id=e.entity_id),0)>0 THEN 'partial' ELSE 'available' END AS availability_state FROM catalog_browse_rows e WHERE e.entity_id=?)
 UPDATE catalog_browse_rows AS current SET (release_key,latest_aired_key,critic_key,episode_count,album_key,artist_key,track_key,file_size,resolution_key,random_key,availability_state)=(SELECT release_key,latest_aired_key,critic_key,episode_count,album_key,artist_key,track_key,file_size,resolution_key,random_key,availability_state FROM next)
 WHERE current.entity_id=(SELECT entity_id FROM next) AND (current.release_key,current.latest_aired_key,current.critic_key,current.episode_count,current.album_key,current.artist_key,current.track_key,current.file_size,current.resolution_key,current.random_key,current.availability_state) IS NOT (SELECT release_key,latest_aired_key,critic_key,episode_count,album_key,artist_key,track_key,file_size,resolution_key,random_key,availability_state FROM next)`, int64(hash.Sum32()), entity)
	return err
}

// An album or artist rename changes its members' sort keys. The existing
// durable fanout cursor bounds that work, including artists with many albums.
const DomainBrowseSortParents = 34

func deriveBrowseSortParent(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, error) {
	var after, revision int64
	err := tx.QueryRowContext(ctx, `SELECT after,revision FROM catalog_fanouts WHERE domain=? AND key=?`, DomainBrowseSortParents, k.ID).Scan(&after, &revision)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if revision != k.Revision {
		after = 0
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_fanouts(domain,key,revision,after) VALUES(?,?,?,?) ON CONFLICT(domain,key) DO UPDATE SET revision=excluded.revision,after=excluded.after`, DomainBrowseSortParents, k.ID, k.Revision, after); err != nil {
		return false, err
	}
	ids, err := scanIDs(ctx, tx, `WITH sort_albums AS(SELECT entity_id AS id FROM catalog_albums WHERE artist_id=?1 AND entity_id>?2 ORDER BY entity_id LIMIT ?3),members AS(SELECT item_id AS id FROM catalog_browse_memberships WHERE entity_id=?1 AND item_id>?2 GROUP BY item_id ORDER BY item_id LIMIT ?3) SELECT id FROM sort_albums UNION SELECT id FROM members ORDER BY id LIMIT ?3`, k.ID, after, limit)
	if err != nil {
		return false, err
	}
	if err = refreshBrowseSortFacts(ctx, tx, k.ID); err != nil {
		return false, err
	}
	for _, id := range ids {
		if err = refreshBrowseSortFacts(ctx, tx, id); err != nil {
			return false, err
		}
	}
	if len(ids) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_fanouts WHERE domain=? AND key=?`, DomainBrowseSortParents, k.ID)
		return true, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_fanouts SET after=? WHERE domain=? AND key=?`, ids[len(ids)-1], DomainBrowseSortParents, k.ID)
	return false, err
}

func init() {
	Register(DomainBrowseSortParents, Derivation{Version: 1, Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
		done := []int64{}
		for _, k := range keys {
			finished, err := deriveBrowseSortParent(ctx, tx, k, limit)
			if err != nil {
				return nil, err
			}
			if finished {
				done = append(done, k.ID)
			}
		}
		return done, nil
	}, Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
		// While every browse row is being rebuilt, each member's sort facts
		// are derived from its album and artist as they are now: walking
		// every album's and artist's members as well did all that work twice.
		var rows bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=? AND rebuilding=1)`, DomainBrowseRows).Scan(&rows); err != nil || rows {
			return nil, err
		}
		return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE id>? AND kind IN(5,6) ORDER BY id LIMIT ?`, after, limit)
	}})
}
