package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Library is one library and the folders in it.
type Library struct {
	ID        int64
	PublicID  string
	Name      string
	Kind      string
	Language  string
	Position  int
	CreatedAt int64
	UpdatedAt int64
	Options   LibraryOptions
	Paths     []LibraryPath
}

// How much of each file a scan reads.
const (
	DepthFiles    = "files"    // names, sizes and dates only
	DepthBasic    = "basic"    // also length, tracks, tags and sidecar files; the default
	DepthComplete = "complete" // also the work that reads whole files
)

// When a library's missing titles are removed.
const (
	TrashAfterDays = "days" // the default, after 30
	TrashAfterScan = "scan"
	TrashManual    = "manual"
)

// LibraryOptions are the owner's choices for one library, stored as JSON.
type LibraryOptions struct {
	Depth       string    `json:"depth,omitempty"`
	Trash       TrashRule `json:"trash"`
	AllowDelete bool      `json:"allowDelete,omitempty"`
}

// TrashRule says when a library's missing titles are removed.
type TrashRule struct {
	Mode string `json:"mode,omitempty"`
	Days int    `json:"days,omitempty"`
}

// Cutoff is the time a title must have gone missing by to be removed now. ok is false when
// the trash is only emptied by hand.
func (t TrashRule) Cutoff(now time.Time) (cutoff time.Time, ok bool) {
	switch t.Mode {
	case TrashManual:
		return time.Time{}, false
	case TrashAfterScan:
		return now, true
	}
	days := t.Days
	if days <= 0 {
		days = 30
	}
	return now.AddDate(0, 0, -days), true
}

func parseOptions(raw sql.NullString) LibraryOptions {
	var o LibraryOptions
	if raw.Valid {
		json.Unmarshal([]byte(raw.String), &o)
	}
	return o
}

// LibraryPath is one folder of a library.
type LibraryPath struct {
	ID   int64
	Path string
}

// CreateLibrary adds a library with its folders and tells open screens that the list changed.
func (d *DB) CreateLibrary(ctx context.Context, name, kind, language string, paths []string) (Library, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Library{}, invalid("a library name has 1 to 200 characters")
	}
	if !libraryKinds[kind] {
		return Library{}, invalid("unknown library kind %q", kind)
	}
	now := time.Now().UnixMilli()
	lib := Library{PublicID: NewID("lib"), Name: name, Kind: kind, Language: language, CreatedAt: now, UpdatedAt: now}
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO libraries (public_id, name, kind, language, position, created_at, updated_at)
			VALUES (?, ?, ?, nullif(?, ''), (SELECT count(*) FROM libraries), ?, ?)`, lib.PublicID, name, kind, language, now, now)
		if err != nil {
			return err
		}
		lib.ID, _ = res.LastInsertId()
		for _, p := range paths {
			res, err := tx.Exec(`INSERT INTO library_paths (library_id, path) VALUES (?, ?)`, lib.ID, p)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			lib.Paths = append(lib.Paths, LibraryPath{ID: id, Path: p})
		}
		return appendChange(tx, Change{At: now, Topic: TopicLibrary, Op: OpCreated, RefID: lib.ID, LibraryID: lib.ID})
	})
	return lib, err
}

const librarySelect = `SELECT l.id, l.public_id, l.name, l.kind, ifnull(l.language, ''), l.position, l.created_at, l.updated_at
	FROM libraries l WHERE `

// Libraries lists the libraries the viewer may see, in the owner's order.
func (d *DB) Libraries(ctx context.Context, v Viewer) ([]Library, error) {
	return d.libraries(ctx, v, "")
}

// Library returns one library the viewer may see.
func (d *DB) Library(ctx context.Context, v Viewer, publicID string) (Library, error) {
	found, err := d.libraries(ctx, v, publicID)
	if err != nil {
		return Library{}, err
	}
	if len(found) == 0 {
		return Library{}, ErrNotFound
	}
	return found[0], nil
}

func (d *DB) libraries(ctx context.Context, v Viewer, publicID string) ([]Library, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	return queryAll(ctx, tx, librarySelect+acc.libraryVisible("l.id")+` AND (? = '' OR l.public_id = ?) ORDER BY l.position, l.id`,
		[]any{publicID, publicID}, func(l *Library) []any {
			return []any{&l.ID, &l.PublicID, &l.Name, &l.Kind, &l.Language, &l.Position, &l.CreatedAt, &l.UpdatedAt}
		})
}

// AllLibraries lists every library with its folders, for the scanner and the owner's console.
func (d *DB) AllLibraries(ctx context.Context) ([]Library, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT l.id, l.public_id, l.name, l.kind, ifnull(l.language, ''), l.position,
		l.created_at, l.updated_at, l.options, p.id, p.path FROM libraries l LEFT JOIN library_paths p ON p.library_id = l.id
		ORDER BY l.position, l.id, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		var pathID sql.NullInt64
		var path, options sql.NullString
		if err := rows.Scan(&l.ID, &l.PublicID, &l.Name, &l.Kind, &l.Language, &l.Position, &l.CreatedAt, &l.UpdatedAt, &options, &pathID, &path); err != nil {
			return nil, err
		}
		l.Options = parseOptions(options)
		if len(out) == 0 || out[len(out)-1].ID != l.ID {
			out = append(out, l)
		}
		if pathID.Valid {
			last := &out[len(out)-1]
			last.Paths = append(last.Paths, LibraryPath{ID: pathID.Int64, Path: path.String})
		}
	}
	return out, rows.Err()
}

// Tags lists the tags of one kind by name: every genre, for example.
func (d *DB) Tags(ctx context.Context, kind string) ([]Tag, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT public_id, kind, name FROM tags WHERE kind = ? ORDER BY norm_name`, []any{kind},
		func(t *Tag) []any { return []any{&t.PublicID, &t.Kind, &t.Name} })
}

// SetLibraryOptions stores the owner's choices for a library.
func (d *DB) SetLibraryOptions(ctx context.Context, libraryID int64, options LibraryOptions) error {
	switch options.Depth {
	case "", DepthFiles, DepthBasic, DepthComplete:
	default:
		return invalid("unknown scan depth %q", options.Depth)
	}
	switch options.Trash.Mode {
	case "", TrashAfterDays, TrashAfterScan, TrashManual:
	default:
		return invalid("unknown trash rule %q", options.Trash.Mode)
	}
	if options.Trash.Days < 0 || options.Trash.Days > 3650 {
		return invalid("missing titles are removed after 1 to 3,650 days")
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return err
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		res, err := tx.Exec(`UPDATE libraries SET options = ?, revision = revision + 1, updated_at = ? WHERE id = ?`, string(raw), now, libraryID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return appendChange(tx, Change{At: now, Topic: TopicLibrary, Op: OpUpdated, RefID: libraryID, LibraryID: libraryID})
	})
}
