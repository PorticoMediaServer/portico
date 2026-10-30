package playbackv1

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Published window maximum (spec §8, invariant 8).
const WindowMax = 200

var shortEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newShortID is a segment or snapshot id: lowercase letters and digits only, so
// "<snapshot>-<ordinal>" entry ids are unambiguous and URL-safe.
func newShortID(prefix string) string {
	var raw [10]byte
	_, _ = rand.Read(raw[:])
	return prefix + shortEncoding.EncodeToString(raw[:])
}

// QueueView is the queue header (spec §8, §17.9): constant size.
type QueueView struct {
	ID              string        `json:"id"`
	Revision        string        `json:"revision"`
	Total           int64         `json:"total"`
	TotalDurationMs *int64        `json:"totalDurationMs,omitempty"`
	DurationKnown   bool          `json:"durationKnown"`
	RemainingCount  int64         `json:"remainingCount"`
	Current         *QueueCurrent `json:"current,omitempty"`
	Repeat          string        `json:"repeat"`
	Shuffle         *QueueShuffle `json:"shuffle,omitempty"`
	// Segments lists at most HeaderSegments of them, from the one holding the
	// current entry; SegmentCount is how many there are.
	Segments     []SegmentView `json:"segments"`
	SegmentCount int           `json:"segmentCount"`
	// Next and PostPlay (spec §18.5) are filled in by the server lane.
	Next     *QueueNext     `json:"next,omitempty"`
	PostPlay *QueuePostPlay `json:"postPlay,omitempty"`
}
type QueueCurrent struct {
	EntryID  string `json:"entryId"`
	Position int64  `json:"position"`
}
type QueueShuffle struct {
	Seed string `json:"seed"`
	Lap  int64  `json:"lap"`
}
type SegmentView struct {
	SourceRevision string `json:"sourceRevision,omitempty"`
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Count          int64  `json:"count"`
	State          string `json:"state"`
}

// QueueEntry is one window entry (ARCH-MEDIA-03 delivery).
type QueueEntry struct {
	EntryID   string `json:"entryId"`
	SegmentID string `json:"segmentId"`
	Position  int64  `json:"position"`
	ItemID    string `json:"itemId"`
	// LibraryID is this visible item's own current library, never its container's.
	LibraryID  string `json:"libraryId,omitempty"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Available  bool   `json:"available"`
	// AlbumID, Disc and Track place a song on its album (a book file in its
	// book), so a client joins consecutive tracks of one album gaplessly rather
	// than crossfading them (Plexamp's behavior, Plan §9.6). Absent when unknown.
	AlbumID     string        `json:"albumId,omitempty"`
	Album       string        `json:"album,omitempty"`
	ArtistID    string        `json:"artistId,omitempty"`
	Artist      string        `json:"artist,omitempty"`
	ShowID      string        `json:"showId,omitempty"`
	SeasonID    string        `json:"seasonId,omitempty"`
	Episode     *QueueEpisode `json:"episode,omitempty"`
	BackdropURL string        `json:"backdropUrl,omitempty"`
	Disc        int           `json:"disc,omitempty"`
	Track       int           `json:"track,omitempty"`
	// PosterURL is the title's poster (an episode's season or show poster, a
	// song's album cover, a book file's cover), present only when one exists.
	// StillURL is an episode's own still. A client's Up next card shows them.
	PosterURL string `json:"posterUrl,omitempty"`
	StillURL  string `json:"stillUrl,omitempty"`
}

type QueueEpisode struct {
	SeasonNumber int `json:"seasonNumber"`
	Number       int `json:"number"`
}

// Page is the style guide's collection envelope for queue entries (§17.9).
type Page struct {
	Limit      int    `json:"limit"`
	Total      int64  `json:"total"`
	Start      int64  `json:"start"`
	Revision   string `json:"revision"`
	NextCursor string `json:"nextCursor,omitempty"`
}
type EntryPage struct {
	Items []QueueEntry `json:"items"`
	Page  Page         `json:"page"`
}

// CreateQueueRequest is POST /v1/queues. The segment form is ARCH-MEDIA-03's;
// selector/shuffle/repeat is spec §17.10's single-selector shorthand.
type CreateQueueRequest struct {
	Owner string `json:"owner,omitempty"`
	// RetainQueueIDs keeps at most three previously committed queues on this
	// exact device and viewer. Only queue data is retained, never a session.
	RetainQueueIDs []string       `json:"retainQueueIds,omitempty"`
	Segments       []SegmentInput `json:"segments,omitempty"`
	Selector       *Selector      `json:"selector,omitempty"`
	Shuffle        *QueueSeed     `json:"shuffle,omitempty"`
	Repeat         string         `json:"repeat,omitempty"`
	StartPlayback  *StartPlayback `json:"startPlayback,omitempty"`
}
type QueueSeed struct {
	Seed string `json:"seed,omitempty"`
}
type StartPlayback struct {
	State   string   `json:"state,omitempty"`
	Quality *Quality `json:"quality,omitempty"`
}

// QueueReply is a command's answer: the new header and, when a session
// started (Play, advance), that session (§17.8).
type QueueReply struct {
	Queue   QueueView    `json:"queue"`
	Window  []QueueEntry `json:"window,omitempty"`
	Session *SessionView `json:"session,omitempty"`
}

// AddSegmentRequest is POST /v1/queues/{id}/segments.
type AddSegmentRequest struct {
	Placement Placement `json:"placement"`
	Source    Selector  `json:"source"`
	Order     *Order    `json:"order,omitempty"`
	Anchor    *Anchor   `json:"anchor,omitempty"`
}

// Placement is "next", "end" or {after: entryId}.
type Placement struct {
	Where string
	After string
}

func (p *Placement) UnmarshalJSON(b []byte) error {
	var word string
	if json.Unmarshal(b, &word) == nil {
		if word != "next" && word != "end" {
			return errors.New("unknown placement")
		}
		p.Where = word
		return nil
	}
	var v struct {
		After string `json:"after"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil || v.After == "" {
		return errors.New("placement needs after")
	}
	p.Where, p.After = "after", v.After
	return nil
}
func (Placement) JSONSchema() map[string]any { return map[string]any{} }

