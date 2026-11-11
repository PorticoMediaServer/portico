// Package playqueue builds and runs play queues: whatever is played has one. A queue's
// entries are rows that outlive a restart. A queue holds at most db.QueueMax entries at once;
// a longer view is kept as a selection and the queue is refilled from it as the listener
// nears the end, so playing or shuffling a library of any size starts at once and nothing
// repeats until everything has played.
//
// The next song got ready ahead of time, for joins without a gap, lives in memory and is
// gone after a restart.
package playqueue

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// Fill is how much of a selection is taken at a time, and RefillBelow how few entries may be
// left after the current one before more is taken. They are variables so that tests can
// watch a queue refill without a library of thousands.
var (
	Fill        = 1000
	RefillBelow = 200
)

const (
	// maxItems is how many titles one request may name one by one.
	maxItems = 500
	// preparedFor is how long a song got ready ahead of time is kept.
	preparedFor = time.Minute
)

// ErrUnsupported: this cannot be played as a queue.
var ErrUnsupported = errors.New("this selection can't be played as a queue")

// ErrNotPrepared: there is no song got ready under that token; it expired or was replaced.
var ErrNotPrepared = errors.New("the prepared next item is gone")

// QueueView is a library view: what a list of items is filtered and sorted by.
type QueueView struct {
	Library    string   `json:"library,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	Person     string   `json:"person,omitempty"`
	Tag        string   `json:"tag,omitempty"`
	Genre      string   `json:"genre,omitempty"`
	Kinds      []string `json:"kinds,omitempty"`
	YearFrom   int      `json:"yearFrom,omitempty"`
	YearTo     int      `json:"yearTo,omitempty"`
	Watched    *bool    `json:"watched,omitempty"`
	Favorite   *bool    `json:"favorite,omitempty"`
	InList     *bool    `json:"inList,omitempty"`
	Resolution string   `json:"resolution,omitempty"`
	Sort       string   `json:"sort,omitempty"`
	Desc       bool     `json:"desc,omitempty"`
}

func (v QueueView) query() (db.ItemQuery, error) {
	q := db.ItemQuery{Library: v.Library, Parent: v.Parent, Person: v.Person, Tag: v.Tag, Genre: v.Genre, YearFrom: v.YearFrom,
		YearTo: v.YearTo, Watched: v.Watched, Favorite: v.Favorite, InList: v.InList, Resolution: v.Resolution, Sort: v.Sort, Desc: v.Desc}
	for _, name := range v.Kinds {
		kind, ok := db.ParseItemKind(name)
		if !ok || !kind.Playable() {
			return q, ErrUnsupported
		}
		q.Kinds = append(q.Kinds, kind)
	}
	return q, nil
}

// QueueSource is what a queue, or an addition to one, is built from. Exactly one field is set.
type QueueSource struct {
	// Container is a show, season, album, artist or audiobook, or one title.
	Container string `json:"container,omitempty"`
	// Items are titles named one by one: a selection.
	Items []string `json:"items,omitempty"`
	// List is a playlist or a collection.
	List string `json:"list,omitempty"`
	// View is a library view with its sort and filters, of any size.
	View *QueueView `json:"view,omitempty"`
	// Radio is the song, album or artist a radio starts from. A radio never ends.
	Radio string `json:"radio,omitempty"`
}

// selection is what a queue too long to hold at once was built from, and how far into it
// the queue has got. It is stored with the queue as JSON.
type selection struct {
	View QueueView `json:"view"`
	// Radio says the selection never ends: when everything has played it starts again in a
	// new order.
	Radio bool `json:"radio,omitempty"`
	// Shuffle walks the view in the order Seed decides, from place After. Otherwise the view
	// is read in its own order from Cursor.
	Shuffle bool   `json:"shuffle,omitempty"`
	Seed    int64  `json:"seed,omitempty"`
	After   int64  `json:"after,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	// Done says everything has been taken.
	Done bool `json:"done,omitempty"`
	// Skip is a title already in the queue that the walk should pass over once: the one
	// that was playing when the order was made.
	Skip int64 `json:"skip,omitempty"`
}

