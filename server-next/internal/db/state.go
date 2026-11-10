package db

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// State is one profile's own state for one item.
type State struct {
	Watched      bool
	PositionMS   int64
	PositionText string // where a reader is in a book, in the reader's own notation
	Favorite     bool
	InList       bool
	Hidden       bool // "not interested"
	Rating       *int // 1 to 10, in half stars; nil is no rating
	PlayCount    int
	LastPlayedAt int64
	UpdatedAt    int64
}

const stateColumns = `ifnull(s.played, 0), ifnull(s.position_ms, 0), ifnull(s.position_text, ''), ifnull(s.favorite, 0),
	ifnull(s.in_list, 0), ifnull(s.hidden, 0), s.rating, ifnull(s.play_count, 0), ifnull(s.last_played_at, 0), ifnull(s.updated_at, 0)`

func (s *State) dest() []any {
	return []any{&s.Watched, &s.PositionMS, &s.PositionText, &s.Favorite, &s.InList, &s.Hidden, &s.Rating, &s.PlayCount,
		&s.LastPlayedAt, &s.UpdatedAt}
}

// StateEdit is a change to a profile's state for one item. Only the fields that are set
// change. Each field keeps the time its last edit was made, and an edit only applies to a
// field whose last edit is not later, so two devices that sync in either order end the same.
//
// Watched is a deliberate mark. PositionMS without Watched is a progress report from playback:
// it never undoes a deliberate mark made after it. PositionMS with Watched false sets where to
// resume.
type StateEdit struct {
	Watched      *bool
	PositionMS   *int64
	PositionText *string
	Favorite     *bool
	InList       *bool
	Hidden       *bool
	Rating       *int
	ClearRating  bool
	// Ago is how long before now the person made the edit, by their device's own measure. It is
	// zero for an edit made online and positive for one queued while offline. The server takes
	// it from its own clock, so a device with the wrong date changes nothing.
	Ago time.Duration
}

// ItemEdit is an edit to one item, for SetStates.
type ItemEdit struct {
	ItemID string
	StateEdit
}

// UndoLifetime is how long a show, season or album mark can be taken back.
const UndoLifetime = time.Minute

// maxUndoOffers bounds the memory undo offers can hold; the oldest goes first.
const maxUndoOffers = 500

// undoOffers holds what is needed to take back recent container marks. It lives in memory:
// after a restart an undo answers "too late", which is also what it answers after a minute.
type undoOffers struct {
	mu     sync.Mutex
	offers map[string]*undoOffer
	order  []string
}

type undoOffer struct {
	profileID int64
	itemID    int64
	libraryID int64
	madeAt    int64 // the edit time the mark stamped on each leaf it changed
	expires   time.Time
	leaves    []leafBefore
}

// leafBefore is one leaf's watched fields as they were before the mark.
type leafBefore struct {
	itemID     int64
	played     bool
	playedAt   sql.NullInt64
	positionMS sql.NullInt64
	positionAt sql.NullInt64
}

func (u *undoOffers) add(offer *undoOffer) string {
	token := NewID("und")
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.offers == nil {
		u.offers = map[string]*undoOffer{}
	}
	u.offers[token] = offer
	u.order = append(u.order, token)
	for len(u.order) > maxUndoOffers {
		delete(u.offers, u.order[0])
		u.order = u.order[1:]
	}
	return token
}

// take removes and returns an offer. An offer is used once.
func (u *undoOffers) take(token string, profileID int64) *undoOffer {
	u.mu.Lock()
	defer u.mu.Unlock()
	offer := u.offers[token]
	if offer == nil || offer.profileID != profileID {
		return nil
	}
	delete(u.offers, token)
	if time.Now().After(offer.expires) {
		return nil
	}
	return offer
}

// SetState applies one edit and returns the state as it now stands, whether or not the edit
// won. Marking a show, season, album or other holder watched or unwatched marks everything
// playable under it that the viewer may see, in the same transaction, and returns a token
// that takes the mark back for about a minute. Sending the same edit twice changes nothing more.
func (d *DB) SetState(ctx context.Context, v Viewer, itemID string, edit StateEdit) (state State, undo string, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		w, err := newStateWriter(ctx, tx, v)
		if err != nil {
			return err
		}
		defer w.close()
		var offer *undoOffer
		if state, offer, err = w.apply(itemID, edit); err != nil {
			return err
		}
		if offer != nil {
			undo = d.undo.add(offer)
		}
		return nil
	})
	return state, undo, err
}

