package playbackdisc

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

func writeStructure(ctx context.Context, tx *sql.Tx, id string, p disc.Probe) error {
	for _, t := range p.Titles {
		if _, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_titles(snapshot_id,disc_order,title_key,playlist,playlist_file,dvd_title,vts,pgc,duration_ms,angle_count) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, t.DiscOrder, t.Key(p.Kind), t.Playlist, t.PlaylistFile, t.Title, t.VTS, t.PGC, t.DurationMs, t.AngleCount); e != nil {
			return e
		}
		for _, c := range t.Clips {
			if _, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_clips(snapshot_id,disc_order,sequence,file,in_time_90k,out_time_90k) VALUES(?,?,?,?,?,?)`, id, t.DiscOrder, c.Sequence, c.File, c.InTime90k, c.OutTime90k); e != nil {
				return e
			}
		}
		for i, v := range t.Streams {
			if _, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_disc_streams(snapshot_id,disc_order,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_known,dv_base_pid,video_role) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, t.DiscOrder, i, v.ClipSequence, v.PID, v.Kind, v.Coding, v.Language, v.DolbyVisionEnhancementLayer, v.DolbyVisionEnhancementKnown, v.DolbyVisionBasePID, v.VideoRole); e != nil {
				return e
			}
		}
		for i, c := range t.Chapters {
			if _, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_disc_chapters(snapshot_id,disc_order,chapter_order,start_ms) VALUES(?,?,?,?)`, id, t.DiscOrder, i, c.StartMs); e != nil {
				return e
			}
		}
	}
	return nil
}
func readStructure(ctx context.Context, tx *sql.Tx, id string, header disc.Probe) (disc.Probe, error) {
	rows, e := tx.QueryContext(ctx, `SELECT disc_order,title_key,playlist,playlist_file,dvd_title,vts,pgc,duration_ms,angle_count FROM playback_disc_snapshot_titles WHERE snapshot_id=? ORDER BY disc_order LIMIT 4097`, id)
	if e != nil {
		return header, e
	}
	header.Titles = []disc.Title{}
	for rows.Next() {
		var t disc.Title
		var key string
		var playlist sql.NullInt64
		if e = rows.Scan(&t.DiscOrder, &key, &playlist, &t.PlaylistFile, &t.Title, &t.VTS, &t.PGC, &t.DurationMs, &t.AngleCount); e != nil {
			rows.Close()
			return header, e
		}
		if t.DiscOrder != len(header.Titles) || len(header.Titles) >= disc.MaxTitles {
			rows.Close()
			return header, ErrInput
		}
		if playlist.Valid {
			v := int(playlist.Int64)
			t.Playlist = &v
		}
		if key != t.Key(header.Kind) {
			rows.Close()
			return header, ErrInput
		}
		t.Clips = []disc.Clip{}
		t.Streams = []disc.Stream{}
		t.Chapters = []disc.Chapter{}
		header.Titles = append(header.Titles, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return header, e
	}
	// Read each bounded child family once, not one SQL statement per title.
	rows, e = tx.QueryContext(ctx, `SELECT disc_order,sequence,file,in_time_90k,out_time_90k FROM playback_disc_snapshot_clips WHERE snapshot_id=? ORDER BY disc_order,sequence LIMIT 131073`, id)
	if e != nil {
		return header, e
	}
	n := 0
	for rows.Next() {
		var i int
		var c disc.Clip
		if e = rows.Scan(&i, &c.Sequence, &c.File, &c.InTime90k, &c.OutTime90k); e != nil {
			break
		}
		if i < 0 || i >= len(header.Titles) || c.Sequence != len(header.Titles[i].Clips) || n >= 131072 {
			e = ErrInput
			break
		}
		header.Titles[i].Clips = append(header.Titles[i].Clips, c)
		n++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return header, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT disc_order,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_known,dv_base_pid,video_role FROM playback_disc_snapshot_disc_streams WHERE snapshot_id=? ORDER BY disc_order,stream_order LIMIT 131073`, id)
	if e != nil {
		return header, e
	}
	n = 0
	for rows.Next() {
		var i, order int
		var v disc.Stream
		var base sql.NullInt64
		if e = rows.Scan(&i, &order, &v.ClipSequence, &v.PID, &v.Kind, &v.Coding, &v.Language, &v.DolbyVisionEnhancementLayer, &v.DolbyVisionEnhancementKnown, &base, &v.VideoRole); e != nil {
			break
		}
		if i < 0 || i >= len(header.Titles) || order != len(header.Titles[i].Streams) || n >= 131072 {
			e = ErrInput
			break
		}
		if base.Valid {
			pid := int(base.Int64)
			v.DolbyVisionBasePID = &pid
		}
		header.Titles[i].Streams = append(header.Titles[i].Streams, v)
		n++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return header, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT disc_order,chapter_order,start_ms FROM playback_disc_snapshot_disc_chapters WHERE snapshot_id=? ORDER BY disc_order,chapter_order LIMIT 131073`, id)
	if e != nil {
		return header, e
	}
	n = 0
	for rows.Next() {
		var i, order int
		var c disc.Chapter
		if e = rows.Scan(&i, &order, &c.StartMs); e != nil {
			break
		}
		if i < 0 || i >= len(header.Titles) || order != len(header.Titles[i].Chapters) || n >= 131072 {
			e = ErrInput
			break
		}
		header.Titles[i].Chapters = append(header.Titles[i].Chapters, c)
		n++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	return header, e
}
func writeChapters(ctx context.Context, tx *sql.Tx, id string, values []assets.Chapter) error {
	for i, v := range values {
		if _, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_chapters(snapshot_id,chapter_order,title,start_seconds,end_seconds) VALUES(?,?,?,?,?)`, id, i, v.Title, v.Start, v.End); e != nil {
			return e
		}
	}
	return nil
}
func readChapters(ctx context.Context, tx *sql.Tx, id string, present bool) ([]assets.Chapter, error) {
	rows, e := tx.QueryContext(ctx, `SELECT chapter_order,title,start_seconds,end_seconds FROM playback_disc_snapshot_chapters WHERE snapshot_id=? ORDER BY chapter_order LIMIT 4097`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []assets.Chapter
	if present {
		out = []assets.Chapter{}
	}
	for rows.Next() {
		var i int
		var v assets.Chapter
		if e = rows.Scan(&i, &v.Title, &v.Start, &v.End); e != nil {
			return nil, e
		}
		if !present || i != len(out) || len(out) >= 4096 {
			return nil, ErrInput
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
