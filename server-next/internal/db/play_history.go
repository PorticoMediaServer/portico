package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
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

// indexed is the part of a query an index answers: the person, the library and the period,
// with the cursor. It chooses which plays are read, newest first.
func (q PlayQuery) indexed() (string, []any) {
	where := []string{`1 = 1`}
	var args []any
	if q.AccountID != "" {
		where, args = append(where, `h.user_id = (SELECT id FROM users WHERE public_id = ?)`), append(args, q.AccountID)
	}
	if q.LibraryID != "" {
		where, args = append(where, `h.library_id = (SELECT id FROM libraries WHERE public_id = ?)`), append(args, q.LibraryID)
	}
	if !q.From.IsZero() {
		where, args = append(where, `h.played_at >= ?`), append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where, args = append(where, `h.played_at < ?`), append(args, q.To.UnixMilli())
	}
	if q.Before > 0 {
		where = append(where, `(h.played_at, h.id) < ((SELECT played_at FROM play_history WHERE id = ?), ?)`)
		args = append(args, q.Before, q.Before)
	}
	return strings.Join(where, " AND "), args
}

// filtered is the part no index answers, tested on each play read: what the asking account may
// read (a play of a removed library only by one with every library), the kind and the text.
func (q PlayQuery) filtered() (string, []any) {
	where := []string{accountSees("h.library_id")}
	args := accountSeesArgs(q.Asker)
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
	return strings.Join(where, " AND "), args
}

// playsWindow is how many plays one page of the list reads at most. A filter no index answers
// (a kind, text, or an administrator held to some libraries) is tested on the newest plays of
// the window; a page that finds fewer than it asked for still answers a cursor, and the next
// page reads the next window. At three million plays a window answers in tens of milliseconds
// whatever the filter, where reading until a page is full took seconds.
const playsWindow = 50_000

