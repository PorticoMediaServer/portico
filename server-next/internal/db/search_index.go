package db

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/searchtext"
)

// The search index is its own file, search.db, beside the main database. It holds nothing
// that is not in main.db, so it can be deleted at any time, and it is synced to disk lazily:
// after a power cut it is at worst a little behind, never wrong.
//
// A write to main.db never waits for search. One goroutine, the indexer, reads the changes
// table after its cursor, rewrites the index rows of what changed, and saves the cursor in the
// same search.db transaction. A crash between the two files only means the same changes are
// indexed again, which changes nothing.
//
// What the indexer relies on from every write to main.db: a new, edited, moved or deleted item
// appends an item change, and so does a tag put on an item or taken off it; deleting an item
// that holds others needs only the one change for the item itself. People are indexed with the items that credit them; a person renamed or
// deleted on their own appends a person change.
//
// The index is rebuilt from nothing when the file is missing or unreadable, when it was
// written by another format or under other word rules, and when the changes it still needs
// have been pruned. The new file is built beside the old one, which keeps answering and keeps
// following changes, and then takes its place.

//go:embed search.sql
var searchSQL string

// searchFormat is raised when search.sql changes, or the way rows are written into it. A file
// of another format cannot answer today's queries, so it is discarded at once.
const searchFormat = "3"

// searchFolding is the word rules the text is folded with. A file folded under other rules
// still answers, a little wrongly, while its replacement is built.
var searchFolding = searchtext.Stamp()

// Besides words, a row holds keys, each one word that begins with a character no title, name
// or query can contain, so nothing a person types ever matches one by chance: the whole title
// as a single word, which makes "is exactly this title" one lookup; the id of each item the
// row sits under; and three facts about an item, its library, its rating age and every tag
// that it or anything it sits under carries, so that a count can be narrowed to some
// libraries and ratings, and can leave out what a blocked label hides, without reading a row
// of main.db. The facts are not rules: who may see which library or rating, and which labels
// a profile has blocked, is only ever read from main.db.
const (
	searchWholeMark   = "\uE000"
	searchWordJoin    = "\uE002"
	searchUnderMark   = "\uE001"
	searchLibraryMark = "\uE003"
	searchRatingMark  = "\uE004"
	searchTagMark     = "\uE005"
)

// searchWhole is a title's words as its one-word key.
func searchWhole(words []string) string { return searchWholeMark + strings.Join(words, searchWordJoin) }

func searchUnder(id int64) string { return searchUnderMark + strconv.FormatInt(id, 10) }

func searchLibrary(id int64) string { return searchLibraryMark + strconv.FormatInt(id, 10) }

// searchRating is the key of a rating age; an item with no rating has none.
func searchRating(age int64) string { return searchRatingMark + strconv.FormatInt(age, 10) }

const (
	searchFollowBatch = 500  // changes applied in one search.db transaction
	searchBuildBatch  = 2000 // rows read from main.db at a time during a rebuild
	searchRetry       = 10 * time.Second
)

// searchTag is the key of a tag that an item, or something it sits under, carries.
func searchTag(id int64) string { return searchTagMark + strconv.FormatInt(id, 10) }

// ErrSearchUnavailable is the answer while there is no index to search: for the moment a new
// server takes to build its first one, or longer after an upgrade that changed its format.
var ErrSearchUnavailable = errors.New("the search index is being built")

// searchGroup is one of the groups results come in.
type searchGroup struct {
	name  string // the wire name, and the kind of what is in it
	code  int64  // the high bits of its rows' ids in the index; never change or reuse one
	kinds []ItemKind
}

// searchPeople is the code of the one group that holds people and not items.
const searchPeople = 4

// searchGroups are the groups in the order they are shown, which is also the order of their codes.
var searchGroups = []searchGroup{
	{"movie", 1, []ItemKind{KindMovie}},
	{"show", 2, []ItemKind{KindShow}},
	{"episode", 3, []ItemKind{KindEpisode}},
	{"person", searchPeople, nil},
	{"artist", 5, []ItemKind{KindArtist}},
	{"album", 6, []ItemKind{KindAlbum}},
	{"track", 7, []ItemKind{KindTrack}},
	{"audiobook", 8, []ItemKind{KindAudiobook}},
	{"book", 9, []ItemKind{KindBook, KindComic, KindIssue}},
}