func (s *selection) text() string {
	if s == nil {
		return ""
	}
	raw, _ := json.Marshal(s)
	return string(raw)
}

func parseSelection(text string) *selection {
	if text == "" {
		return nil
	}
	var s selection
	if json.Unmarshal([]byte(text), &s) != nil {
		return nil
	}
	return &s
}

// Who is asking.
type Caller = playback.Caller

type prepared struct {
	token   string
	queueID string
	entryID int64
	stream  *playback.Prepared
	timer   *time.Timer
}

// Service builds and runs queues.
type Service struct {
	db       *db.DB
	playback *playback.Service
	now      func() time.Time

	mu sync.Mutex
	// prepared is the next song got ready, by queue id. committed remembers which session a
	// token became, so a commit whose reply was lost can be sent again.
	prepared  map[string]*prepared
	committed map[string]string
	// advances counts, by queue id, the entries that played to their end one after another
	// without the person touching the queue: what "Still watching?" goes by.
	advances map[string]int
}

func New(d *db.DB, p *playback.Service, now func() time.Time) *Service {
	return &Service{db: d, playback: p, now: now, prepared: map[string]*prepared{}, committed: map[string]string{}, advances: map[string]int{}}
}

func newSeed() int64 { return rand.Int64N(1<<62) + 1 }

// take reads the next part of a selection, and moves the selection on.
func (s *Service) take(ctx context.Context, v db.Viewer, sel *selection, limit int) ([]int64, error) {
	if sel.Done {
		return nil, nil
	}
	q, err := sel.View.query()
	if err != nil {
		return nil, err
	}
	var items []int64
	if sel.Shuffle {
		if items, sel.After, err = s.db.ShuffledItems(ctx, v, q, sel.Seed, sel.After, limit); err != nil {
			return nil, err
		}
		sel.Done = len(items) < limit
	} else {
		q.Cursor = sel.Cursor
		if items, sel.Cursor, err = s.db.ViewItems(ctx, v, q, limit); err != nil {
			return nil, err
		}
		sel.Done = sel.Cursor == ""
	}
	kept := items[:0]
	for _, id := range items {
		if id == sel.Skip {
			sel.Skip = 0
			continue
		}
		kept = append(kept, id)
	}
	// A radio starts again when it runs out, in a new order.
	if sel.Done && sel.Radio && len(kept) > 0 {
		sel.Done, sel.Seed, sel.After = false, newSeed(), 0
	}
	return kept, nil
}

// CreateInput asks for a new queue.
type CreateInput struct {
	Source  QueueSource
	Shuffle bool
	// StartAt is the title to play first: the episode or song the person pressed. Resume
	// starts a show at the first episode not yet watched. With neither, the first title plays.
	StartAt string
	Resume  bool
}

