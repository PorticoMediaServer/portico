package catalog

import (
	"strings"
	"time"
)

// Recently played is explicitly library-scoped. Radio history is the finite
// current profile's accepted starts, not global play_counts or a guessed seed
// from a recently played song. Artist, album and song seeds retain their kinds.
func (s *Service) musicDiscoverRecent(r ContentRequest, offset int, now time.Time) (ContentSection, error) {
	section := contentSection("recently_played", "rail", "Recently played", []ContentEntry{}, 0, "")
	section.Start = offset
	albumVisible, albumArgs := listeningSectionVisibility("album", r.Viewer)
	albumVisible = strings.ReplaceAll(albumVisible, "projected.entity_id", "e.entity_id")
	albumVisible = strings.ReplaceAll(albumVisible, "WHERE album.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND album.id=`)
	itemVisible, itemArgs := r.Viewer.itemVisibilitySQL("item.id")
	query := `SELECT pid(entity.public_id) id,0 radio,e.last_played_at played FROM catalog_personal_sort_rows e INDEXED BY catalog_personal_order_last JOIN catalog_entities entity ON entity.id=e.entity_id WHERE e.profile_id=? AND e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=6 AND e.last_played_at<>'' AND e.last_played_at<=? AND ` + albumVisible + `
 UNION ALL
 SELECT pid(seed.public_id) id,1 radio,strftime('%Y-%m-%dT%H:%M:%fZ',recent.last_started_ms/1000.0,'unixepoch') played
 FROM personal_music_radios recent INDEXED BY personal_music_radios_recent JOIN catalog_entities seed ON seed.id=recent.seed_id
 WHERE recent.profile_id=? AND recent.last_started_ms<=? AND seed.retired=0 AND seed.kind IN(5,6,7)
 AND seed.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)
 AND EXISTS(SELECT 1 FROM catalog_entities item JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 WHERE ((seed.kind=7 AND item.id=seed.id) OR (seed.kind=6 AND song.album_id=seed.id) OR (seed.kind=5 AND (album.artist_id=seed.id OR EXISTS(SELECT 1 FROM catalog_song_artists credit WHERE credit.song_id=item.id AND credit.artist_id=seed.id))))
 AND ` + itemVisible + ` AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0))`
	args := append([]any{r.Profile, r.Library, now.UTC().Format("2006-01-02T15:04:05.000Z")}, albumArgs...)
	args = append(args, r.Profile, now.UnixMilli(), r.Library)
	args = append(args, itemArgs...)
	if err := s.read().QueryRow(`SELECT count(*) FROM (`+query+`)`, args...).Scan(&section.TotalCount); err != nil {
		return section, err
	}
	rows, err := s.read().Query(`SELECT id,radio FROM (`+query+`) ORDER BY played DESC,radio DESC,id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), r.Limit+1, offset)...)
	if err != nil {
		return section, err
	}
	type candidate struct {
		id    string
		radio bool
	}
	candidates := []candidate{}
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.id, &value.radio); err != nil {
			break
		}
		candidates = append(candidates, value)
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return section, err
	}
	if len(candidates) > r.Limit {
		candidates = candidates[:r.Limit]
		section.NextCursor = "more"
	}
	ids, seen := []string{}, map[string]bool{}
	for _, value := range candidates {
		if !seen[value.id] {
			ids = append(ids, value.id)
			seen[value.id] = true
		}
	}
	entries, err := s.homeEngineEntries(r.Profile, ids)
	if err != nil {
		return section, err
	}
	byID := map[string]ContentEntry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, value := range candidates {
		seed, ok := byID[value.id]
		if !ok {
			continue
		}
		if value.radio {
			section.Entries = append(section.Entries, ContentEntry{ID: "radio:" + seed.ID, Kind: "radio", LibraryID: r.Library,
				Title: seed.Title, PosterURL: seed.PosterURL, RadioSeed: &MusicRadioSeed{ID: seed.ID, Kind: seed.Kind},
				Navigation: &ContentNavigation{View: "radio", EntityID: seed.ID}})
		} else {
			section.Entries = append(section.Entries, seed)
		}
	}
	return section, nil
}
