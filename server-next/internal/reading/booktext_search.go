package reading

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/searchtext"
)

// BookHit is one place in one book where the words searched for stand, in the order they
// were typed. Offset and MatchLength are in UTF-16 units of the section's normalised text;
// MatchStart is where the same words begin in Excerpt.
type BookHit struct {
	Owner        db.BookTextOwner
	Revision     string // the file's fingerprint when it was read
	SectionPath  string
	SectionIndex int
	Offset       int
	MatchLength  int
	Excerpt      string
	MatchStart   int
}

// BookHits is one page of hits. Next is where the following page starts, or "" after the last.
type BookHits struct {
	Hits     []BookHit
	Next     string
	Revision int64
}

// Why a search is refused.
var (
	ErrBadCursor = errors.New("this page of results is no longer available")
)

const (
	excerptAround = 80   // UTF-16 units of text kept on each side of a match
	maxMatch      = 4096 // UTF-16 units; with its surroundings an excerpt stays inside the 8192 a reader takes
	// A page reads the index a window of sections at a time, and stops starting new windows
	// after pageTime, answering with what it found and where to carry on. However common the
	// words and however few books the viewer may see, one page costs about the same.
	scanWindow = 20_000 // section ids
	scanRows   = 200    // matching sections read at a time
	ownerBatch = 50
	pageTime   = 250 * time.Millisecond
	// fewBooks is the most books whose own runs of sections are looked up so the scan reads
	// only those; beyond it, the whole index is scanned.
	fewBooks = 2000
	joinGap  = 2000 // section ids between two books' runs that are read through rather than skipped
)

// Search finds where the words of query stand, in that order, in the books the viewer may
// see (in one library when libraryID is not empty), after the place cursor names. A book
// whose file changed since it was read is left out until it is read again, so every hit's
// offset is right for the file the reader opens.
func (t *BookText) Search(ctx context.Context, d *db.DB, v db.Viewer, query, libraryID, cursor string, limit int) (out BookHits, err error) {
	// The revision is read once the page has been read: a change made while it was read is
	// already counted (replace raises it before its change can be seen), so a page is never
	// labelled older than the text it holds.
	defer func() { out.Revision = t.Revision() }()
	if len(searchtext.Spans(query)) == 0 {
		return out, nil // only marks, say: nothing a word could match
	}
	// Only the books the viewer may see are read: a hidden book's sections are passed over
	// and never fill a page, and nothing in the answer depends on them.
	visible, err := d.VisibleBookFiles(ctx, v, libraryID)
	if err != nil || len(visible) == 0 {
		return out, err
	}
	owners := func(ids []int64) (map[int64]db.BookTextOwner, error) { return d.BookTextOwners(ctx, v, ids, libraryID) }
	return t.search(ctx, visible, owners, query, cursor, limit)
}

// span is a run of section ids the scan reads.
type span struct{ lo, hi int64 }

