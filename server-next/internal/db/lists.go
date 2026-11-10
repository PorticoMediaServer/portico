package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Playlists, collections people make, and saved views. A list belongs to the profile that
// made it. Others read it when it is shared with them; a playlist shared to edit can have its
// entries changed by them too. Only the maker renames, describes or deletes a list. Every
// reader sees only the entries they may see, and every count is of those.

// ErrNotAllowed: the caller can read this list and may not make this change to it.
var ErrNotAllowed = errors.New("not allowed")

// The kinds of list.
const (
	ListPlaylist   = "playlist"
	ListCollection = "collection"
	ListSavedView  = "saved_view"
)

// MaxListAdd is the most titles one request may name to add.
const MaxListAdd = 500

// listGap is the space left between positions, so a move or an insert rewrites one row.
const listGap = 1024

// List is a list as one profile sees it.
type List struct {
	ID          int64
	PublicID    string
	Kind        string
	Name        string
	Description string
	// Owner is the profile that made the list.
	OwnerPublicID string
	OwnerName     string
	// Owned says the viewer made it; CanEdit says the viewer may change its entries.
	Owned   bool
	CanEdit bool
	// Count is how many entries the viewer may see.
	Count     int
	Revision  int64
	CreatedAt int64
	UpdatedAt int64
	// Covers are the first entries the viewer may see, up to four, for the list's picture.
	Covers []Item

	ownerID  int64
	sortName string
}

// readableSQL is the condition that the viewer may read the list with alias l.
const readableSQL = `(l.owner_profile_id = @viewer_profile OR EXISTS (
	SELECT 1 FROM list_shares sh WHERE sh.list_id = l.id AND sh.profile_id = @viewer_profile))`

// listColumns are what a List scans from, given how its entries are counted.
func listColumns(count string) string {
	return `l.id, l.public_id, l.kind, l.name, ifnull(l.description, ''), op.public_id, op.name, l.owner_profile_id,
	l.owner_profile_id = @viewer_profile, l.owner_profile_id = @viewer_profile OR (l.kind = 'playlist' AND EXISTS (
		SELECT 1 FROM list_shares sh WHERE sh.list_id = l.id AND sh.profile_id = @viewer_profile AND sh.can_edit = 1)),
	` + count + `, l.revision, l.created_at, l.updated_at, l.sort_name FROM lists l JOIN profiles op ON op.id = l.owner_profile_id`
}

func (l *List) dest() []any {
	return []any{&l.ID, &l.PublicID, &l.Kind, &l.Name, &l.Description, &l.OwnerPublicID, &l.OwnerName, &l.ownerID,
		&l.Owned, &l.CanEdit, &l.Count, &l.Revision, &l.CreatedAt, &l.UpdatedAt, &l.sortName}
}

// unrestricted says the viewer sees every title of every library, so a list's stored entry
// count is also the viewer's.
func (a access) unrestricted() bool {
	return a.known && a.allLibraries && a.maxAge == nil && len(a.blockedTags) == 0
}

// countSQL counts the entries of the list with alias l that the viewer may see. For a viewer
// who sees everything it is the count kept on the row, so a list of any size costs nothing.
func (a access) countSQL() string {
	if a.unrestricted() {
		return "l.entry_count"
	}
	return `(SELECT count(*) FROM list_entries ce CROSS JOIN items i ON i.id = ce.item_id WHERE ce.list_id = l.id AND ` + a.visible("i") + `)`
}