// Create builds a queue for the device asking and makes it that device's queue for its
// medium. The first entry is current; the app then starts it.
func (s *Service) Create(ctx context.Context, c Caller, in CreateInput) (db.Queue, error) {
	device, err := s.db.SessionDevice(ctx, c.SignInID)
	if err != nil {
		return db.Queue{}, err
	}
	n := db.NewQueue{Viewer: c.Viewer, DeviceID: device.ID, Now: s.now()}
	if in.Shuffle {
		seed := newSeed()
		n.ShuffleSeed = &seed
	}
	var sel *selection
	startAt := int64(0)
	if in.StartAt != "" {
		ids, err := s.db.PlayableItems(ctx, c.Viewer, []string{in.StartAt})
		if err != nil {
			return db.Queue{}, err
		}
		if len(ids) == 1 {
			startAt = ids[0]
		}
	}
	src := in.Source
	if src.Radio != "" {
		if sel, src, err = s.radio(ctx, c.Viewer, src.Radio); err != nil {
			return db.Queue{}, err
		}
		seed := sel.Seed
		n.ShuffleSeed = &seed
	}
	switch {
	case sel != nil:
		// A radio: the song it starts from, then the walk.
		first, err := s.db.PlayableUnder(ctx, c.Viewer, in.Source.Radio)
		if err != nil {
			return db.Queue{}, err
		}
		if len(first) > 0 {
			n.Items, sel.Skip = first[:1], first[0]
		}
		more, err := s.take(ctx, c.Viewer, sel, Fill)
		if err != nil {
			return db.Queue{}, err
		}
		n.Items, n.InOrder = append(n.Items, more...), true
	case src.View != nil:
		if n.Items, sel, err = s.fromView(ctx, c.Viewer, *src.View, in.Shuffle, n.ShuffleSeed, startAt); err != nil {
			return db.Queue{}, err
		}
		n.InOrder = sel != nil
	default:
		if n.Items, err = s.items(ctx, c.Viewer, src, db.QueueMax); err != nil {
			return db.Queue{}, err
		}
	}
	if len(n.Items) == 0 {
		return db.Queue{}, ErrUnsupported
	}
	if sel == nil {
		// The queue holds everything: start where asked.
		for index, id := range n.Items {
			if id == startAt {
				n.Start = index
			}
		}
		if in.Resume && startAt == 0 {
			if n.Start, err = s.db.FirstUnwatched(ctx, c.Viewer, n.Items); err != nil {
				return db.Queue{}, err
			}
		}
	}
	n.Selection = sel.text()
	return s.db.CreateQueue(ctx, n)
}

// items resolves a source that is held whole: a container, named titles or a list.
func (s *Service) items(ctx context.Context, v db.Viewer, src QueueSource, limit int) ([]int64, error) {
	switch {
	case src.Container != "":
		return s.db.PlayableUnder(ctx, v, src.Container)
	case len(src.Items) > 0:
		if len(src.Items) > maxItems {
			return nil, db.ErrQueueFull
		}
		return s.db.PlayableItems(ctx, v, src.Items)
	case src.List != "":
		items, _, err := s.db.ListItems(ctx, v, src.List, 0, limit)
		return items, err
	case src.View != nil:
		q, err := src.View.query()
		if err != nil {
			return nil, err
		}
		items, more, err := s.db.ViewItems(ctx, v, q, limit+1)
		if err == nil && (more != "" || len(items) > limit) {
			err = db.ErrQueueFull
		}
		return items, err
	}
	return nil, ErrUnsupported
}

// fromView starts a queue from a view. A view the queue can hold whole is held whole, so
// shuffle can be turned on and off in place. A longer one becomes a selection: the first
// part now, the rest as the listener gets there.
func (s *Service) fromView(ctx context.Context, v db.Viewer, view QueueView, shuffle bool, seed *int64, startAt int64) ([]int64, *selection, error) {
	q, err := view.query()
	if err != nil {
		return nil, nil, err
	}
	whole, more, err := s.db.ViewItems(ctx, v, q, Fill)
	if err != nil {
		return nil, nil, err
	}
	if more == "" {
		return whole, nil, nil
	}
	count := q
	count.WithTotal, count.Limit = true, 1
	page, err := s.db.Items(ctx, v, count)
	if err != nil {
		return nil, nil, err
	}
	if page.Total <= db.QueueMax {
		q.Cursor = more
		rest, _, err := s.db.ViewItems(ctx, v, q, db.QueueMax-len(whole))
		return append(whole, rest...), nil, err
	}
	sel := &selection{View: view, Shuffle: shuffle}
	var items []int64
	if startAt != 0 {
		items, sel.Skip = []int64{startAt}, startAt
	}
	switch {
	case shuffle:
		sel.Seed = *seed
	case startAt != 0:
		// In the view's own order, playback goes on from the title that was pressed.
		public, err := s.db.ItemPublicID(ctx, startAt)
		if err != nil {
			return nil, nil, err
		}
		if sel.Cursor, _, err = s.db.ViewCursorAt(ctx, v, q, public); err != nil {
			return nil, nil, err
		}
		sel.Skip = 0
	}
	taken, err := s.take(ctx, v, sel, Fill)
	return append(items, taken...), sel, err
}

