package catalog

import (
	"strconv"
	"strings"
)

// albumContinuationScan bounds each music library's read of song activity: an
// album is "continue listening" from the profile's most recent songs, never by
// grouping its whole listening history.
const albumContinuationScan = 512

// homeAlbumContinuation is the SQL for the albums a profile is part-way
// through (W2-A5). Songs never keep a mid-track position, so an album resumes
// at the start of its most recently played song, or at the next available
// song when that one finished. An album whose last song finished is done and
// has no row. Columns: album (entity id), target (song entity id), ord.
//
// albums, when not empty, limits the result to those album public ids.
func homeAlbumContinuation(r HomeRequest, libraries []string, albums []string) (string, []any) {
	recent := make([]string, 0, len(libraries))
	args := []any{}
	for _, library := range libraries {
		recent = append(recent, `SELECT * FROM (SELECT a.item_id,a.updated_at,a.state FROM progress_activity a INDEXED BY progress_recent
 WHERE a.profile_id=? AND a.library_id=? ORDER BY a.updated_at DESC,a.item_id DESC LIMIT `+strconv.Itoa(albumContinuationScan)+`)`)
		args = append(args, r.Profile, library)
	}
	if len(recent) == 0 {
		return `SELECT NULL AS album,NULL AS target,NULL AS ord WHERE 0`, nil
	}
	// SQLite takes the bare columns from the row holding max(updated_at).
	latest := `SELECT sg.album_id AS album,recent.item_id AS item,max(recent.updated_at) AS ord,recent.state AS state,
 COALESCE(sg.disc_number,1) AS disc,COALESCE(sg.track_number,0) AS track
 FROM (` + strings.Join(recent, ` UNION ALL `) + `) recent CROSS JOIN catalog_songs sg ON sg.entity_id=recent.item_id GROUP BY sg.album_id`
	next := `(SELECT n.entity_id FROM catalog_songs n INDEXED BY catalog_songs_listening_order WHERE n.album_id=latest.album
 AND (COALESCE(n.disc_number,1),COALESCE(n.track_number,0),n.entity_id)>(latest.disc,latest.track,latest.item) AND ` + homeVisible("n.entity_id") + `
 ORDER BY COALESCE(n.disc_number,1),COALESCE(n.track_number,0),n.entity_id LIMIT 1)`
	visible, visibleArgs := r.Viewer.itemVisibilitySQL("cont.target")
	query := `SELECT cont.album,cont.target,cont.ord FROM (SELECT latest.album,CASE WHEN latest.state!='ended' THEN latest.item ELSE ` + next + ` END AS target,latest.ord FROM (` + latest + `) latest) cont
 JOIN catalog_entities album ON album.id=cont.album AND album.retired=0
 WHERE cont.target IS NOT NULL AND ` + homeVisible("cont.target") + ` AND ` + visible
	args = append(args, visibleArgs...)
	if len(albums) > 0 {
		query += ` AND album.public_id IN(SELECT pid_blob(value) FROM json_each(?))`
		args = append(args, idsJSON(albums))
	}
	return query, args
}

// homeMusicLibraries is the music subset of the row's requested libraries.
func (s *Service) homeMusicLibraries(requested []string) ([]string, error) {
	libraries, err := s.homeLibraries(requested)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, library := range libraries {
		if library.Kind == "music" {
			out = append(out, library.ID)
		}
	}
	return out, nil
}

// homeAlbumResumes gives each album entry on the page its continuation song,
// played from its start (songs keep no position).
func (s *Service) homeAlbumResumes(r HomeRequest, entries []ContentEntry) ([]HomeResume, error) {
	albums := []string{}
	for _, entry := range entries {
		if entry.Kind == "album" {
			albums = append(albums, entry.ID)
		}
	}
	if len(albums) == 0 {
		return nil, nil
	}
	libraries, err := s.homeMusicLibraries(r.Libraries)
	if err != nil || len(libraries) == 0 {
		return nil, err
	}
	query, args := homeAlbumContinuation(r, libraries, albums)
	rows, err := s.read().Query(`SELECT pid(album.public_id),pid(target.public_id) FROM (`+query+`) chosen
 JOIN catalog_entities album ON album.id=chosen.album JOIN catalog_entities target ON target.id=chosen.target`, args...)
	if err != nil {
		return nil, err
	}
	targetAlbum := map[string]string{}
	targets := []string{}
	for rows.Next() {
		var album, target string
		if err = rows.Scan(&album, &target); err != nil {
			break
		}
		targetAlbum[target] = album
		targets = append(targets, target)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil || len(targets) == 0 {
		return nil, err
	}
	hydrated, err := s.homeEntries(r.Profile, targets)
	if err != nil {
		return nil, err
	}
	out := []HomeResume{}
	byAlbum := map[string]ContentEntry{}
	for _, target := range hydrated {
		album := targetAlbum[target.ID]
		byAlbum[album] = target
		out = append(out, HomeResume{EntryID: album, Target: target})
	}
	for index := range entries {
		entry := &entries[index]
		target, ok := byAlbum[entry.ID]
		if entry.Kind != "album" || !ok {
			continue
		}
		start := 0.0
		entry.Playback = &ContentPlayback{ItemID: target.ID, StartSeconds: &start}
	}
	return out, nil
}
