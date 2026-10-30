package catalog

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// BookProgress keeps the playable part's position separate from the whole book.
// Missing lengths, unavailable/restricted parts and books over the bounded
// summary window never produce invented whole-book durations or offsets.
type BookProgress struct {
	BookID         string               `json:"bookId"`
	ItemID         string               `json:"itemId"`
	PartPositionMs int64                `json:"partPositionMs"`
	PositionMs     *int64               `json:"positionMs,omitempty"`
	DurationMs     *int64               `json:"durationMs,omitempty"`
	RemainingMs    *int64               `json:"remainingMs,omitempty"`
	Chapter        *BookProgressChapter `json:"chapter,omitempty"`
}

// Index is zero-based within ItemID and names an actual stored chapter mark.
// Part numbers are never chapter numbers. All chapter times are part-relative.
type BookProgressChapter struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}
type BookTimelinePart struct {
	ItemID     string `json:"itemId"`
	Title      string `json:"title"`
	DiscNumber *int   `json:"discNumber,omitempty"`
	PartNumber *int   `json:"partNumber,omitempty"`
	OffsetMs   *int64 `json:"offsetMs,omitempty"`
	DurationMs *int64 `json:"durationMs,omitempty"`
}
type BookSeek struct {
	ItemID  string `json:"itemId"`
	StartMs int64  `json:"startMs"`
}
type BookTimeline struct {
	BookID     string             `json:"bookId"`
	LibraryID  string             `json:"libraryId"`
	Revision   ContentRevision    `json:"revision"`
	Parts      []BookTimelinePart `json:"parts"`
	TotalCount int                `json:"totalCount"`
	NextCursor string             `json:"nextCursor"`
	DurationMs *int64             `json:"durationMs,omitempty"`
	Progress   *BookProgress      `json:"progress,omitempty"`
	Seek       *BookSeek          `json:"seek,omitempty"`
}

const bookPartOrder = `COALESCE(f.disc_number,1),COALESCE(f.part_number,0),file.sort_key,file.id`
const bookPartDuration = `(SELECT max(NULLIF(COALESCE(link.end_seconds,a.duration)-link.start_seconds,0)) FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=file.id AND link.available=1 AND a.available=1)`
const bookPartAvailable = `EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=file.id AND av.available=1 AND av.retired=0)`
const bookPartFrom = ` FROM catalog_entities book CROSS JOIN catalog_book_files f INDEXED BY catalog_book_files_listening_order ON f.book_id=book.id CROSS JOIN catalog_entities file ON file.id=f.entity_id WHERE book.public_id=pid_blob(?) AND book.kind=8 AND book.retired=0 AND file.retired=0`

type bookTimelineSummary struct {
	parts    map[string]BookTimelinePart
	duration *int64
}

