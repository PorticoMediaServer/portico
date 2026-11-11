package db

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"
)

// QueueMax is how many entries a queue holds at once. A longer selection is kept as a
// selection and the queue is refilled from it as the listener nears the end. It is a
// variable so that tests can fill a queue without ten thousand rows.
var QueueMax = 10_000

const (
	// queueStep is the gap left between positions, so an insert or a move rewrites few rows.
	queueStep = 1024
	// queueIdle is how long a queue nobody plays is kept.
	queueIdle = 30 * 24 * time.Hour
)

var (
	// ErrStale: the edit was made on an out-of-date copy. The app rereads and the person edits again.
	ErrStale = errors.New("edited on an out-of-date copy")
	// ErrQueueFull: the queue already holds as much as a queue can.
	ErrQueueFull = errors.New("the queue is full")
	// ErrQueueEnded: there is nothing after this in the queue.
	ErrQueueEnded = errors.New("the queue has ended")
)

// Queue media. A device keeps one queue of each.
const (
	MediumAudio = "audio"
	MediumVideo = "video"
)

// Queue is a play queue as its owner sees it now: only titles the profile may see are
// counted, shown and played.
type Queue struct {
	ID       int64
	PublicID string
	DeviceID int64
	Medium   string
	Repeat   string
	Shuffled bool
	// Selection is what a queue too long to hold at once was built from. See playqueue.
	Selection string
	Revision  int64
	Total     int
	// CurrentEntry is the entry being played, 0 for none, and CurrentIndex where it stands.
	CurrentEntry int64
	CurrentIndex int
	// Remaining counts the entries after the current one, and RemainingMS how long they run.
	Remaining   int
	RemainingMS int64
	// NextEntry is what plays when the current entry ends, 0 at the end.
	NextEntry int64
	PlayedAt  int64

	currentPosition int64
	seed            sql.NullInt64
}

// QueueEntry is one entry of a queue.
type QueueEntry struct {
	EntryID int64
	// Index is where the entry stands among those the viewer may see, from 0.
	Index int
	// Added is true for what a person added by hand, as against what came with the album,
	// show or list being played.
	Added     bool
	Available bool
	Item      Item
	// Parent and Root name what the title belongs to: its season and show, its album and artist.
	ParentTitle string
	RootTitle   string
}

func queueByID(ctx context.Context, tx *sql.Tx, v Viewer, acc access, publicID string) (Queue, error) {
	var q Queue
	var selection sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT q.id, q.public_id, q.device_id, q.medium, q.repeat, q.shuffle_seed, q.selection, q.revision,
		ifnull(q.current_entry_id, 0), ifnull((SELECT position FROM queue_entries WHERE id = q.current_entry_id), -1), q.played_at
		FROM play_queues q WHERE q.public_id = ? AND q.profile_id = ?`, publicID, v.ProfileID).
		Scan(&q.ID, &q.PublicID, &q.DeviceID, &q.Medium, &q.Repeat, &q.seed, &selection, &q.Revision, &q.CurrentEntry, &q.currentPosition, &q.PlayedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	q.Shuffled, q.Selection = q.seed.Valid, selection.String
	var before, after, afterMS sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT count(*), sum(e.position < @cur), sum(e.position > @cur),
		sum(CASE WHEN e.position > @cur THEN ifnull(i.duration_ms, 0) END)
		FROM queue_entries e JOIN items i ON i.id = e.item_id WHERE e.queue_id = @queue AND `+acc.visible("i"),
		append(v.args(), sql.Named("cur", q.currentPosition), sql.Named("queue", q.ID))...).Scan(&q.Total, &before, &after, &afterMS)
	if err != nil {
		return q, err
	}
	q.CurrentIndex, q.Remaining, q.RemainingMS = -1, int(after.Int64), afterMS.Int64
	if q.CurrentEntry != 0 {
		q.CurrentIndex = int(before.Int64)
	}
	if q.NextEntry, err = stepTarget(ctx, tx, v, acc, q, "completion"); errors.Is(err, ErrQueueEnded) {
		err = nil
	}
	return q, err
}