// MoveRequest is POST /v1/queues/{id}/entries/{entryId}:move.
type MoveRequest struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// QueueChange is PATCH /v1/queues/{id}.
type QueueChange struct {
	Repeat  string         `json:"repeat,omitempty"`
	Shuffle *ShuffleSwitch `json:"shuffle,omitempty"`
}
type ShuffleSwitch struct {
	On   bool   `json:"on"`
	Seed string `json:"seed,omitempty"`
}

// AdvanceRequest is POST /v1/queues/{id}:advance.
type AdvanceRequest struct {
	Reason  string `json:"reason"`
	EntryID string `json:"entryId,omitempty"`
	// A receiver completion names the occurrence it actually finished. A queue
	// header revision alone cannot identify a replaced session on the same entry.
	CompletionSessionID  string `json:"completionSessionId,omitempty"`
	CompletionGeneration int    `json:"completionGeneration,omitempty"`
}

// queueRow is one queues_v1 row.
type queueRow struct {
	automatic                                                     int
	custody                                                       int64
	currentEntry                                                  string
	id, ownerKind, ownerID, authority, account, profile, session  string
	repeat                                                        string
	revision, current, seed, lap, domain, first, created, updated int64
	shuffle                                                       bool
}

const queueColumns = `id,owner_kind,owner_id,authority,account_id,profile_id,session_id,repeat,revision,current_position,shuffle_seed,shuffle_lap,shuffle_domain,shuffle_first,created_ms,updated_ms,shuffle_on,automatic_advances,custody_generation,current_entry_id`

func scanQueue(r scanner) (queueRow, error) {
	var q queueRow
	err := r.Scan(&q.id, &q.ownerKind, &q.ownerID, &q.authority, &q.account, &q.profile, &q.session, &q.repeat, &q.revision, &q.current, &q.seed, &q.lap, &q.domain, &q.first, &q.created, &q.updated, &q.shuffle, &q.automatic, &q.custody, &q.currentEntry)
	return q, err
}

// segment is one queue_v1_segments row with its snapshot's kind and label.
type segment struct {
	musicOnly                          bool
	id, snapshot, kind, label, sortKey string
	sourceRevision                     string
	first, count, removed              int64
	// A snapshot still building (P16) holds keys [0, built); the rest are
	// pending, except one resolved ahead (hint). waiting: a start waits for it.
	state   string
	built   int64
	waiting bool
	hint    int64
	hintKey string
}