// scanSpans are the runs of section ids a scan reads: for a few books, each book's own
// sections (a book's sections are written together, so their ids run on), runs close
// together joined; for more, the whole index.
func (t *BookText) scanSpans(ctx context.Context, visible []int64) ([]span, error) {
	if len(visible) > fewBooks {
		var lo, hi sql.NullInt64
		if err := t.conn.QueryRowContext(ctx, `SELECT min(id), max(id) FROM sections`).Scan(&lo, &hi); err != nil || !lo.Valid {
			return nil, err
		}
		return []span{{lo.Int64, hi.Int64}}, nil
	}
	list, _ := json.Marshal(visible)
	rows, err := t.conn.QueryContext(ctx, `SELECT min(id), max(id) FROM sections WHERE file_id IN (SELECT value FROM json_each(?))
		GROUP BY file_id ORDER BY 1`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []span
	for rows.Next() {
		var r span
		if err := rows.Scan(&r.lo, &r.hi); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+joinGap {
			out[n-1].hi = max(out[n-1].hi, r.hi)
		} else {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (t *BookText) search(ctx context.Context, visible []int64, owners func([]int64) (map[int64]db.BookTextOwner, error),
	query, cursor string, limit int) (out BookHits, err error) {
	spans := searchtext.Spans(query)
	words := make([]string, len(spans))
	for n, s := range spans {
		words[n] = s.Word
	}
	afterSection, afterOffset, err := readCursor(cursor)
	if err != nil {
		return out, err
	}
	phrase := `"` + strings.Join(words, " ") + `"`
	allowed := make(map[int64]bool, len(visible))
	for _, id := range visible {
		allowed[id] = true
	}
	runs, err := t.scanSpans(ctx, visible)
	if err != nil {
		return out, err
	}
	type candidate struct {
		id, fileID  int64
		spine       int
		path, print string
	}
	hitSections, deadline, first := []int64{}, time.Now().Add(pageTime), true
	pos := afterSection
	for _, run := range runs {
		if run.hi < pos {
			continue
		}
		pos = max(pos, run.lo)
		for pos <= run.hi {
			if !first && time.Now().After(deadline) {
				// The page has read as long as it may: it ends here, and the next one carries on.
				offset := -1
				if pos == afterSection {
					offset = afterOffset
				}
				out.Next = writeCursor(pos, offset)
				return out, nil
			}
			first = false
			end := min(pos+scanWindow, run.hi+1)
			rows, err := t.conn.QueryContext(ctx, `SELECT s.id, s.file_id, s.spine, s.path, b.fingerprint
				FROM section_words w JOIN sections s ON s.id = w.rowid JOIN books b ON b.file_id = s.file_id
				WHERE section_words MATCH ? AND w.rowid >= ? AND w.rowid < ? ORDER BY w.rowid LIMIT ?`, phrase, pos, end, scanRows)
			if err != nil {
				return out, err
			}
			var found []candidate
			read, next := 0, end
			for rows.Next() {
				var c candidate
				if err := rows.Scan(&c.id, &c.fileID, &c.spine, &c.path, &c.print); err != nil {
					rows.Close()
					return out, err
				}
				if read++; read == scanRows {
					next = c.id + 1
				}
				if allowed[c.fileID] {
					found = append(found, c)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return out, err
			}
			for len(found) > 0 {
				batch := found[:min(ownerBatch, len(found))]
				found = found[len(batch):]
				ids := make([]int64, 0, len(batch))
				for _, c := range batch {
					ids = append(ids, c.fileID)
				}
				known, err := owners(ids)
				if err != nil {
					return out, err
				}
				for _, c := range batch {
					owner, ok := known[c.fileID]
					if !ok || Fingerprint(owner.Size, owner.ModifiedNS) != c.print {
						continue
					}
					var text string
					if err := t.conn.QueryRowContext(ctx, `SELECT text FROM sections WHERE id = ?`, c.id).Scan(&text); err != nil {
						return out, err
					}
					from := -1
					if c.id == afterSection {
						from = afterOffset
					}
					for _, hit := range sectionHits(text, words, from) {
						if len(out.Hits) == limit {
							// One more hit is there: the next page starts after the last one sent.
							out.Next = writeCursor(hitSections[limit-1], out.Hits[limit-1].Offset)
							return out, nil
						}
						hit.Owner, hit.Revision, hit.SectionPath, hit.SectionIndex = owner, c.print, c.path, c.spine
						out.Hits, hitSections = append(out.Hits, hit), append(hitSections, c.id)
					}
				}
			}
			pos = next
		}
	}
	return out, nil
}

// sectionHits finds every place in text, after the UTF-16 offset from, where words stand in
// order, each with an excerpt around it.
func sectionHits(text string, words []string, from int) []BookHit {
	spans := searchtext.Spans(text)
	var units []uint16
	var out []BookHit
	for n := 0; n+len(words) <= len(spans); n++ {
		if spans[n].Start <= from {
			continue
		}
		match := true
		for k, w := range words {
			if spans[n+k].Word != w {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if units == nil {
			units = utf16.Encode([]rune(text))
		}
		start, end := spans[n].Start, spans[n+len(words)-1].End
		if end-start > maxMatch {
			continue // words far apart across a run of punctuation: no excerpt could carry it
		}
		first, last := max(0, start-excerptAround), min(len(units), end+excerptAround)
		if first > 0 && utf16.IsSurrogate(rune(units[first])) && units[first] >= 0xdc00 {
			first++
		}
		if last < len(units) && utf16.IsSurrogate(rune(units[last])) && units[last] >= 0xdc00 {
			last++
		}
		out = append(out, BookHit{Offset: start, MatchLength: end - start, Excerpt: string(utf16.Decode(units[first:last])), MatchStart: start - first})
	}
	return out
}

// A cursor is the section a page goes on from and the offset in it after which hits are new.
func writeCursor(section int64, offset int) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d.%d", section, offset))
}

func readCursor(cursor string) (section int64, offset int, err error) {
	if cursor == "" {
		return 0, -1, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, 0, ErrBadCursor
	}
	if _, err := fmt.Sscanf(string(raw), "%d.%d", &section, &offset); err != nil || section < 0 || offset < -1 {
		return 0, 0, ErrBadCursor
	}
	return section, offset, nil
}