// radio makes the selection a radio plays: songs of the same library that share a genre
// with what it starts from, in a shuffled order that starts again when it runs out.
// Choosing by how songs sound arrives with the music analysis.
func (s *Service) radio(ctx context.Context, v db.Viewer, from string) (*selection, QueueSource, error) {
	library, genre, err := s.db.RadioSeed(ctx, v, from)
	if err != nil {
		return nil, QueueSource{}, err
	}
	view := QueueView{Library: library, Tag: genre, Kinds: []string{db.KindTrack.String()}}
	return &selection{View: view, Radio: true, Shuffle: true, Seed: newSeed()}, QueueSource{View: &view}, nil
}

// refill takes more of a queue's selection when few entries are left after the current one.
func (s *Service) refill(ctx context.Context, v db.Viewer, q db.Queue) (db.Queue, error) {
	sel := parseSelection(q.Selection)
	if sel == nil || q.Remaining >= RefillBelow {
		return q, nil
	}
	if sel.Done && q.Repeat == "all" && q.Remaining == 0 {
		// Everything has played and the queue repeats: the selection starts again.
		sel.Done, sel.Cursor, sel.After = false, "", 0
		if sel.Shuffle {
			sel.Seed = newSeed()
		}
	}
	if sel.Done {
		return q, nil
	}
	items, err := s.take(ctx, v, sel, Fill)
	if err != nil {
		return q, err
	}
	return s.db.RefillQueue(ctx, v, q.PublicID, db.QueueRefill{Items: items, Selection: sel.text()}, s.now())
}

// Add puts more titles into a queue: next, after an entry, or at the end.
func (s *Service) Add(ctx context.Context, c Caller, id string, ifMatch int64, src QueueSource, place db.QueuePlace) (db.Queue, error) {
	items, err := s.items(ctx, c.Viewer, src, db.QueueMax)
	if err != nil {
		return db.Queue{}, err
	}
	if len(items) == 0 {
		return db.Queue{}, ErrUnsupported
	}
	s.cancelPrepared(id)
	return s.db.AddToQueue(ctx, c.Viewer, id, ifMatch, items, place, s.now())
}

// Remove takes an entry out and Move puts one before or after another.
func (s *Service) Remove(ctx context.Context, c Caller, id string, ifMatch, entryID int64) (db.Queue, error) {
	s.cancelPrepared(id)
	return s.db.RemoveFromQueue(ctx, c.Viewer, id, ifMatch, entryID, s.now())
}

func (s *Service) Move(ctx context.Context, c Caller, id string, ifMatch, entryID, before, after int64) (db.Queue, error) {
	s.cancelPrepared(id)
	return s.db.MoveInQueue(ctx, c.Viewer, id, ifMatch, entryID, before, after, s.now())
}

// Change sets repeat and turns shuffle on or off. Shuffle on keeps the current entry playing
// and shuffles the rest; shuffle off carries on in the original order from the current
// entry. In a queue built from a selection, "the rest" is the rest of the selection.
func (s *Service) Change(ctx context.Context, c Caller, id string, ifMatch int64, repeat string, shuffle *bool) (db.Queue, error) {
	s.cancelPrepared(id)
	q, err := s.db.Queue(ctx, c.Viewer, id)
	if err != nil {
		return q, err
	}
	sel := parseSelection(q.Selection)
	change := db.QueueChange{Repeat: repeat, Shuffle: shuffle, Seed: newSeed()}
	if sel == nil || sel.Radio || shuffle == nil || *shuffle == q.Shuffled {
		if sel != nil {
			change.Shuffle = nil
		}
		return s.db.ChangeQueue(ctx, c.Viewer, id, ifMatch, change, s.now())
	}
	if q, err = s.db.ChangeQueue(ctx, c.Viewer, id, ifMatch, db.QueueChange{Repeat: repeat}, s.now()); err != nil {
		return q, err
	}
	current, err := s.currentItem(ctx, c.Viewer, q)
	if err != nil {
		return q, err
	}
	next := &selection{View: sel.View, Shuffle: *shuffle}
	refill := db.QueueRefill{Replace: true}
	if *shuffle {
		next.Seed, next.Skip = change.Seed, current.ID
		refill.ShuffleSeed = &next.Seed
	} else if current.PublicID != "" {
		view, err := sel.View.query()
		if err != nil {
			return q, err
		}
		if next.Cursor, _, err = s.db.ViewCursorAt(ctx, c.Viewer, view, current.PublicID); err != nil {
			return q, err
		}
	}
	if refill.Items, err = s.take(ctx, c.Viewer, next, Fill); err != nil {
		return q, err
	}
	refill.Selection = next.text()
	return s.db.RefillQueue(ctx, c.Viewer, id, refill, s.now())
}