// searchGroupOf is the group code of each kind that is searched. Seasons, series and
// collections are not: a season's name is "Season 2".
var searchGroupOf = func() map[ItemKind]int64 {
	codes := map[ItemKind]int64{}
	for _, g := range searchGroups {
		for _, kind := range g.kinds {
			codes[kind] = g.code
		}
	}
	return codes
}()

type searchIndex struct {
	d    *DB
	path string
	log  *slog.Logger

	// mu is held for reading by every search and for writing only while a rebuilt file
	// takes the old one's place. read and write are nil while there is no file to search.
	mu    sync.RWMutex
	read  *sql.DB
	write *sql.DB // the indexer's own connection

	cursor   atomic.Int64 // the last change the file being searched reflects; -1 with no file
	rebuilds chan chan error
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// StartSearch opens the search index at path, or starts building it, and keeps it following
// the main database until Close. Call it once, before the first search.
func (d *DB) StartSearch(path string, log *slog.Logger) {
	ix := &searchIndex{d: d, path: path, log: log, rebuilds: make(chan chan error), done: make(chan struct{})}
	ix.ctx, ix.cancel = context.WithCancel(context.Background())
	ix.cursor.Store(-1)
	removeSearchFile(path + ".new") // what a rebuild left behind if the server stopped in the middle of one
	current := ix.adopt()
	d.search = ix
	go ix.run(!current)
}

func openSearchFile(path, mode string, conns int, journal, synchronous string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", mode)
	q.Set("_txlock", "immediate")
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(" + journal + ")", "synchronous(" + synchronous + ")", "cache_size(-8192)"} {
		q.Add("_pragma", pragma)
	}
	conn, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(conns)
	return conn, conn.Ping()
}

func removeSearchFile(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
}

// open opens the file at the index's path for the indexer and for searches.
func (ix *searchIndex) open() error {
	write, err := openSearchFile(ix.path, "rw", 1, "WAL", "NORMAL")
	if err != nil {
		return err
	}
	read, err := openSearchFile(ix.path, "ro", 4, "WAL", "NORMAL")
	if err != nil {
		write.Close()
		return err
	}
	ix.read, ix.write = read, write
	return nil
}

func (ix *searchIndex) close() {
	if ix.read != nil {
		ix.read.Close()
		ix.write.Close()
		ix.read, ix.write = nil, nil
	}
	ix.cursor.Store(-1)
}

// adopt starts using the file already on disk if this build can search it, and reports
// whether it is also current. A file that cannot be searched is removed.
func (ix *searchIndex) adopt() (current bool) {
	if _, err := os.Stat(ix.path); err != nil {
		return false
	}
	var format, folding string
	var cursor int64
	err := ix.open()
	if err == nil {
		err = ix.write.QueryRow(`SELECT (SELECT value FROM meta WHERE key = 'format'), (SELECT value FROM meta WHERE key = 'folding'),
			(SELECT value FROM meta WHERE key = 'cursor')`).Scan(&format, &folding, &cursor)
	}
	if err != nil || format != searchFormat {
		ix.close()
		removeSearchFile(ix.path)
		ix.log.Info("the search index cannot be used and will be rebuilt", "error", err, "format", format)
		return false
	}
	ix.cursor.Store(cursor)
	return folding == searchFolding
}