func knownMillis(v sql.NullFloat64) *int64 {
	if !v.Valid || math.IsNaN(v.Float64) || math.IsInf(v.Float64, 0) || v.Float64 <= 0 || v.Float64 > float64(math.MaxInt64/1000) {
		return nil
	}
	n := int64(math.Round(v.Float64 * 1000))
	return &n
}
func (s *Service) bookTimelineSummary(viewer Viewer, book string) (bookTimelineSummary, error) {
	out := bookTimelineSummary{parts: map[string]BookTimelinePart{}}
	visible, args := viewer.itemVisibilitySQL("file.id")
	rows, err := s.read().Query(`SELECT pid(file.public_id),CASE WHEN (`+visible+`) AND `+bookPartAvailable+` THEN 1 ELSE 0 END,`+bookPartDuration+bookPartFrom+` ORDER BY `+bookPartOrder+` LIMIT 513`, append(args, book)...)
	if err != nil {
		return out, err
	}
	total, n, prefix := int64(0), 0, true
	for rows.Next() {
		var id string
		var permitted bool
		var seconds sql.NullFloat64
		if err = rows.Scan(&id, &permitted, &seconds); err != nil {
			break
		}
		n++
		duration := knownMillis(seconds)
		part := BookTimelinePart{ItemID: id}
		if permitted {
			part.DurationMs = duration
			if prefix && n <= 512 {
				v := total
				part.OffsetMs = &v
			}
			out.parts[id] = part
		}
		if !permitted || duration == nil || duration != nil && total > math.MaxInt64-*duration {
			prefix = false
		}
		if prefix {
			total += *duration
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err == nil && prefix && n > 0 && n <= 512 {
		out.duration = &total
	}
	return out, err
}
func (s *Service) bookProgressAt(viewer Viewer, book, item string, position int64, summary bookTimelineSummary) (*BookProgress, error) {
	part, ok := summary.parts[item]
	if !ok {
		// Current-part progress remains useful beyond the bounded whole-book
		// summary, without inventing that part's global offset.
		visible, args := viewer.itemVisibilitySQL("file.id")
		var seconds sql.NullFloat64
		err := s.read().QueryRow(`SELECT `+bookPartDuration+bookPartFrom+` AND file.public_id=pid_blob(?) AND `+visible+` AND `+bookPartAvailable, append([]any{book, item}, args...)...).Scan(&seconds)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		part = BookTimelinePart{ItemID: item, DurationMs: knownMillis(seconds)}
	}
	position = max(position, 0)
	if part.DurationMs != nil {
		position = min(position, *part.DurationMs)
	}
	out := &BookProgress{BookID: book, ItemID: item, PartPositionMs: position, DurationMs: summary.duration}
	if part.OffsetMs != nil {
		n := *part.OffsetMs + position
		out.PositionMs = &n
	}
	if out.PositionMs != nil && out.DurationMs != nil {
		n := max(int64(0), *out.DurationMs-*out.PositionMs)
		out.RemainingMs = &n
	}
	var chapter BookProgressChapter
	var start, end float64
	err := s.read().QueryRow(`SELECT c.chapter_index,c.title,c.start_seconds,c.end_seconds FROM catalog_book_chapters c JOIN catalog_entities file ON file.id=c.file_id WHERE file.public_id=pid_blob(?) AND c.start_seconds<=? AND c.end_seconds>? ORDER BY c.start_seconds DESC,c.chapter_index LIMIT 1`, item, float64(position)/1000, float64(position)/1000).Scan(&chapter.Index, &chapter.Title, &start, &end)
	if err == nil {
		chapter.StartMs = int64(math.Round(start * 1000))
		chapter.EndMs = int64(math.Round(end * 1000))
		out.Chapter = &chapter
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	return out, nil
}

// BookTimeline uses the caller's snapshot, current visibility and signed
// revision cursor. It resolves seeks; only the playback owner changes sessions.
func (s *Service) BookTimeline(r ContentRequest, book string, position *int64) (BookTimeline, error) {
	if _, ok := entityid.Decode(book); !ok {
		return BookTimeline{}, sql.ErrNoRows
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var out BookTimeline
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).BookTimeline(r, book, position)
			return err
		})
		return out, err
	}
	out := BookTimeline{BookID: book, Parts: []BookTimelinePart{}}
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		return out, sql.ErrNoRows
	}
	r = r.scoped()
	r.Limit = pageLimit(r.Limit)
	if r.Limit > 100 {
		r.Limit = 100
	}
	library, err := s.LibraryForAudioEntity("book", book)
	if err != nil {
		return out, err
	}
	if !r.Viewer.AllowsLibrary(library) {
		return out, sql.ErrNoRows
	}
	r.Library = library
	out.LibraryID = library
	if err = s.prepareViewer(r.Viewer); err != nil {
		return out, err
	}
	if err = s.compactProjectionReady(20); err != nil {
		return out, err
	}
	out.Revision, err = s.ContentRevision(library, r.Profile)
	if err != nil {
		return out, err
	}
	scope := cursorScope{Library: library, Profile: r.Profile, Viewer: r.ViewerFence, View: "book_timeline", Entity: book, Limit: r.Limit}
	offset := 0
	if r.Cursor != "" {
		cursor, e := s.contentContinuation(r.Cursor, scope)
		if e != nil {
			return out, e
		}
		offset, e = strconv.Atoi(cursor.Value)
		if e != nil || offset < 0 {
			return out, ErrCursor
		}
	}
	visible, args := r.Viewer.itemVisibilitySQL("file.id")
	where := bookPartFrom + ` AND ` + visible + ` AND ` + bookPartAvailable
	params := append([]any{book}, args...)
	if err = s.read().QueryRow(`SELECT count(*)`+where, params...).Scan(&out.TotalCount); err != nil {
		return out, err
	}
	if out.TotalCount == 0 {
		return out, sql.ErrNoRows
	}
	summary, err := s.bookTimelineSummary(r.Viewer, book)
	if err != nil {
		return out, err
	}
	out.DurationMs = summary.duration
	rows, err := s.read().Query(`SELECT pid(file.public_id),file.title,f.disc_number,f.part_number,`+bookPartDuration+where+` ORDER BY `+bookPartOrder+` LIMIT ? OFFSET ?`, append(params, r.Limit+1, offset)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var part BookTimelinePart
		var seconds sql.NullFloat64
		if err = rows.Scan(&part.ItemID, &part.Title, &part.DiscNumber, &part.PartNumber, &seconds); err != nil {
			break
		}
		part.DurationMs = knownMillis(seconds)
		part.OffsetMs = summary.parts[part.ItemID].OffsetMs
		out.Parts = append(out.Parts, part)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Parts) > r.Limit {
		out.Parts = out.Parts[:r.Limit]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: strconv.Itoa(offset + r.Limit), Expires: time.Now().Add(30 * time.Minute).Unix()}, out.Revision)
		if err != nil {
			return out, err
		}
	}
	var item string
	var bookmark int64
	err = s.read().QueryRow(`SELECT pid(file.public_id),resume.position FROM book_resume resume JOIN catalog_entities book ON book.id=resume.book_id JOIN catalog_entities file ON file.id=resume.item_id JOIN catalog_book_files f ON f.entity_id=file.id AND f.book_id=book.id WHERE resume.profile_id=? AND book.public_id=pid_blob(?) AND resume.completed=0 AND `+visible+` AND `+bookPartAvailable, append([]any{r.Profile, book}, args...)...).Scan(&item, &bookmark)
	if err == nil {
		out.Progress, err = s.bookProgressAt(r.Viewer, book, item, bookmark, summary)
	} else if err == sql.ErrNoRows {
		err = nil
	}
	if err != nil {
		return out, err
	}
	if position != nil {
		if *position < 0 {
			return out, requestinput.New("invalid book position")
		}
		for _, p := range summary.parts {
			if p.OffsetMs != nil && p.DurationMs != nil && *position >= *p.OffsetMs && *position < *p.OffsetMs+*p.DurationMs {
				out.Seek = &BookSeek{p.ItemID, *position - *p.OffsetMs}
				break
			}
		}
		if out.Seek == nil && summary.duration != nil && *position == *summary.duration {
			for _, p := range summary.parts {
				if p.OffsetMs != nil && p.DurationMs != nil && *p.OffsetMs+*p.DurationMs == *position {
					out.Seek = &BookSeek{p.ItemID, *p.DurationMs}
					break
				}
			}
		}
		if out.Seek == nil {
			return out, requestinput.New("book position has no verified available part")
		}
	}
	return out, nil
}

func (s *Service) enrichBookProgress(viewer Viewer, entry *ContentEntry, item string, position int64) error {
	book := entry.ID
	if entry.Kind == "audiobook_file" {
		if err := s.read().QueryRow(`SELECT pid(book.public_id) FROM catalog_book_files f JOIN catalog_entities file ON file.id=f.entity_id JOIN catalog_entities book ON book.id=f.book_id WHERE file.public_id=pid_blob(?)`, entry.ID).Scan(&book); err != nil {
			return err
		}
	}
	summary, err := s.bookTimelineSummary(viewer, book)
	if err != nil {
		return err
	}
	entry.BookProgress, err = s.bookProgressAt(viewer, book, item, position, summary)
	return err
}
