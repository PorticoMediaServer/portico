package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Selector is the style guide's tagged union (amendment 9): exactly one of a
// container, a browse query, or an explicit list of at most 500 items.
type Selector struct {
	Container *ContainerRef       `json:"container,omitempty"`
	Query     *catalog.QueueQuery `json:"query,omitempty"`
	Saved     *SavedRef           `json:"saved,omitempty"`
	Items     *ItemList           `json:"items,omitempty"`
	Journey   *JourneySelector    `json:"journey,omitempty"`
}
type JourneySelector struct {
	From   string `json:"from" ref:"title"`
	To     string `json:"to" ref:"title"`
	Length int    `json:"length"`
}
type SavedRef struct {
	ID string `json:"id"`
}

func (s Selector) isQuery() bool { return s.Query != nil || s.Saved != nil }

type ContainerRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id" ref:"title"`
}
type ItemList struct {
	IDs []string `json:"ids" ref:"title"`
}

// Order is a segment's order: its source order or a seeded shuffle.
type Order struct {
	Mode string `json:"mode"`
	Seed string `json:"seed,omitempty"`
}

// Anchor says where playback starts in a segment: an item, a position, or
// "resume" (a show's next episode for this viewer). In a playlist, EntryID
// names one occurrence of an item that appears more than once, so the copy
// that was pressed is the one that plays; ItemID, when also given, is where
// it starts should that occurrence have been removed meanwhile.
type Anchor struct {
	ItemID   string
	EntryID  string
	Position *int64
	Resume   bool
}

