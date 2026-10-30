package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// RadioBatchRequest is server selection state, not a client-supplied history or
// a playback grant. The queue supplies the actual previous occurrence and its
// bounded lookahead exclusions; the profile's history is always read here.
type RadioBatchRequest struct {
	Seed, LastItem string
	Exclude        []string
	Cycle          int64
	Limit          int
	Start          bool
	Now            time.Time
}
type RadioBatch struct {
	IDs     []string
	BySound bool
}
type radioCandidate struct {
	id       int64
	public   string
	artists  []int64
	sound    bool
	score    float64
	related  int
	genres   int
	recent   int64
	excluded bool
	tie      uint64
}

const radioSeedMembershipSQL = `((?=7 AND item.id=?) OR (?=6 AND song.album_id=?) OR (?=5 AND (album.artist_id=? OR EXISTS(SELECT 1 FROM catalog_song_artists credit WHERE credit.song_id=item.id AND credit.artist_id=?))))`

const radioArtistsSQL = `CASE WHEN EXISTS(SELECT 1 FROM catalog_song_artists credit WHERE credit.song_id=item.id)
 THEN (SELECT json_group_array(artist_id) FROM catalog_song_artists credit WHERE credit.song_id=item.id)
 ELSE json_array(album.artist_id) END`

func radioOverlap(a, b []int64) bool {
	for _, one := range a {
		for _, two := range b {
			if one == two {
				return true
			}
		}
	}
	return false
}

