package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// A page of media rows carries per-kind detail: an episode's show and numbering,
// a song's album and MusicBrainz link, a book file's part and local metadata.
// `mediaPage` batched the item rows and their sources and then asked for that
// detail one item at a time — six statements per row of a home page that already
// cost two, which is thirty statements on a page of twelve mixed items.
//
// These load the same detail for a whole page. The per-item functions remain the
// definition of what the answer is, and `TestPageEnrichmentMatchesPerItemReads`
// asserts the two agree field for field over a mixed catalogue; any id a batch
// does not return falls back to the per-item read, so a shape the batch has not
// anticipated is answered correctly rather than silently omitted.

// enrichPage fills the per-kind detail for the items on one page.
func (s *Service) enrichPage(items map[string]*Item) error {
	if e := s.musicFactsPage(items); e != nil {
		return e
	}
	episodes, songs, books := []string{}, []string{}, []string{}
	for id, item := range items {
		switch item.Kind {
		case "episode":
			episodes = append(episodes, id)
		case "song":
			songs = append(songs, id)
		case "audiobook_file":
			books = append(books, id)
		}
	}
	if len(episodes) > 0 {
		loaded, err := s.episodeInfoPage(episodes)
		if err != nil {
			return err
		}
		if err = fill(items, episodes, loaded, func(item *Item, value *EpisodeInfo) { item.Episode = value }, s.episodeInfo); err != nil {
			return err
		}
	}
	if len(songs) > 0 {
		loaded, err := s.songInfoPage(songs)
		if err != nil {
			return err
		}
		if err = fill(items, songs, loaded, func(item *Item, value *SongInfo) { item.Song = value }, s.songInfo); err != nil {
			return err
		}
	}
	if len(books) > 0 {
		loaded, err := s.bookFileInfoPage(books)
		if err != nil {
			return err
		}
		if err = fill(items, books, loaded, func(item *Item, value *BookFileInfo) { item.BookFile = value }, s.bookFileInfo); err != nil {
			return err
		}
	}
	return nil
}