// readList reads one list the viewer may read, inside a transaction.
func readList(ctx context.Context, tx *sql.Tx, v Viewer, acc access, publicID string) (List, error) {
	var l List
	err := tx.QueryRowContext(ctx, `SELECT `+listColumns(acc.countSQL())+` WHERE l.public_id = @id AND `+readableSQL,
		append(v.args(), sql.Named("id", publicID))...).Scan(l.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	return l, loadCovers(ctx, tx, v, acc, []*List{&l})
}

// loadCovers attaches the first four entries the viewer may see to each list.
func loadCovers(ctx context.Context, tx *sql.Tx, v Viewer, acc access, lists []*List) error {
	for _, l := range lists {
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM list_entries e CROSS JOIN items i ON i.id = e.item_id`+stateJoin+
			`e.list_id = @list AND `+acc.visible("i")+` ORDER BY e.position LIMIT 4`, append(v.args(), sql.Named("list", l.ID))...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var it Item
			if err := scanItem(rows, &it); err != nil {
				rows.Close()
				return err
			}
			l.Covers = append(l.Covers, it)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := loadImages(ctx, tx, l.Covers); err != nil {
			return err
		}
	}
	return nil
}

// List returns one list the viewer may read.
func (d *DB) List(ctx context.Context, v Viewer, publicID string) (List, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return List{}, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return List{}, err
	}
	return readList(ctx, tx, v, acc, publicID)
}

// nameCursor is where the next page of lists by name starts.
type nameCursor struct {
	Name string `json:"n"`
	ID   int64  `json:"i"`
}

// Lists returns the lists the viewer may read, of one kind or all, by name. limit is at most
// the largest page.
func (d *DB) Lists(ctx context.Context, v Viewer, kind, cursor string, limit int) (lists []List, next string, err error) {
	var after nameCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil {
			return nil, "", invalid("the cursor does not belong to this list")
		}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+listColumns(acc.countSQL())+` WHERE `+readableSQL+`
		AND (@kind = '' OR l.kind = @kind) AND (l.sort_name, l.id) > (@name, @after) ORDER BY l.sort_name, l.id LIMIT @limit`,
		append(v.args(), sql.Named("kind", kind), sql.Named("name", after.Name), sql.Named("after", after.ID), sql.Named("limit", limit+1))...)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var l List
		if err := rows.Scan(l.dest()...); err != nil {
			rows.Close()
			return nil, "", err
		}
		lists = append(lists, l)
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	if len(lists) > limit {
		lists = lists[:limit]
		last := lists[limit-1]
		raw, _ := json.Marshal(nameCursor{Name: last.sortName, ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	page := make([]*List, len(lists))
	for n := range lists {
		page[n] = &lists[n]
	}
	return lists, next, loadCovers(ctx, tx, v, acc, page)
}

// EntryID is a list entry's id on the wire.
func EntryID(id int64) string { return "le" + strconv.FormatInt(id, 36) }

// ParseEntryID reads a list entry id from the wire. An id that is not one names no entry.
func ParseEntryID(text string) int64 {
	rest, ok := strings.CutPrefix(text, "le")
	if !ok {
		return -1
	}
	id, err := strconv.ParseInt(rest, 36, 64)
	if err != nil || id <= 0 {
		return -1
	}
	return id
}

// ListEntries reads a list in its own order, with each entry's id, after q's cursor, under
// q's other filters. A playlist may hold a title twice; each entry is listed. Only titles the
// viewer may see are listed, and the total is of those.
func (d *DB) ListEntries(ctx context.Context, v Viewer, q ItemQuery) (ItemPage, error) {
	var page ItemPage
	var after int64
	if q.Cursor != "" {
		var err error
		if after, err = strconv.ParseInt(q.Cursor, 36, 64); err != nil {
			return page, invalid("the cursor does not belong to this list")
		}
	}
	limit := q.Limit
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
	var list int64
	err = tx.QueryRowContext(ctx, `SELECT l.id FROM lists l WHERE l.public_id = @id AND l.kind <> 'saved_view' AND `+readableSQL,
		append(v.args(), sql.Named("id", q.List))...).Scan(&list)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	// The rows are the list's entries, read through its own order; the filters are checks
	// on each.
	where := newQuery(v)
	q.List, q.entries = "", true
	if found, err := q.filters(ctx, tx, where, acc); err != nil || !found {
		return page, err
	}
	entries := ` FROM list_entries e CROSS JOIN items i ON i.id = e.item_id` + stateJoin + `e.list_id = ` + strconv.FormatInt(list, 10) + ` AND `
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+`, e.id, e.position`+entries+where.pageWhere()+
		` AND e.position > `+where.arg(after)+` ORDER BY e.position LIMIT `+strconv.Itoa(limit+1), where.args...)
	if err != nil {
		return page, err
	}
	var positions []int64
	for rows.Next() {
		var it Item
		var position int64
		if err := scanItem(rows, &it, &it.EntryID, &position); err != nil {
			rows.Close()
			return page, err
		}
		page.Items, positions = append(page.Items, it), append(positions, position)
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = strconv.FormatInt(positions[limit-1], 36)
	}
	if q.WithTotal {
		if err := tx.QueryRowContext(ctx, `SELECT count(*)`+entries+where.countWhere(), where.args...).Scan(&page.Total); err != nil {
			return page, err
		}
	}
	if err := loadImages(ctx, tx, page.Items); err != nil {
		return page, err
	}
	return page, loadLeafCounts(ctx, tx, v, acc, page.Items)
}

// listInQuery is the condition, for the item list, that an item is in a list the viewer may
// read. A list the viewer may not read is not found. viewer is the statement's viewer_profile
// value.
func listInQuery(ctx context.Context, tx *sql.Tx, viewer any, publicID string) (string, error) {
	var list int64
	err := tx.QueryRowContext(ctx, `SELECT l.id FROM lists l WHERE l.public_id = @id AND l.kind <> 'saved_view' AND `+readableSQL,
		viewer, sql.Named("id", publicID)).Scan(&list)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return ` AND i.id IN (SELECT item_id FROM list_entries WHERE list_id = ` + strconv.FormatInt(list, 10) + `)`, err
}

// NewList is a list to make. Items are the titles it starts with; a show, season, album or
// artist added to a playlist adds what plays under it.
type NewList struct {
	Kind        string
	Name        string
	SortName    string
	Description string
	Items       []string
}

// CreateList makes a list. The same key with the same request returns the list it made.
func (d *DB) CreateList(ctx context.Context, v Viewer, n NewList, key, requestHash string, now time.Time) (out List, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		made, err := createdBefore(tx, v.UserID, key, requestHash)
		if err != nil {
			return err
		}
		if made != "" {
			out, err = readList(ctx, tx, v, acc, made)
			return err
		}
		publicID, at := NewID("lst"), now.UnixMilli()
		res, err := tx.Exec(`INSERT INTO lists (public_id, kind, owner_profile_id, name, sort_name, description, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, ?)`, publicID, n.Kind, v.ProfileID, n.Name, n.SortName, n.Description, at, at)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		e := listEdit{tx: tx, ctx: ctx, v: v, acc: acc, id: id, kind: n.Kind, at: at}
		if _, _, err := e.add(n.Items, 0); err != nil {
			return err
		}
		if err := appendChange(tx, Change{At: at, Topic: TopicList, Op: OpCreated, RefID: id}); err != nil {
			return err
		}
		if err := rememberCreated(tx, v.UserID, key, requestHash, publicID, now); err != nil {
			return err
		}
		out, err = readList(ctx, tx, v, acc, publicID)
		return err
	})
	return out, err
}

// ListChange edits a list's name or description. Nil fields stay as they are.
type ListChange struct {
	Name        *string
	SortName    string
	Description *string
}

// EditList renames or describes a list. Only its maker may. revision, when not 0, is the
// revision the edit was made on; an older one is ErrStale.
func (d *DB) EditList(ctx context.Context, v Viewer, publicID string, revision int64, c ListChange, now time.Time) (out List, err error) {
	err = d.listWrite(ctx, v, publicID, revision, true, now, func(e *listEdit) error {
		if c.Name != nil {
			if _, err := e.tx.Exec(`UPDATE lists SET name = ?, sort_name = ? WHERE id = ?`, *c.Name, c.SortName, e.id); err != nil {
				return err
			}
		}
		if c.Description != nil {
			_, err := e.tx.Exec(`UPDATE lists SET description = nullif(?, '') WHERE id = ?`, *c.Description, e.id)
			return err
		}
		return nil
	}, &out)
	return out, err
}

// DeleteList deletes a list for everyone it was shared with; the titles stay. Only its maker
// may. A list that is gone, or that the viewer cannot read, is deleted already.
func (d *DB) DeleteList(ctx context.Context, v Viewer, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id int64
		var owned bool
		err := tx.QueryRowContext(ctx, `SELECT l.id, l.owner_profile_id = @viewer_profile FROM lists l WHERE l.public_id = @id AND `+readableSQL,
			append(v.args(), sql.Named("id", publicID))...).Scan(&id, &owned)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !owned {
			return ErrNotAllowed
		}
		// Everyone who could read it is told, by profile: once it is gone nobody can be
		// looked up through it.
		readers, err := ids(ctx, tx, `SELECT owner_profile_id FROM lists WHERE id = ?1 UNION SELECT profile_id FROM list_shares WHERE list_id = ?1`, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM lists WHERE id = ?`, id); err != nil {
			return err
		}
		at := time.Now().UnixMilli()
		for _, reader := range readers {
			if err := appendChange(tx, Change{At: at, Topic: TopicList, Op: OpDeleted, RefID: id, ProfileID: reader}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListAdded is what adding to a list did.
type ListAdded struct {
	List List
	// Added is how many entries were added; Already is how many titles a collection held already.
	Added   int
	Already int
}

// AddToList adds titles to a list, at the end or just after the entry after (0 for the
// end). The same key with the same request changes nothing more and answers the list.
func (d *DB) AddToList(ctx context.Context, v Viewer, publicID string, revision int64, items []string, after int64, key, requestHash string,
	now time.Time) (out ListAdded, err error) {
	err = d.listWrite(ctx, v, publicID, revision, false, now, func(e *listEdit) error {
		made, err := createdBefore(e.tx, v.UserID, key, requestHash)
		if err != nil {
			return err
		}
		if made != "" {
			// What the first request did, kept with its key.
			e.unchanged = true
			_, err := fmt.Sscanf(made, "%d %d", &out.Added, &out.Already)
			return err
		}
		if out.Added, out.Already, err = e.add(items, after); err != nil {
			return err
		}
		return rememberCreated(e.tx, v.UserID, key, requestHash, fmt.Sprintf("%d %d", out.Added, out.Already), now)
	}, &out.List)
	return out, err
}

// MoveInList moves an entry to just before the entry before, or just after the entry after.
func (d *DB) MoveInList(ctx context.Context, v Viewer, publicID string, revision, entry, before, after int64, now time.Time) (out List, err error) {
	err = d.listWrite(ctx, v, publicID, revision, false, now, func(e *listEdit) error {
		if entry == before || entry == after {
			e.unchanged = true
			return nil
		}
		if _, err := e.position(entry); err != nil {
			return err
		}
		anchor := after
		if before != 0 {
			// Just before an entry is just after the one in front of it, or the start.
			pos, err := e.position(before)
			if err != nil {
				return err
			}
			err = e.tx.QueryRow(`SELECT ifnull((SELECT id FROM list_entries WHERE list_id = ? AND position < ? AND id <> ?
				ORDER BY position DESC LIMIT 1), 0)`, e.id, pos, entry).Scan(&anchor)
			if err != nil {
				return err
			}
			if anchor == 0 {
				anchor = -1
			}
		}
		// Out of the way first, so the gap it leaves is not in the way of its new place.
		if _, err := e.tx.Exec(`UPDATE list_entries SET position = -id WHERE id = ?`, entry); err != nil {
			return err
		}
		positions, err := e.slots(anchor, 1)
		if err != nil {
			return err
		}
		_, err = e.tx.Exec(`UPDATE list_entries SET position = ? WHERE id = ?`, positions[0], entry)
		return err
	}, &out)
	return out, err
}

// RemoveFromList removes one entry. Removing one that is gone changes nothing.
func (d *DB) RemoveFromList(ctx context.Context, v Viewer, publicID string, revision, entry int64, now time.Time) (out List, err error) {
	err = d.listWrite(ctx, v, publicID, revision, false, now, func(e *listEdit) error {
		res, err := e.tx.Exec(`DELETE FROM list_entries WHERE id = ? AND list_id = ?`, entry, e.id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			e.unchanged = true
			return nil
		}
		_, err = e.tx.Exec(`UPDATE lists SET entry_count = entry_count - 1 WHERE id = ?`, e.id)
		return err
	}, &out)
	return out, err
}

// listEdit is one change to a list, inside its transaction.
type listEdit struct {
	ctx  context.Context
	tx   *sql.Tx
	v    Viewer
	acc  access
	id   int64
	kind string
	at   int64
	// unchanged says the change turned out to change nothing, so the revision stays.
	unchanged bool
}

// listWrite runs one change to a list the viewer may change: its entries when it may edit
// them, or anything when it made the list and ownerOnly is set. The revision moves on and
// readers are told, unless the change changed nothing. out is the list afterwards.
func (d *DB) listWrite(ctx context.Context, v Viewer, publicID string, revision int64, ownerOnly bool, now time.Time,
	fn func(e *listEdit) error, out *List) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		l, err := readList(ctx, tx, v, acc, publicID)
		if err != nil {
			return err
		}
		if !l.CanEdit || ownerOnly && !l.Owned {
			return ErrNotAllowed
		}
		if revision != 0 && revision != l.Revision {
			return ErrStale
		}
		e := &listEdit{ctx: ctx, tx: tx, v: v, acc: acc, id: l.ID, kind: l.Kind, at: now.UnixMilli()}
		if err := fn(e); err != nil {
			return err
		}
		if !e.unchanged {
			if _, err := tx.Exec(`UPDATE lists SET revision = revision + 1, updated_at = ? WHERE id = ?`, e.at, l.ID); err != nil {
				return err
			}
			if err := appendChange(tx, Change{At: e.at, Topic: TopicList, Op: OpUpdated, RefID: l.ID}); err != nil {
				return err
			}
		}
		*out, err = readList(ctx, tx, v, acc, publicID)
		return err
	})
}

// position is an entry's place in the list.
func (e *listEdit) position(entry int64) (int64, error) {
	var pos int64
	err := e.tx.QueryRow(`SELECT position FROM list_entries WHERE id = ? AND list_id = ?`, entry, e.id).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invalid("that entry is not in this list")
	}
	return pos, err
}