// SetStates applies many edits in one transaction and returns each item's state, or the
// reason it was refused, in the same order. It offers no undo.
func (d *DB) SetStates(ctx context.Context, p Priority, v Viewer, edits []ItemEdit) ([]State, []error, error) {
	states, refusals := make([]State, len(edits)), make([]error, len(edits))
	err := d.write(ctx, p, func(tx *sql.Tx) error {
		w, err := newStateWriter(ctx, tx, v)
		if err != nil {
			return err
		}
		defer w.close()
		for n, edit := range edits {
			state, _, err := w.apply(edit.ItemID, edit.StateEdit)
			if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) {
				return err
			}
			states[n], refusals[n] = state, err
		}
		return nil
	})
	return states, refusals, err
}

type stateWriter struct {
	*batchTx
	viewer Viewer
	access access
	now    int64
}

// stateRow is a user_item_state row with the time of each field's last edit.
type stateRow struct {
	exists       bool
	positionMS   sql.NullInt64
	posText      sql.NullString
	positionAt   sql.NullInt64
	played       bool
	playedAt     sql.NullInt64
	favorite     bool
	favoriteAt   sql.NullInt64
	inList       bool
	inListAt     sql.NullInt64
	hidden       bool
	hiddenAt     sql.NullInt64
	rating       sql.NullInt64
	ratingAt     sql.NullInt64
	lastPlayedAt sql.NullInt64
}

func newStateWriter(ctx context.Context, tx *sql.Tx, v Viewer) (*stateWriter, error) {
	acc, err := loadAccess(ctx, tx, v)
	return &stateWriter{batchTx: newBatchTx(tx), viewer: v, access: acc, now: time.Now().UnixMilli()}, err
}

func (w *stateWriter) named(extra ...any) []any { return append(w.viewer.args(), extra...) }

func (w *stateWriter) apply(itemID string, edit StateEdit) (State, *undoOffer, error) {
	var state State
	var id, libraryID, duration int64
	var kind ItemKind
	err := w.queryRow(`SELECT i.id, i.kind, i.library_id, ifnull(i.duration_ms, 0)
		FROM items i WHERE i.public_id = @item AND `+w.access.visible("i"), w.named(sql.Named("item", itemID))...).
		Scan(&id, &kind, &libraryID, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil, ErrNotFound
	}
	if err != nil {
		return state, nil, err
	}
	if err := edit.check(kind, duration); err != nil {
		return state, nil, err
	}
	// When the edit was made, on the server's clock. Never in the future.
	at := w.now - max(edit.Ago, 0).Milliseconds()

	row, err := w.load(id)
	if err != nil {
		return state, nil, err
	}
	changed := false
	set := func(apply bool, fieldAt *sql.NullInt64, write func()) {
		if apply && (!fieldAt.Valid || at >= fieldAt.Int64) {
			write()
			*fieldAt = sql.NullInt64{Int64: at, Valid: true}
			changed = true
		}
	}
	position := func() {
		row.positionMS, row.posText = sql.NullInt64{}, sql.NullString{}
		if edit.PositionMS != nil {
			row.positionMS = sql.NullInt64{Int64: *edit.PositionMS, Valid: true}
		}
		if edit.PositionText != nil {
			row.posText = sql.NullString{String: *edit.PositionText, Valid: true}
		}
	}
	set(edit.Favorite != nil, &row.favoriteAt, func() { row.favorite = *edit.Favorite })
	set(edit.InList != nil, &row.inListAt, func() { row.inList = *edit.InList })
	set(edit.Hidden != nil, &row.hiddenAt, func() { row.hidden = *edit.Hidden })
	set(edit.Rating != nil || edit.ClearRating, &row.ratingAt, func() {
		row.rating = sql.NullInt64{}
		if edit.Rating != nil {
			row.rating = sql.NullInt64{Int64: int64(*edit.Rating), Valid: true}
		}
	})

	watching := edit.Watched != nil || edit.PositionMS != nil || edit.PositionText != nil
	var offer *undoOffer
	if watching {
		switch {
		case edit.Watched != nil && !kind.Playable():
			if offer, err = w.markLeaves(id, *edit.Watched, at); err != nil {
				return state, nil, err
			}
		case edit.Watched != nil:
			// A deliberate mark also decides where to resume: from the start, or from the
			// position sent with "unwatched". It stamps the position's time as well, which is
			// what stops an older progress report from undoing the mark.
			set(true, &row.playedAt, func() { row.played = *edit.Watched })
			set(row.playedAt.Int64 == at, &row.positionAt, position)
		default:
			// A progress report from playback.
			set(true, &row.positionAt, func() {
				position()
				row.lastPlayedAt = sql.NullInt64{Int64: max(row.lastPlayedAt.Int64, at), Valid: true}
			})
		}
	}
	if changed {
		if err := w.store(id, row); err != nil {
			return state, nil, err
		}
	}
	if offer != nil && len(offer.leaves) == 0 {
		offer = nil
	}
	if changed || offer != nil {
		if err := appendChange(w.tx, Change{At: w.now, Topic: TopicState, Op: OpUpdated, RefID: id, LibraryID: libraryID,
			ProfileID: w.viewer.ProfileID}); err != nil {
			return state, nil, err
		}
	}
	if offer != nil {
		offer.libraryID = libraryID
	}
	state, err = w.read(id, kind)
	return state, offer, err
}

