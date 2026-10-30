package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/listeningrules"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrListeningResumeUnavailable = errors.New("the saved book part is unavailable; choose a part or start from the beginning")

type ListeningSelectionEntry struct {
	ItemID    string          `json:"itemId"`
	EditionID *string         `json:"editionId"`
	PartID    *string         `json:"partId"`
	Source    ListeningSource `json:"sourceContext"`
}
type ListeningSelection struct {
	Target           ListeningTarget           `json:"target"`
	Entries          []ListeningSelectionEntry `json:"entries"`
	TotalCount       int                       `json:"totalCount"`
	UnavailableCount int                       `json:"unavailableCount"`
	NextCursor       string                    `json:"nextCursor"`
	Seed             string                    `json:"seed"`
	Resume           *BookResume               `json:"resume,omitempty"`
}

type listeningContinuation struct {
	Offset  int                `json:"offset"`
	Seed    string             `json:"seed"`
	Last    *listeningOrderKey `json:"last,omitempty"`
	Total   int                `json:"total,omitempty"`
	Missing int                `json:"missing,omitempty"`
}

// listeningWhere verifies every parent against the already authorized library.
// Disc navigation uses the existing album ID, not another media identity.
func (s *Service) listeningWhere(t ListeningTarget) (string, []any, error) {
	switch t.Kind {
	case "library":
		if t.ID != t.LibraryID {
			return "", nil, sql.ErrNoRows
		}
		var kind string
		if e := s.read().QueryRow(`SELECT CASE kind WHEN 4 THEN 'music' WHEN 5 THEN 'audiobook' ELSE '' END FROM catalog_libraries WHERE library_id=? AND retired=0`, t.LibraryID).Scan(&kind); e != nil {
			return "", nil, e
		}
		if kind != "music" {
			return "", nil, requestinput.New("play a book rather than an entire audiobook library")
		}
		return `i.kind=7`, nil, nil
	case "song":
		var yes bool
		e := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?) AND l.library_id=? AND i.kind=7 AND i.retired=0)`, t.ID, t.LibraryID).Scan(&yes)
		if e != nil {
			return "", nil, e
		}
		if !yes {
			return "", nil, sql.ErrNoRows
		}
		return `i.public_id=pid_blob(?)`, []any{t.ID}, nil
	case "artist", "album", "book", "disc":
		kind, id := t.Kind, t.ID
		disc := 0
		var e error
		if kind == "disc" {
			id, disc, e = discTarget(id)
			if e != nil {
				return "", nil, e
			}
			kind = "album"
		}
		lib, e := s.LibraryForAudioEntity(kind, id)
		if e != nil {
			return "", nil, e
		}
		if lib != t.LibraryID {
			return "", nil, sql.ErrNoRows
		}
		if t.Kind == "book" {
			return `i.kind=9 AND EXISTS(SELECT 1 FROM catalog_book_files f JOIN catalog_entities book ON book.id=f.book_id WHERE f.entity_id=i.id AND book.public_id=pid_blob(?) AND book.retired=0)`, []any{id}, nil
		}
		if t.Kind == "artist" {
			return `i.kind=7 AND EXISTS(SELECT 1 FROM catalog_songs ss JOIN catalog_albums aa ON aa.entity_id=ss.album_id JOIN catalog_entities artist ON artist.id=aa.artist_id WHERE ss.entity_id=i.id AND (artist.public_id=pid_blob(?) OR EXISTS(SELECT 1 FROM catalog_song_artists sa JOIN catalog_entities credited ON credited.id=sa.artist_id WHERE sa.song_id=i.id AND credited.public_id=pid_blob(?))))`, []any{id, id}, nil
		}
		where := `i.kind=7 AND EXISTS(SELECT 1 FROM catalog_songs ss JOIN catalog_entities album ON album.id=ss.album_id WHERE ss.entity_id=i.id AND album.public_id=pid_blob(?) AND album.retired=0`
		args := []any{id}
		if t.Kind == "disc" {
			where += ` AND COALESCE(ss.disc_number,1)=?`
			args = append(args, disc)
		}
		return where + `)`, args, nil
	}
	return "", nil, requestinput.New("invalid listening selection")
}
func (s *Service) ListeningSelection(r ContentRequest, t ListeningTarget, mode, seed string, resume bool, startItem string) (ListeningSelection, error) {
	out := ListeningSelection{Target: t, Entries: []ListeningSelectionEntry{}}
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		if r.Cursor != "" {
			return out, ErrCursor
		}
		return out, sql.ErrNoRows
	}
	if !r.Viewer.AllowsLibrary(t.LibraryID) || r.Library != t.LibraryID {
		return out, sql.ErrNoRows
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return out, err
	}
	if len(startItem) > 128 || strings.ContainsAny(startItem, "\x00\r\n:") || startItem != "" && (mode != "ordered" || resume) {
		return out, requestinput.New("invalid listening start")
	}
	if mode != "ordered" && mode != "mix" {
		return out, requestinput.New("invalid listening selection mode")
	}
	if resume && t.Kind != "book" {
		return out, requestinput.New("resume requires a book")
	}
	if mode == "mix" && t.Kind == "book" {
		return out, requestinput.New("instant mix requires music")
	}
	if t.Kind == "book" {
		if err := s.compactProjectionReady(20); err != nil {
			return out, err
		}
	} else if err := s.compactProjectionReady(18, 20); err != nil {
		return out, err
	}
	where, args, e := s.listeningWhere(t)
	if e != nil {
		return out, e
	}
	where += ` AND i.retired=0`
	restriction, bound := ItemRestrictionSQL("i.id", r.Restrictions)
	where += ` AND ` + restriction
	args = append(args, bound...)
	if t.Kind != "library" {
		var visible bool
		if e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities i WHERE i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND i.retired=0 AND `+where+`)`, append([]any{t.LibraryID}, args...)...).Scan(&visible); e != nil {
			return out, e
		}
		if !visible {
			return out, sql.ErrNoRows
		}
	}
	r.Limit = pageLimit(r.Limit)
	revision, e := s.ContentRevision(t.LibraryID, r.Profile)
	if e != nil {
		return out, e
	}
	scope := cursorScope{Library: t.LibraryID, Profile: r.Profile, Viewer: r.ViewerFence, View: "listening_selection", Entity: t.ID, Sort: mode, Category: t.Kind, Search: strconv.FormatBool(resume) + ":" + startItem, Limit: r.Limit}
	offset := 0
	var continuation listeningContinuation
	if r.Cursor != "" {
		c, e := s.decodeRevisionCursor(r.Cursor, scope, revision)
		if e != nil {
			return out, e
		}
		if json.Unmarshal([]byte(c.Value), &continuation) != nil || continuation.Offset < 0 {
			return out, ErrCursor
		}
		if mode == "ordered" && (continuation.Last == nil || continuation.Last.ID < 1 || continuation.Missing < 0 || continuation.Total < continuation.Offset) {
			return out, ErrCursor
		}
		offset, seed = continuation.Offset, continuation.Seed
	}
	if mode == "mix" {
		if seed == "" {
			seed = identity.Token()
		}
		if len(seed) > 128 || strings.ContainsAny(seed, "\x00\r\n") {
			return out, requestinput.New("invalid mix seed")
		}
	}
	out.Seed = seed
	if resume && t.Kind == "book" {
		var saved BookResume
		resumeArgs := append([]any{t.ID, r.Profile}, bound...)
		var resumeMillis int64
		e = s.read().QueryRow(`SELECT pid(i.public_id),r.position FROM book_resume r JOIN catalog_entities i ON i.id=r.item_id JOIN catalog_book_files f ON f.entity_id=i.id AND f.book_id=r.book_id JOIN catalog_entities book ON book.id=r.book_id WHERE book.public_id=pid_blob(?) AND r.profile_id=? AND `+restriction, resumeArgs...).Scan(&saved.ItemID, &resumeMillis)
		if e == nil {
			saved.PositionSeconds, e = listeningrules.Resume(float64(resumeMillis)/1000, r.ResumeRewindMs)
			if e != nil {
				return out, e
			}
			out.Resume = &saved
			var available bool
			if e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_item_availability v JOIN catalog_entities i ON i.id=v.entity_id WHERE i.public_id=pid_blob(?) AND v.available=1 AND v.retired=0)`, saved.ItemID).Scan(&available); e != nil {
				return out, e
			}
			if !available {
				return out, ErrListeningResumeUnavailable
			}
		} else if e != sql.ErrNoRows {
			return out, e
		}
	}
	var eligible []listeningCandidate
	end := 0
	hasMore := false
	if mode == "ordered" {
		anchor := startItem
		if out.Resume != nil {
			anchor = out.Resume.ItemID
		}
		var known *listeningCounts
		if r.Cursor != "" {
			known = &listeningCounts{continuation.Total, continuation.Missing}
		}
		// One extra row says whether more exist; the queue pages by keyset.
		eligible, out.TotalCount, out.UnavailableCount, e = s.orderedListeningPage(t, where, args, anchor, continuation.Last, known, r.Viewer.EffectiveRestrictions(), r.Limit+1)
		if e != nil {
			return out, e
		}
		if offset > out.TotalCount || len(eligible) == 0 && offset < out.TotalCount {
			return out, ErrStaleContinuation
		}
		hasMore = len(eligible) > r.Limit
		if hasMore {
			eligible = eligible[:r.Limit]
		}
		end = offset + len(eligible)
	} else {
		candidates := []listeningCandidate{}
		params := append([]any{t.LibraryID}, args...)
		var upper int64
		// The library's newest entity: the greatest of each kind's newest, each
		// one seek on (library_id,kind,id). A plain max(id) read every entity.
		if e = s.read().QueryRow(`SELECT COALESCE(max((SELECT max(x.id) FROM catalog_entities x WHERE x.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND x.kind=k.id)),0) FROM catalog_kinds k`, t.LibraryID).Scan(&upper); e != nil {
			return out, e
		}
		hash := sha256.Sum256([]byte(seed))
		pivot := int64(0)
		if upper > 0 {
			pivot = int64(binary.BigEndian.Uint64(hash[:8])%uint64(upper)) + 1
		}
		// Score at most 640 candidates gathered by indexed album, artist, and
		// library membership. A seed makes ordering reproducible across pages.
		query := `WITH seed_items AS (SELECT i.id FROM catalog_entities i WHERE i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where + ` ORDER BY i.id LIMIT 64), mix_ids AS MATERIALIZED (
 SELECT id FROM seed_items
	 UNION SELECT entity_id FROM (SELECT x.entity_id FROM catalog_songs x WHERE x.album_id IN(SELECT y.album_id FROM catalog_songs y JOIN seed_items z ON z.id=y.entity_id) ORDER BY x.entity_id LIMIT 192)
	 UNION SELECT song_id FROM (SELECT x.song_id FROM catalog_song_artists x JOIN catalog_entities candidate ON candidate.id=x.song_id WHERE x.artist_id IN(SELECT y.artist_id FROM catalog_song_artists y JOIN seed_items z ON z.id=y.song_id) ORDER BY candidate.id LIMIT 192)
	 UNION SELECT id FROM (
 SELECT id,0 tier FROM (SELECT id FROM catalog_entities WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=7 AND id>=? ORDER BY id LIMIT 192)
 UNION ALL SELECT id,1 tier FROM (SELECT id FROM catalog_entities WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=7 AND id<? ORDER BY id LIMIT 192)
 ORDER BY tier,id LIMIT 192)
	 ) SELECT pid(i.public_id),EXISTS(SELECT 1 FROM catalog_item_availability v WHERE v.entity_id=i.id AND v.available=1 AND v.retired=0),
   CASE WHEN i.id IN(SELECT id FROM seed_items) THEN 8 ELSE 0 END+
	   CASE WHEN EXISTS(SELECT 1 FROM catalog_song_artists x JOIN catalog_song_artists y ON y.artist_id=x.artist_id WHERE x.song_id=i.id AND y.song_id IN(SELECT id FROM seed_items)) THEN 4 ELSE 0 END+
	   CASE WHEN EXISTS(SELECT 1 FROM catalog_songs x JOIN catalog_songs y ON y.album_id=x.album_id WHERE x.entity_id=i.id AND y.entity_id IN(SELECT id FROM seed_items)) THEN 2 ELSE 0 END+
	   CASE WHEN EXISTS(SELECT 1 FROM catalog_entity_terms own JOIN catalog_terms term ON term.id=own.term_id JOIN catalog_entity_terms other ON other.term_id=own.term_id WHERE own.entity_id=i.id AND term.vocab=1 AND other.entity_id IN(SELECT id FROM seed_items)) THEN 1 ELSE 0 END
	   FROM catalog_entities i WHERE i.id IN(SELECT id FROM mix_ids) AND i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND i.kind=7 AND i.retired=0 AND ` + restriction
		// After seed_items: the library branch of mix_ids, the genre evidence
		// (keyed by the public library id) and the outer library filter.
		params = append(params, t.LibraryID, pivot, t.LibraryID, pivot, t.LibraryID)
		params = append(params, bound...)
		rows, e := s.read().Query(query, params...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var c listeningCandidate
			if e = rows.Scan(&c.id, &c.available, &c.score); e != nil {
				rows.Close()
				return out, e
			}
			h := sha256.Sum256([]byte(seed + ":" + c.id))
			c.rank = hex.EncodeToString(h[:])
			candidates = append(candidates, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			return candidates[i].rank < candidates[j].rank
		})
		eligible = []listeningCandidate{}
		for _, c := range candidates {
			if c.available {
				eligible = append(eligible, c)
			} else {
				out.UnavailableCount++
			}
		}
		out.TotalCount = len(eligible)
		if offset > len(eligible) {
			return out, ErrCursor
		}
		end = offset + r.Limit
		if end > len(eligible) {
			end = len(eligible)
		}
		hasMore = end < out.TotalCount
		eligible = eligible[offset:end]
	}
	sourceKind, sourceID := t.Kind, t.ID
	if sourceKind == "disc" {
		sourceKind = "album"
		sourceID, _, _ = discTarget(t.ID)
	}
	for _, c := range eligible {
		kind, id := sourceKind, sourceID
		if kind == "library" || kind == "song" {
			kind, id = "item", c.id
		}
		rev := strconv.FormatInt(revision.Catalog, 10)
		var revisionPtr *string
		if revision.Catalog > 0 {
			revisionPtr = &rev
		}
		out.Entries = append(out.Entries, ListeningSelectionEntry{ItemID: c.id, Source: ListeningSource{Kind: kind, ID: id, Revision: revisionPtr}})
	}
	if hasMore {
		value := listeningContinuation{Offset: end, Seed: seed, Total: out.TotalCount, Missing: out.UnavailableCount}
		if mode == "ordered" && len(eligible) > 0 {
			key := eligible[len(eligible)-1].key
			value.Last = &key
		}
		raw, _ := json.Marshal(value)
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: string(raw), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
		if e != nil {
			return out, e
		}
	}
	after, e := s.ContentRevision(t.LibraryID, r.Profile)
	if e != nil {
		return out, e
	}
	if after != revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}

