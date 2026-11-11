package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The kinds of annotation, as stored. A note hangs from either.
const (
	AnnotationBookmark  = 1
	AnnotationHighlight = 2
)

// AnnotationColors are a highlight's four colours, stored as 1 to 4.
var AnnotationColors = []string{"yellow", "green", "blue", "pink"}

// Bounds on what one person may keep in one book.
const (
	MaxAnnotations     = 5000
	maxAnnotationText  = 8000
	maxAnnotationNote  = 4000
	maxAnnotationPlace = 4096
	maxAnnotationExtra = 16 << 10
	// annotationsKept is how long a deleted annotation is kept as a marker, so an older edit
	// from a device that was out of reach cannot bring it back. Apps drop an unsent edit at 30.
	annotationsKept = 35 * 24 * time.Hour
)

// ErrAnnotationsFull refuses one more annotation in a book that holds MaxAnnotations.
var ErrAnnotationsFull = errors.New("This book has as many highlights as it can hold.")

// ErrAnnotationDeleted says the annotation was deleted, on this device or another; deleting
// one deletes it everywhere, and no later edit brings it back.
var ErrAnnotationDeleted = errors.New("This was deleted.")

// Annotation is a bookmark or highlight, with its note, in a profile's book.
type Annotation struct {
	PublicID string
	Kind     int
	Color    int // 1 to 4, a highlight's colour; 0 for none
	// Location is the exact spot in the file it was made on, in the reader's notation; MadeOn
	// is that file's fingerprint; Progress is how far through the book (0 to 1), which places
	// it in another file.
	Location string
	MadeOn   string
	Progress float64
	Chapter  string
	Text     string // the words it marked
	Note     string
	Extra    string // JSON the reader keeps with it, such as a PDF highlight's rectangles
	// CreatedAt and UpdatedAt are when the person made and last changed it.
	CreatedAt int64
	UpdatedAt int64
}

// AnnotationEdit adds, changes or deletes one annotation in one book. Ago is how long before
// it arrived the person made it, for an edit that waited while the device was out of reach.
type AnnotationEdit struct {
	ItemID string
	Annotation
	Delete bool
	Ago    time.Duration
}

func (e AnnotationEdit) check(kind ItemKind) error {
	id := e.PublicID
	if id == "" || len(id) > 64 || strings.IndexFunc(id, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
	}) >= 0 {
		return invalid("an annotation id is 1 to 64 letters, digits, - or _")
	}
	if e.Ago < 0 || e.Ago > MaxEditAge {
		return invalid("an edit made more than 35 days ago is too old to apply")
	}
	if e.Delete {
		return nil
	}
	switch {
	case e.Kind != AnnotationBookmark && e.Kind != AnnotationHighlight:
		return invalid("an annotation is a bookmark or a highlight")
	case e.Kind == AnnotationHighlight && (kind == KindComic || kind == KindIssue):
		return invalid("comics take bookmarks only: they have no text to highlight")
	case e.Kind == AnnotationHighlight && (e.Color < 1 || e.Color > len(AnnotationColors)):
		return invalid("a highlight is yellow, green, blue or pink")
	case e.Kind == AnnotationBookmark && e.Color != 0:
		return invalid("a bookmark has no colour")
	case e.Location == "" || len(e.Location) > maxAnnotationPlace || len(e.MadeOn) > 64 || len(e.Chapter) > 1024:
		return invalid("an annotation needs its place in the book, of at most 4 KB")
	case e.Progress < 0 || e.Progress > 1:
		return invalid("progress is between 0 and 1")
	case utf8.RuneCountInString(e.Text) > maxAnnotationText:
		return invalid("a highlight is at most 8,000 characters")
	case utf8.RuneCountInString(e.Note) > maxAnnotationNote:
		return invalid("a note is at most 4,000 characters")
	case e.Extra != "" && (len(e.Extra) > maxAnnotationExtra || !json.Valid([]byte(e.Extra))):
		return invalid("extra is JSON of at most 16 KB")
	}
	return nil
}

