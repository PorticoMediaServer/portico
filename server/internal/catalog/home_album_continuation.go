package catalog

import (
	"encoding/json"
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
		// A library that is not music reads no rows: the guard is part of this
		// statement, so a caller needs no separate library read.
		recent = append(recent, `SELECT * FROM (SELECT a.item_id,a.updated_at,a.state FROM progress_activity a INDEXED BY progress_recent
 WHERE a.profile_id=? AND a.library_id=? AND EXISTS(SELECT 1 FROM libraries ml WHERE ml.id=a.library_id AND ml.kind='music') ORDER BY a.updated_at DESC,a.item_id DESC LIMIT `+strconv.Itoa(albumContinuationScan)+`)`)
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

// homeAlbumProgress is how far through each album its current song is (W2-A5),
// in one statement for the page: the album's visible songs' total length and
// the length of those before the current song, in seconds, by album public id.
// An album whose songs have no known length gets no progress.
func (s *Service) homeAlbumProgress(r HomeRequest, current map[string]string) (map[string][2]float64, error) {
	out := map[string][2]float64{}
	if len(current) == 0 {
		return out, nil
	}
	pairs := make([][2]string, 0, len(current))
	for album, song := range current {
		pairs = append(pairs, [2]string{album, song})
	}
	visible, args := r.Viewer.itemVisibilitySQL("n.entity_id")
	rows, err := s.read().Query(`SELECT album,sum(seconds),sum(CASE WHEN before THEN seconds ELSE 0 END) FROM (
 SELECT json_extract(pair.value,'$[0]') AS album,
 (COALESCE(n.disc_number,1),COALESCE(n.track_number,0),n.entity_id)<(COALESCE(t.disc_number,1),COALESCE(t.track_number,0),t.entity_id) AS before,
 (SELECT COALESCE(max(NULLIF(COALESCE(l.end_seconds,a.duration)-l.start_seconds,0)),0) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=n.entity_id AND a.available=1 AND l.available=1) AS seconds
 FROM json_each(?) pair
 JOIN catalog_entities album ON album.public_id=pid_blob(json_extract(pair.value,'$[0]'))
 JOIN catalog_entities target ON target.public_id=pid_blob(json_extract(pair.value,'$[1]'))
 JOIN catalog_songs t ON t.entity_id=target.id AND t.album_id=album.id
 JOIN catalog_songs n INDEXED BY catalog_songs_listening_order ON n.album_id=album.id
 WHERE `+homeVisible("n.entity_id")+` AND `+visible+`) GROUP BY album`, append([]any{pairsJSON(pairs)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var album string
		var total, before float64
		if err = rows.Scan(&album, &total, &before); err != nil {
			return nil, err
		}
		if total > 0 {
			out[album] = [2]float64{total, before}
		}
	}
	return out, rows.Err()
}

func pairsJSON(pairs [][2]string) string {
	raw, _ := json.Marshal(pairs)
	return string(raw)
}