func (s *Service) currentItem(ctx context.Context, v db.Viewer, q db.Queue) (db.Item, error) {
	if q.CurrentIndex < 0 {
		return db.Item{}, nil
	}
	entries, _, err := s.db.QueueEntries(ctx, v, q.PublicID, q.CurrentIndex, 1)
	if err != nil || len(entries) == 0 {
		return db.Item{}, err
	}
	return entries[0].Item, nil
}

// Advance moves the queue on: reason is completion (the entry played to its end), next,
// previous or entry (jump to entryID). The app then starts the new current entry. At the
// end of a queue that does not repeat it is db.ErrQueueEnded.
func (s *Service) Advance(ctx context.Context, c Caller, id string, ifMatch int64, reason string, entryID, from int64) (db.Queue, error) {
	s.cancelPrepared(id)
	q, err := s.db.Queue(ctx, c.Viewer, id)
	if err != nil {
		return q, err
	}
	if ifMatch != 0 && ifMatch != q.Revision {
		return q, db.ErrStale
	}
	if from != 0 && from != q.CurrentEntry && reason != "entry" {
		// The same move sent again after its answer was lost: it has been made.
		return q, nil
	}
	if q, err = s.refill(ctx, c.Viewer, q); err != nil {
		return q, err
	}
	if q, err = s.db.StepQueue(ctx, c.Viewer, id, 0, reason, entryID, s.now()); err != nil {
		return q, err
	}
	s.mu.Lock()
	if reason == "completion" {
		s.advances[id]++
	} else {
		delete(s.advances, id)
	}
	s.mu.Unlock()
	return s.refill(ctx, c.Viewer, q)
}

// Advances is how many entries of a queue have played to their end in a row without the
// person touching it.
func (s *Service) Advances(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advances[id]
}

// Begin says an entry is about to be played and returns its title. An entry other than the
// current one becomes current.
func (s *Service) Begin(ctx context.Context, c Caller, id string, entryID int64) (itemID string, err error) {
	q, err := s.db.Queue(ctx, c.Viewer, id)
	if err != nil {
		return "", err
	}
	if q.CurrentEntry != entryID {
		if _, err := s.Advance(ctx, c, id, 0, "entry", entryID, 0); err != nil {
			return "", err
		}
	}
	return s.db.QueueEntryItem(ctx, c.Viewer, id, entryID)
}

// Delete clears a queue.
func (s *Service) Delete(ctx context.Context, c Caller, id string) error {
	s.cancelPrepared(id)
	s.mu.Lock()
	delete(s.advances, id)
	s.mu.Unlock()
	return s.db.DeleteQueue(ctx, c.Viewer, id)
}

// PreparedNext is the next entry of a queue with its stream already running.
type PreparedNext struct {
	Token     string
	ExpiresAt time.Time
	EntryID   int64
	Stream    *playback.Prepared
}