func (a *Anchor) UnmarshalJSON(b []byte) error {
	var word string
	if json.Unmarshal(b, &word) == nil {
		if word != "resume" {
			return errors.New("unknown anchor")
		}
		a.Resume = true
		return nil
	}
	var v struct {
		ItemID   string `json:"itemId"`
		EntryID  string `json:"entryId"`
		Position *int64 `json:"position"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	if (v.ItemID == "" && v.EntryID == "") == (v.Position == nil) {
		return errors.New("anchor needs itemId, entryId or position")
	}
	if v.EntryID != "" && !itemIDPattern.MatchString(v.EntryID) {
		return errors.New("anchor entryId is not an entry id")
	}
	a.ItemID, a.EntryID, a.Position = v.ItemID, v.EntryID, v.Position
	return nil
}
func (a Anchor) MarshalJSON() ([]byte, error) {
	switch {
	case a.Resume:
		return []byte(`"resume"`), nil
	case a.Position != nil:
		return json.Marshal(map[string]int64{"position": *a.Position})
	case a.EntryID != "":
		out := map[string]string{"entryId": a.EntryID}
		if a.ItemID != "" {
			out["itemId"] = a.ItemID
		}
		return json.Marshal(out)
	}
	return json.Marshal(map[string]string{"itemId": a.ItemID})
}
func (Anchor) JSONSchema() map[string]any { return map[string]any{} }

// SegmentInput is one segment of a create or an insert.
type SegmentInput struct {
	Source Selector `json:"source"`
	Order  *Order   `json:"order,omitempty"`
	Anchor *Anchor  `json:"anchor,omitempty"`
}

// MaxSnapshotKeys is the resource bound per segment (ARCH-MEDIA-03: a
// resource bound, not a product limit).
const MaxSnapshotKeys = 10_000_000

// chunkKeys is how many item keys one stored chunk holds; a window of 200
// shuffled entries reads at most 200 chunks of this size.
const chunkKeys = 256

var itemIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func (s Selector) validate(path string) error {
	n := 0
	if s.Journey != nil {
		n++
		j := s.Journey
		if !itemIDPattern.MatchString(j.From) || !itemIDPattern.MatchString(j.To) || j.From == j.To || j.Length < 2 || j.Length > 128 {
			return &FieldError{Path: path + ".journey"}
		}
	}
	if s.Container != nil {
		n++
		if !itemIDPattern.MatchString(s.Container.ID) || s.Container.Kind == "" || len(s.Container.Kind) > 32 {
			return &FieldError{Path: path + ".container"}
		}
	}
	if s.Query != nil {
		n++
	}
	if s.Saved != nil {
		n++
		if !itemIDPattern.MatchString(s.Saved.ID) {
			return &FieldError{Path: path + ".saved.id"}
		}
	}
	if s.Items != nil {
		n++
		if len(s.Items.IDs) == 0 || len(s.Items.IDs) > 500 {
			return &FieldError{Path: path + ".items.ids"}
		}
		for _, id := range s.Items.IDs {
			if !itemIDPattern.MatchString(id) {
				return &FieldError{Path: path + ".items.ids"}
			}
		}
	}
	if n != 1 {
		return &FieldError{Path: path}
	}
	return nil
}

// containerQuery is how one container kind lists its items in canonical order
// (ARCH-MEDIA-05): the library it belongs to, a label, and the ordered keys.
type containerQuery struct {
	library, label, keys string
	// arg transforms the container id into the key query's arguments.
	args func(id string) ([]any, error)
}

// containerDomains is the derived-domain wait for a container kind's key
// query. Container queries read catalogue facts only (entities, side tables,
// links, members, playlist entries); facts are synchronous, so nothing is
// waited for. It stays because keysQuery calls it.
func containerDomains(kind string) []int {
	return nil
}

// Episodes play in season order, then episode (or air date) order. Specials
// (season 0) are left out unless the play starts from a special (ARCH-MEDIA-05,
// default excluded).
const episodeOrder = ` ORDER BY COALESCE(s.number,1),CASE WHEN e.numbering='date' THEN COALESCE(e.air_date,0) ELSE e.number END,item.id`

var containers = map[string]containerQuery{
	"album": {library: `SELECT l.library_id FROM catalog_entities a JOIN catalog_albums detail ON detail.entity_id=a.id JOIN catalog_libraries l ON l.id=a.library_id WHERE a.public_id=pid_blob(?) AND a.kind=6 AND a.retired=0`, label: `SELECT a.title FROM catalog_entities a JOIN catalog_albums detail ON detail.entity_id=a.id WHERE a.public_id=pid_blob(?) AND a.kind=6 AND a.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities album JOIN catalog_albums detail ON detail.entity_id=album.id CROSS JOIN catalog_songs song INDEXED BY catalog_songs_listening_order ON song.album_id=album.id CROSS JOIN catalog_entities item ON item.id=song.entity_id WHERE album.public_id=pid_blob(?) AND album.kind=6 AND album.retired=0 ORDER BY COALESCE(song.disc_number,1),COALESCE(song.track_number,0),song.entity_id`},
	// An artist spans libraries: no single library to check; each song is
	// filtered by the caller's access instead (P13).
	"artist": {label: `SELECT a.title FROM catalog_entities a JOIN catalog_artists detail ON detail.entity_id=a.id WHERE a.public_id=pid_blob(?) AND a.kind=5 AND a.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities artist JOIN catalog_artists owner ON owner.entity_id=artist.id JOIN catalog_albums a ON a.artist_id=artist.id JOIN catalog_entities album ON album.id=a.entity_id CROSS JOIN catalog_songs song INDEXED BY catalog_songs_listening_order ON song.album_id=album.id CROSS JOIN catalog_entities item ON item.id=song.entity_id WHERE artist.public_id=pid_blob(?) AND artist.kind=5 AND artist.retired=0 ORDER BY album.year,album.sort_key,album.id,COALESCE(song.disc_number,1),COALESCE(song.track_number,0),song.entity_id`},
	"book": {library: `SELECT l.library_id FROM catalog_entities b JOIN catalog_books detail ON detail.entity_id=b.id JOIN catalog_libraries l ON l.id=b.library_id WHERE b.public_id=pid_blob(?) AND b.kind=8 AND b.retired=0`, label: `SELECT b.title FROM catalog_entities b JOIN catalog_books detail ON detail.entity_id=b.id WHERE b.public_id=pid_blob(?) AND b.kind=8 AND b.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities book JOIN catalog_books detail ON detail.entity_id=book.id CROSS JOIN catalog_book_files part INDEXED BY catalog_book_files_listening_order ON part.book_id=book.id CROSS JOIN catalog_entities item ON item.id=part.entity_id WHERE book.public_id=pid_blob(?) AND book.kind=8 AND book.retired=0 ORDER BY COALESCE(part.disc_number,1),COALESCE(part.part_number,0),part.entity_id`},
	"show": {library: `SELECT l.library_id FROM catalog_entities sh JOIN catalog_shows detail ON detail.entity_id=sh.id JOIN catalog_libraries l ON l.id=sh.library_id WHERE sh.public_id=pid_blob(?) AND sh.kind=2 AND sh.retired=0`, label: `SELECT sh.title FROM catalog_entities sh JOIN catalog_shows detail ON detail.entity_id=sh.id WHERE sh.public_id=pid_blob(?) AND sh.kind=2 AND sh.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities show JOIN catalog_shows detail ON detail.entity_id=show.id JOIN catalog_episodes e ON e.show_id=show.id LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id JOIN catalog_entities item ON item.id=e.entity_id WHERE show.public_id=pid_blob(?) AND show.kind=2 AND show.retired=0 AND (COALESCE(s.number,1)<>0 OR ?)` + episodeOrder},
	"season": {library: `SELECT l.library_id FROM catalog_entities se JOIN catalog_seasons detail ON detail.entity_id=se.id JOIN catalog_libraries l ON l.id=se.library_id WHERE se.public_id=pid_blob(?) AND se.kind=3 AND se.retired=0`, label: `SELECT sh.title||' · '||CASE WHEN se.number=0 THEN 'Specials' ELSE 'Season '||se.number END FROM catalog_entities season JOIN catalog_seasons se ON se.entity_id=season.id JOIN catalog_entities sh ON sh.id=se.show_id WHERE season.public_id=pid_blob(?) AND season.kind=3 AND season.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities season JOIN catalog_seasons detail ON detail.entity_id=season.id JOIN catalog_episodes e ON e.season_id=season.id LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id JOIN catalog_entities item ON item.id=e.entity_id WHERE season.public_id=pid_blob(?) AND season.kind=3 AND season.retired=0` + episodeOrder},
	"collection": {library: `SELECT l.library_id FROM catalog_entities c JOIN catalog_collections detail ON detail.entity_id=c.id JOIN catalog_libraries l ON l.id=c.library_id WHERE c.public_id=pid_blob(?) AND c.kind=10 AND c.retired=0`, label: `SELECT c.title FROM catalog_entities c JOIN catalog_collections detail ON detail.entity_id=c.id WHERE c.public_id=pid_blob(?) AND c.kind=10 AND c.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities collection JOIN catalog_collections detail ON detail.entity_id=collection.id JOIN catalog_collection_members member ON member.collection_id=collection.id JOIN catalog_entities item ON item.id=member.item_id WHERE collection.public_id=pid_blob(?) AND collection.kind=10 AND collection.retired=0 ORDER BY member.order_key,item.id`},
	"playlist": {label: `SELECT name FROM catalog_playlists WHERE token=? AND deleted=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_playlists playlist JOIN catalog_playlist_entries entry ON entry.playlist_id=playlist.id JOIN catalog_entities item ON item.id=entry.item_id WHERE playlist.token=? AND playlist.deleted=0 AND entry.item_id IS NOT NULL ORDER BY entry.order_key,entry.id`},
	"disc": {library: `SELECT l.library_id FROM catalog_entities a JOIN catalog_albums detail ON detail.entity_id=a.id JOIN catalog_libraries l ON l.id=a.library_id WHERE a.public_id=pid_blob(?) AND a.kind=6 AND a.retired=0`, label: `SELECT a.title FROM catalog_entities a JOIN catalog_albums detail ON detail.entity_id=a.id WHERE a.public_id=pid_blob(?) AND a.kind=6 AND a.retired=0`,
		keys: `SELECT pid(item.public_id) FROM catalog_entities album JOIN catalog_albums detail ON detail.entity_id=album.id CROSS JOIN catalog_songs song INDEXED BY catalog_songs_listening_order ON song.album_id=album.id CROSS JOIN catalog_entities item ON item.id=song.entity_id WHERE album.public_id=pid_blob(?) AND album.kind=6 AND album.retired=0 AND COALESCE(song.disc_number,1)=? ORDER BY COALESCE(song.track_number,0),item.id`,
		args: func(id string) ([]any, error) {
			album, disc, ok := strings.Cut(id, ":")
			n, err := strconv.Atoi(disc)
			if !ok || err != nil || n < 0 {
				return nil, &FieldError{Path: "source.container.id"}
			}
			return []any{album, n}, nil
		}},
}

// snapshot is one selector frozen into keys. It is scanned in a read snapshot,
// never inside the queue's write transaction (P16): a snapshot that fits in one
// batch is kept here and written by the queue command itself; a larger one is
// written while it is scanned, a batch per short write at background
// priority, and never held in memory whole.
type snapshot struct {
	checkBasis                func(context.Context, *sql.Tx) error
	id, kind, label, selector string
	sourceRevision            string
	count                     int64
	anchor                    int64
	pending                   []chunkRow // written with the queue command
	stored                    bool       // the snapshot row exists already
	// A scan stopped by its budget is partial: seen source rows are accounted
	// for, and the builder continues from there (P16).
	partial bool
	seen    int64
	// anchorItem is an item anchor not reached before the scan stopped;
	// anchorAt a playlist occurrence's source position not reached.
	anchorItem string
	anchorAt   *int64
	// For a partial snapshot, what the command decided (see CreateQueue):
	// declared is its full size when known up front (a shuffle), start a queue
	// start that waits for it, hint one key resolved ahead, caller whose access
	// the background build filters by.
	declared int64
	start    string
	hint     int64
	hintKey  string
	caller   string
}

// size is how many entries the segment has: all of them when known, else what's built.
func (snap snapshot) size() int64 {
	if snap.declared > 0 {
		return snap.declared
	}
	return snap.count
}

type chunkRow struct {
	no   int64
	keys []byte
}

// snapshotBatchChunks is how many chunks (256 keys each) one write holds: the
// most a queue command writes itself, and the size of each background batch.
const snapshotBatchChunks = 32

// MaxQueueKeys bounds a queue's entries across all its segments (a resource
// bound, like MaxSnapshotKeys).
const MaxQueueKeys = 10_000_000

func (s *Service) queueKeyLimit() int64 {
	if s.QueueKeyLimit > 0 {
		return s.QueueKeyLimit
	}
	return MaxQueueKeys
}

// storeSnapshot writes what the scan kept in memory (the snapshot row for a
// small one, the last chunks, the count and label) inside the queue command.
func storeSnapshot(ctx context.Context, tx *sql.Tx, queue string, snap *snapshot) error {
	if snap.checkBasis != nil {
		if err := snap.checkBasis(ctx, tx); err != nil {
			return err
		}
	}
	if !snap.stored {
		if _, err := tx.ExecContext(ctx, `INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width,music_only) VALUES(?,?,?,?,?,0,0,1)`, snap.id, queue, snap.kind, snap.selector, ""); err != nil {
			return err
		}
	}
	for _, c := range snap.pending {
		if err := writeQueueChunkTx(ctx, tx, snap.id, c.no, c.keys, false); err != nil {
			return err
		}
	}
	state := "ready"
	if snap.partial {
		state = "building"
	}
	hint := int64(-1)
	if snap.hintKey != "" {
		hint = snap.hint
	}
	_, err := tx.ExecContext(ctx, `UPDATE queue_v1_snapshots SET count=?,label=?,state=?,built=?,seen=?,caller=?,start=?,hint_ordinal=?,hint_key=?,source_revision=? WHERE id=?`, snap.size(), snap.label, state, snap.count, snap.seen, snap.caller, snap.start, hint, snap.hintKey, snap.sourceRevision, snap.id)
	return err
}

// discardSnapshot removes a snapshot whose batches were written but whose
// queue command didn't commit.
func (s *Service) discardSnapshot(ctx context.Context, snap snapshot) {
	s.releaseCapture(snap.id)
	if snap.stored {
		_, _ = dbwork.ExecWrite(context.WithoutCancel(ctx), s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM queue_v1_snapshots WHERE id=?`, snap.id)
	}
}

