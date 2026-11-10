package db

import (
	"context"
	"database/sql"
	"path"
	"time"
)

// UnprobedFile is a media file whose inside has not been read yet.
type UnprobedFile struct {
	ID         int64
	PathID     int64 // the library folder it is under
	Root       string
	Rel        string
	Size       int64
	ModifiedNS int64
}

// UnprobedFiles returns up to limit media files of a library that have not been read, after
// the given file id. A file that could not be read stays unread and is offered again by the
// next scan, which starts from 0.
func (d *DB) UnprobedFiles(ctx context.Context, libraryID, after int64, limit int) ([]UnprobedFile, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT f.id, p.id, p.path, d.rel_path, f.name, f.size, f.modified_ns FROM files f
		JOIN directories d ON d.id = f.directory_id JOIN library_paths p ON p.id = d.library_path_id
		WHERE f.probed_at IS NULL AND f.probe_version = 0 AND f.id > ? AND f.role = 1 AND f.missing_since_at IS NULL AND p.library_id = ?
		ORDER BY f.probe_version, f.id LIMIT ?`, after, libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnprobedFile
	for rows.Next() {
		var f UnprobedFile
		var dir, name string
		if err := rows.Scan(&f.ID, &f.PathID, &f.Root, &dir, &name, &f.Size, &f.ModifiedNS); err != nil {
			return nil, err
		}
		f.Rel = path.Join(dir, name)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Probe is what reading one file found. With Problem set the file could not be read: it goes
// on the Files skipped list, keeps the title its name gave it and stays unread.
type Probe struct {
	FileID     int64
	Size       int64 // the file as it was when read; a file that changed since is left for the next scan
	ModifiedNS int64
	Container  string
	DurationMS int64
	Bitrate    int64
	Streams    []Stream
	Chapters   []Marker
	Problem    FileProblem
}

// StoreProbes writes what a batch of files was found to hold, and brings the versions and
// titles those files play up to date.
func (d *DB) StoreProbes(ctx context.Context, probes []Probe) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		for i := range probes {
			if err := storeProbe(b, &probes[i], now); err != nil {
				return err
			}
		}
		return nil
	})
}

func storeProbe(b *batchTx, p *Probe, now int64) error {
	if p.Problem != ProblemNone {
		if err := checkCode("files.problem", fileProblemNames, p.Problem); err != nil {
			return err
		}
		_, err := b.exec(`UPDATE files SET problem = ? WHERE id = ? AND size = ? AND modified_ns = ? AND problem IS NOT ?`,
			p.Problem, p.FileID, p.Size, p.ModifiedNS, p.Problem)
		return err
	}
	res, err := b.exec(`UPDATE files SET probed_at = ?, container = ?, duration_ms = ?, bitrate = ?, problem = NULL
		WHERE id = ? AND size = ? AND modified_ns = ?`, now, nz(p.Container), nz(p.DurationMS), nz(p.Bitrate), p.FileID, p.Size, p.ModifiedNS)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if _, err := b.exec(`DELETE FROM media_streams WHERE file_id = ?`, p.FileID); err != nil {
		return err
	}
	if err := b.insertStreams(p.FileID, p.Streams); err != nil {
		return err
	}
	// Every version this file is part of gets its summary again from its files.
	rows, err := b.tx.Query(`SELECT m.id, m.item_id, i.library_id, p.position, p.start_ms IS NULL AND (SELECT count(*) FROM media_parts o
		WHERE o.media_id = m.id) = 1 FROM media_parts p JOIN media m ON m.id = p.media_id JOIN items i ON i.id = m.item_id WHERE p.file_id = ?`, p.FileID)
	if err != nil {
		return err
	}
	type version struct {
		id, item, library int64
		position          int
		whole             bool
	}
	var versions []version
	for rows.Next() {
		var v version
		if err := rows.Scan(&v.id, &v.item, &v.library, &v.position, &v.whole); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, v)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	facts := streamFacts(p.Streams)
	for _, v := range versions {
		if v.position == 0 {
			if _, err := b.exec(`UPDATE media SET container = ?, bitrate = ?, width = ?, height = ?, video_codec = ?, audio_codec = ?,
				audio_channels = ?, hdr = ? WHERE id = ?`, nz(p.Container), nz(p.Bitrate), nz(facts.width), nz(facts.height),
				nz(facts.videoCodec), nz(facts.audioCodec), nz(facts.audioChannel), nz(facts.hdr), v.id); err != nil {
				return err
			}
		}
		if _, err := b.exec(`UPDATE media SET duration_ms = (SELECT nullif(sum(CASE WHEN p.end_ms IS NOT NULL THEN p.end_ms - ifnull(p.start_ms, 0)
			ELSE max(ifnull(f.duration_ms, 0) - ifnull(p.start_ms, 0), 0) END), 0) FROM media_parts p JOIN files f ON f.id = p.file_id
			WHERE p.media_id = media.id) WHERE id = ?`, v.id); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET duration_ms = (SELECT duration_ms FROM media WHERE item_id = items.id ORDER BY position, id LIMIT 1),
			updated_at = ? WHERE id = ?`, now, v.item); err != nil {
			return err
		}
		if v.whole {
			// Chapters written in a file are those of a version that is the whole file.
			if _, err := b.exec(`DELETE FROM media_markers WHERE media_id = ? AND source = ?`, v.id, MarkerFromFile); err != nil {
				return err
			}
			for _, chapter := range p.Chapters {
				chapter.Kind, chapter.Source = MarkerChapter, MarkerFromFile
				if err := b.insertMarker(v.id, chapter); err != nil {
					return err
				}
			}
		}
		if err := appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: v.item, LibraryID: v.library}); err != nil {
			return err
		}
	}
	return nil
}

// streamFacts is the part of a version's summary that comes from its first file's tracks.
func streamFacts(streams []Stream) versionFacts {
	var facts versionFacts
	var audio *Stream
	for s := range streams {
		stream := &streams[s]
		switch {
		case stream.Kind == StreamVideo && facts.videoCodec == "":
			facts.videoCodec, facts.width, facts.height, facts.hdr = stream.Codec, stream.Width, stream.Height, stream.HDR
		case stream.Kind == StreamAudio && (audio == nil || stream.Default && !audio.Default):
			audio = stream
		}
	}
	if audio != nil {
		facts.audioCodec, facts.audioChannel = audio.Codec, audio.Channels
	}
	return facts
}