// stepTarget finds the entry a step lands on, among those the viewer may see.
func stepTarget(ctx context.Context, tx *sql.Tx, v Viewer, acc access, q Queue, reason string) (int64, error) {
	find := func(where, order string) (int64, error) {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT e.id FROM queue_entries e JOIN items i ON i.id = e.item_id
			WHERE e.queue_id = @queue AND `+acc.visible("i")+where+` ORDER BY e.position `+order+` LIMIT 1`,
			append(v.args(), sql.Named("cur", q.currentPosition), sql.Named("queue", q.ID))...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrQueueEnded
		}
		return id, err
	}
	switch {
	case reason == "previous":
		if id, err := find(` AND e.position < @cur`, "DESC"); !errors.Is(err, ErrQueueEnded) {
			return id, err
		}
		if q.CurrentEntry != 0 {
			return q.CurrentEntry, nil // the first entry starts again
		}
		return 0, ErrQueueEnded
	case reason == "completion" && q.Repeat == "one" && q.CurrentEntry != 0:
		return q.CurrentEntry, nil
	}
	id, err := find(` AND e.position > @cur`, "ASC")
	if errors.Is(err, ErrQueueEnded) && q.Repeat == "all" {
		return find("", "ASC")
	}
	return id, err
}

// Queue returns one of the profile's queues.
func (d *DB) Queue(ctx context.Context, v Viewer, publicID string) (Queue, error) {
	_, q, err := d.QueueEntries(ctx, v, publicID, 0, 0)
	return q, err
}

// DeviceQueues lists the queues a profile has on one device: at most one of music and one
// of video.
func (d *DB) DeviceQueues(ctx context.Context, v Viewer, deviceID int64) ([]Queue, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT public_id FROM play_queues WHERE profile_id = ? AND device_id = ? ORDER BY played_at DESC`,
		v.ProfileID, deviceID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []Queue
	for _, id := range ids {
		q, err := queueByID(ctx, tx, v, acc, id)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

// QueueEntries returns a queue and up to limit of its entries from the one at index from.
func (d *DB) QueueEntries(ctx context.Context, v Viewer, publicID string, from, limit int) ([]QueueEntry, Queue, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, Queue{}, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, Queue{}, err
	}
	q, err := queueByID(ctx, tx, v, acc, publicID)
	if err != nil || limit <= 0 {
		return nil, q, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+`, e.id, e.added, i.available, ifnull(p.title, ''), ifnull(r.title, '')
		FROM queue_entries e JOIN items i ON i.id = e.item_id
		LEFT JOIN items p ON p.id = i.parent_id LEFT JOIN items r ON r.id = i.root_id
		LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
		WHERE e.queue_id = @queue AND `+acc.visible("i")+` ORDER BY e.position LIMIT @limit OFFSET @from`,
		append(v.args(), sql.Named("queue", q.ID), sql.Named("limit", min(limit, maxPageSize)), sql.Named("from", max(from, 0)))...)
	if err != nil {
		return nil, q, err
	}
	defer rows.Close()
	var out []QueueEntry
	var items []Item
	for rows.Next() {
		e := QueueEntry{Index: max(from, 0) + len(out)}
		if err := scanItem(rows, &e.Item, &e.EntryID, &e.Added, &e.Available, &e.ParentTitle, &e.RootTitle); err != nil {
			return nil, q, err
		}
		out = append(out, e)
		items = append(items, e.Item)
	}
	if err := rows.Err(); err != nil {
		return nil, q, err
	}
	rows.Close()
	// The same title can be queued twice, so pictures are loaded once per title.
	if err := loadImages(ctx, tx, items); err != nil {
		return nil, q, err
	}
	images := map[int64][]Image{}
	for _, item := range items {
		if len(item.Images) > 0 {
			images[item.ID] = item.Images
		}
	}
	for n := range out {
		out[n].Item.Images = images[out[n].Item.ID]
	}
	return out, q, nil
}

// QueueEntryItem returns the title one entry of the profile's queue plays.
func (d *DB) QueueEntryItem(ctx context.Context, v Viewer, publicID string, entryID int64) (itemPublicID string, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT i.public_id FROM queue_entries e JOIN play_queues q ON q.id = e.queue_id
		JOIN items i ON i.id = e.item_id WHERE q.public_id = ? AND q.profile_id = ? AND e.id = ?`, publicID, v.ProfileID, entryID).Scan(&itemPublicID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return itemPublicID, err
}

// playOrder is the order n entries play in when shuffled: first stays first and the rest
// follow in an order the seed decides.
func playOrder(n, first int, seed int64) []int {
	order := make([]int, 0, n)
	for i := range n {
		if i != first {
			order = append(order, i)
		}
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
	if first >= 0 && first < n {
		order = append([]int{first}, order...)
	}
	return order
}

// NewQueue is a queue to make. Items are in the order of what they came from; Start is the
// index of the one to play first. With ShuffleSeed set, Start plays first and the rest follow
// shuffled, while the original order is kept for when shuffle is turned off.
type NewQueue struct {
	Viewer      Viewer
	DeviceID    int64
	Items       []int64
	Start       int
	ShuffleSeed *int64
	// InOrder says Items are already in the order to play: a slice of a shuffled selection.
	InOrder   bool
	Selection string
	Now       time.Time
}

// CreateQueue makes a queue and replaces the one of the same medium the profile had on the
// device. Queues nobody has played for 30 days are removed as it goes.
func (d *DB) CreateQueue(ctx context.Context, n NewQueue) (Queue, error) {
	var out Queue
	if len(n.Items) == 0 {
		return out, ErrQueueEnded
	}
	if len(n.Items) > QueueMax {
		return out, ErrQueueFull
	}
	publicID := NewID("que")
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var kind ItemKind
		if err := tx.QueryRow(`SELECT kind FROM items WHERE id = ?`, n.Items[min(max(n.Start, 0), len(n.Items)-1)]).Scan(&kind); err != nil {
			return err
		}
		medium := MediumVideo
		if kind == KindTrack || kind == KindAudiobook {
			medium = MediumAudio
		}
		at := n.Now.UnixMilli()
		if _, err := tx.Exec(`DELETE FROM play_queues WHERE (profile_id = ? AND device_id = ? AND medium = ?) OR played_at < ?`,
			n.Viewer.ProfileID, n.DeviceID, medium, n.Now.Add(-queueIdle).UnixMilli()); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO play_queues (public_id, profile_id, device_id, medium, shuffle_seed, selection, created_at, played_at)
			VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, ?)`, publicID, n.Viewer.ProfileID, n.DeviceID, medium, n.ShuffleSeed, n.Selection, at, at)
		if err != nil {
			return err
		}
		queueID, _ := res.LastInsertId()
		order := make([]int, len(n.Items))
		for i := range order {
			order[i] = i
		}
		if n.ShuffleSeed != nil && !n.InOrder {
			order = playOrder(len(n.Items), n.Start, *n.ShuffleSeed)
		}
		b := newBatchTx(tx)
		defer b.close()
		var current int64
		for place, index := range order {
			res, err := b.exec(`INSERT INTO queue_entries (queue_id, item_id, position, source_position) VALUES (?, ?, ?, ?)`,
				queueID, n.Items[index], int64(place+1)*queueStep, int64(index+1)*queueStep)
			if err != nil {
				return err
			}
			if index == n.Start {
				current, _ = res.LastInsertId()
			}
		}
		if _, err := tx.Exec(`UPDATE play_queues SET current_entry_id = ? WHERE id = ?`, current, queueID); err != nil {
			return err
		}
		acc, err := loadAccess(ctx, tx, n.Viewer)
		if err != nil {
			return err
		}
		out, err = queueByID(ctx, tx, n.Viewer, acc, publicID)
		return err
	})
	return out, err
}