// LibraryCheck says whether the caller may use a library (hooked by HTTP).
type LibraryCheck func(ctx context.Context, tx *sql.Tx, p identity.Principal, library string) error

// resumeIndex is the show's next episode for this viewer (ARCH-MEDIA-05
// "Resume a show"), as its index in the show's source order: the most recently
// played unfinished episode, else the one after the last finished, else the
// first. One statement, in the key query's own order.
func (s *Service) resumeIndex(ctx context.Context, tx *sql.Tx, p identity.Principal, show string, specials bool) int64 {
	profile := identity.PersonalKey(p.Viewer)
	rows, err := tx.QueryContext(ctx, `SELECT `+personalstate.CompactSQL("?", "item.id")+`,COALESCE(pr.position,0),COALESCE(pi.last_played_at,'') FROM catalog_entities sh JOIN catalog_episodes e ON e.show_id=sh.id LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id JOIN catalog_entities item ON item.id=e.entity_id LEFT JOIN personal_items pi ON pi.profile_id=? AND pi.item_id=item.id LEFT JOIN progress pr ON pr.profile_id=? AND pr.item_id=item.id WHERE sh.public_id=pid_blob(?) AND (COALESCE(s.number,1)<>0 OR ?)`+episodeOrder, profile, profile, profile, show, specials)
	if err != nil {
		return -1
	}
	defer rows.Close()
	best, bestAt, afterWatched := int64(-1), "", int64(0)
	for ordinal := int64(0); rows.Next(); ordinal++ {
		var watched int
		var position int64
		var at string
		if rows.Scan(&watched, &position, &at) != nil {
			return -1
		}
		if watched == 1 {
			afterWatched = ordinal + 1
		} else if position > 0 && at >= bestAt {
			best, bestAt = ordinal, at
		}
	}
	if best >= 0 {
		return best
	}
	return afterWatched
}

func jsonList(ids []string) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// encodeChunk stores keys at a fixed width (the chunk's longest key) behind a
// one-byte width, so key k is one slice.
func encodeChunk(keys []string) []byte {
	width := 1
	for _, k := range keys {
		width = max(width, len(k))
	}
	out := make([]byte, 1+width*len(keys))
	out[0] = byte(width)
	for i, k := range keys {
		copy(out[1+i*width:], k)
	}
	return out
}

func chunkKey(chunk []byte, i int) (string, bool) {
	if len(chunk) < 1 {
		return "", false
	}
	width := int(chunk[0])
	start := 1 + i*width
	if width == 0 || start+width > len(chunk) {
		return "", false
	}
	return strings.TrimRight(string(chunk[start:start+width]), "\x00"), true
}