// MusicRadioBatch reads local facts in one snapshot. It ranks exact cosine
// scores and uses the existing genre/co-listening selector when sound is not
// available. Hidden songs/history never enter the center, candidate pool or
// artist exclusions. The queue must still reauthorize every actual playback.
func (s *Service) MusicRadioBatch(viewer Viewer, request RadioBatchRequest) (RadioBatch, error) {
	out := RadioBatch{IDs: []string{}}
	actor, ok := viewerFromPersonalKey(viewer.Profile)
	if !ok || actor.Authority == "" || actor.AccountID == "" || actor.ProfileID == "" || request.Cycle < 0 || len(request.Exclude) > 500 {
		return out, ErrAdminQuery
	}
	if _, ok = entityid.Decode(request.Seed); !ok {
		return out, sql.ErrNoRows
	}
	if request.LastItem != "" {
		if _, ok = entityid.Decode(request.LastItem); !ok {
			return out, ErrAdminQuery
		}
	}
	excluded := map[string]bool{request.Seed: true}
	for _, id := range request.Exclude {
		if _, ok = entityid.Decode(id); !ok {
			return out, ErrAdminQuery
		}
		excluded[id] = true
	}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).MusicRadioBatch(viewer, request)
			return err
		})
		return out, err
	}
	if request.Now.IsZero() {
		request.Now = s.recommendationNow(time.Time{})
	}
	limit := min(pageLimit(request.Limit), 200)
	var seed int64
	var kind int
	var library string
	err := s.read().QueryRow(`SELECT item.id,item.kind,library.library_id FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music' WHERE item.public_id=pid_blob(?) AND item.retired=0 AND library.retired=0`, request.Seed).Scan(&seed, &kind, &library)
	if err != nil {
		return out, err
	}
	if !viewer.AllowsLibrary(library) || (kind != 5 && kind != 6 && kind != 7) {
		return out, sql.ErrNoRows
	}
	// The existing selector supplies the same authorized genre/co-listening
	// fallback, including its contributor-sharing threshold and visibility.
	fallback, err := s.ListeningSimilar(viewer, request.Seed, 24)
	if err != nil {
		return out, err
	}
	related := map[string]int{}
	for i, item := range fallback.Entries {
		related[item.ID] = len(fallback.Entries) - i
	}
	var center sonic.Embedding
	hasCenter := false
	if err = s.sonicGroups(viewer, kind, seed, func(group sonicGroup) { center = group.vector; hasCenter = true }); err != nil {
		return out, err
	}
	visible, args := viewer.itemVisibilitySQL("item.id")
	// Derive genre evidence from this seed's permitted, available leaf songs.
	// ListeningSimilar's co-listening pool is library-local; radio also ranges
	// across other authorized Music libraries, so it must compare their real
	// facets directly rather than falling through to an arbitrary tie order.
	genres := []string{}
	rows, err := s.read().Query(`SELECT facet.facet FROM catalog_entities item
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 JOIN catalog_rec_facets facet ON facet.entity_id=item.id AND facet.facet LIKE 'g:%'
 WHERE `+radioSeedMembershipSQL+`
 AND `+visible+` AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 GROUP BY facet.facet ORDER BY count(*) DESC,facet.facet LIMIT 8`, append([]any{kind, seed, kind, seed, kind, seed, seed}, args...)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var genre string
		if err = rows.Scan(&genre); err != nil {
			break
		}
		genres = append(genres, genre)
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	recent := map[int64]int64{}
	lastArtists := []int64{}
	rows, err = s.read().Query(`SELECT item.id,h.started_ms,`+radioArtistsSQL+` FROM play_history h INDEXED BY play_history_listen_profile JOIN catalog_entities item ON item.id=h.item_id JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id WHERE h.authority=? AND h.account_id=? AND h.profile_id=? AND h.kind=7 AND h.watched_ms>0 AND h.started_ms>=? AND h.started_ms<=? AND `+visible+` ORDER BY h.started_ms DESC,h.id DESC LIMIT 256`, append([]any{actor.Authority, actor.AccountID, actor.ProfileID, request.Now.Add(-24 * time.Hour).UnixMilli(), request.Now.UnixMilli()}, args...)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, at int64
		var raw string
		if err = rows.Scan(&id, &at, &raw); err != nil {
			break
		}
		if recent[id] < at {
			recent[id] = at
		}
		if len(lastArtists) == 0 {
			if err = json.Unmarshal([]byte(raw), &lastArtists); err != nil {
				break
			}
		}
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if request.LastItem != "" {
		var raw string
		err = s.read().QueryRow(`SELECT `+radioArtistsSQL+` FROM catalog_entities item JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id WHERE item.public_id=pid_blob(?) AND `+visible, append([]any{request.LastItem}, args...)...).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &lastArtists); err != nil {
				return out, err
			}
		}
	}
	rows, err = s.read().Query(`SELECT item.id,pid(item.public_id),pid(album_item.public_id),pid(artist.public_id),`+radioArtistsSQL+`,(SELECT count(*) FROM catalog_rec_facets facet WHERE facet.entity_id=item.id AND facet.facet IN(SELECT value FROM json_each(?))),CASE WHEN ? AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=library.library_id),?)=1 THEN `+currentSonicVectorSQL+` END
 FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id
 JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 JOIN catalog_entities album_item ON album_item.id=album.entity_id AND album_item.retired=0
 JOIN catalog_entities artist ON artist.id=album.artist_id AND artist.retired=0
 WHERE item.kind=7 AND `+visible+` AND `+currentSonicEligibleSQL+`
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
	 ORDER BY item.id`, append([]any{idsJSON(genres), hasCenter, SonicDefaultEnabled(), sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256}, args...)...)
	if err != nil {
		return out, err
	}
	pool := []radioCandidate{}
	for rows.Next() {
		var item radioCandidate
		var album, artist, raw string
		var data []byte
		if err = rows.Scan(&item.id, &item.public, &album, &artist, &raw, &item.genres, &data); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &item.artists); err != nil {
			break
		}
		item.related = max(related[album], related[artist])
		item.recent = recent[item.id]
		item.excluded = excluded[item.public]
		if hasCenter {
			if vector, e := sonic.FromBytes(data); e == nil {
				if item.score, e = sonic.Cosine(center, vector); e == nil {
					item.sound = true
				}
			}
		}
		hash := sha256.Sum256([]byte(viewer.Profile + "\x00" + request.Seed + "\x00" + strconv.FormatInt(request.Cycle, 10) + "\x00" + item.public))
		item.tie = binary.LittleEndian.Uint64(hash[:8])
		pool = append(pool, item)
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	sort.Slice(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.excluded != b.excluded {
			return !a.excluded
		}
		if (a.public == request.LastItem) != (b.public == request.LastItem) {
			return a.public != request.LastItem
		}
		if (a.recent > 0) != (b.recent > 0) {
			return a.recent == 0
		}
		if a.recent != b.recent {
			return a.recent < b.recent
		}
		if a.sound != b.sound {
			return a.sound
		}
		if a.sound && a.score != b.score {
			return a.score > b.score
		}
		if a.genres != b.genres {
			return a.genres > b.genres
		}
		if a.related != b.related {
			return a.related > b.related
		}
		if a.tie != b.tie {
			return a.tie < b.tie
		}
		return a.id < b.id
	})
	if request.Start {
		// A song radio starts with the actual seed; an album/artist starts
		// with its first permitted available song in canonical release order.
		var first int64
		err = s.read().QueryRow(`SELECT item.id FROM catalog_entities item
 JOIN catalog_libraries library ON library.id=item.library_id
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 JOIN catalog_entities album_item ON album_item.id=album.entity_id AND album_item.retired=0
 WHERE `+radioSeedMembershipSQL+`
 AND `+visible+` AND `+currentSonicEligibleSQL+`
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 ORDER BY album_item.year,album_item.sort_key,album_item.id,COALESCE(song.disc_number,1),COALESCE(song.track_number,0),item.id LIMIT 1`, append([]any{kind, seed, kind, seed, kind, seed, seed}, args...)...).Scan(&first)
		if err != nil {
			return out, err
		}
		for i, item := range pool {
			if item.id == first {
				out.IDs = append(out.IDs, item.public)
				out.BySound = item.sound
				lastArtists = item.artists
				pool = append(pool[:i], pool[i+1:]...)
				break
			}
		}
		if len(out.IDs) == 0 {
			return out, sql.ErrNoRows
		}
	}
	for len(out.IDs) < limit && len(pool) > 0 {
		chosen := 0
		// Prefer a different actually credited artist when one remains. With
		// a one-artist/one-song library, relaxing this is the only way to keep
		// an authorized radio going; no unavailable placeholder is invented.
		for i, item := range pool {
			// Artist variety must not revive a recently played or excluded
			// occurrence while a fresh, unexcluded song remains. Relax the
			// artist constraint within the best available freshness tier first.
			if item.excluded != pool[0].excluded || (item.recent > 0) != (pool[0].recent > 0) {
				continue
			}
			if !radioOverlap(lastArtists, item.artists) {
				chosen = i
				break
			}
		}
		item := pool[chosen]
		out.IDs = append(out.IDs, item.public)
		out.BySound = out.BySound || item.sound
		lastArtists = item.artists
		pool = append(pool[:chosen], pool[chosen+1:]...)
	}
	// A one-song library still has an honest next occurrence. Keeping one
	// buffered occurrence lets normal completion/prepare policy see it; no
	// invented song or second active session is needed.
	if request.Start && len(out.IDs) == 1 && limit > 1 {
		out.IDs = append(out.IDs, out.IDs[0])
	}
	return out, nil
}

// MusicRadioBatchTx keeps source selection and the caller's current permission
// facts on the same admitted read transaction used to build a v1 snapshot.
func (s *Service) MusicRadioBatchTx(ctx context.Context, tx *sql.Tx, viewer Viewer, request RadioBatchRequest) (RadioBatch, error) {
	if tx == nil {
		return RadioBatch{}, ErrAdminQuery
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	return clone.MusicRadioBatch(viewer, request)
}