// queueEdit is one change to a queue, run inside the transaction that checks the revision.
type queueEdit struct {
	ctx context.Context
	tx  *sql.Tx
	v   Viewer
	acc access
	q   Queue
	now time.Time
}

// editQueue runs one change to a queue. With ifMatch not zero, the change is refused as
// stale unless that is the queue's revision. Every change moves the revision on.
func (d *DB) editQueue(ctx context.Context, v Viewer, publicID string, ifMatch int64, now time.Time, fn func(e *queueEdit) error) (Queue, error) {
	var out Queue
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		q, err := queueByID(ctx, tx, v, acc, publicID)
		if err != nil {
			return err
		}
		if ifMatch != 0 && ifMatch != q.Revision {
			return ErrStale
		}
		if err := fn(&queueEdit{ctx, tx, v, acc, q, now}); err != nil {
			return err
		}
		// While a queue is not shuffled, the order it plays in is its original order.
		if _, err := tx.Exec(`UPDATE queue_entries SET source_position = position WHERE queue_id = ?1 AND source_position <> position
			AND (SELECT shuffle_seed FROM play_queues WHERE id = ?1) IS NULL`, q.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE play_queues SET revision = revision + 1 WHERE id = ?`, q.ID); err != nil {
			return err
		}
		out, err = queueByID(ctx, tx, v, acc, publicID)
		return err
	})
	return out, err
}

func (e *queueEdit) entryPosition(entryID int64) (position, source int64, err error) {
	err = e.tx.QueryRow(`SELECT position, source_position FROM queue_entries WHERE id = ? AND queue_id = ?`, entryID, e.q.ID).Scan(&position, &source)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return position, source, err
}

// respace gives every entry a fresh position with the usual gaps, keeping the order.
func (e *queueEdit) respace(column string) error {
	rows, err := e.tx.Query(`SELECT id FROM queue_entries WHERE queue_id = ? ORDER BY `+column+`, id`, e.q.ID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	b := newBatchTx(e.tx)
	defer b.close()
	for n, id := range ids {
		if _, err := b.exec(`UPDATE queue_entries SET `+column+` = ? WHERE id = ?`, int64(n+1)*queueStep, id); err != nil {
			return err
		}
	}
	return nil
}

// room finds count free positions after the given one, respacing the queue if they are not there.
func (e *queueEdit) room(after int64, count int, entryAfter int64) (first, step int64, err error) {
	next := func() (int64, error) {
		var n sql.NullInt64
		err := e.tx.QueryRow(`SELECT min(position) FROM queue_entries WHERE queue_id = ? AND position > ?`, e.q.ID, after).Scan(&n)
		if !n.Valid {
			return after + int64(count+1)*queueStep, err
		}
		return n.Int64, err
	}
	following, err := next()
	if err != nil {
		return 0, 0, err
	}
	if following-after <= int64(count) {
		if err := e.respace("position"); err != nil {
			return 0, 0, err
		}
		// Each gap is now queueStep wide; shift everything after the place to make room.
		if entryAfter != 0 {
			if after, _, err = e.entryPosition(entryAfter); err != nil {
				return 0, 0, err
			}
		} else {
			after = 0
		}
		if _, err := e.tx.Exec(`UPDATE queue_entries SET position = position + ? WHERE queue_id = ? AND position > ?`,
			int64(count+1)*queueStep, e.q.ID, after); err != nil {
			return 0, 0, err
		}
		if following, err = next(); err != nil {
			return 0, 0, err
		}
	}
	step = (following - after) / int64(count+1)
	return after + step, step, nil
}

// QueuePlace says where added entries go: straight after the current entry (Next), after
// one entry (After), or at the end.
type QueuePlace struct {
	Next  bool
	After int64
}

// AddToQueue adds titles to a queue, marked as added by the person. A queue that would then
// hold more than it can is refused whole.
func (d *DB) AddToQueue(ctx context.Context, v Viewer, publicID string, ifMatch int64, items []int64, place QueuePlace, now time.Time) (Queue, error) {
	return d.editQueue(ctx, v, publicID, ifMatch, now, func(e *queueEdit) error { return e.insert(items, place, true) })
}

func (e *queueEdit) insert(items []int64, place QueuePlace, added bool) error {
	var held int
	if err := e.tx.QueryRow(`SELECT count(*) FROM queue_entries WHERE queue_id = ?`, e.q.ID).Scan(&held); err != nil {
		return err
	}
	if held+len(items) > QueueMax {
		return ErrQueueFull
	}
	after, sourceAfter, anchor := int64(0), int64(0), int64(0)
	var err error
	switch {
	case place.After != 0:
		anchor = place.After
	case place.Next && e.q.CurrentEntry != 0:
		anchor = e.q.CurrentEntry
	default:
		if err = e.tx.QueryRow(`SELECT ifnull(max(position), 0), ifnull(max(source_position), 0) FROM queue_entries WHERE queue_id = ?`,
			e.q.ID).Scan(&after, &sourceAfter); err != nil {
			return err
		}
	}
	if anchor != 0 {
		if after, sourceAfter, err = e.entryPosition(anchor); err != nil {
			return err
		}
	}
	first, step, err := e.room(after, len(items), anchor)
	if err != nil {
		return err
	}
	b := newBatchTx(e.tx)
	defer b.close()
	for n, item := range items {
		position := first + int64(n)*step
		// In a queue that is not shuffled the two orders are the same. In a shuffled one the
		// added entry keeps its place beside what it was added after when shuffle goes off.
		source := position
		if e.q.Shuffled {
			source = sourceAfter + 1 + int64(n)
		}
		if _, err := b.exec(`INSERT INTO queue_entries (queue_id, item_id, position, source_position, added) VALUES (?, ?, ?, ?, ?)`,
			e.q.ID, item, position, source, added); err != nil {
			return err
		}
	}
	return nil
}

// RemoveFromQueue takes one entry out. The entry that is playing cannot be removed
// (ErrConflict): the person skips to another first. Removing one that is already gone
// changes nothing.
func (d *DB) RemoveFromQueue(ctx context.Context, v Viewer, publicID string, ifMatch, entryID int64, now time.Time) (Queue, error) {
	return d.editQueue(ctx, v, publicID, ifMatch, now, func(e *queueEdit) error {
		if entryID == e.q.CurrentEntry {
			return ErrConflict
		}
		_, err := e.tx.Exec(`DELETE FROM queue_entries WHERE id = ? AND queue_id = ?`, entryID, e.q.ID)
		return err
	})
}

// MoveInQueue moves one entry to just before or just after another.
func (d *DB) MoveInQueue(ctx context.Context, v Viewer, publicID string, ifMatch, entryID, before, after int64, now time.Time) (Queue, error) {
	return d.editQueue(ctx, v, publicID, ifMatch, now, func(e *queueEdit) error {
		if _, _, err := e.entryPosition(entryID); err != nil {
			return err
		}
		if before == entryID || after == entryID {
			return nil
		}
		place := after
		if before != 0 {
			// Just before one entry is just after the one ahead of it.
			position, _, err := e.entryPosition(before)
			if err != nil {
				return err
			}
			err = e.tx.QueryRow(`SELECT id FROM queue_entries WHERE queue_id = ? AND position < ? AND id <> ? ORDER BY position DESC LIMIT 1`,
				e.q.ID, position, entryID).Scan(&place)
			if errors.Is(err, sql.ErrNoRows) {
				place, err = 0, nil
			}
			if err != nil {
				return err
			}
		}
		// Taken out of the order first, so it is not in its own way.
		if _, err := e.tx.Exec(`UPDATE queue_entries SET position = -1 WHERE id = ?`, entryID); err != nil {
			return err
		}
		afterPosition := int64(0)
		if place != 0 {
			var err error
			if afterPosition, _, err = e.entryPosition(place); err != nil {
				return err
			}
		}
		position, _, err := e.room(afterPosition, 1, place)
		if err != nil {
			return err
		}
		sync := ""
		if !e.q.Shuffled {
			sync = ", source_position = ?1"
		}
		_, err = e.tx.Exec(`UPDATE queue_entries SET position = ?1`+sync+` WHERE id = ?2`, position, entryID)
		return err
	})
}

// QueueChange sets repeat, or turns shuffle on or off in a queue that holds everything it
// plays. Shuffle on keeps the current entry playing and shuffles the rest behind it; shuffle
// off puts every entry back in its original order, so playback carries on from the current
// entry in that order.
type QueueChange struct {
	Repeat  string
	Shuffle *bool
	Seed    int64
}

func (d *DB) ChangeQueue(ctx context.Context, v Viewer, publicID string, ifMatch int64, c QueueChange, now time.Time) (Queue, error) {
	return d.editQueue(ctx, v, publicID, ifMatch, now, func(e *queueEdit) error {
		if c.Repeat != "" {
			if c.Repeat != "off" && c.Repeat != "one" && c.Repeat != "all" {
				return invalid("repeat is off, one or all")
			}
			if _, err := e.tx.Exec(`UPDATE play_queues SET repeat = ? WHERE id = ?`, c.Repeat, e.q.ID); err != nil {
				return err
			}
		}
		switch {
		case c.Shuffle == nil || *c.Shuffle == e.q.Shuffled:
			return nil
		case !*c.Shuffle:
			if _, err := e.tx.Exec(`UPDATE queue_entries SET position = source_position WHERE queue_id = ?`, e.q.ID); err != nil {
				return err
			}
			if err := e.respace("position"); err != nil {
				return err
			}
			_, err := e.tx.Exec(`UPDATE play_queues SET shuffle_seed = NULL WHERE id = ?`, e.q.ID)
			return err
		}
		rows, err := e.tx.Query(`SELECT id FROM queue_entries WHERE queue_id = ? ORDER BY position, id`, e.q.ID)
		if err != nil {
			return err
		}
		var ids []int64
		first := -1
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if id == e.q.CurrentEntry {
				first = len(ids)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		b := newBatchTx(e.tx)
		defer b.close()
		for place, index := range playOrder(len(ids), first, c.Seed) {
			if _, err := b.exec(`UPDATE queue_entries SET position = ? WHERE id = ?`, int64(place+1)*queueStep, ids[index]); err != nil {
				return err
			}
		}
		_, err = e.tx.Exec(`UPDATE play_queues SET shuffle_seed = ? WHERE id = ?`, c.Seed, e.q.ID)
		return err
	})
}

// StepQueue moves a queue's current entry: to the next one ("next", or "completion" when the
// entry played to its end, which repeat-one answers with the same entry), to the one before
// ("previous"), or to a named entry. At the end of a queue that does not repeat it is
// ErrQueueEnded and nothing changes. It also marks the queue as played now.
//
// from, when not 0, is the entry the app was on when it asked for a completion, next or
// previous. If the queue is no longer on it, the same request already moved it (its answer was
// lost and it was sent again, perhaps while the first was still being written), and the queue
// is answered as it stands without moving again.
func (d *DB) StepQueue(ctx context.Context, v Viewer, publicID string, ifMatch int64, reason string, entryID, from int64, now time.Time) (Queue, error) {
	q, err := d.editQueue(ctx, v, publicID, ifMatch, now, func(e *queueEdit) error {
		if from != 0 && reason != "entry" && e.q.CurrentEntry != from {
			return errAlreadyMoved
		}
		target := entryID
		if reason != "entry" {
			var err error
			if target, err = stepTarget(e.ctx, e.tx, e.v, e.acc, e.q, reason); err != nil {
				return err
			}
		} else if _, _, err := e.entryPosition(entryID); err != nil {
			return err
		}
		_, err := e.tx.Exec(`UPDATE play_queues SET current_entry_id = ?, played_at = ? WHERE id = ?`, target, now.UnixMilli(), e.q.ID)
		return err
	})
	if errors.Is(err, errAlreadyMoved) {
		return d.Queue(ctx, v, publicID)
	}
	return q, err
}

// errAlreadyMoved is a move StepQueue was asked for a second time.
var errAlreadyMoved = errors.New("the queue already moved off that entry")

// QueueRefill is more of a queue's selection to put behind what it holds.
type QueueRefill struct {
	Items []int64
	// Selection is the selection as it stands after these were taken from it.
	Selection string
	// Replace drops everything but the current entry and what the person added after it
	// first: the selection is being played in a new order.
	Replace     bool
	ShuffleSeed *int64
}

// RefillQueue appends the next part of a queue's selection. Entries already played are
// dropped from the front when the queue would otherwise hold more than it can, so nothing is
// refused and nothing repeats.
func (d *DB) RefillQueue(ctx context.Context, v Viewer, publicID string, r QueueRefill, now time.Time) (Queue, error) {
	return d.editQueue(ctx, v, publicID, 0, now, func(e *queueEdit) error {
		if r.Replace {
			if _, err := e.tx.Exec(`DELETE FROM queue_entries WHERE queue_id = ?1 AND id <> ?2 AND NOT (added = 1 AND position > ?3)`,
				e.q.ID, e.q.CurrentEntry, e.q.currentPosition); err != nil {
				return err
			}
			if _, err := e.tx.Exec(`UPDATE play_queues SET shuffle_seed = ? WHERE id = ?`, r.ShuffleSeed, e.q.ID); err != nil {
				return err
			}
			e.q.Shuffled = false // what follows is appended in the order it is given
		}
		var held int
		if err := e.tx.QueryRow(`SELECT count(*) FROM queue_entries WHERE queue_id = ?`, e.q.ID).Scan(&held); err != nil {
			return err
		}
		if over := held + len(r.Items) - QueueMax; over > 0 {
			if _, err := e.tx.Exec(`DELETE FROM queue_entries WHERE id IN (SELECT id FROM queue_entries WHERE queue_id = ? AND position < ?
				ORDER BY position LIMIT ?)`, e.q.ID, e.q.currentPosition, over); err != nil {
				return err
			}
		}
		if _, err := e.tx.Exec(`UPDATE play_queues SET selection = nullif(?, '') WHERE id = ?`, r.Selection, e.q.ID); err != nil {
			return err
		}
		if len(r.Items) == 0 {
			return nil
		}
		return e.insert(r.Items, QueuePlace{}, false)
	})
}

// DeleteQueue removes a queue. Removing one that is gone changes nothing.
func (d *DB) DeleteQueue(ctx context.Context, v Viewer, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM play_queues WHERE public_id = ? AND profile_id = ?`, publicID, v.ProfileID)
		return err
	})
}

