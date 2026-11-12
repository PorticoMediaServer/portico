package db

import (
	"context"
	"database/sql"
	"time"
)

// RecordedTitle is a recording's title in the Recorded TV library as one viewer sees it: its
// ids, its length, and the viewer's own place in it.
type RecordedTitle struct {
	PublicID   string
	LibraryID  string
	DurationMS int64
	PositionMS int64
	Watched    bool
}

// RecordedTitles reads the recordings' titles the viewer may see, by the same condition as
// VisibleItems, with the viewer's place in each. It reads as many rows as it is given ids.
func (d *DB) RecordedTitles(ctx context.Context, v Viewer, itemIDs []int64) (map[int64]RecordedTitle, error) {
	out := map[int64]RecordedTitle{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(itemIDs); start += 200 {
		part := itemIDs[start:min(start+200, len(itemIDs))]
		rows, err := tx.QueryContext(ctx, `SELECT i.id, i.public_id, l.public_id, ifnull(i.duration_ms, 0), ifnull(s.position_ms, 0), ifnull(s.played, 0)
			FROM items i JOIN libraries l ON l.id = i.library_id
			LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
			WHERE i.id IN `+intList(part)+` AND i.available = 1 AND `+acc.visible("i"), v.args()...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var t RecordedTitle
			if err := rows.Scan(&id, &t.PublicID, &t.LibraryID, &t.DurationMS, &t.PositionMS, &t.Watched); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = t
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LetGoOfOtherChannels gives up a rule's bookings not yet started on any channel but the one
// it now follows, after its channel changed: each passes to the next person who also asked
// for it, as a cancel by its booker does, and is cancelled only when nobody is left. A rule
// that follows every channel (channelID 0) keeps them all.
func (d *DB) LetGoOfOtherChannels(ctx context.Context, ruleID, channelID int64, now time.Time) error {
	if channelID == 0 {
		return nil
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT `+recordingColumns+` FROM recordings r WHERE r.rule_id = ? AND r.state = ? AND ifnull(r.channel_id, 0) <> ?`,
			ruleID, RecordingScheduled, channelID)
		if err != nil {
			return err
		}
		var held []Recording
		for rows.Next() {
			r, err := scanRecording(rows)
			if err != nil {
				rows.Close()
				return err
			}
			held = append(held, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range held {
			if err := passOn(tx, r, now); err != nil {
				return err
			}
		}
		return nil
	})
}