const annotationColumns = `public_id, kind, ifnull(color, 0), location, ifnull(made_on, ''), progress, ifnull(chapter, ''),
	ifnull(text, ''), ifnull(note, ''), ifnull(extra, ''), created_at, updated_at`

func (a *Annotation) dest() []any {
	return []any{&a.PublicID, &a.Kind, &a.Color, &a.Location, &a.MadeOn, &a.Progress, &a.Chapter, &a.Text, &a.Note, &a.Extra,
		&a.CreatedAt, &a.UpdatedAt}
}

// Annotations lists a profile's annotations in a book it may see, in book order, a page at a
// time. Deleted ones are not listed: an app removes those it has sent before and no longer finds.
func (d *DB) Annotations(ctx context.Context, v Viewer, itemID, cursor string, limit int) ([]Annotation, string, error) {
	after, afterID := -1.0, ""
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", invalid("the cursor is not one this server gave")
		}
		if _, err := fmt.Sscanf(string(raw), "%g %s", &after, &afterID); err != nil {
			return nil, "", invalid("the cursor is not one this server gave")
		}
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	id, _, err := readableItem(ctx, tx, v, itemID)
	if err != nil {
		return nil, "", err
	}
	out, err := queryAll(ctx, tx, `SELECT `+annotationColumns+` FROM annotations WHERE profile_id = ? AND item_id = ?
		AND deleted_at IS NULL AND (progress > ? OR progress = ? AND public_id > ?) ORDER BY progress, public_id LIMIT ?`,
		[]any{v.ProfileID, id, after, after, afterID, limit + 1}, (*Annotation).dest)
	if err != nil || len(out) <= limit {
		return out, "", err
	}
	out = out[:limit]
	last := out[limit-1]
	return out, base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%g %s", last.Progress, last.PublicID)), nil
}

// readableItem is a book, comic or issue the viewer may see, or ErrNotFound.
func readableItem(ctx context.Context, tx *sql.Tx, v Viewer, publicID string) (int64, ItemKind, error) {
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return 0, 0, err
	}
	return acc.readable(ctx, tx, v, publicID)
}