// slots returns n free positions, in order, just after the entry after: 0 is the end and -1
// the start. When the gap there is too small the list is numbered afresh first.
func (e *listEdit) slots(after int64, n int) ([]int64, error) {
	for range 2 {
		var low, high int64
		var err error
		switch after {
		case 0:
			err = e.tx.QueryRow(`SELECT ifnull(max(position), 0) FROM list_entries WHERE list_id = ?`, e.id).Scan(&low)
			high = low + int64(n+1)*listGap
		case -1:
			err = e.tx.QueryRow(`SELECT ifnull(min(position), ?) FROM list_entries WHERE list_id = ? AND position > 0`, int64(n+1)*listGap, e.id).Scan(&high)
		default:
			if low, err = e.position(after); err == nil {
				err = e.tx.QueryRow(`SELECT ifnull(min(position), ?) FROM list_entries WHERE list_id = ? AND position > ?`,
					low+int64(n+1)*listGap, e.id, low).Scan(&high)
			}
		}
		if err != nil {
			return nil, err
		}
		if step := (high - low) / int64(n+1); step > 0 {
			out := make([]int64, n)
			for k := range out {
				out[k] = low + step*int64(k+1)
			}
			return out, nil
		}
		if err := e.renumber(); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("no room in the list after numbering it afresh")
}

// renumber spaces the list's positions evenly again. It goes through positions far below
// zero, so two entries never hold the same position on the way and an entry being moved,
// which waits at minus its id, is left where it is.
func (e *listEdit) renumber() error {
	if _, err := e.tx.Exec(`UPDATE list_entries SET position = -o.n * ?2 - ?3 FROM (
		SELECT id, row_number() OVER (ORDER BY position) AS n FROM list_entries WHERE list_id = ?1 AND position > 0) o
		WHERE list_entries.id = o.id`, e.id, listGap, renumberOffset); err != nil {
		return err
	}
	_, err := e.tx.Exec(`UPDATE list_entries SET position = -position - ? WHERE list_id = ? AND position < ?`, renumberOffset, e.id, -renumberOffset)
	return err
}

// renumberOffset is below any entry id, so positions on the way through renumber never meet
// an entry that waits at minus its id.
const renumberOffset = int64(1) << 52

// add adds titles the viewer may see, just after the entry after (0 for the end). For a
// playlist a title that holds others adds what plays under it, and a title may be added
// twice. A collection holds each title once: one it holds already is counted, not added.
func (e *listEdit) add(publicIDs []string, after int64) (added, already int, err error) {
	if e.kind == ListSavedView {
		return 0, 0, invalid("a saved view holds no entries")
	}
	if len(publicIDs) > MaxListAdd {
		return 0, 0, invalid("add at most %d titles at once", MaxListAdd)
	}
	var items []int64
	for _, publicID := range publicIDs {
		var id int64
		var kind ItemKind
		err := e.tx.QueryRowContext(e.ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = @id AND `+e.acc.visible("i"),
			append(e.v.args(), sql.Named("id", publicID))...).Scan(&id, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		if err != nil {
			return 0, 0, err
		}
		if e.kind != ListPlaylist || kind.Playable() {
			items = append(items, id)
			continue
		}
		under, err := playableUnder(e.ctx, e.tx, e.v, e.acc, id, kind)
		if err != nil {
			return 0, 0, err
		}
		items = append(items, under...)
	}
	if e.kind == ListCollection {
		var fresh []int64
		seen := map[int64]bool{}
		for _, id := range items {
			var held bool
			if err := e.tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM list_entries WHERE item_id = ? AND list_id = ?)`, id, e.id).Scan(&held); err != nil {
				return 0, 0, err
			}
			if held || seen[id] {
				already++
				continue
			}
			seen[id] = true
			fresh = append(fresh, id)
		}
		items = fresh
	}
	if len(items) == 0 {
		e.unchanged = true
		return 0, already, nil
	}
	if after != 0 {
		if _, err := e.position(after); err != nil {
			return 0, 0, err
		}
	}
	positions, err := e.slots(after, len(items))
	if err != nil {
		return 0, 0, err
	}
	insert, err := e.tx.Prepare(`INSERT INTO list_entries (list_id, item_id, position, added_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, 0, err
	}
	defer insert.Close()
	for n, id := range items {
		if _, err := insert.Exec(e.id, id, positions[n], e.at); err != nil {
			return 0, 0, err
		}
	}
	_, err = e.tx.Exec(`UPDATE lists SET entry_count = entry_count + ? WHERE id = ?`, len(items), e.id)
	return len(items), already, err
}
