package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The owner's Play history: every finished play on the server, for the owner and the
// administrators. It is in time order only. A filter by kind or text, and the summary, read
// the plays of their range: an accepted cost on this page, bounded at a million plays.

// Play is one finished play.
type Play struct {
	ID          int64
	PlayedAt    time.Time
	AccountName string
	ProfileName string
	// AccountID and LibraryID are the public ids, "" once the account or library is gone.
	AccountID, LibraryID string
	LibraryName          string
	// ItemID is the title's public id, "" once it is gone; Title and Subtitle stay.
	ItemID     string
	Kind       ItemKind
	Title      string
	Subtitle   string
	DeviceName string
	WatchedMS  int64
	Away       bool
	Delivery   PlayDelivery
}

// PlayQuery picks plays. Times bound played_at; zero is no bound.
type PlayQuery struct {
	// Asker is who is reading: an administrator whose account is held to some libraries
	// reads only the plays in those (and plays whose library is gone).
	Asker     int64
	AccountID string
	LibraryID string
	Kinds     []ItemKind
	Text      string
	From, To  time.Time
	// Before is the id of the last play of the page before, 0 for the newest.
	Before int64
	Limit  int
}

func (q PlayQuery) where() (string, []any) {
	where := []string{`1 = 1`}
	var args []any
	where = append(where, `(h.library_id IS NULL OR (SELECT all_libraries FROM users WHERE id = ?) = 1
		OR h.library_id IN (SELECT library_id FROM library_access WHERE user_id = ?))`)
	args = append(args, q.Asker, q.Asker)
	if q.AccountID != "" {
		where, args = append(where, `h.user_id = (SELECT id FROM users WHERE public_id = ?)`), append(args, q.AccountID)
	}
	if q.LibraryID != "" {
		where, args = append(where, `h.library_id = (SELECT id FROM libraries WHERE public_id = ?)`), append(args, q.LibraryID)
	}
	if len(q.Kinds) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",")
		where = append(where, `h.item_kind IN (`+marks+`)`)
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if q.Text != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Text)
		where = append(where, `(h.title LIKE ? ESCAPE '\' OR h.subtitle LIKE ? ESCAPE '\')`)
		args = append(args, "%"+escaped+"%", "%"+escaped+"%")
	}
	if !q.From.IsZero() {
		where, args = append(where, `h.played_at >= ?`), append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where, args = append(where, `h.played_at < ?`), append(args, q.To.UnixMilli())
	}
	return strings.Join(where, " AND "), args
}

// Plays lists plays newest first.
func (d *DB) Plays(ctx context.Context, q PlayQuery) ([]Play, error) {
	where, args := q.where()
	if q.Before > 0 {
		where += ` AND (h.played_at, h.id) < ((SELECT played_at FROM play_history WHERE id = ?), ?)`
		args = append(args, q.Before, q.Before)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT h.id, h.played_at, h.account_name, h.profile_name, ifnull(u.public_id, ''),
		ifnull(l.public_id, ''), ifnull(l.name, ''), ifnull(i.public_id, ''), h.item_kind, h.title, ifnull(h.subtitle, ''), h.device_name,
		ifnull(h.watched_ms, 0), h.away, h.delivery
		FROM play_history h LEFT JOIN users u ON u.id = h.user_id LEFT JOIN libraries l ON l.id = h.library_id LEFT JOIN items i ON i.id = h.item_id
		WHERE `+where+` ORDER BY h.played_at DESC, h.id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Play
	for rows.Next() {
		var p Play
		var at int64
		if err := rows.Scan(&p.ID, &at, &p.AccountName, &p.ProfileName, &p.AccountID, &p.LibraryID, &p.LibraryName, &p.ItemID, &p.Kind,
			&p.Title, &p.Subtitle, &p.DeviceName, &p.WatchedMS, &p.Away, &p.Delivery); err != nil {
			return nil, err
		}
		p.PlayedAt = time.UnixMilli(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// summaryCap is the most plays a summary reads.
const summaryCap = 1_000_000

// PlaySummary is the top people, busiest times and top played over a range.
type PlaySummary struct {
	People []PlayCount
	Titles []PlayCount
	// Busiest counts plays by weekday (0 is Sunday) and hour, in the server's time zone.
	Busiest [7][24]int
	// Stopped says the range held more than a million plays and only the newest were read.
	Stopped bool
}

// PlayCount is how much one person or one title was played.
type PlayCount struct {
	Name, Detail string
	ID           string // the account's or the title's public id, "" once it is gone
	Plays        int
	WatchedMS    int64
}

// SummarizePlays reads the summary of a range's plays, the newest million at most.
func (d *DB) SummarizePlays(ctx context.Context, q PlayQuery) (PlaySummary, error) {
	var out PlaySummary
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	where, args := q.where()
	plays := `(SELECT h.* FROM play_history h WHERE ` + where + ` ORDER BY h.played_at DESC LIMIT ` + itoa(summaryCap) + `)`
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM play_history h WHERE `+where+` LIMIT `+itoa(summaryCap+1)+`)`,
		args...).Scan(&n); err != nil {
		return out, err
	}
	out.Stopped = n > summaryCap
	top := func(query string) ([]PlayCount, error) {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var list []PlayCount
		for rows.Next() {
			var c PlayCount
			if err := rows.Scan(&c.Name, &c.Detail, &c.ID, &c.Plays, &c.WatchedMS); err != nil {
				return nil, err
			}
			list = append(list, c)
		}
		return list, rows.Err()
	}
	if out.People, err = top(`SELECT p.account_name, '', ifnull(u.public_id, ''), count(*), ifnull(sum(p.watched_ms), 0) FROM ` + plays + ` p
		LEFT JOIN users u ON u.id = p.user_id GROUP BY ifnull(p.user_id, -1), p.account_name ORDER BY count(*) DESC, 1 LIMIT 10`); err != nil {
		return out, err
	}
	if out.Titles, err = top(`SELECT p.title, ifnull(p.subtitle, ''), ifnull(i.public_id, ''), count(*), ifnull(sum(p.watched_ms), 0) FROM ` + plays + ` p
		LEFT JOIN items i ON i.id = p.item_id GROUP BY ifnull(p.item_id, -1), p.title, p.subtitle ORDER BY count(*) DESC, 1 LIMIT 10`); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT CAST(strftime('%w', played_at / 1000, 'unixepoch', 'localtime') AS INTEGER),
		CAST(strftime('%H', played_at / 1000, 'unixepoch', 'localtime') AS INTEGER), count(*) FROM `+plays+` GROUP BY 1, 2`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var day, hour, count int
		if err := rows.Scan(&day, &hour, &count); err != nil {
			return out, err
		}
		if day >= 0 && day < 7 && hour >= 0 && hour < 24 {
			out.Busiest[day][hour] = count
		}
	}
	return out, rows.Err()
}