// run is the indexer: it rebuilds when it must and otherwise follows the changes table,
// woken by every commit to the main database.
func (ix *searchIndex) run(rebuild bool) {
	defer close(ix.done)
	var waiting []chan error
	for ix.ctx.Err() == nil {
		if rebuild {
			err := ix.build()
			for _, w := range waiting {
				w <- err
			}
			waiting = nil
			if err != nil {
				if ix.ctx.Err() == nil {
					ix.log.Error("rebuilding the search index failed; it will be tried again", "error", err)
				}
				ix.pause()
				continue
			}
		}
		var err error
		if rebuild, err = ix.follow(); err != nil && ix.ctx.Err() == nil {
			// Whatever went wrong, the index is the file that can be thrown away. The pause
			// keeps a fault that will not go away from rebuilding it without rest.
			ix.log.Error("updating the search index failed; it will be rebuilt", "error", err)
			ix.pause()
			rebuild = true
		}
		if rebuild {
			continue
		}
		select {
		case <-ix.d.changed:
		case w := <-ix.rebuilds:
			waiting, rebuild = append(waiting, w), true
		case <-ix.ctx.Done():
		}
	}
}

// pause waits before another try, or until the server stops.
func (ix *searchIndex) pause() {
	select {
	case <-time.After(searchRetry):
	case <-ix.ctx.Done():
	}
}

// follow applies the changes after the cursor until none are left. It reports true when
// changes it needed have been pruned, so that only a rebuild can make the index right.
func (ix *searchIndex) follow() (rebuild bool, err error) {
	for ix.ctx.Err() == nil {
		changes, reread, _, err := ix.d.Changes(ix.ctx, ix.cursor.Load(), searchFollowBatch)
		if err != nil || reread || len(changes) == 0 {
			return reread, err
		}
		if err := ix.apply(changes); err != nil {
			return false, err
		}
	}
	return false, nil
}

// A row of the index is numbered by its group and the id of its item or person.
const searchIDBits = 40

func searchRow(code, id int64) int64 { return code<<searchIDBits | id }
func searchRowID(row int64) int64    { return row & (1<<searchIDBits - 1) }

// searchDoc is one row of the index.
type searchDoc struct {
	row                        int64
	title, joined, other, keys string
}

// searchTitle folds a title or a name into its two indexed forms and their whole-title keys.
func searchTitle(text string) (title, joined string, keys []string) {
	split, kept := searchtext.Words(text)
	title, keys = strings.Join(split, " "), []string{searchWhole(split)}
	if kept != nil {
		joined, keys = strings.Join(kept, " "), append(keys, searchWhole(kept))
	}
	return title, joined, keys
}

