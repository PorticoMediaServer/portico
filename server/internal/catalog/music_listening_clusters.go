package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// MusicListeningCluster is private selection evidence, not a display label or
// playback grant. Names come from actual credited artists; the original MTT
// head is not mislabeled as the different MSD style-tag head.
type MusicListeningCluster struct {
	Tracks        []string
	Artists       []ContentEntityRef
	CoverAlbums   []ContentEntityRef
	Weight        float64
	Tags          []MusicClusterTag
	TaggedTracks  int
	HistoryDigest string
	MoreArtists   bool
}

// MusicClusterTag is the actual original MTT output index and weighted score.
// It is not an invented style name or a tag from another model's head.
type MusicClusterTag struct {
	Index int
	Score float64
}
type MusicListeningClusters struct {
	EligibleTracks, AnalyzedTracks int
	Model                          *sonic.Identity
	Groups                         []MusicListeningCluster
	HistoryDigest                  string
}
type musicPlayedTrack struct {
	public  string
	vector  sonic.Embedding
	weight  float64
	artists []int64
	album   ContentEntityRef
	result  string
	tags    *[sonic.TagCount]float64
}

// ListeningClusters reads only this profile's completed plays. Visibility and
// available source facts filter before the bounded history sample, so hidden
// plays cannot crowd permitted tracks out of it. No provider, inference, SQL
// write, scheduler or global artist mean participates in a read.
func (s *Service) ListeningClusters(viewer Viewer, at time.Time) (MusicListeningClusters, error) {
	out := MusicListeningClusters{Groups: []MusicListeningCluster{}}
	actor, ok := viewerFromPersonalKey(viewer.Profile)
	if !ok || actor.Authority == "" || actor.AccountID == "" || actor.ProfileID == "" {
		return out, ErrAdminQuery
	}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).ListeningClusters(viewer, at)
			return err
		})
		return out, err
	}
	if at.IsZero() {
		at = s.recommendationNow(time.Time{})
	}
	visible, args := viewer.itemVisibilitySQL("item.id")
	bind := []any{actor.Authority, actor.AccountID, actor.ProfileID, at.UnixMilli()}
	bind = append(bind, args...)
	bind = append(bind, SonicDefaultEnabled(), sonic.MaxClusterPoints, sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256)
	bind = append(bind, sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256)
	rows, err := s.read().Query(`WITH recent AS(
 SELECT h.item_id,h.started_ms FROM play_history h INDEXED BY play_history_listen_profile
 JOIN catalog_entities item ON item.id=h.item_id JOIN catalog_libraries library ON library.id=item.library_id
 JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
 WHERE h.authority=? AND h.account_id=? AND h.profile_id=? AND h.kind=7 AND h.completed=1 AND h.started_ms<=?
 AND item.kind=7 AND `+visible+` AND `+currentSonicEligibleSQL+`
 AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=library.library_id),?)=1
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 ORDER BY h.started_ms DESC,h.id DESC LIMIT ?)
 SELECT item.id,pid(item.public_id),count(*),`+currentSonicVectorSQL+`,`+currentSonicResultIDSQL+`,`+radioArtistsSQL+`,pid(album_item.public_id),album_item.title
 FROM recent JOIN catalog_entities item ON item.id=recent.item_id
 JOIN catalog_libraries library ON library.id=item.library_id
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 JOIN catalog_entities album_item ON album_item.id=album.entity_id AND album_item.retired=0
 GROUP BY item.id ORDER BY max(recent.started_ms) DESC,item.id`, bind...)
	if err != nil {
		return out, err
	}
	tracks := map[int64]musicPlayedTrack{}
	points := []sonic.ClusterPoint{}
	for rows.Next() {
		var id int64
		var track musicPlayedTrack
		var data []byte
		var artists string
		var result sql.NullString
		if err = rows.Scan(&id, &track.public, &track.weight, &data, &result, &artists, &track.album.ID, &track.album.Name); err != nil {
			break
		}
		out.EligibleTracks++
		track.vector, err = sonic.FromBytes(data)
		if err != nil {
			err = nil
			continue
		}
		if err = json.Unmarshal([]byte(artists), &track.artists); err != nil {
			break
		}
		track.result = result.String
		tracks[id] = track
		points = append(points, sonic.ClusterPoint{ID: id, Vector: track.vector, Weight: track.weight})
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	out.AnalyzedTracks = len(points)
	if len(points) == 0 {
		return out, nil
	}
	if err = s.listeningClusterTags(tracks); err != nil {
		return out, err
	}
	// Every row's style reuse and ordering depend on the complete permitted
	// listening sample, not only that mix's selected members. Bind all IDs to
	// that sample so a play in another cluster also expires old row names.
	historyIDs := make([]int64, 0, len(tracks))
	for id := range tracks {
		historyIDs = append(historyIDs, id)
	}
	sort.Slice(historyIDs, func(i, j int) bool { return historyIDs[i] < historyIDs[j] })
	history := [][]any{}
	for _, id := range historyIDs {
		track := tracks[id]
		history = append(history, []any{track.public, track.result, track.weight})
	}
	encodedHistory, _ := json.Marshal(history)
	historyDigest := sha256.Sum256(encodedHistory)
	out.HistoryDigest = hex.EncodeToString(historyDigest[:])
	out.Model = &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}
	// The publisher decides whether its row has enough plays/coverage. These
	// counts and clusters remain actual facts, without a guessed UI threshold.
	limit := min(sonic.MaxClusters, max(1, len(points)/6))
	clusters, err := sonic.ClusterListening(points, limit)
	if err != nil {
		return out, err
	}
	type rankedArtist struct {
		id     int64
		weight float64
	}
	groupArtists := [][]int64{}
	needed := map[int64]bool{}
	for _, cluster := range clusters {
		members := append([]int64(nil), cluster.Members...)
		sort.Slice(members, func(i, j int) bool {
			a, _ := sonic.Cosine(tracks[members[i]].vector, cluster.Center)
			b, _ := sonic.Cosine(tracks[members[j]].vector, cluster.Center)
			if a != b {
				return a > b
			}
			return members[i] < members[j]
		})
		// A mix is a finite ordinary playlist selection. Use that writer's
		// existing bound, so Save copies every playable mix key exactly.
		members = members[:min(MaxPlaylistEntries, len(members))]
		group := MusicListeningCluster{Tracks: []string{}, Artists: []ContentEntityRef{}, CoverAlbums: []ContentEntityRef{}, Tags: []MusicClusterTag{}}
		var tagSums [sonic.TagCount]float64
		inputs := [][]any{}
		weights := map[int64]float64{}
		albums := map[string]bool{}
		for _, id := range members {
			track := tracks[id]
			group.Weight += track.weight
			group.Tracks = append(group.Tracks, track.public)
			inputs = append(inputs, []any{track.public, track.result, track.weight})
			if track.tags != nil {
				group.TaggedTracks++
				for index, score := range track.tags {
					tagSums[index] += score * track.weight
				}
			}
			for _, artist := range track.artists {
				if artist > 0 {
					weights[artist] += track.weight
				}
			}
			if !albums[track.album.ID] && len(group.CoverAlbums) < 3 {
				albums[track.album.ID] = true
				group.CoverAlbums = append(group.CoverAlbums, track.album)
			}
		}
		if group.TaggedTracks == len(members) {
			for index, sum := range tagSums {
				group.Tags = append(group.Tags, MusicClusterTag{Index: index, Score: sum / group.Weight})
			}
		}
		encoded, _ := json.Marshal(inputs)
		digest := sha256.Sum256(encoded)
		group.HistoryDigest = hex.EncodeToString(digest[:])
		artists := []rankedArtist{}
		for id, weight := range weights {
			artists = append(artists, rankedArtist{id, weight})
		}
		sort.Slice(artists, func(i, j int) bool {
			if artists[i].weight != artists[j].weight {
				return artists[i].weight > artists[j].weight
			}
			return artists[i].id < artists[j].id
		})
		ids := []int64{}
		for _, artist := range artists {
			ids = append(ids, artist.id)
			needed[artist.id] = true
		}
		groupArtists = append(groupArtists, ids)
		out.Groups = append(out.Groups, group)
	}
	ids := []int64{}
	for id := range needed {
		ids = append(ids, id)
	}
	refs := map[int64]ContentEntityRef{}
	if len(ids) > 0 {
		rows, err = s.read().Query(`SELECT item.id,pid(item.public_id),item.title FROM json_each(?) wanted JOIN catalog_entities item ON item.id=wanted.value WHERE item.kind=5 AND item.retired=0`, idsJSON64(ids))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id int64
			var ref ContentEntityRef
			if err = rows.Scan(&id, &ref.ID, &ref.Name); err != nil {
				break
			}
			refs[id] = ref
		}
		if next := rows.Err(); err == nil {
			err = next
		}
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	for n, ids := range groupArtists {
		for _, id := range ids {
			if ref, ok := refs[id]; ok {
				if len(out.Groups[n].Artists) < 2 {
					out.Groups[n].Artists = append(out.Groups[n].Artists, ref)
				} else {
					out.Groups[n].MoreArtists = true
					break
				}
			}
		}
	}
	return out, nil
}