// SaveQueueAsPlaylist makes a playlist of what a queue holds, in the order it plays. The
// same key and name sent again returns the playlist that was made.
func (d *DB) SaveQueueAsPlaylist(ctx context.Context, v Viewer, publicID, name, sortName, key, requestHash string, now time.Time) (listID string, entries int, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		q, err := queueByID(ctx, tx, v, acc, publicID)
		if err != nil {
			return err
		}
		if listID, err = createdBefore(tx, v.UserID, key, requestHash); err != nil {
			return err
		}
		if listID != "" {
			return tx.QueryRow(`SELECT entry_count FROM lists WHERE public_id = ?`, listID).Scan(&entries)
		}
		listID = NewID("lst")
		at := now.UnixMilli()
		res, err := tx.Exec(`INSERT INTO lists (public_id, kind, owner_profile_id, name, sort_name, created_at, updated_at)
			VALUES (?, 'playlist', ?, ?, ?, ?, ?)`, listID, v.ProfileID, name, sortName, at, at)
		if err != nil {
			return err
		}
		list, _ := res.LastInsertId()
		res, err = tx.Exec(`INSERT INTO list_entries (list_id, item_id, position, added_at)
			SELECT @list, e.item_id, row_number() OVER (ORDER BY e.position) * 1024, @at FROM queue_entries e JOIN items i ON i.id = e.item_id
			WHERE e.queue_id = @queue AND `+acc.visible("i")+` ORDER BY e.position`,
			append(v.args(), sql.Named("list", list), sql.Named("at", at), sql.Named("queue", q.ID))...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		entries = int(n)
		if _, err := tx.Exec(`UPDATE lists SET entry_count = ? WHERE id = ?`, entries, list); err != nil {
			return err
		}
		if err := appendChange(tx, Change{At: at, Topic: TopicList, Op: OpCreated, RefID: list, ProfileID: v.ProfileID}); err != nil {
			return err
		}
		return rememberCreated(tx, v.UserID, key, requestHash, listID, now)
	})
	return listID, entries, err
}