// PrepareNext gets the entry after the current one ready before the current one ends, so
// the two join without a gap. from is the session playing now. It lasts a minute, and any
// other change to the queue cancels it.
func (s *Service) PrepareNext(ctx context.Context, c Caller, id, from string) (PreparedNext, error) {
	s.cancelPrepared(id)
	q, err := s.db.Queue(ctx, c.Viewer, id)
	if err != nil {
		return PreparedNext{}, err
	}
	if q, err = s.refill(ctx, c.Viewer, q); err != nil {
		return PreparedNext{}, err
	}
	if q.NextEntry == 0 || q.NextEntry == q.CurrentEntry {
		return PreparedNext{}, db.ErrQueueEnded
	}
	item, err := s.db.QueueEntryItem(ctx, c.Viewer, id, q.NextEntry)
	if err != nil {
		return PreparedNext{}, err
	}
	stream, err := s.playback.Prepare(ctx, c, item, from)
	if err != nil {
		return PreparedNext{}, err
	}
	p := &prepared{token: db.NewID("nxt"), queueID: id, entryID: q.NextEntry, stream: stream}
	p.timer = time.AfterFunc(preparedFor, func() { s.cancel(id, p) })
	s.mu.Lock()
	s.prepared[id] = p
	s.mu.Unlock()
	return PreparedNext{Token: p.token, ExpiresAt: s.now().Add(preparedFor), EntryID: p.entryID, Stream: stream}, nil
}

func (s *Service) cancelPrepared(id string) { s.cancel(id, nil) }

// cancel drops the song got ready for a queue: only is nil for whatever is there.
func (s *Service) cancel(id string, only *prepared) {
	s.mu.Lock()
	p := s.prepared[id]
	if p == nil || only != nil && p != only {
		s.mu.Unlock()
		return
	}
	delete(s.prepared, id)
	s.mu.Unlock()
	p.timer.Stop()
	s.playback.Drop(p.stream)
}

// CommitNext makes the prepared entry the one playing: the queue moves to it and its stream
// becomes a session, which replaces the one that was playing. The same token sent again
// returns the same session.
func (s *Service) CommitNext(ctx context.Context, c Caller, id, token string) (db.Queue, playback.Session, error) {
	s.mu.Lock()
	p := s.prepared[id]
	done := s.committed[token]
	if p != nil && p.token == token {
		delete(s.prepared, id)
		p.timer.Stop()
	} else {
		p = nil
	}
	s.mu.Unlock()
	if p == nil {
		if done == "" {
			return db.Queue{}, playback.Session{}, ErrNotPrepared
		}
		session, err := s.playback.Get(done, c.Viewer, c.SignInID)
		if err != nil {
			return db.Queue{}, session, err
		}
		q, err := s.db.Queue(ctx, c.Viewer, id)
		return q, session, err
	}
	q, err := s.db.StepQueue(ctx, c.Viewer, id, 0, "entry", p.entryID, s.now())
	if err != nil {
		s.playback.Drop(p.stream)
		return q, playback.Session{}, err
	}
	in := playback.StartInput{Caller: c, Key: token, Hash: token, ItemID: p.stream.ItemID, Prepared: p.stream,
		Queue: &playback.QueueLink{QueueID: id, EntryID: EntryID(p.entryID)}}
	session, _, _, err := s.playback.Start(ctx, in)
	if err != nil {
		s.playback.Drop(p.stream)
		return q, session, err
	}
	s.mu.Lock()
	s.committed[token] = session.ID
	s.advances[id]++
	if len(s.committed) > 1000 {
		clear(s.committed)
		s.committed[token] = session.ID
	}
	s.mu.Unlock()
	q, err = s.refill(ctx, c.Viewer, q)
	return q, session, err
}

// EntryID is the id an entry has on the wire. Apps never parse it.
func EntryID(id int64) string { return "qe" + strconv.FormatInt(id, 36) }

// ParseEntryID reads an entry id from the wire. An id that is not one names no entry.
func ParseEntryID(text string) int64 {
	rest, ok := strings.CutPrefix(text, "qe")
	if !ok {
		return -1
	}
	id, err := strconv.ParseInt(rest, 36, 64)
	if err != nil || id <= 0 {
		return -1
	}
	return id
}
