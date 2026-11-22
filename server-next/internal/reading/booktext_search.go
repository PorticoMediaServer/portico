package reading

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	excerptAround   = 80  // UTF-16 units of text kept on each side of a match
	sectionsPerPage = 400 // the most sections one page reads before it answers with what it found
	sectionsPerRead = 50
	maxMatch        = 4096 // UTF-16 units; with its surroundings an excerpt stays inside the 8192 a reader takes
)

// Search finds where the words of query stand, in that order, in the books the viewer may
// see (in one library when libraryID is not empty), after the place cursor names. A book
// whose file changed since it was read is left out until it is read again, so every hit's
// offset is right for the file the reader opens.
func (t *BookText) Search(ctx context.Context, d *db.DB, v db.Viewer, query, libraryID, cursor string, limit int) (BookHits, error) {
	out := BookHits{Revision: t.Revision()}
	spans := searchtext.Spans(query)
	if len(spans) == 0 {
		return out, nil // only marks, say: nothing a word could match
	}
	words := make([]string, len(spans))
	for n, s := range spans {
		words[n] = s.Word
	}
	afterSection, afterOffset, err := readCursor(cursor)
	if err != nil {
		return out, err
	}
	phrase := `"` + strings.Join(words, " ") + `"`
	// Only the books the viewer may see are read, so a page is never filled with hidden
	// books' sections and says nothing about them.
	visible, err := d.VisibleBookFiles(ctx, v, libraryID)
	if err != nil || len(visible) == 0 {
		return out, err
	}
	allowed, err := json.Marshal(visible)
	if err != nil {
		return out, err
	}
	read, hitSections := 0, []int64{}
	for read < sectionsPerPage {
		rows, err := t.conn.QueryContext(ctx, `SELECT s.id, s.file_id, s.spine, s.path, b.fingerprint
			FROM section_words w JOIN sections s ON s.id = w.rowid JOIN books b ON b.file_id = s.file_id
			WHERE section_words MATCH ? AND w.rowid >= ? AND s.file_id IN (SELECT value FROM json_each(?))
			ORDER BY w.rowid LIMIT ?`, phrase, afterSection, string(allowed), sectionsPerRead)
		if err != nil {
			return out, err
		}
		type candidate struct {
			id, fileID  int64
			spine       int
			path, print string
		}
		var found []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.fileID, &c.spine, &c.path, &c.print); err != nil {
				rows.Close()
				return out, err
			}
			found = append(found, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return out, err
		}
		if len(found) == 0 {
			return out, nil
		}
		ids := make([]int64, 0, len(found))
		for _, c := range found {
			ids = append(ids, c.fileID)
		}
		owners, err := d.BookTextOwners(ctx, v, ids, libraryID)
		if err != nil {
			return out, err
		}
		for _, c := range found {
			read++
			owner, ok := owners[c.fileID]
			if ok && Fingerprint(owner.Size, owner.ModifiedNS) == c.print {
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
			afterSection, afterOffset = c.id+1, -1
			if read == sectionsPerPage {
				break
			}
		}
		if len(found) < sectionsPerRead && read < sectionsPerPage {
			return out, nil
		}
	}
	// The page read as many sections as it may: it ends here, and the next one carries on.
	out.Next = writeCursor(afterSection, -1)
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