func (a access) readable(ctx context.Context, tx *sql.Tx, v Viewer, publicID string) (int64, ItemKind, error) {
	var id int64
	var kind ItemKind
	err := tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = @id AND `+a.visible("i"),
		append(v.args(), sql.Named("id", publicID))...).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !kind.Reading() {
		return 0, 0, ErrNotFound
	}
	return id, kind, err
}

// annotatedBook is one book a batch of edits touches: read, tidied and counted once.
type annotatedBook struct {
	id   int64
	kind ItemKind
	live int
	err  error
}

// SaveAnnotations applies edits in one transaction and answers, for each, the annotation as it
// now stands or why it was refused. The later edit to one annotation wins, so an edit sent
// twice, or one made earlier than what is stored, changes nothing. Deleting one keeps a marker
// for 35 days, and no edit brings it back: an edit to a deleted one answers
// ErrAnnotationDeleted. A delete that arrives before the annotation itself leaves the marker
// too, so the annotation never appears.
func (d *DB) SaveAnnotations(ctx context.Context, v Viewer, edits []AnnotationEdit) ([]Annotation, []error, error) {
	out, refusals := make([]Annotation, len(edits)), make([]error, len(edits))
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		now := time.Now()
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		books := map[string]*annotatedBook{}
		for n, e := range edits {
			book := books[e.ItemID]
			if book == nil {
				if book, err = openAnnotatedBook(ctx, tx, v, acc, e.ItemID, now); err != nil {
					return err
				}
				books[e.ItemID] = book
			}
			stored, err := saveAnnotation(ctx, tx, v, book, e, now)
			if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) &&
				!errors.Is(err, ErrAnnotationsFull) && !errors.Is(err, ErrAnnotationDeleted) {
				return err
			}
			out[n], refusals[n] = stored, err
		}
		return nil
	})
	return out, refusals, err
}

// openAnnotatedBook reads a book once for a batch: whether the viewer may see it, and how many
// annotations it holds after markers too old to matter are removed.
func openAnnotatedBook(ctx context.Context, tx *sql.Tx, v Viewer, acc access, publicID string, now time.Time) (*annotatedBook, error) {
	book := &annotatedBook{}
	if book.id, book.kind, book.err = acc.readable(ctx, tx, v, publicID); book.err != nil {
		if errors.Is(book.err, ErrNotFound) {
			return book, nil
		}
		return nil, book.err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM annotations WHERE profile_id = ? AND item_id = ? AND deleted_at < ?`,
		v.ProfileID, book.id, now.Add(-annotationsKept).UnixMilli()); err != nil {
		return nil, err
	}
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM annotations WHERE profile_id = ? AND item_id = ? AND deleted_at IS NULL`,
		v.ProfileID, book.id).Scan(&book.live)
	return book, err
}

func saveAnnotation(ctx context.Context, tx *sql.Tx, v Viewer, book *annotatedBook, e AnnotationEdit, now time.Time) (Annotation, error) {
	if book.err != nil {
		return Annotation{}, book.err
	}
	itemID := book.id
	if err := e.check(book.kind); err != nil {
		return Annotation{}, err
	}
	at := now.Add(-e.Ago).UnixMilli()
	var rowID, rowItem int64
	var deleted sql.NullInt64
	var stored Annotation
	err := tx.QueryRowContext(ctx, `SELECT id, item_id, deleted_at, `+annotationColumns+` FROM annotations WHERE profile_id = ? AND public_id = ?`,
		v.ProfileID, e.PublicID).Scan(append([]any{&rowID, &rowItem, &deleted}, stored.dest()...)...)
	found := err == nil
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Annotation{}, err
	case found && rowItem != itemID:
		return Annotation{}, invalid("this annotation id is already used in another book")
	case found && deleted.Valid:
		return Annotation{}, ErrAnnotationDeleted
	case e.Delete && found:
		book.live--
		_, err := tx.ExecContext(ctx, `UPDATE annotations SET deleted_at = ?, text = NULL, note = NULL, chapter = NULL, extra = NULL,
			updated_at = ? WHERE id = ?`, now.UnixMilli(), max(at, stored.UpdatedAt), rowID)
		return Annotation{}, err
	case e.Delete:
		_, err := tx.ExecContext(ctx, `INSERT INTO annotations (profile_id, item_id, public_id, kind, location, progress, created_at, updated_at,
			deleted_at) VALUES (?, ?, ?, ?, '', 0, ?, ?, ?)`, v.ProfileID, itemID, e.PublicID, AnnotationBookmark, at, at, now.UnixMilli())
		return Annotation{}, err
	case found && stored.UpdatedAt > at:
		return stored, nil
	case found:
		_, err := tx.ExecContext(ctx, `UPDATE annotations SET kind = ?, color = nullif(?, 0), location = ?, made_on = nullif(?, ''),
			progress = ?, chapter = nullif(?, ''), text = nullif(?, ''), note = nullif(?, ''), extra = nullif(?, ''), updated_at = ? WHERE id = ?`,
			e.Kind, e.Color, e.Location, e.MadeOn, e.Progress, e.Chapter, e.Text, e.Note, e.Extra, at, rowID)
		e.CreatedAt, e.UpdatedAt = stored.CreatedAt, at
		return e.Annotation, err
	}
	if book.live >= MaxAnnotations {
		return Annotation{}, ErrAnnotationsFull
	}
	book.live++
	_, err = tx.ExecContext(ctx, `INSERT INTO annotations (profile_id, item_id, public_id, kind, color, location, made_on, progress,
		chapter, text, note, extra, created_at, updated_at) VALUES (?, ?, ?, ?, nullif(?, 0), ?, nullif(?, ''), ?, nullif(?, ''), nullif(?, ''),
		nullif(?, ''), nullif(?, ''), ?, ?)`, v.ProfileID, itemID, e.PublicID, e.Kind, e.Color, e.Location, e.MadeOn, e.Progress,
		e.Chapter, e.Text, e.Note, e.Extra, at, at)
	e.CreatedAt, e.UpdatedAt = at, at
	return e.Annotation, err
}