// Plays lists plays newest first. next is the cursor of the next page, 0 at the end: the last
// play of the page, or, when the window ran out before the page was full, the last play read.
func (d *DB) Plays(ctx context.Context, q PlayQuery) (out []Play, next int64, err error) {
	indexed, iargs := q.indexed()
	filtered, fargs := q.filtered()
	var edgeAt, edgeID int64
	err = d.read.QueryRowContext(ctx, `SELECT h.played_at, h.id FROM play_history h WHERE `+indexed+`
		ORDER BY h.played_at DESC, h.id DESC LIMIT 1 OFFSET ?`, append(iargs, playsWindow-1)...).Scan(&edgeAt, &edgeID)
	window := ""
	switch {
	case err == nil:
		window = ` AND (h.played_at, h.id) >= (?, ?)`
	case errors.Is(err, sql.ErrNoRows):
		err = nil // fewer plays than a window: all of them are read
	default:
		return nil, 0, err
	}
	args := append(append([]any{}, iargs...), fargs...)
	if window != "" {
		args = append(args, edgeAt, edgeID)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT h.id, h.played_at, h.account_name, h.profile_name, ifnull(u.public_id, ''),
		ifnull(l.public_id, ''), ifnull(l.name, ''), ifnull(i.public_id, ''), h.item_kind, h.title, ifnull(h.subtitle, ''), h.device_name,
		ifnull(h.watched_ms, 0), h.away, h.delivery
		FROM play_history h LEFT JOIN users u ON u.id = h.user_id LEFT JOIN libraries l ON l.id = h.library_id LEFT JOIN items i ON i.id = h.item_id
		WHERE `+indexed+` AND `+filtered+window+` ORDER BY h.played_at DESC, h.id DESC LIMIT ?`, append(args, q.Limit+1)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Play
		var at int64
		if err := rows.Scan(&p.ID, &at, &p.AccountName, &p.ProfileName, &p.AccountID, &p.LibraryID, &p.LibraryName, &p.ItemID, &p.Kind,
			&p.Title, &p.Subtitle, &p.DeviceName, &p.WatchedMS, &p.Away, &p.Delivery); err != nil {
			return nil, 0, err
		}
		p.PlayedAt = time.UnixMilli(at)
		out = append(out, p)
	}
	switch {
	case len(out) > q.Limit:
		out = out[:q.Limit]
		next = out[len(out)-1].ID
	case window != "":
		next = edgeID // the window ran out: the next page reads on from its last play
	}
	return out, next, rows.Err()
}

// PlaySummary is the top people, busiest times and top played over a range.
type PlaySummary struct {
	People []PlayCount
	Titles []PlayCount
	// Busiest counts plays by weekday (0 is Sunday) and hour, in the server's time zone.
	Busiest [7][24]int
	// Stopped says the range held more plays than are counted; CountedFrom is when the
	// oldest play counted was.
	Stopped     bool
	CountedFrom time.Time
}

// PlayCount is how much one person or one title was played.
type PlayCount struct {
	Name, Detail string
	ID           string // the account's or the title's public id, "" once it is gone
	Plays        int
	WatchedMS    int64
}

// SummaryWindow is how many plays the summary counts: the newest of its range. Top people,
// Top played and Busiest times need the plays themselves (the daily totals hold no person,
// title or hour), so they are counted from this window and the page says so; the range's
// totals come from the daily totals instead, whatever its length. At three million plays the
// three counts, run side by side, answer in a few hundred milliseconds.
const SummaryWindow = 20_000

// SummarizePlays counts top people, top played and busiest times over the newest
// SummaryWindow plays of the range. CountedFrom says when the oldest play counted was, when
// the range held more than that. The three counts are separate short reads, run at once.
func (d *DB) SummarizePlays(ctx context.Context, q PlayQuery) (PlaySummary, error) {
	var out PlaySummary
	q.Before = 0
	indexed, iargs := q.indexed()
	filtered, fargs := q.filtered()
	var edgeAt, edgeID int64
	err := d.read.QueryRowContext(ctx, `SELECT h.played_at, h.id FROM play_history h WHERE `+indexed+`
		ORDER BY h.played_at DESC, h.id DESC LIMIT 1 OFFSET ?`, append(iargs, SummaryWindow-1)...).Scan(&edgeAt, &edgeID)
	window := ""
	switch {
	case err == nil:
		window, out.Stopped, out.CountedFrom = ` AND (h.played_at, h.id) >= (?, ?)`, true, time.UnixMilli(edgeAt)
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	}
	args := append(append([]any{}, iargs...), fargs...)
	if window != "" {
		args = append(args, edgeAt, edgeID)
	}
	from := ` FROM play_history h WHERE ` + indexed + ` AND ` + filtered + window
	top := func(query string) ([]PlayCount, error) {
		rows, err := d.read.QueryContext(ctx, query, args...)
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
	busiest := func() error {
		rows, err := d.read.QueryContext(ctx, `SELECT CAST(strftime('%w', h.played_at / 1000, 'unixepoch', 'localtime') AS INTEGER),
			CAST(strftime('%H', h.played_at / 1000, 'unixepoch', 'localtime') AS INTEGER), count(*)`+from+` GROUP BY 1, 2`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day, hour, count int
			if err := rows.Scan(&day, &hour, &count); err != nil {
				return err
			}
			if day >= 0 && day < 7 && hour >= 0 && hour < 24 {
				out.Busiest[day][hour] = count
			}
		}
		return rows.Err()
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		out.People, errs[0] = top(`SELECT p.name, '', ifnull(u.public_id, ''), p.plays, p.watched FROM (SELECT h.user_id AS user_id,
			max(h.account_name) AS name, count(*) AS plays, ifnull(sum(h.watched_ms), 0) AS watched` + from + `
			GROUP BY ifnull(h.user_id, -1), CASE WHEN h.user_id IS NULL THEN h.account_name END ORDER BY plays DESC, name LIMIT 10) p
			LEFT JOIN users u ON u.id = p.user_id ORDER BY p.plays DESC, p.name`)
	}()
	go func() {
		defer wg.Done()
		out.Titles, errs[1] = top(`SELECT t.title, t.subtitle, ifnull(i.public_id, ''), t.plays, t.watched FROM (SELECT h.item_id AS item_id,
			max(h.title) AS title, ifnull(max(h.subtitle), '') AS subtitle, count(*) AS plays, ifnull(sum(h.watched_ms), 0) AS watched` + from + `
			GROUP BY ifnull(h.item_id, -1), CASE WHEN h.item_id IS NULL THEN h.title END ORDER BY plays DESC, title LIMIT 10) t
			LEFT JOIN items i ON i.id = t.item_id ORDER BY t.plays DESC, t.title`)
	}()
	go func() {
		defer wg.Done()
		errs[2] = busiest()
	}()
	wg.Wait()
	return out, errors.Join(errs...)
}
