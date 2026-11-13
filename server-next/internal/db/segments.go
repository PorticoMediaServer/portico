package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// SegmentEntry is a measured version and the source snapshot it was read from.
type SegmentEntry struct {
	VersionID, VersionName                                                  string
	MediaID, FileID, Size, ModifiedNS, DurationMS, StartMS, EndMS, RemoteID int64
	ItemID, Title, Kind, TopID, TopTitle, LibraryID, Root, Relative         string
	Season, Number, Parts                                                   int
	Video, Audio                                                            bool
	Width, Height                                                           int
	HDR, Mode, State, Problem, SourceKey                                    string
	Evidence                                                                []byte
	Revision, UpdatedAt                                                     int64
}

func (e SegmentEntry) Key() string {
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%s", e.FileID, e.Size, e.ModifiedNS, e.StartMS, e.EndMS, e.Parts, e.DurationMS, e.HDR)
}
func (e SegmentEntry) Path() string { return filepath.Join(e.Root, filepath.FromSlash(e.Relative)) }

const segmentJoins = ` FROM media m JOIN items i ON i.id=m.item_id JOIN libraries l ON l.id=i.library_id
 LEFT JOIN items p ON p.id=i.parent_id LEFT JOIN items g ON g.id=p.parent_id
 JOIN media_parts mp ON mp.media_id=m.id AND mp.position=0 JOIN files f ON f.id=mp.file_id
 JOIN directories dir ON dir.id=f.directory_id JOIN library_paths lp ON lp.id=dir.library_path_id
 LEFT JOIN segment_analysis a ON a.media_id=m.id `
const segmentTop = `coalesce(g.public_id,p.public_id,i.public_id)`

// SegmentEntries pages by version ID, also when one show contains many episodes.
func (d *DB) SegmentEntries(ctx context.Context, library, title string, after int64, limit int) ([]SegmentEntry, error) {
	if limit < 1 || limit > 4096 {
		return nil, ErrInvalid
	}
	return d.segmentEntries(ctx, ` AND l.public_id=? AND (?='' OR `+segmentTop+`=?) AND m.id>? ORDER BY m.id LIMIT ?`,
		library, title, title, after, limit)
}

func (d *DB) SegmentNeighbours(ctx context.Context, e SegmentEntry, mode string) ([]SegmentEntry, error) {
	return d.segmentEntries(ctx, ` AND `+segmentTop+`=? AND i.id<>(SELECT item_id FROM media WHERE id=?) AND a.mode=? AND a.state='complete'
 ORDER BY CASE WHEN ifnull(p.index_number,-1)=? THEN 0 ELSE 1 END,abs(ifnull(i.index_number,0)-?),m.id LIMIT 8`,
		e.TopID, e.MediaID, mode, e.Season, e.Number)
}

func (d *DB) segmentEntries(ctx context.Context, where string, args ...any) ([]SegmentEntry, error) {
	args = append([]any{KindMovie, KindSeason, KindMovie, KindEpisode}, args...)
	tx, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT m.id,f.id,f.size,f.modified_ns,ifnull(m.duration_ms,0),ifnull(mp.start_ms,0),ifnull(mp.end_ms,0),
 ifnull(lp.remote_source_id,0),i.public_id,i.title,CASE WHEN i.kind=? THEN 'movie' ELSE 'episode' END,`+segmentTop+`,
 coalesce(g.title,p.title,i.title),l.public_id,lp.path,dir.rel_path || CASE WHEN dir.rel_path='' THEN '' ELSE '/' END || f.name,
 CASE WHEN p.kind=? THEN ifnull(p.index_number,-1) ELSE -1 END,ifnull(i.index_number,0),
 (SELECT count(*) FROM media_parts WHERE media_id=m.id),m.video_codec IS NOT NULL,m.audio_codec IS NOT NULL,
 ifnull(m.width,0),ifnull(m.height,0),ifnull(m.hdr,''),ifnull(a.mode,''),ifnull(a.state,'pending'),ifnull(a.problem,''),
 ifnull(a.source_key,''),a.evidence,ifnull(a.revision,0),ifnull(a.updated_at,0),m.public_id,ifnull(m.name,'')`+segmentJoins+`
 WHERE i.kind IN (?,?) AND i.missing_since_at IS NULL AND f.missing_since_at IS NULL`+where, args, func(e *SegmentEntry) []any {
		return []any{&e.MediaID, &e.FileID, &e.Size, &e.ModifiedNS, &e.DurationMS, &e.StartMS, &e.EndMS, &e.RemoteID, &e.ItemID, &e.Title, &e.Kind,
			&e.TopID, &e.TopTitle, &e.LibraryID, &e.Root, &e.Relative, &e.Season, &e.Number, &e.Parts, &e.Video, &e.Audio, &e.Width, &e.Height, &e.HDR,
			&e.Mode, &e.State, &e.Problem, &e.SourceKey, &e.Evidence, &e.Revision, &e.UpdatedAt, &e.VersionID, &e.VersionName}
	})
}

// SegmentMarker includes the owner's decision, including a dismissal hidden from viewers.
type SegmentMarker struct {
	ID             int64
	Kind           MarkerKind
	Source         MarkerSource
	StartMS, EndMS int64
	Title          string
	Dismissed      bool
	Confidence     float64
	Provenance     string
}

func (d *DB) SegmentMarkers(ctx context.Context, media int64) ([]SegmentMarker, error) {
	tx, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT id,kind,source,start_ms,end_ms,ifnull(title,''),dismissed,confidence,provenance
 FROM media_markers WHERE media_id=? AND kind<>? ORDER BY start_ms,id`, []any{media, MarkerChapter}, func(m *SegmentMarker) []any {
		return []any{&m.ID, &m.Kind, &m.Source, &m.StartMS, &m.EndMS, &m.Title, &m.Dismissed, &m.Confidence, &m.Provenance}
	})
}

