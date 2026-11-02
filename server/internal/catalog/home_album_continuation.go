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
