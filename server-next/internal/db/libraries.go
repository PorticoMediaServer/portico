package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
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
	// NoWatch turns off watching the library's folders for changes.
	NoWatch bool `json:"noWatch,omitempty"`
	// RescanMinutes is how often the library's local folders are scanned by themselves; 0 is
	// no periodic scan. Watching and a periodic scan are separate choices: one, both or neither.
	RescanMinutes int `json:"rescanMinutes,omitempty"`
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
	// RemoteID is the remote source the folder is on, or 0 for a folder on a disk here.
	RemoteID int64
	// LastScanAt is when a scan last listed the folder, in milliseconds, or 0 for never.
	LastScanAt int64
}

// CreateLibrary adds a library with its folders and tells open screens that the list changed.
func (d *DB) CreateLibrary(ctx context.Context, name, kind, language string, paths []string) (Library, error) {
	return d.NewLibrary(ctx, NewLibrary{Name: name, Kind: kind, Language: language, Folders: paths})
}

// RemoteFolder is a library folder on a remote source: Path is the folder within the source.
type RemoteFolder struct {
	SourceID int64
	Path     string
}

// NewLibrary is a library to make. With an IdempotencyKey, the same request sent twice makes
// one library and answers with it both times.
type NewLibrary struct {
	Name, Kind, Language string
	Folders              []string
	RemoteFolders        []RemoteFolder
	Options              LibraryOptions
	UserID               int64
	IdempotencyKey       string
	RequestHash          string
}

// NewLibrary adds a library.
func (d *DB) NewLibrary(ctx context.Context, n NewLibrary) (Library, error) {
	name := strings.TrimSpace(n.Name)
	if name == "" || len(name) > 200 {
		return Library{}, invalid("a library name has 1 to 200 characters")
	}
	if !libraryKinds[n.Kind] {
		return Library{}, invalid("unknown library kind %q", n.Kind)
	}
	if err := n.Options.check(); err != nil {
		return Library{}, err
	}
	now := time.Now()
	var lib Library
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		lib = Library{PublicID: NewID("lib"), Name: name, Kind: n.Kind, Language: n.Language, Options: n.Options,
			CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
		before, err := createdBefore(tx, n.UserID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if before != "" {
			lib.PublicID = before
			return tx.QueryRow(`SELECT id FROM libraries WHERE public_id = ?`, before).Scan(&lib.ID)
		}
		res, err := tx.Exec(`INSERT INTO libraries (public_id, name, kind, language, options, position, created_at, updated_at)
			VALUES (?, ?, ?, nullif(?, ''), ?, (SELECT count(*) FROM libraries), ?, ?)`, lib.PublicID, name, n.Kind, n.Language,
			n.Options.json(), lib.CreatedAt, lib.CreatedAt)
		if err != nil {
			return err
		}
		lib.ID, _ = res.LastInsertId()
		for _, p := range n.Folders {
			res, err := tx.Exec(`INSERT INTO library_paths (library_id, path) VALUES (?, ?)`, lib.ID, p)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			lib.Paths = append(lib.Paths, LibraryPath{ID: id, Path: p})
		}
		for _, f := range n.RemoteFolders {
			res, err := tx.Exec(`INSERT INTO library_paths (library_id, remote_source_id, path) VALUES (?, ?, ?)`, lib.ID, f.SourceID, f.Path)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			lib.Paths = append(lib.Paths, LibraryPath{ID: id, Path: f.Path, RemoteID: f.SourceID})
		}
		if len(lib.Paths) == 0 {
			return invalid("a library needs at least one folder")
		}
		if err := rememberCreated(tx, n.UserID, n.IdempotencyKey, n.RequestHash, lib.PublicID, now); err != nil {
			return err
		}
		return appendChange(tx, Change{At: lib.CreatedAt, Topic: TopicLibrary, Op: OpCreated, RefID: lib.ID, LibraryID: lib.ID})
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
		l.created_at, l.updated_at, l.options, p.id, p.path, ifnull(p.remote_source_id, 0), ifnull(p.last_scan_at, 0)
		FROM libraries l LEFT JOIN library_paths p ON p.library_id = l.id ORDER BY l.position, l.id, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		var pathID sql.NullInt64
		var path, options sql.NullString
		var remoteID, lastScanAt int64
		if err := rows.Scan(&l.ID, &l.PublicID, &l.Name, &l.Kind, &l.Language, &l.Position, &l.CreatedAt, &l.UpdatedAt, &options, &pathID, &path,
			&remoteID, &lastScanAt); err != nil {
			return nil, err
		}
		l.Options = parseOptions(options)
		if len(out) == 0 || out[len(out)-1].ID != l.ID {
			out = append(out, l)
		}
		if pathID.Valid {
			last := &out[len(out)-1]
			last.Paths = append(last.Paths, LibraryPath{ID: pathID.Int64, Path: path.String, RemoteID: remoteID, LastScanAt: lastScanAt})
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
	if err := options.check(); err != nil {
		return err
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		res, err := tx.Exec(`UPDATE libraries SET revision = revision + 1, updated_at = ? WHERE id = ?`, now, libraryID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if err := options.save(tx, libraryID); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicLibrary, Op: OpUpdated, RefID: libraryID, LibraryID: libraryID})
	})
}

func (o LibraryOptions) json() string {
	raw, _ := json.Marshal(o)
	return string(raw)
}

// save writes the owner's choices into a library's options document and changes nothing else
// in it. The document is shared: other areas keep keys of their own there (the region
// metadata reads, what automatic collections remember), and a save of these choices must not
// wipe them. Every key these choices own is replaced, also by its absence (a choice back at
// its default is left out); every other key stays as it was.
func (o LibraryOptions) save(tx *sql.Tx, libraryID int64) error {
	var stored sql.NullString
	if err := tx.QueryRow(`SELECT options FROM libraries WHERE id = ?`, libraryID).Scan(&stored); err != nil {
		return err
	}
	document := map[string]json.RawMessage{}
	if stored.Valid {
		json.Unmarshal([]byte(stored.String), &document)
	}
	fields := reflect.TypeFor[LibraryOptions]()
	for i := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		delete(document, name)
	}
	var mine map[string]json.RawMessage
	json.Unmarshal([]byte(o.json()), &mine)
	for name, value := range mine {
		document[name] = value
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE libraries SET options = ? WHERE id = ?`, string(raw), libraryID)
	return err
}

// OrderLibraries puts the libraries in the order given, which is the order everyone sees.
// ids must name every library once.
func (d *DB) OrderLibraries(ctx context.Context, ids []string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT public_id, id FROM libraries`)
		if err != nil {
			return err
		}
		have := map[string]int64{}
		for rows.Next() {
			var publicID string
			var id int64
			if err := rows.Scan(&publicID, &id); err != nil {
				rows.Close()
				return err
			}
			have[publicID] = id
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(ids) != len(have) {
			return invalid("the order must name every library once")
		}
		for position, publicID := range ids {
			id, ok := have[publicID]
			if !ok {
				return invalid("the order must name every library once")
			}
			delete(have, publicID)
			res, err := tx.Exec(`UPDATE libraries SET position = ?, updated_at = ? WHERE id = ? AND position <> ?`, position, now.UnixMilli(), id, position)
			if err != nil {
				return err
			}
			// Open screens hear of each library that moved and read the list again.
			if n, _ := res.RowsAffected(); n == 1 {
				if err := appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicLibrary, Op: OpUpdated, RefID: id, LibraryID: id}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