// fill assigns a batch's results and falls back to the per-item read for
// anything the batch did not return.
func fill[T any](items map[string]*Item, ids []string, loaded map[string]*T, assign func(*Item, *T), single func(string) (*T, error)) error {
	for _, id := range ids {
		value, ok := loaded[id]
		if !ok {
			resolved, err := single(id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
			value = resolved
		}
		assign(items[id], value)
	}
	return nil
}

// idList renders a bound id set as a JSON array, so one parameter carries a page.
func idList(ids []string) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

func (s *Service) episodeInfoPage(ids []string) (map[string]*EpisodeInfo, error) {
	out := map[string]*EpisodeInfo{}
	rows, err := s.read().Query(`SELECT pid(item.public_id),pid(sh.public_id),sh.title,COALESCE(pid(se.public_id),''),sd.number,ep.numbering,ep.number,ep.local_identity_status,COALESCE((SELECT NULLIF(status,'delegated_tvdb') FROM screen_metadata_work WHERE target_kind='item' AND target_id=item.id AND accepted_publication<>''),(SELECT status FROM tvdb_episode_links WHERE item_id=item.id),'unmatched'),ep.ordering_basis,COALESCE(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch'),''),CASE WHEN EXISTS(SELECT 1 FROM episode_asset_boundaries b WHERE b.item_id=item.id AND b.status='unknown_multi_episode') THEN 'unknown_multi_episode' ELSE 'whole_source' END FROM catalog_entities item JOIN catalog_episodes ep ON ep.entity_id=item.id JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_entities se ON se.id=ep.season_id LEFT JOIN catalog_seasons sd ON sd.entity_id=se.id WHERE item.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, idList(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var info EpisodeInfo
		var season sql.NullInt64
		if err = rows.Scan(&id, &info.ShowID, &info.ShowTitle, &info.SeasonID, &season, &info.Numbering, &info.Number, &info.LocalIdentityStatus, &info.ProviderMatchStatus, &info.OrderingBasis, &info.AirDate, &info.SourceBoundary); err != nil {
			return nil, err
		}
		if season.Valid {
			number := int(season.Int64)
			info.SeasonNumber = &number
		}
		value := info
		out[id] = &value
	}
	return out, rows.Err()
}

func (s *Service) songInfoPage(ids []string) (map[string]*SongInfo, error) {
	out := map[string]*SongInfo{}
	rows, err := s.read().Query(`SELECT pid(item.public_id),pid(album.public_id),album.title,artist.title,
 COALESCE((SELECT group_concat(title,'; ') FROM (SELECT performer.title FROM catalog_song_artists sa JOIN catalog_entities performer ON performer.id=sa.artist_id WHERE sa.song_id=s.entity_id ORDER BY performer.title)),''),
 s.disc_number,s.track_number,COALESCE((SELECT status FROM mb_jobs WHERE kind='song' AND entity_id=item.id),'unmatched'),
 COALESCE((SELECT issue.issue FROM catalog_asset_links link JOIN catalog_asset_issues issue ON issue.asset_id=link.asset_id AND issue.library_id=item.library_id WHERE link.entity_id=item.id LIMIT 1),''),
 s.recording_id,s.release_id,s.release_group_id,s.track_id,s.release_status,s.provider_observed_at,s.provider_title,s.provider_artist
 FROM catalog_entities item JOIN catalog_songs s ON s.entity_id=item.id
 JOIN catalog_entities album ON album.id=s.album_id JOIN catalog_albums a ON a.entity_id=album.id
 JOIN catalog_entities artist ON artist.id=a.artist_id
 WHERE item.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND item.kind=7 AND album.retired=0
 AND EXISTS(SELECT 1 FROM catalog_song_artists sa WHERE sa.song_id=item.id)`, idList(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var info SongInfo
		var disc, track sql.NullInt64
		if err = rows.Scan(&id, &info.AlbumID, &info.AlbumTitle, &info.AlbumArtist, &info.Artist, &disc, &track, &info.ProviderMatchStatus, &info.LocalMetadataIssue, &info.ProviderRecordingID, &info.ProviderReleaseID, &info.ProviderReleaseGroupID, &info.ProviderTrackID, &info.ProviderReleaseStatus, &info.ProviderObservedAt, &info.ProviderTitle, &info.ProviderArtist); err != nil {
			rows.Close()
			return nil, err
		}
		info.DiscNumber = intPointer(disc)
		info.TrackNumber = intPointer(track)
		value := info
		out[id] = &value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, info := range out {
		if info.ProviderRecordingID != "" {
			info.ProviderSourceURL = "https://musicbrainz.org/recording/" + info.ProviderRecordingID
		}
	}
	return out, nil
}

func (s *Service) bookFileInfoPage(ids []string) (map[string]*BookFileInfo, error) {
	out := map[string]*BookFileInfo{}
	rows, err := s.read().Query(`SELECT pid(item.public_id),pid(b.public_id),b.title,f.disc_number,f.part_number,
 COALESCE((SELECT issue.issue FROM catalog_asset_links link JOIN catalog_asset_issues issue ON issue.asset_id=link.asset_id AND issue.library_id=item.library_id WHERE link.entity_id=item.id LIMIT 1),''),
 p.revision,p.local_mode,d.local_metadata_payload
 FROM catalog_entities item JOIN catalog_book_files f ON f.entity_id=item.id
 JOIN catalog_entities b ON b.id=f.book_id JOIN catalog_books d ON d.entity_id=b.id
 JOIN catalog_libraries l ON l.id=b.library_id
 LEFT JOIN audio_metadata_policies p ON p.library_id=l.library_id
 WHERE item.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND item.kind=9 AND b.retired=0`, idList(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, payload string
		var info BookFileInfo
		var disc, part, revision sql.NullInt64
		var mode sql.NullString
		if err = rows.Scan(&id, &info.BookID, &info.BookTitle, &disc, &part, &info.LocalMetadataIssue, &revision, &mode, &payload); err != nil {
			return nil, err
		}
		info.DiscNumber, info.PartNumber = intPointer(disc), intPointer(part)
		info.SourceBoundary = "whole_source"
		if revision.Valid {
			info.LocalPolicy = &LocalAudioPolicy{Revision: revision.Int64, LocalMode: mode.String}
			if mode.String != "off" && payload != "" {
				var local LocalBookMetadata
				if err = json.Unmarshal([]byte(payload), &local); err != nil {
					return nil, err
				}
				info.LocalMetadata = &local
			}
		}
		out[id] = &info
	}
	return out, rows.Err()
}