// SaveSegmentAnalysis replaces only automatic markers; an owner decision for a
// kind wins over every new detection of that kind, even after Detect again.
func (d *DB) SaveSegmentAnalysis(ctx context.Context, e SegmentEntry, mode, state, problem string, evidence json.RawMessage, markers []SegmentMarker) error {
	return d.saveSegments(ctx, e, mode, state, problem, evidence, markers, false)
}

func (d *DB) SaveSegmentComparison(ctx context.Context, e SegmentEntry, mode string, evidence json.RawMessage, markers []SegmentMarker) error {
	return d.saveSegments(ctx, e, mode, "complete", "", evidence, markers, true)
}

func (d *DB) saveSegments(ctx context.Context, e SegmentEntry, mode, state, problem string, evidence json.RawMessage, markers []SegmentMarker, comparison bool) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		if comparison {
			var key, measuredMode string
			if err := tx.QueryRow(`SELECT source_key,mode FROM segment_analysis WHERE media_id=?`, e.MediaID).Scan(&key, &measuredMode); err != nil {
				return err
			}
			if key != e.Key() || measuredMode != mode {
				return nil
			}
		}

		current, err := segmentCurrentKey(tx, e)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if current != e.Key() {
			return nil
		}

		_, err = tx.Exec(`INSERT INTO segment_analysis(media_id,source_key,mode,state,problem,evidence,updated_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(media_id) DO UPDATE SET source_key=excluded.source_key,mode=excluded.mode,state=excluded.state,
 problem=excluded.problem,evidence=excluded.evidence,revision=revision+1,updated_at=excluded.updated_at`,
			e.MediaID, e.Key(), mode, state, problem, string(evidence), time.Now().UnixMilli())
		if err != nil {
			return err
		}
		if state != "complete" {
			return nil
		}
		if _, err = tx.Exec(`DELETE FROM media_markers WHERE media_id=? AND source IN (?,?)`, e.MediaID, MarkerFromAnalysis, MarkerConfirmed); err != nil {
			return err
		}
		for _, m := range markers {
			_, err = tx.Exec(`INSERT INTO media_markers(media_id,kind,source,start_ms,end_ms,title,confidence,provenance)
 SELECT ?,?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM media_markers WHERE media_id=? AND kind=? AND source=?)`,
				e.MediaID, m.Kind, m.Source, m.StartMS, m.EndMS, m.Title, m.Confidence, m.Provenance, e.MediaID, m.Kind, MarkerFromPerson)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func segmentCurrentKey(tx *sql.Tx, e SegmentEntry) (string, error) {
	var key string
	err := tx.QueryRow(`SELECT `+segmentSourceKey+` FROM media m JOIN media_parts mp ON mp.media_id=m.id AND mp.position=0
 JOIN files f ON f.id=mp.file_id WHERE m.id=? AND m.item_id=(SELECT id FROM items WHERE public_id=?)
 AND f.missing_since_at IS NULL`, e.MediaID, e.ItemID).Scan(&key)
	return key, err
}