type ListeningItem struct {
	ItemID           string `json:"itemId"`
	LibraryID        string `json:"libraryId"`
	Kind             string `json:"kind"`
	BookID           string `json:"bookId,omitempty"`
	BookEndAvailable *bool  `json:"bookEndAvailable,omitempty"`
	LastBookItemID   string `json:"lastBookItemId,omitempty"`
	AlbumID          string `json:"albumId,omitempty"`
	ArtistID         string `json:"artistId,omitempty"`
}

func (s *Service) ListeningItem(viewer Viewer, id string) (ListeningItem, error) {
	var out ListeningItem
	if err := s.prepareViewer(viewer); err != nil {
		return out, err
	}
	visibility, bound := viewer.itemVisibilitySQL("i.id")
	args := append([]any{id}, bound...)
	e := s.read().QueryRow(`SELECT pid(i.public_id),l.library_id,`+compactBrowseKindSQL("i.kind")+` FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?) AND i.retired=0 AND `+visibility, args...).Scan(&out.ItemID, &out.LibraryID, &out.Kind)
	if e != nil {
		return out, e
	}
	if out.Kind == "audiobook_file" {
		if e = s.compactProjectionReady(20); e != nil {
			return out, e
		}
		partVisibility, partBound := viewer.itemVisibilitySQL("x.id")
		partArgs := append(append([]any{}, partBound...), id)
		e = s.read().QueryRow(`SELECT pid(book.public_id),COALESCE((SELECT pid(x.public_id) FROM catalog_book_files part INDEXED BY catalog_book_files_listening_order JOIN catalog_entities x ON x.id=part.entity_id WHERE part.book_id=f.book_id AND x.retired=0 AND `+partVisibility+` ORDER BY COALESCE(part.disc_number,1) DESC,COALESCE(part.part_number,0) DESC,x.sort_key DESC,part.entity_id DESC LIMIT 1),'') FROM catalog_entities i JOIN catalog_book_files f ON f.entity_id=i.id JOIN catalog_entities book ON book.id=f.book_id WHERE i.public_id=pid_blob(?) AND book.retired=0`, partArgs...).Scan(&out.BookID, &out.LastBookItemID)
	}
	if e == nil && out.Kind == "audiobook_file" {
		var available bool
		e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_item_availability v JOIN catalog_entities i ON i.id=v.entity_id WHERE i.public_id=pid_blob(?) AND v.available=1 AND v.retired=0)`, out.LastBookItemID).Scan(&available)
		out.BookEndAvailable = &available
	}
	if out.Kind == "song" {
		e = s.read().QueryRow(`SELECT pid(album.public_id),pid(artist.public_id) FROM catalog_entities i JOIN catalog_songs s ON s.entity_id=i.id JOIN catalog_entities album ON album.id=s.album_id JOIN catalog_albums a ON a.entity_id=album.id JOIN catalog_entities artist ON artist.id=a.artist_id WHERE i.public_id=pid_blob(?)`, id).Scan(&out.AlbumID, &out.ArtistID)
	}
	return out, e
}

// ListeningSource is where a listening selection's entry came from (the
// container it was chosen in), as a v1 queue entry records it.
type ListeningSource struct {
	Kind     string  `json:"kind"`
	ID       string  `json:"id"`
	Revision *string `json:"revision"`
	EntryID  *string `json:"entryId"`
}
