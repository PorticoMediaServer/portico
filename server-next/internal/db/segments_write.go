package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SegmentChange changes only the version and revision the owner just reviewed.
type SegmentChange struct {
	Entry                  SegmentEntry
	SourceKey              string
	Revision               int64
	UserID                 int64
	RequestID, RequestHash string
	Action                 string
	Marker                 SegmentMarker
}

func (d *DB) ChangeSegmentMarker(ctx context.Context, c SegmentChange) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		if ref, err := createdBefore(tx, c.UserID, c.RequestID, c.RequestHash); err != nil || ref != "" {
			return err
		}
		e := c.Entry
		key, err := segmentCurrentKey(tx, e)
		if err != nil {
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		if key != c.SourceKey || key != e.Key() {
			return ErrConflict
		}

		_, err = tx.Exec(`INSERT INTO segment_analysis(media_id,source_key,mode,state,updated_at) VALUES(?,'','','pending',?)
 ON CONFLICT(media_id) DO NOTHING`, e.MediaID, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		var revision int64
		if err = tx.QueryRow(`SELECT revision FROM segment_analysis WHERE media_id=?`, e.MediaID).Scan(&revision); err != nil {
			return err
		}
		if revision != c.Revision {
			return ErrConflict
		}
		m := c.Marker
		if c.Action == "create" || c.Action == "edit" {
			if m.Kind == MarkerChapter || markerKindNames[m.Kind] == "" || m.StartMS < 0 || m.EndMS <= m.StartMS || m.EndMS > e.DurationMS {
				return ErrInvalid
			}
		}
		switch c.Action {
		case "create":
			_, err = tx.Exec(`INSERT INTO media_markers(media_id,kind,source,start_ms,end_ms,title,confidence,provenance)
 VALUES(?,?,?,?,?,'',1,'owner')`, e.MediaID, m.Kind, MarkerFromPerson, m.StartMS, m.EndMS)
		case "edit", "approve", "dismiss":
			var res sql.Result
			if c.Action == "edit" {
				res, err = tx.Exec(`UPDATE media_markers SET kind=?,source=?,start_ms=?,end_ms=?,dismissed=0,confidence=1,provenance='owner_edit'
 WHERE media_id=? AND id=? AND kind<>?`, m.Kind, MarkerFromPerson, m.StartMS, m.EndMS, e.MediaID, m.ID, MarkerChapter)
			} else {
				res, err = tx.Exec(`UPDATE media_markers SET source=?,dismissed=?,provenance='owner',confidence=1 WHERE media_id=? AND id=? AND kind<>?`,
					MarkerFromPerson, c.Action == "dismiss", e.MediaID, m.ID, MarkerChapter)
			}
			if err == nil {
				if n, _ := res.RowsAffected(); n == 0 {
					return ErrNotFound
				}
			}
		default:
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE segment_analysis SET revision=revision+1 WHERE media_id=?`, e.MediaID); err != nil {
			return err
		}
		return rememberCreated(tx, c.UserID, c.RequestID, c.RequestHash, fmt.Sprint(e.MediaID), time.Now())
	})
}

// RedetectSegments leaves the owner's markers in place and marks just this title unread.
func (d *DB) RedetectSegments(ctx context.Context, library, title string) (int, error) {
	return d.remeasureSegments(ctx, library, title, false)
}

func (d *DB) RetryFailedSegments(ctx context.Context, library string) (int, error) {
	return d.remeasureSegments(ctx, library, "", true)
}

func (d *DB) remeasureSegments(ctx context.Context, library, title string, failedOnly bool) (int, error) {
	count := 0
	var after int64
	for {
		entries, err := d.SegmentEntries(ctx, library, title, after, 100)
		if err != nil || len(entries) == 0 {
			return count, err
		}
		err = d.write(ctx, User, func(tx *sql.Tx) error {
			for _, e := range entries {
				if failedOnly && (e.State != "failed" || e.SourceKey != e.Key()) {
					continue
				}
				if _, err := tx.Exec(`UPDATE segment_analysis SET source_key='' WHERE media_id=? AND (?=0 OR state='failed')`, e.MediaID, failedOnly); err != nil {
					return err
				}
				count++
			}
			return nil
		})
		if err != nil {
			return count, err
		}
		after = entries[len(entries)-1].MediaID
	}
}