func (g segment) live() int64 { return g.count - g.removed }

// layout is a queue's segments in order plus the removals inside them; it is
// read once per request (a queue has few segments) and answers position ↔
// entry in O(segments + removals), independent of queue length.
type layout struct {
	sonicWindows map[int64]sonicWindowCapture
	sonicMisses  map[int64]bool
	sonicPrepare func(*layout, int64) error
	sonicErr     error
	spans        []orderSpan
	q            queueRow
	segments     []segment
	removals     map[string][]int64 // snapshot → sorted ordinals
	chunks       map[string][]byte  // snapshot/chunk → keys (per request)
	building     []segment          // segments of snapshots still building
	tx           *sql.Tx
	ctx          context.Context
}

func (s *Service) readLayout(ctx context.Context, tx *sql.Tx, q queueRow) (*layout, error) {
	l := &layout{q: q, removals: map[string][]int64{}, chunks: map[string][]byte{}, tx: tx, ctx: ctx}
	rows, err := tx.QueryContext(ctx, `SELECT g.id,g.snapshot_id,s.kind,s.label,g.sort_key,g.first_ordinal,g.count,g.removed,s.state,s.built,s.start<>'',s.hint_ordinal,s.hint_key,s.source_revision,s.music_only FROM queue_v1_segments g JOIN queue_v1_snapshots s ON s.id=g.snapshot_id WHERE g.queue_id=? ORDER BY g.sort_key`, q.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g segment
		if err = rows.Scan(&g.id, &g.snapshot, &g.kind, &g.label, &g.sortKey, &g.first, &g.count, &g.removed, &g.state, &g.built, &g.waiting, &g.hint, &g.hintKey, &g.sourceRevision, &g.musicOnly); err != nil {
			return nil, err
		}
		g.waiting = g.waiting && g.state == "building"
		l.segments = append(l.segments, g)
		if g.state == "building" {
			l.building = append(l.building, g)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	removals, err := tx.QueryContext(ctx, `SELECT r.snapshot_id,r.ordinal FROM queue_v1_removals r JOIN queue_v1_snapshots s ON s.id=r.snapshot_id WHERE s.queue_id=? ORDER BY r.snapshot_id,r.ordinal`, q.id)
	if err != nil {
		return nil, err
	}
	defer removals.Close()
	for removals.Next() {
		var snap string
		var ordinal int64
		if err = removals.Scan(&snap, &ordinal); err != nil {
			return nil, err
		}
		l.removals[snap] = append(l.removals[snap], ordinal)
	}
	if err = removals.Err(); err != nil {
		return nil, err
	}
	removals.Close()
	if q.shuffle {
		rows, e := tx.QueryContext(ctx, `SELECT source,start,count FROM queue_v1_play_spans WHERE queue_id=? ORDER BY rank`, q.id)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var span orderSpan
			if e = rows.Scan(&span.source, &span.start, &span.count); e != nil {
				rows.Close()
				return nil, e
			}
			l.spans = append(l.spans, span)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return l, nil
}

func (l *layout) total() int64 {
	if l.q.shuffle && len(l.spans) > 0 {
		var n int64
		for _, span := range l.spans {
			n += span.count
		}
		return n
	}
	var n int64
	for _, g := range l.segments {
		n += g.live()
	}
	return n
}

// sourceAt maps a source position to (segment index, absolute ordinal).
func (l *layout) sourceAt(p int64) (int, int64, bool) {
	if p < 0 {
		return 0, 0, false
	}
	for i, g := range l.segments {
		live := g.live()
		if l.q.shuffle {
			live = g.count
		}
		if p < live {
			ordinal := g.first + p
			for _, r := range l.removals[g.snapshot] {
				if l.q.shuffle {
					break
				}
				if r < g.first || r >= g.first+g.count {
					continue
				}
				if r <= ordinal {
					ordinal++
				}
			}
			return i, ordinal, true
		}
		p -= live
	}
	return 0, 0, false
}

// sourceIndexOf maps an entry id to its source position.
func (l *layout) sourceIndexOf(entry string) (int64, int, bool) {
	snap, ordinalText, ok := strings.Cut(entry, "-")
	ordinal, err := strconv.ParseInt(ordinalText, 10, 64)
	if !ok || err != nil {
		return 0, 0, false
	}
	var base int64
	for i, g := range l.segments {
		if g.snapshot == snap && ordinal >= g.first && ordinal < g.first+g.count {
			before := int64(0)
			for _, r := range l.removals[snap] {
				if r == ordinal {
					return 0, 0, false
				}
				if !l.q.shuffle && r >= g.first && r < ordinal {
					before++
				}
			}
			return base + ordinal - g.first - before, i, true
		}
		if l.q.shuffle {
			base += g.count
		} else {
			base += g.live()
		}
	}
	return 0, 0, false
}

func (l *layout) shuffle() *Shuffle {
	if !l.q.shuffle || l.q.domain < 1 {
		return nil
	}
	s, err := NewShuffle(uint64(l.q.domain), uint32(l.q.seed), uint32(l.q.lap))
	if err != nil {
		return nil
	}
	return s
}

// orderToSource: play order → source position. With shuffle on, the current
// entry at the time is position 0, the rest follow the permutation, and
// entries added later form an unshuffled tail (spec §17.10).
func (l *layout) orderToSource(p int64) int64 {
	if l.q.shuffle && len(l.spans) > 0 {
		for _, s := range l.spans {
			if p < s.count {
				if s.source {
					return s.start + p
				}
				return l.baseOrderToSource(s.start + p)
			}
			p -= s.count
		}
		return -1
	}
	return l.baseOrderToSource(p)
}
func (l *layout) sourceToOrder(src int64) int64 {
	if l.q.shuffle && len(l.spans) > 0 {
		base := l.baseSourceToOrder(src)
		var pos int64
		for _, s := range l.spans {
			at := base
			if s.source {
				at = src
			}
			if at >= s.start && at < s.start+s.count {
				return pos + at - s.start
			}
			pos += s.count
		}
		return -1
	}
	return l.baseSourceToOrder(src)
}

// key reads an entry's item key from its snapshot chunk (cached per request).
// pendingKey says whether a snapshot's ordinal isn't built yet (and the one
// key resolved ahead of the build, if it's that one).
func (l *layout) pendingKey(snap string, ordinal int64) (string, bool) {
	for _, g := range l.building {
		if g.snapshot == snap && ordinal >= g.built {
			if ordinal == g.hint && g.hintKey != "" {
				return g.hintKey, true
			}
			return "", true
		}
	}
	return "", false
}

// waiting says whether a start waits for a building snapshot (no current yet).
func (l *layout) waiting() bool {
	for _, g := range l.building {
		if g.waiting {
			return true
		}
	}
	return false
}

func (l *layout) key(snap string, ordinal int64) (string, error) {
	if key, pending := l.pendingKey(snap, ordinal); pending {
		if key != "" {
			return key, nil
		}
		return "", ErrQueueBuilding
	}
	no := ordinal / chunkKeys
	cacheKey := snap + "/" + strconv.FormatInt(no, 10)
	chunk, ok := l.chunks[cacheKey]
	if !ok {
		if err := l.tx.QueryRowContext(l.ctx, `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, snap, no).Scan(&chunk); err != nil {
			return "", err
		}
		l.chunks[cacheKey] = chunk
	}
	k, ok := chunkKey(chunk, int(ordinal%chunkKeys))
	if !ok {
		return "", ErrNotFound
	}
	return k, nil
}

// prefetch reads, in one statement, every chunk that positions [from, from+n)
// need and the request hasn't read yet: a shuffled window touches up to n
// different chunks, which were n statements (P14).
func (l *layout) prefetch(from, n int64) error {
	if n <= 0 {
		return nil
	}
	sources := make([]int64, 0, min(n, WindowMax))
	for p := from; p < from+n; p++ {
		sources = append(sources, l.orderToSource(p))
	}
	return l.prefetchSources(sources)
}

func (l *layout) prefetchSources(sources []int64) error {
	type ref struct {
		Snapshot string `json:"s"`
		Chunk    int64  `json:"c"`
	}
	want := []ref{}
	seen := map[string]bool{}
	for _, source := range sources {
		i, ordinal, ok := l.sourceAt(source)
		if !ok {
			continue
		}
		snap, no := l.segments[i].snapshot, ordinal/chunkKeys
		if _, pending := l.pendingKey(snap, ordinal); pending {
			continue
		}
		cacheKey := snap + "/" + strconv.FormatInt(no, 10)
		if _, cached := l.chunks[cacheKey]; cached || seen[cacheKey] {
			continue
		}
		seen[cacheKey] = true
		want = append(want, ref{snap, no})
	}
	if len(want) == 0 {
		return nil
	}
	raw, _ := json.Marshal(want)
	rows, err := l.tx.QueryContext(l.ctx, `SELECT c.snapshot_id,c.chunk_no,c.keys FROM json_each(?) w JOIN queue_v1_chunks c ON c.snapshot_id=json_extract(w.value,'$.s') AND c.chunk_no=json_extract(w.value,'$.c')`, string(raw))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var snap string
		var no int64
		var keys []byte
		if err = rows.Scan(&snap, &no, &keys); err != nil {
			return err
		}
		l.chunks[snap+"/"+strconv.FormatInt(no, 10)] = keys
	}
	return rows.Err()
}

// entryAt resolves a play-order position to its entry id and item key.
func (l *layout) entryAt(p int64) (string, string, bool) {
	entry, key, err := l.entryAtErr(p)
	return entry, key, err == nil
}

// entryAtErr is entryAt that tells a pending entry (ErrQueueBuilding: it has an
// id and a place, but its key isn't built yet) from one that doesn't exist.
func (l *layout) entryAtErr(p int64) (string, string, error) {
	source := l.orderToSource(p)
	if l.sonicErr != nil {
		return "", "", l.sonicErr
	}
	i, ordinal, ok := l.sourceAt(source)
	if !ok {
		return "", "", ErrNotFound
	}
	snap := l.segments[i].snapshot
	entry := snap + "-" + strconv.FormatInt(ordinal, 10)
	key, err := l.key(snap, ordinal)
	if errors.Is(err, ErrQueueBuilding) {
		return entry, "", err
	}
	if err != nil {
		return "", "", ErrNotFound
	}
	return entry, key, nil
}

func (l *layout) positionOf(entry string) (int64, bool) {
	src, _, ok := l.sourceIndexOf(entry)
	if !ok {
		return 0, false
	}
	p := l.sourceToOrder(src)
	return p, p >= 0
}

func (s *Service) header(l *layout) QueueView {
	v := QueueView{ID: l.q.id, Revision: strconv.FormatInt(l.q.revision, 10), Total: l.total(), Repeat: l.q.repeat, Segments: []SegmentView{}, RemainingCount: max(0, l.total()-l.q.current-1)}
	// While a start waits for its snapshot (P16) there's no current entry; a
	// current entry that's still pending has its id and position already.
	if !l.waiting() {
		if entry, _, err := l.entryAtErr(l.q.current); err == nil || errors.Is(err, ErrQueueBuilding) {
			v.Current = &QueueCurrent{EntryID: entry, Position: l.q.current}
		}
	}

	if l.q.shuffle {
		v.Shuffle = &QueueShuffle{Seed: strconv.FormatInt(l.q.seed, 10), Lap: l.q.lap}
	}
	// A constant-size header (P15): the segments from the current one on.
	v.SegmentCount = len(l.segments)
	start := 0
	if !l.waiting() {
		if i, _, ok := l.sourceAt(l.orderToSource(l.q.current)); ok {
			start = i
		}
	}
	start = max(0, min(start, len(l.segments)-HeaderSegments))
	for _, g := range l.segments[start:min(len(l.segments), start+HeaderSegments)] {
		kind := g.kind
		if kind != "items" && kind != "radio" {
			kind = "selector"
		}
		v.Segments = append(v.Segments, SegmentView{ID: g.id, Kind: kind, Label: g.label, Count: g.live(), State: g.state, SourceRevision: g.sourceRevision})
	}
	return v
}

// HeaderSegments is the most segments a queue header lists.
const HeaderSegments = 64

// Visibility is the item fence as one SQL predicate over an item id expression:
// it admits only what the caller may play (library access, profile
// restrictions, member limits). HTTP installs contentaccess.VisibleItemsSQL.
type Visibility func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (string, []any, error)

// visibleFilter keeps, in order, the ids the caller may play: one statement per
// call, for a window or a chunk of a snapshot. Inaccessible and absent ids are
// the same: dropped.
func (s *Service) visibleFilter(ctx context.Context, tx *sql.Tx, p identity.Principal) (func([]string) ([]string, error), error) {
	// The filter reads catalogue facts; the visibility clause brings its own
	// reads. No derived-domain wait here.
	clause, args := "1", []any(nil)
	if s.Visibility != nil {
		var err error
		if clause, args, err = s.Visibility(ctx, tx, p, "i.id"); err != nil {
			return nil, err
		}
	}
	query := `SELECT v.value FROM json_each(?) v JOIN catalog_entities i ON i.public_id=pid_blob(v.value) JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 WHERE ` + clause + ` ORDER BY v.key`
	return func(ids []string) ([]string, error) {
		if len(ids) == 0 {
			return nil, nil
		}
		rows, err := tx.QueryContext(ctx, query, append([]any{jsonList(ids)}, args...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]string, 0, len(ids))
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}, nil
}

// window resolves positions [from, from+n) with one batched item read and one
// set-based authorization; unavailable entries are placeholders (ARCH-MEDIA-03).
func (s *Service) window(ctx context.Context, l *layout, p identity.Principal, from, n int64) ([]QueueEntry, error) {
	total := l.total()
	if from < 0 {
		from = 0
	}
	n = max(0, min(n, WindowMax, total-from))
	if err := l.prefetch(from, n); err != nil {
		return nil, err
	}
	out := make([]QueueEntry, 0, n)
	keys := make([]string, 0, n)
	for i := int64(0); i < n; i++ {
		entry, key, err := l.entryAtErr(from + i)
		segmentID := ""
		if si, _, ok := l.sourceAt(l.orderToSource(from + i)); ok {
			segmentID = l.segments[si].id
		}
		if errors.Is(err, ErrQueueBuilding) {
			// Still being snapshotted (P16): a placeholder that fills in shortly.
			out = append(out, QueueEntry{EntryID: entry, SegmentID: segmentID, Position: from + i, Kind: "pending"})
			continue
		}
		if err != nil {
			continue
		}
		out = append(out, QueueEntry{EntryID: entry, SegmentID: segmentID, Position: from + i, ItemID: key})
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return out, nil
	}
	// Facts only (entities, songs, book files, links, assets): no wait.
	type info struct {
		entity                                     int64
		kind, title, album, still, library         string
		albumTitle, artistID, artist, show, season string
		seasonNumber, episodeNumber                int
		backdrop                                   bool
		duration                                   float64
		disc, track                                int
		poster                                     bool
	}
	found := map[string]info{}
	// Artwork is keyed lookups on the selection's primary key: the item's own
	// still, and whether the poster the item art route would serve exists (its
	// own, else its album's or book's cover, else its season's or show's).
	rows, err := l.tx.QueryContext(ctx, `SELECT pid(i.public_id),i.id,k.name,i.title,COALESCE(lib.library_id,''),
 COALESCE((SELECT a.duration FROM catalog_asset_links x JOIN catalog_assets a ON a.id=x.asset_id WHERE x.entity_id=i.id ORDER BY x.part_index,a.id LIMIT 1),0),
 COALESCE(pid(album.public_id),pid(book.public_id),''),COALESCE(s.disc_number,b.disc_number,0),COALESCE(s.track_number,b.part_number,0),
 COALESCE((SELECT a.role||'/'||a.digest FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.kind='item' AND a.entity_id=i.id AND a.role IN('still','thumbnail') AND a.subject='' ORDER BY a.role LIMIT 1),''),
 EXISTS(SELECT 1 FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.subject='' AND (
  (a.kind='item' AND a.entity_id=i.id AND a.role IN('poster','cover')) OR (a.kind='album' AND a.entity_id=s.album_id AND a.role='cover') OR
  (a.kind='book' AND a.entity_id=b.book_id AND a.role='cover') OR (a.kind='season' AND a.entity_id=ep.season_id AND a.role='poster') OR
  (a.kind='show' AND a.entity_id=ep.show_id AND a.role='poster'))),
 COALESCE(album.title,book.title,''),COALESCE(pid(artist.public_id),''),COALESCE(artist.title,''),
 COALESCE(pid(show.public_id),''),COALESCE(pid(season.public_id),''),COALESCE(sn.number,0),COALESCE(ep.number,0),
 EXISTS(SELECT 1 FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.subject='' AND a.role='backdrop' AND ((a.kind='item' AND a.entity_id=i.id) OR (a.kind='show' AND a.entity_id=ep.show_id)))
	FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1
 LEFT JOIN catalog_libraries lib ON lib.id=i.library_id AND lib.retired=0
	LEFT JOIN catalog_songs s ON s.entity_id=i.id LEFT JOIN catalog_entities album ON album.id=s.album_id
	LEFT JOIN catalog_book_files b ON b.entity_id=i.id LEFT JOIN catalog_entities book ON book.id=b.book_id
	LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id
 LEFT JOIN catalog_albums al ON al.entity_id=s.album_id LEFT JOIN catalog_entities artist ON artist.id=al.artist_id
 LEFT JOIN catalog_entities show ON show.id=ep.show_id LEFT JOIN catalog_entities season ON season.id=ep.season_id
 LEFT JOIN catalog_seasons sn ON sn.entity_id=ep.season_id
	WHERE i.public_id IN (SELECT pid_blob(value) FROM json_each(?))`, jsonList(keys))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var v info
		if err = rows.Scan(&id, &v.entity, &v.kind, &v.title, &v.library, &v.duration, &v.album, &v.disc, &v.track, &v.still, &v.poster, &v.albumTitle, &v.artistID, &v.artist, &v.show, &v.season, &v.seasonNumber, &v.episodeNumber, &v.backdrop); err != nil {
			rows.Close()
			return nil, err
		}
		found[id] = v
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	wanted := make([]durationKey, 0, len(found))
	for _, v := range found {
		wanted = append(wanted, durationKey{entity: v.entity})
	}
	lengths, err := durationsOf(ctx, l.tx, wanted)
	if err != nil {
		return nil, err
	}
	filter, err := s.visibleFilter(ctx, l.tx, p)
	if err != nil {
		return nil, err
	}
	allowed, err := filter(keys)
	if err != nil {
		return nil, err
	}
	visible := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		visible[id] = true
	}
	stills := map[int][2]string{}
	for i := range out {
		if out[i].Kind == "pending" {
			continue
		}
		v, ok := found[out[i].ItemID]
		if ok && visible[out[i].ItemID] {
			out[i].Kind, out[i].Title, out[i].DurationMs, out[i].Available = v1Kind(v.kind), v.title, lengths[durationKey{entity: v.entity}], true
			out[i].LibraryID = v.library
			out[i].Album, out[i].ArtistID, out[i].Artist = v.albumTitle, v.artistID, v.artist
			out[i].ShowID, out[i].SeasonID = v.show, v.season
			if v.kind == "episode" {
				out[i].Episode = &QueueEpisode{SeasonNumber: v.seasonNumber, Number: v.episodeNumber}
			}
			out[i].AlbumID, out[i].Disc, out[i].Track = v.album, max(v.disc, 0), max(v.track, 0)
			escaped := url.PathEscape(out[i].ItemID)
			if v.backdrop {
				out[i].BackdropURL = "/v1/items/" + escaped + "/art/backdrop"
			}
			if v.poster {
				out[i].PosterURL = "/v1/items/" + escaped + "/art/poster"
			}
			if role, digest, ok := strings.Cut(v.still, "/"); ok {
				stills[i] = [2]string{role, digest}
			}
		} else {
			// A title that's gone or became restricted after the snapshot is a
			// placeholder: no id, no title, not playable.
			out[i].ItemID, out[i].Kind, out[i].Available = "", "unavailable", false
		}
	}
	// Every picture carries its source and both slots, so a client caches it
	// for good and picks the slot its box needs.
	sources := make([]string, 0, len(stills))
	for _, still := range stills {
		sources = append(sources, still[1])
	}
	described, err := artworkref.Load(ctx, l.tx, sources)
	if err != nil {
		return nil, err
	}
	urls := make([]*string, 0, 2*len(out))
	for i := range out {
		if still, ok := stills[i]; ok {
			if d, ok := described[still[1]]; ok {
				out[i].StillURL = "/v1/metadata/item/" + url.PathEscape(out[i].ItemID) + "/art/" + still[0] + "?" + d.Query(still[1])
			}
		}
		urls = append(urls, &out[i].PosterURL, &out[i].BackdropURL)
	}
	if err = artworkref.DescribeItemURLs(ctx, l.tx, urls...); err != nil {
		return nil, err
	}
	return out, nil
}

const sortDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// sortKeyBetween is a fractional key strictly between a and b ("" as b is the
// open end; a < b), so inserting a segment between two others is one row
// write. Generated keys never end in '0', which keeps a gap below every key.
func sortKeyBetween(a, b string) string {
	digit := func(s string, i int) int {
		if i < len(s) {
			return strings.IndexByte(sortDigits, s[i])
		}
		return 0
	}
	if b == "" {
		// Appending is the common case: step the first digit that can still
		// grow, so keys lengthen by one character per ~35 appends.
		for i := 0; ; i++ {
			if d := digit(a, i); d < len(sortDigits)-1 {
				return a[:min(i, len(a))] + strings.Repeat("0", max(0, i-len(a))) + string(sortDigits[d+1])
			}
		}
	}
	var out []byte
	for i := 0; ; i++ {
		lo, hi := digit(a, i), len(sortDigits)
		if b != "" {
			hi = digit(b, i)
		}
		if b != "" && hi == lo {
			out = append(out, sortDigits[lo])
			continue
		}
		if hi-lo > 1 {
			return string(append(out, sortDigits[(lo+hi)/2]))
		}
		// Adjacent digits: keep a's digit and find room above the rest of a.
		out = append(out, sortDigits[lo])
		b = ""
	}
}

func seedFrom(text string) (int64, error) {
	if text == "" {
		var raw [4]byte
		_, _ = rand.Read(raw[:])
		return int64(binary.BigEndian.Uint32(raw[:])), nil
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, &FieldError{Path: "shuffle.seed"}
	}
	return int64(uint32(n)), nil
}

func loadQueue(ctx context.Context, q querier, id string) (queueRow, error) {
	row, err := scanQueue(q.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM queues_v1 WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

// ownsQueue: the queue's profile, from its own device or another of that
// profile's devices. The device alone is never enough: another profile signed
// in on the same TV can't read or control it (P17).
// ownsQueue: the caller's profile's queue, and a live one: a queue being
// staged ("~" owner) or retired by a replacement ("!" owner) is no one's.
// Root42 transfers permanently narrow custody to its exact device; stale source
// controllers cannot recover queue command authority through profile ownership.
func ownsQueue(c Caller, q queueRow) bool {
	return q.authority == c.Principal.Authority && q.account == c.Principal.AccountID && q.profile == c.Principal.ProfileID &&
		!strings.HasPrefix(q.ownerID, "~") && !strings.HasPrefix(q.ownerID, "!") && (q.custody == 0 || q.ownerKind == "device" && q.ownerID == c.DeviceID)
}

func (s *Service) queueEventsTx(ctx context.Context, tx *sql.Tx, l *layout, now time.Time) error {
	v := s.header(l)
	if l.sonicErr != nil {
		return l.sonicErr
	}
	// Persist the authoritative header alongside every layout mutation/event.
	// Transfer reads this indexed fact instead of walking segments/removals/spans.
	entry := ""
	if v.Current != nil {
		entry = v.Current.EntryID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE queues_v1 SET current_entry_id=? WHERE id=?`, entry, l.q.id); err != nil {
		return err
	}
	l.q.currentEntry = entry
	data := map[string]any{"total": v.Total}
	if v.Current != nil {
		data["current"] = v.Current
	}
	resource := &EventResource{Kind: "queue", ID: l.q.id}
	rev := strconv.FormatInt(l.q.revision, 10)
	if l.q.ownerKind == "group" {
		return publishTx(ctx, tx, now, GroupAudience(l.q.ownerID), "queue.updated", resource, rev, data)
	}
	if err := publishTx(ctx, tx, now, DeviceAudience(l.q.ownerID), "queue.updated", resource, rev, data); err != nil {
		return err
	}
	return publishTx(ctx, tx, now, ProfileAudience(l.q.authority, l.q.account, l.q.profile), "queue.updated", resource, rev, data)
}
