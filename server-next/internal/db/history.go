package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// HistoryEntry is one finished play as the profile itself is shown it. Item is nil when the
// title has left the server: the entry keeps its name and cannot be opened.
type HistoryEntry struct {
	ID        string
	PlayedAt  int64
	WatchedMS int64
	Kind      ItemKind
	Title     string
	Subtitle  string // the show, album or book the title belongs to
	Item      *Item
}

// HistoryPage is one page of a profile's History, newest first.
type HistoryPage struct {
	Entries    []HistoryEntry
	NextCursor string
}

// historyCursor is the last entry sent: its time and row.
type historyCursor struct {
	At int64 `json:"t"`
	ID int64 `json:"i"`
}

// An entry's public id is its row number with a prefix. Clients never parse it, and removing
// one checks that it is the asking profile's.
const historyPrefix = "hst_"

func historyID(id int64) string { return historyPrefix + strconv.FormatInt(id, 36) }

func parseHistoryID(text string) (int64, bool) {
	if !strings.HasPrefix(text, historyPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(text, historyPrefix), 36, 64)
	return id, err == nil && id > 0
}

// historyShown is what the profile itself is shown of its plays: not the plays made while it
// had history paused, not the ones it removed, nothing from before it last cleared History,
// and no title it may no longer see. A title that left the server stays, by name.
const historyShown = `h.profile_id = @viewer_profile AND h.hidden_from_profile = 0
	AND h.played_at > ifnull((SELECT history_cleared_at FROM profiles WHERE id = @viewer_profile), 0)`

// History lists the viewer's own finished plays since a time (0 for all), newest first. The
// page starts after the cursor, so a deep page costs what the first does.
func (d *DB) History(ctx context.Context, v Viewer, since int64, cursor string, limit int) (HistoryPage, error) {
	var page HistoryPage
	after := historyCursor{At: 1<<62 - 1, ID: 1<<62 - 1}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID <= 0 {
			return page, invalid("the cursor does not belong to this list")
		}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return page, err
	}
	type row struct {
		HistoryEntry
		id, itemID int64
	}
	rows, err := queryAll(ctx, tx, `SELECT h.id, h.played_at, ifnull(h.watched_ms, 0), h.item_kind, h.title, ifnull(h.subtitle, ''),
		ifnull(h.item_id, 0) FROM play_history h LEFT JOIN items i ON i.id = h.item_id
		WHERE `+historyShown+` AND h.played_at > @since AND h.played_at <= @at AND (h.played_at < @at OR h.id < @id)
		AND (h.item_id IS NULL OR `+acc.visible("i")+`)
		ORDER BY h.played_at DESC, h.id DESC LIMIT @limit`,
		append(v.args(), sql.Named("since", since), sql.Named("at", after.At), sql.Named("id", after.ID), sql.Named("limit", limit+1)),
		func(r *row) []any {
			return []any{&r.id, &r.PlayedAt, &r.WatchedMS, &r.Kind, &r.Title, &r.Subtitle, &r.itemID}
		})
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		raw, _ := json.Marshal(historyCursor{At: last.PlayedAt, ID: last.id})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}

	// The titles still on the server, with the viewer's state on each: where it stopped.
	var ids []int64
	for _, r := range rows {
		if r.itemID != 0 {
			ids = append(ids, r.itemID)
		}
	}
	items := map[int64]*Item{}
	if len(ids) > 0 {
		list, err := queryAll(ctx, tx, `SELECT `+itemColumns+itemFrom+`i.id IN `+intList(ids), v.args(),
			func(it *Item) []any { return it.dest() })
		if err != nil {
			return page, err
		}
		if err := loadImages(ctx, tx, list); err != nil {
			return page, err
		}
		for n := range list {
			items[list[n].ID] = &list[n]
		}
	}
	page.Entries = make([]HistoryEntry, len(rows))
	for n, r := range rows {
		r.HistoryEntry.ID = historyID(r.id)
		r.HistoryEntry.Item = items[r.itemID]
		page.Entries[n] = r.HistoryEntry
	}
	return page, nil
}

// RemoveHistoryEntry takes one play out of the viewer's History. The owner's play record
// keeps it. Removing one already removed changes nothing; another profile's is not found.
func (d *DB) RemoveHistoryEntry(ctx context.Context, v Viewer, entryID string) error {
	id, ok := parseHistoryID(entryID)
	if !ok {
		return ErrNotFound
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM play_history WHERE id = ? AND profile_id = ?)`, id, v.ProfileID).
			Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		_, err := tx.Exec(`UPDATE play_history SET hidden_from_profile = 1 WHERE id = ? AND profile_id = ?`, id, v.ProfileID)
		return err
	})
}

// ClearHistory empties the viewer's History. It is one write however long the history is:
// History shows nothing played before this moment. The owner's play record, watched marks,
// resume points, play counts and ratings all stay.
func (d *DB) ClearHistory(ctx context.Context, v Viewer, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE profiles SET history_cleared_at = max(ifnull(history_cleared_at, 0), ?) WHERE id = ?`,
			now.UnixMilli(), v.ProfileID)
		return err
	})
}