// check refuses an edit that cannot apply to this kind of item.
func (e StateEdit) check(kind ItemKind, durationMS int64) error {
	// Half a star to five stars in half-star steps: 1 to 10. No rating is null, not 0.
	if e.Rating != nil && (*e.Rating < 1 || *e.Rating > 10) {
		return invalid("A rating is half a star to five stars, sent as 1 to 10, or null for no rating.")
	}
	if e.PositionMS == nil && e.PositionText == nil {
		return nil
	}
	if !kind.Playable() {
		return invalid("only something playable has a position")
	}
	if e.Watched != nil && *e.Watched && e.PositionMS != nil && *e.PositionMS != 0 {
		return invalid("a position can go with unwatched, not with watched")
	}
	if e.PositionMS != nil && (*e.PositionMS < 0 || durationMS > 0 && *e.PositionMS > durationMS) {
		return invalid("the position is outside the item")
	}
	return nil
}

func (w *stateWriter) load(itemID int64) (stateRow, error) {
	row := stateRow{exists: true}
	err := w.queryRow(`SELECT position_ms, position_text, position_at, played, played_at, favorite, favorite_at, in_list, in_list_at,
		hidden, hidden_at, rating, rating_at, last_played_at FROM user_item_state WHERE profile_id = ? AND item_id = ?`, w.viewer.ProfileID, itemID).
		Scan(&row.positionMS, &row.posText, &row.positionAt, &row.played, &row.playedAt, &row.favorite, &row.favoriteAt,
			&row.inList, &row.inListAt, &row.hidden, &row.hiddenAt, &row.rating, &row.ratingAt, &row.lastPlayedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return stateRow{}, nil
	}
	return row, err
}

// store is the one write to user_item_state.
func (w *stateWriter) store(itemID int64, row stateRow) error {
	_, err := w.exec(`INSERT INTO user_item_state (profile_id, item_id, position_ms, position_text, position_at, played, played_at,
		favorite, favorite_at, in_list, in_list_at, hidden, hidden_at, rating, rating_at, last_played_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (profile_id, item_id) DO UPDATE SET position_ms = excluded.position_ms, position_text = excluded.position_text,
			position_at = excluded.position_at, played = excluded.played, played_at = excluded.played_at,
			favorite = excluded.favorite, favorite_at = excluded.favorite_at, in_list = excluded.in_list,
			in_list_at = excluded.in_list_at, hidden = excluded.hidden, hidden_at = excluded.hidden_at, rating = excluded.rating,
			rating_at = excluded.rating_at, last_played_at = excluded.last_played_at, updated_at = excluded.updated_at`,
		w.viewer.ProfileID, itemID, row.positionMS, row.posText, row.positionAt, row.played, row.playedAt, row.favorite,
		row.favoriteAt, row.inList, row.inListAt, row.hidden, row.hiddenAt, row.rating, row.ratingAt, row.lastPlayedAt, w.now)
	return err
}

