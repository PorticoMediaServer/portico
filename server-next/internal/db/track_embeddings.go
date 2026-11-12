package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"
)

// TrackToAnalyse is one song sound analysis has still to read: its title, the file it plays
// from on a library folder of this machine, and the stretch of that file that is the song (a
// CUE sheet's track is part of one file; 0 and 0 is the whole file).
type TrackToAnalyse struct {
	ItemID     int64
	FileID     int64
	Path       string
	Size       int64
	ModifiedNS int64
	StartMS    int64
	EndMS      int64
	DurationMS int64
}

// TracksToAnalyse reads, in id order after afterItem, up to limit songs of a library that
// have no analysis by model, or one of a file that has since been replaced. Songs on remote
// sources are left out: their analysis would read the whole file through the provider.
func (d *DB) TracksToAnalyse(ctx context.Context, libraryID int64, model string, afterItem int64, limit int) ([]TrackToAnalyse, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT i.id, f.id, lp.path, d.rel_path, f.name, f.size, f.modified_ns, ifnull(p.start_ms, 0), ifnull(p.end_ms, 0),
			ifnull(f.duration_ms, ifnull(i.duration_ms, 0))
		FROM items i
		JOIN media m ON m.item_id = i.id AND m.position = 0
		JOIN media_parts p ON p.media_id = m.id AND p.position = 0
		JOIN files f ON f.id = p.file_id AND f.missing_since_at IS NULL
		JOIN directories d ON d.id = f.directory_id
		JOIN library_paths lp ON lp.id = d.library_path_id AND lp.remote_source_id IS NULL
		LEFT JOIN track_embeddings e ON e.item_id = i.id
		WHERE i.library_id = ? AND i.kind = ? AND i.available = 1 AND i.id > ?
			AND (e.item_id IS NULL OR e.model <> ? OR e.file_id <> f.id OR e.size <> f.size OR e.modified_ns <> f.modified_ns)
		ORDER BY i.id LIMIT ?`, libraryID, KindTrack, afterItem, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackToAnalyse
	for rows.Next() {
		var t TrackToAnalyse
		var root, dir, name string
		if err := rows.Scan(&t.ItemID, &t.FileID, &root, &dir, &name, &t.Size, &t.ModifiedNS, &t.StartMS, &t.EndMS, &t.DurationMS); err != nil {
			return nil, err
		}
		t.Path = filepath.Join(root, filepath.FromSlash(dir), name)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TrackAnalysis is what sound analysis made of one song: the embedding and tags as stored
// (little-endian float32), or why the song could not be read.
type TrackAnalysis struct {
	Track     TrackToAnalyse
	Model     string
	Embedding []byte
	Tags      []byte
	Failed    string
}

// SaveTrackAnalyses keeps a batch of analyses in one Background write, so people's own writes
// go first. A song removed meanwhile is skipped.
func (d *DB) SaveTrackAnalyses(ctx context.Context, batch []TrackAnalysis, now time.Time) error {
	if len(batch) == 0 {
		return nil
	}
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		for _, a := range batch {
			var failed any
			if a.Failed != "" {
				failed = a.Failed
			}
			if _, err := tx.Exec(`INSERT INTO track_embeddings (item_id, file_id, size, modified_ns, model, embedding, tags, failed, analysed_at)
				SELECT ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)
				ON CONFLICT (item_id) DO UPDATE SET file_id = excluded.file_id, size = excluded.size, modified_ns = excluded.modified_ns,
					model = excluded.model, embedding = excluded.embedding, tags = excluded.tags, failed = excluded.failed,
					analysed_at = excluded.analysed_at`,
				a.Track.ItemID, a.Track.FileID, a.Track.Size, a.Track.ModifiedNS, a.Model, a.Embedding, a.Tags, failed, now.UnixMilli(),
				a.Track.ItemID); err != nil {
				return err
			}
		}
		return nil
	})
}

// MusicLibraries lists the music libraries, for starting their analysis.
func (d *DB) MusicLibraries(ctx context.Context) ([]Library, error) {
	all, err := d.AllLibraries(ctx)
	if err != nil {
		return nil, err
	}
	var out []Library
	for _, l := range all {
		if l.Kind == "music" {
			out = append(out, l)
		}
	}
	return out, nil
}

// SoundAnalysisCounts are a music library's songs on this machine's folders and how many of
// them the model has read (a song it could not read counts: it was read). One count over the
// library's songs, as large as the library: an accepted cost on the owner's Scanning tab.
func (d *DB) SoundAnalysisCounts(ctx context.Context, libraryID int64, model string) (songs, analysed int, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT count(*), ifnull(sum(e.item_id IS NOT NULL AND e.model = ? AND e.file_id = f.id AND e.size = f.size
			AND e.modified_ns = f.modified_ns), 0)
		FROM items i
		JOIN media m ON m.item_id = i.id AND m.position = 0
		JOIN media_parts p ON p.media_id = m.id AND p.position = 0
		JOIN files f ON f.id = p.file_id AND f.missing_since_at IS NULL
		JOIN directories d ON d.id = f.directory_id
		JOIN library_paths lp ON lp.id = d.library_path_id AND lp.remote_source_id IS NULL
		LEFT JOIN track_embeddings e ON e.item_id = i.id
		WHERE i.library_id = ? AND i.kind = ? AND i.available = 1`, model, libraryID, KindTrack).Scan(&songs, &analysed)
	return songs, analysed, err
}