// Tags must come from exactly the result and cue entity that supplied each
// current vector. Missing/partial heads remain unknown; zero is a real score.
func (s *Service) listeningClusterTags(tracks map[int64]musicPlayedTrack) error {
	pairs := make([][]any, 0, len(tracks))
	for id, track := range tracks {
		pairs = append(pairs, []any{id, track.result})
	}
	data, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	rows, err := s.read().Query(`SELECT tag.entity_id,tag.tag_index,tag.score
 FROM json_each(?) wanted JOIN music_sonic_tags tag
 ON tag.entity_id=json_extract(wanted.value,'$[0]') AND tag.result_id=json_extract(wanted.value,'$[1]')
 ORDER BY tag.entity_id,tag.tag_index`, string(data))
	if err != nil {
		return err
	}
	defer rows.Close()
	type head struct {
		values  [sonic.TagCount]float64
		count   int
		invalid bool
	}
	heads := map[int64]*head{}
	for rows.Next() {
		var id int64
		var index int
		var score float64
		if err = rows.Scan(&id, &index, &score); err != nil {
			return err
		}
		if heads[id] == nil {
			heads[id] = &head{}
		}
		h := heads[id]
		if index != h.count || index >= sonic.TagCount || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			h.invalid = true
		} else {
			h.values[index] = score
		}
		h.count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for id, h := range heads {
		if !h.invalid && h.count == sonic.TagCount {
			track := tracks[id]
			track.tags = &h.values
			tracks[id] = track
		}
	}
	return nil
}