// read returns the state as stored, and for a holder whether everything under it is watched.
func (w *stateWriter) read(itemID int64, kind ItemKind) (State, error) {
	var state State
	err := w.queryRow(`SELECT `+stateColumns+` FROM (SELECT 1) LEFT JOIN user_item_state s ON s.profile_id = ? AND s.item_id = ?`,
		w.viewer.ProfileID, itemID).Scan(state.dest()...)
	if err != nil || kind.Playable() {
		return state, err
	}
	var leaves, watched int
	err = w.queryRow(`SELECT count(*), count(played) FROM (`+underHolder(`SELECT c.id, w.item_id AS played FROM items c
		LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1
		WHERE {under} AND `+w.access.visible("c"), "@id", " UNION ")+`)`, w.named(sql.Named("id", itemID))...).Scan(&leaves, &watched)
	state.Watched = leaves > 0 && leaves == watched
	return state, err
}

// markLeaves sets watched or unwatched on every playable item under a holder that the viewer
// may see and whose own last mark is not later. It returns what those leaves held before.
func (w *stateWriter) markLeaves(holderID int64, watched bool, at int64) (*undoOffer, error) {
	offer := &undoOffer{profileID: w.viewer.ProfileID, itemID: holderID, madeAt: at,
		expires: time.Now().Add(UndoLifetime)}
	rows, err := w.tx.Query(underHolder(`SELECT c.id, ifnull(s.played, 0), s.played_at, s.position_ms, s.position_at FROM items c
		LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = c.id
		WHERE {under} AND `+w.access.visible("c")+`
		AND ifnull(s.played_at, 0) <= @at AND (ifnull(s.played, 0) <> @watched OR ifnull(s.position_ms, 0) <> 0)`, "@id", " UNION "),
		w.named(sql.Named("id", holderID), sql.Named("at", at), sql.Named("watched", watched))...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var leaf leafBefore
		if err := rows.Scan(&leaf.itemID, &leaf.played, &leaf.playedAt, &leaf.positionMS, &leaf.positionAt); err != nil {
			rows.Close()
			return nil, err
		}
		offer.leaves = append(offer.leaves, leaf)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, leaf := range offer.leaves {
		if _, err := w.exec(`INSERT INTO user_item_state (profile_id, item_id, played, played_at, position_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?4, ?5) ON CONFLICT (profile_id, item_id) DO UPDATE SET played = excluded.played,
			played_at = excluded.played_at, updated_at = excluded.updated_at,
			position_ms = CASE WHEN ifnull(position_at, 0) <= ?4 THEN NULL ELSE position_ms END,
			position_text = CASE WHEN ifnull(position_at, 0) <= ?4 THEN NULL ELSE position_text END,
			position_at = max(ifnull(position_at, 0), ?4)`, w.viewer.ProfileID, leaf.itemID, watched, at, w.now); err != nil {
			return nil, err
		}
	}
	return offer, nil
}

// UndoState takes back a show, season or album mark. Each leaf goes back to what it held
// before the mark, unless it has been edited since, in which case the later edit stands.
// A token that is unknown, used, expired or another profile's answers ErrNotFound.
func (d *DB) UndoState(ctx context.Context, v Viewer, token string) error {
	offer := d.undo.take(token, v.ProfileID)
	if offer == nil {
		return ErrNotFound
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		for _, leaf := range offer.leaves {
			// The position goes back too, unless playback has reported a newer one since.
			if _, err := b.exec(`UPDATE user_item_state SET played = ?1, played_at = ?2, updated_at = ?5,
				position_ms = CASE WHEN position_at = ?8 THEN ?3 ELSE position_ms END,
				position_at = CASE WHEN position_at = ?8 THEN ?4 ELSE position_at END
				WHERE profile_id = ?6 AND item_id = ?7 AND played_at = ?8`, leaf.played, leaf.playedAt, leaf.positionMS,
				leaf.positionAt, time.Now().UnixMilli(), offer.profileID, leaf.itemID, offer.madeAt); err != nil {
				return err
			}
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicState, Op: OpUpdated, RefID: offer.itemID,
			LibraryID: offer.libraryID, ProfileID: offer.profileID})
	})
}