// searchItemDocs reads items from the main database as index rows. tail is the condition,
// with any order and limit. present lists every item found, in the order read, including
// those of kinds that are not searched.
func (d *DB) searchItemDocs(ctx context.Context, tail string, args ...any) (docs []searchDoc, present []int64, err error) {
	rows, err := d.read.QueryContext(ctx, `SELECT i.id, i.kind, i.title, ifnull(i.original_title, ''), ifnull(i.parent_id, 0),
		ifnull(i.root_id, 0), ifnull(p.kind, 0), ifnull(p.title, ''), ifnull(r.kind, 0), ifnull(r.title, ''), i.library_id, i.rating_age
		FROM items i LEFT JOIN items p ON p.id = i.parent_id LEFT JOIN items r ON r.id = i.root_id WHERE `+tail, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var lines [][3]int64 // for each row of docs: the item, and what it sits under
	for rows.Next() {
		var id, parentID, rootID, library int64
		var kind, parentKind, rootKind ItemKind
		var title, original, parentTitle, rootTitle string
		var rating sql.NullInt64
		if err := rows.Scan(&id, &kind, &title, &original, &parentID, &rootID, &parentKind, &parentTitle, &rootKind, &rootTitle, &library, &rating); err != nil {
			return nil, nil, err
		}
		present = append(present, id)
		code, searched := searchGroupOf[kind]
		if !searched {
			continue
		}
		doc := searchDoc{row: searchRow(code, id)}
		var other, keys []string
		doc.title, doc.joined, keys = searchTitle(title)
		add := func(text string) {
			split, kept := searchtext.Words(text)
			other = append(append(other, split...), kept...)
		}
		if original != title {
			add(original)
		}
		// A season's name says nothing; the show's does.
		if _, named := searchGroupOf[parentKind]; named {
			add(parentTitle)
		}
		if _, named := searchGroupOf[rootKind]; named && rootID != parentID {
			add(rootTitle)
		}
		for _, above := range []int64{parentID, rootID} {
			if above != 0 {
				keys = append(keys, searchUnder(above))
			}
		}
		if keys = append(keys, searchLibrary(library)); rating.Valid {
			keys = append(keys, searchRating(rating.Int64))
		}
		doc.other, doc.keys = strings.Join(other, " "), strings.Join(keys, " ")
		docs, lines = append(docs, doc), append(lines, [3]int64{id, parentID, rootID})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	// The tags of each item and of what it sits under, the same reach a blocked label has.
	var carriers []int64
	for _, line := range lines {
		carriers = append(carriers, line[:]...)
	}
	tagged, err := d.read.QueryContext(ctx, `SELECT item_id, tag_id FROM item_tags WHERE item_id`+inIDs+` ORDER BY tag_id`, idsJSON(carriers))
	if err != nil {
		return nil, nil, err
	}
	defer tagged.Close()
	tags := map[int64][]int64{}
	for tagged.Next() {
		var item, tag int64
		if err := tagged.Scan(&item, &tag); err != nil {
			return nil, nil, err
		}
		tags[item] = append(tags[item], tag)
	}
	for n, line := range lines {
		var carried []int64
		for _, item := range line {
			carried = append(carried, tags[item]...)
		}
		slices.Sort(carried)
		for _, tag := range slices.Compact(carried) {
			docs[n].keys += " " + searchTag(tag)
		}
	}
	return docs, present, tagged.Err()
}

// searchPeopleDocs reads people from the main database as index rows, in the order read.
func (d *DB) searchPeopleDocs(ctx context.Context, tail string, args ...any) ([]searchDoc, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT id, name FROM people WHERE `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []searchDoc
	for rows.Next() {
		var doc searchDoc
		var name string
		var id int64
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		doc.row = searchRow(searchPeople, id)
		var keys []string
		doc.title, doc.joined, keys = searchTitle(name)
		doc.keys = strings.Join(keys, " ")
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func idsJSON(ids []int64) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

const inIDs = ` IN (SELECT value FROM json_each(?))`

// searchWriter writes index rows inside one search.db transaction.
type searchWriter struct{ *batchTx }

// get reads the row with this number, if there is one.
func (w searchWriter) get(row int64) (doc searchDoc, found bool, err error) {
	err = w.queryRow(`SELECT title, joined, other, keys FROM search_titles WHERE rowid = ?`, row).Scan(&doc.title, &doc.joined, &doc.other, &doc.keys)
	doc.row = row
	if errors.Is(err, sql.ErrNoRows) {
		return doc, false, nil
	}
	return doc, err == nil, err
}

func (w searchWriter) remove(row int64) error {
	_, err := w.exec(`DELETE FROM search_titles WHERE rowid = ?`, row)
	return err
}

func (w searchWriter) add(doc searchDoc) error {
	_, err := w.exec(`INSERT INTO search_titles (rowid, title, joined, other, keys) VALUES (?, ?, ?, ?, ?)`,
		doc.row, doc.title, doc.joined, doc.other, doc.keys)
	return err
}

// put writes a row unless the index already holds exactly it. changed says whether the row
// was there with another title, under other items or with other tags, which the rows under
// it carry too.
func (w searchWriter) put(doc searchDoc) (changed bool, err error) {
	old, found, err := w.get(doc.row)
	if err != nil || found && old == doc {
		return false, err
	}
	if found {
		if err := w.remove(doc.row); err != nil {
			return false, err
		}
	}
	return found && (old.title != doc.title || old.joined != doc.joined || placeKeys(old.keys) != placeKeys(doc.keys)), w.add(doc)
}

// placeKeys is the part of a row's keys that the rows under it depend on: what it sits under
// and the tags it carries.
func placeKeys(keys string) string {
	var above []string
	for _, key := range strings.Fields(keys) {
		if strings.HasPrefix(key, searchUnderMark) || strings.HasPrefix(key, searchTagMark) {
			above = append(above, key)
		}
	}
	return strings.Join(above, " ")
}

// under lists the rows of the items that sit under an item.
func (w searchWriter) under(id int64) ([]int64, error) {
	rows, err := w.tx.Query(`SELECT rowid FROM search_titles WHERE search_titles MATCH ?`, `"`+searchUnder(id)+`"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var row int64
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// apply brings the index up to date with one batch of changes and moves the cursor past it.
func (ix *searchIndex) apply(changes []Change) error {
	var items, people []int64
	seen := map[Change]bool{}
	for _, c := range changes {
		key := Change{Topic: c.Topic, RefID: c.RefID}
		if seen[key] {
			continue
		}
		seen[key] = true
		switch c.Topic {
		case TopicItem:
			items = append(items, c.RefID)
		case TopicPerson:
			people = append(people, c.RefID)
		}
	}
	tx, err := ix.write.BeginTx(ix.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := searchWriter{newBatchTx(tx)}
	defer w.close()
	if err := ix.applyItems(w, items); err != nil {
		return err
	}
	if err := ix.applyPeople(w, items, people); err != nil {
		return err
	}
	last := changes[len(changes)-1].ID
	if _, err := w.exec(`INSERT INTO meta (key, value) VALUES ('cursor', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		strconv.FormatInt(last, 10)); err != nil {
		return err
	}
	w.close()
	if err := tx.Commit(); err != nil {
		return err
	}
	ix.cursor.Store(last)
	return nil
}

// applyItems rewrites the rows of the changed items, and of everything under an item whose
// title or place changed, because those rows carry its title too.
func (ix *searchIndex) applyItems(w searchWriter, items []int64) error {
	var under []int64
	for len(items) > 0 {
		n := min(len(items), searchFollowBatch)
		docs, present, err := ix.d.searchItemDocs(ix.ctx, `i.id`+inIDs, idsJSON(items[:n]))
		if err != nil {
			return err
		}
		for _, doc := range docs {
			changed, err := w.put(doc)
			if err != nil {
				return err
			}
			if changed {
				below, err := w.under(searchRowID(doc.row))
				if err != nil {
					return err
				}
				under = append(under, below...)
			}
		}
		// What is no longer in the main database goes, and everything under it went with it.
		// Its kind went too, so its row is looked for in every group.
		for _, id := range items[:n] {
			if slices.Contains(present, id) {
				continue
			}
			gone, err := w.under(id)
			if err != nil {
				return err
			}
			for _, g := range searchGroups {
				gone = append(gone, searchRow(g.code, id))
			}
			for _, row := range gone {
				if err := w.remove(row); err != nil {
					return err
				}
			}
		}
		items = items[n:]
	}
	for len(under) > 0 {
		n := min(len(under), searchFollowBatch)
		ids := make([]int64, n)
		for i, row := range under[:n] {
			ids[i] = searchRowID(row)
		}
		docs, _, err := ix.d.searchItemDocs(ix.ctx, `i.id`+inIDs, idsJSON(ids))
		if err != nil {
			return err
		}
		for _, doc := range docs {
			if _, err := w.put(doc); err != nil {
				return err
			}
		}
		under = under[n:]
	}
	return nil
}

// applyPeople indexes the people the changed items credit, and the people changed themselves.
func (ix *searchIndex) applyPeople(w searchWriter, items, people []int64) error {
	if len(items) == 0 && len(people) == 0 {
		return nil
	}
	docs, err := ix.d.searchPeopleDocs(ix.ctx, `id IN (SELECT person_id FROM credits WHERE item_id`+inIDs+`) OR id`+inIDs,
		idsJSON(items), idsJSON(people))
	if err != nil {
		return err
	}
	exists := map[int64]bool{}
	for _, doc := range docs {
		exists[doc.row] = true
		if _, err := w.put(doc); err != nil {
			return err
		}
	}
	for _, id := range people {
		if row := searchRow(searchPeople, id); !exists[row] {
			if err := w.remove(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// build writes a whole new index beside the one in use and then puts it in its place.
func (ix *searchIndex) build() error {
	started := time.Now()
	// Changes after this one are applied again once the new file is in place. Whatever the
	// walk below already saw of them is simply written twice.
	head, err := ix.d.ChangesHead(ix.ctx)
	if err != nil {
		return err
	}
	tmp := ix.path + ".new"
	removeSearchFile(tmp)
	defer removeSearchFile(tmp)
	// No journal and no sync: a build that is interrupted is started again from nothing.
	conn, err := openSearchFile(tmp, "rwc", 1, "OFF", "OFF")
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ix.ctx, searchSQL); err != nil {
		return err
	}
	batch := func(fill func(w searchWriter) error) error {
		tx, err := conn.BeginTx(ix.ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		w := searchWriter{newBatchTx(tx)}
		defer w.close()
		if err := fill(w); err != nil {
			return err
		}
		w.close()
		if err := tx.Commit(); err != nil {
			return err
		}
		// The index in use keeps up with new changes while its replacement is built.
		if ix.write != nil {
			ix.follow()
		}
		return ix.ctx.Err()
	}
	fill := func(docs []searchDoc) error {
		return batch(func(w searchWriter) error {
			for _, doc := range docs {
				if err := w.add(doc); err != nil {
					return err
				}
			}
			return nil
		})
	}
	var items, people int
	for after, more := int64(0), true; more; {
		docs, present, err := ix.d.searchItemDocs(ix.ctx, `i.id > ? ORDER BY i.id LIMIT ?`, after, searchBuildBatch)
		if err != nil {
			return err
		}
		if more = len(present) > 0; !more {
			break
		}
		after = present[len(present)-1]
		items += len(docs)
		if err := fill(docs); err != nil {
			return err
		}
	}
	for after, more := int64(0), true; more; {
		docs, err := ix.d.searchPeopleDocs(ix.ctx, `id > ? ORDER BY id LIMIT ?`, after, searchBuildBatch)
		if err != nil {
			return err
		}
		if more = len(docs) > 0; !more {
			break
		}
		after = searchRowID(docs[len(docs)-1].row)
		people += len(docs)
		if err := fill(docs); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ix.ctx, `INSERT INTO meta (key, value) VALUES ('format', ?), ('folding', ?), ('cursor', ?)`,
		searchFormat, searchFolding, strconv.FormatInt(head, 10)); err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}

	// Searches in flight finish on the old file; the next ones wait the moment this takes.
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.close()
	os.Remove(ix.path + "-wal")
	os.Remove(ix.path + "-shm")
	if err := os.Rename(tmp, ix.path); err != nil {
		return err
	}
	if err := ix.open(); err != nil {
		return err
	}
	ix.cursor.Store(head)
	ix.log.Info("the search index was rebuilt", "items", items, "people", people, "took", time.Since(started).Round(time.Millisecond))
	return nil
}

// RebuildSearch builds the search index again from nothing and returns when the new one is
// in use. Searches are answered from the old one meanwhile.
func (d *DB) RebuildSearch(ctx context.Context) error {
	done := make(chan error, 1)
	select {
	case d.search.rebuilds <- done:
	case <-d.search.done:
		return errClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-d.search.done:
		return errClosed
	}
}

// SearchCaughtUp returns once search reflects every change committed before the call.
func (d *DB) SearchCaughtUp(ctx context.Context) error {
	head, err := d.ChangesHead(ctx)
	if err != nil {
		return err
	}
	return d.SearchReflects(ctx, head)
}

// SearchReflects returns once search reflects the change with this number, as ChangesHead
// counts them, and every change before it.
func (d *DB) SearchReflects(ctx context.Context, change int64) error {
	for d.search.cursor.Load() < change {
		select {
		case <-time.After(time.Millisecond):
		case <-d.search.done:
			return errClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (ix *searchIndex) stop() {
	ix.cancel()
	<-ix.done
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.close()
}
