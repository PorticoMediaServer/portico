package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// ReadInventoryDiscProbeTx reads one exact current source observation. It grants
// no content access: the caller must authorize its item/library in this same
// snapshot. A stale revision/generation/incarnation yields no result; the reader
// never silently changes the requested observation or takes a fresh remote lease.
// discQueryer is the read a caller already holds: a transaction for a write, or the
// request's read handle so the observation is read in the same authorized snapshot.
type discQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ReadInventoryDiscProbeTx(ctx context.Context, tx *sql.Tx, object, revision string) (disc.Probe, error) {
	return readInventoryDiscProbe(ctx, tx, object, revision)
}

// ReadInventoryDiscProbe reads the same exact observation through an existing read
// handle. It grants no access by itself; the caller authorizes its item and library.
func ReadInventoryDiscProbe(ctx context.Context, q discQueryer, object, revision string) (disc.Probe, error) {
	return readInventoryDiscProbe(ctx, q, object, revision)
}

func readInventoryDiscProbe(ctx context.Context, q discQueryer, object, revision string) (disc.Probe, error) {
	out := disc.Probe{Schema: disc.Schema, Titles: []disc.Title{}}
	var fingerprint string
	err := q.QueryRowContext(ctx, `SELECT d.kind,d.packaging,d.encryption,d.fingerprint
 FROM inventory_disc_facts d JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=d.asset_id
 JOIN library_sources s ON s.id=o.source_id
 WHERE d.object_id=? AND d.source_revision=? AND o.revision=d.source_revision
 AND d.source_generation=s.generation AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation
 AND s.enabled=1 AND s.kind='local' AND o.retired=0 AND o.state='available'`, object, revision).Scan(&out.Kind, &out.Packaging, &out.Encryption, &fingerprint)
	if err != nil {
		return disc.Probe{}, err
	}
	// Fixed query count and bounded result sets, independent of title count.
	read := func(query string, limit int, scan func(*sql.Rows) error) error {
		rows, err := q.QueryContext(ctx, query, object, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
			if count > limit {
				return disc.ErrProtocol
			}
			if err = scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	byKey := map[string]int{}
	err = read(`SELECT title_key,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc FROM inventory_disc_titles WHERE object_id=? ORDER BY disc_order LIMIT ?`, disc.MaxTitles, func(rows *sql.Rows) error {
		var key string
		var title disc.Title
		if err := rows.Scan(&key, &title.DiscOrder, &title.DurationMs, &title.AngleCount, &title.Playlist, &title.Title, &title.VTS, &title.PGC); err != nil {
			return err
		}
		if _, exists := byKey[key]; exists || key != title.Key(out.Kind) {
			return disc.ErrProtocol
		}
		if title.Playlist != nil {
			title.PlaylistFile = fmt.Sprintf("%05d.mpls", *title.Playlist)
		}
		title.Clips, title.Streams, title.Chapters = []disc.Clip{}, []disc.Stream{}, []disc.Chapter{}
		byKey[key] = len(out.Titles)
		out.Titles = append(out.Titles, title)
		return nil
	})
	if err != nil {
		return disc.Probe{}, err
	}
	const maxRows = 65536
	err = read(`SELECT title_key,sequence,file,in_time_90k,out_time_90k FROM inventory_disc_clips WHERE object_id=? ORDER BY title_key,sequence LIMIT ?`, maxRows, func(rows *sql.Rows) error {
		var key string
		var clip disc.Clip
		if err := rows.Scan(&key, &clip.Sequence, &clip.File, &clip.InTime90k, &clip.OutTime90k); err != nil {
			return err
		}
		index, exists := byKey[key]
		if !exists || clip.Sequence != len(out.Titles[index].Clips) {
			return disc.ErrProtocol
		}
		out.Titles[index].Clips = append(out.Titles[index].Clips, clip)
		return nil
	})
	if err != nil {
		return disc.Probe{}, err
	}
	err = read(`SELECT title_key,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_enhancement_known,dv_base_pid,video_role FROM inventory_disc_streams WHERE object_id=? ORDER BY title_key,stream_order LIMIT ?`, maxRows, func(rows *sql.Rows) error {
		var key string
		var order int
		var stream disc.Stream
		if err := rows.Scan(&key, &order, &stream.ClipSequence, &stream.PID, &stream.Kind, &stream.Coding, &stream.Language, &stream.DolbyVisionEnhancementLayer, &stream.DolbyVisionEnhancementKnown, &stream.DolbyVisionBasePID, &stream.VideoRole); err != nil {
			return err
		}
		index, exists := byKey[key]
		if !exists || order != len(out.Titles[index].Streams) {
			return disc.ErrProtocol
		}
		out.Titles[index].Streams = append(out.Titles[index].Streams, stream)
		return nil
	})
	if err != nil {
		return disc.Probe{}, err
	}
	err = read(`SELECT title_key,chapter_order,start_ms FROM inventory_disc_chapters WHERE object_id=? ORDER BY title_key,chapter_order LIMIT ?`, maxRows, func(rows *sql.Rows) error {
		var key string
		var order int
		var chapter disc.Chapter
		if err := rows.Scan(&key, &order, &chapter.StartMs); err != nil {
			return err
		}
		index, exists := byKey[key]
		if !exists || order != len(out.Titles[index].Chapters) {
			return disc.ErrProtocol
		}
		out.Titles[index].Chapters = append(out.Titles[index].Chapters, chapter)
		return nil
	})
	if err != nil {
		return disc.Probe{}, err
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > disc.MaxOutputBytes {
		return disc.Probe{}, disc.ErrProtocol
	}
	digest := sha256.Sum256(append([]byte("disc_structure_v1\x00"), raw...))
	if hex.EncodeToString(digest[:]) != fingerprint {
		return disc.Probe{}, disc.ErrProtocol
	}
	return disc.DecodeProbe(raw)
}
