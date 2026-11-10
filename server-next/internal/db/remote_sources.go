package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RemoteSource is a remote source as the owner manages it. Its sealed secret is not part of
// it: only RemoteSourceSecret reads that, for the one place that opens sources.
type RemoteSource struct {
	ID        int64
	PublicID  string
	Kind      string
	Name      string
	Config    string // the source's own settings as JSON, never anything secret
	State     string // ok, unreachable or sign_in, as last found
	LastError string
	Revision  int64
	CreatedAt int64
	UpdatedAt int64
	// Folders is how many library folders are on the source.
	Folders int
}

// NewRemoteSource is a source to add, with its secret already sealed.
type NewRemoteSource struct {
	UserID                    int64
	Kind, Name, Config        string
	Secret                    []byte
	IdempotencyKey, RequestID string
	Now                       time.Time
}

const remoteSourceSelect = `SELECT s.id, s.public_id, s.kind, s.name, s.config, s.state, ifnull(s.last_error, ''), s.revision, s.created_at,
	s.updated_at, (SELECT count(*) FROM library_paths p WHERE p.remote_source_id = s.id) FROM remote_sources s `

func remoteSourceRow(s *RemoteSource) []any {
	return []any{&s.ID, &s.PublicID, &s.Kind, &s.Name, &s.Config, &s.State, &s.LastError, &s.Revision, &s.CreatedAt, &s.UpdatedAt, &s.Folders}
}

func (d *DB) remoteSources(ctx context.Context, where string, args ...any) ([]RemoteSource, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, remoteSourceSelect+where, args, remoteSourceRow)
}

// RemoteSources lists every remote source by name.
func (d *DB) RemoteSources(ctx context.Context) ([]RemoteSource, error) {
	return d.remoteSources(ctx, `ORDER BY s.name, s.id`)
}

// RemoteSource returns one source by its public id.
func (d *DB) RemoteSource(ctx context.Context, publicID string) (RemoteSource, error) {
	return d.oneRemoteSource(ctx, `WHERE s.public_id = ?`, publicID)
}

// RemoteSourceByID returns one source by its row id, as a library folder names it.
func (d *DB) RemoteSourceByID(ctx context.Context, id int64) (RemoteSource, error) {
	return d.oneRemoteSource(ctx, `WHERE s.id = ?`, id)
}

func (d *DB) oneRemoteSource(ctx context.Context, where string, arg any) (RemoteSource, error) {
	found, err := d.remoteSources(ctx, where, arg)
	if err != nil {
		return RemoteSource{}, err
	}
	if len(found) == 0 {
		return RemoteSource{}, ErrNotFound
	}
	return found[0], nil
}

// RemoteSourceSecret returns a source's sealed secret and the revision it belongs to.
func (d *DB) RemoteSourceSecret(ctx context.Context, id int64) ([]byte, int64, error) {
	var secret []byte
	var revision int64
	err := d.read.QueryRowContext(ctx, `SELECT secret, revision FROM remote_sources WHERE id = ?`, id).Scan(&secret, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	return secret, revision, err
}

// CreateRemoteSource adds a source whose connection was just tested. publicID is chosen by
// the caller, because the secret is sealed to it.
func (d *DB) CreateRemoteSource(ctx context.Context, publicID string, n NewRemoteSource) (RemoteSource, error) {
	var created string
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		before, err := createdBefore(tx, n.UserID, n.IdempotencyKey, n.RequestID)
		if err != nil || before != "" {
			created = before
			return err
		}
		now := n.Now.UnixMilli()
		if _, err := tx.Exec(`INSERT INTO remote_sources (public_id, kind, name, config, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			publicID, n.Kind, n.Name, n.Config, n.Secret, now, now); err != nil {
			return err
		}
		created = publicID
		return rememberCreated(tx, n.UserID, n.IdempotencyKey, n.RequestID, publicID, n.Now)
	})
	if err != nil {
		return RemoteSource{}, err
	}
	return d.RemoteSource(ctx, created)
}

// RemoteSourceEdit changes a source. Nil fields stay; a nil Secret keeps the one stored.
type RemoteSourceEdit struct {
	Name, Config *string
	Secret       []byte
	Now          time.Time
}

// UpdateRemoteSource changes a source and moves it to a new revision, so whatever has it open
// opens it again.
func (d *DB) UpdateRemoteSource(ctx context.Context, publicID string, e RemoteSourceEdit) (RemoteSource, error) {
	var secret any // NULL keeps the stored secret
	if e.Secret != nil {
		secret = e.Secret
	}
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE remote_sources SET name = ifnull(?, name), config = ifnull(?, config), secret = ifnull(?, secret),
			state = 'ok', last_error = NULL, revision = revision + 1, updated_at = ? WHERE public_id = ?`,
			e.Name, e.Config, secret, e.Now.UnixMilli(), publicID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return RemoteSource{}, err
	}
	return d.RemoteSource(ctx, publicID)
}

// DeleteRemoteSource disconnects a source and forgets its secret. A source with library
// folders on it is ErrConflict: the folders are removed from their libraries first. A
// source that is already gone is not an error.
func (d *DB) DeleteRemoteSource(ctx context.Context, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var used int
		if err := tx.QueryRow(`SELECT count(*) FROM library_paths p JOIN remote_sources s ON s.id = p.remote_source_id WHERE s.public_id = ?`,
			publicID).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return ErrConflict
		}
		_, err := tx.Exec(`DELETE FROM remote_sources WHERE public_id = ?`, publicID)
		return err
	})
}

// SetRemoteSourceState records how a source was last found: ok, unreachable or sign_in, with
// what went wrong. It is background work, written only when the state changes.
func (d *DB) SetRemoteSourceState(ctx context.Context, id int64, state, lastError string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE remote_sources SET state = ?, last_error = nullif(?, '') WHERE id = ?`, state, lastError, id)
		return err
	})
}

// AddRemoteFolder adds a folder on a remote source to a library: path is the folder within
// the source. It tells open screens that the library changed.
func (d *DB) AddRemoteFolder(ctx context.Context, libraryID, sourceID int64, path string) (LibraryPath, error) {
	out := LibraryPath{Path: path, RemoteID: sourceID}
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO library_paths (library_id, remote_source_id, path) VALUES (?, ?, ?)`, libraryID, sourceID, path)
		if err != nil {
			return err
		}
		out.ID, _ = res.LastInsertId()
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicLibrary, Op: OpUpdated, RefID: libraryID, LibraryID: libraryID})
	})
	return out, err
}

// RemoteFileOf returns where a file on remote storage is: its source, and its path within
// the source (the library folder's path there, then the file's place beneath it), with its
// size and modified time as scanned. A local file, or one that has gone missing, is
// ErrNotFound.
func (d *DB) RemoteFileOf(ctx context.Context, fileID int64) (int64, FileOnDisk, error) {
	var source int64
	var out FileOnDisk
	err := d.read.QueryRowContext(ctx, `SELECT lp.remote_source_id, ltrim(rtrim(lp.path, '/') || '/' || CASE WHEN dir.rel_path = '' THEN '' ELSE dir.rel_path || '/' END || f.name, '/'),
		f.size, f.modified_ns FROM files f JOIN directories dir ON dir.id = f.directory_id
		JOIN library_paths lp ON lp.id = dir.library_path_id
		WHERE f.id = ? AND f.missing_since_at IS NULL AND lp.remote_source_id IS NOT NULL`, fileID).Scan(&source, &out.Path, &out.Size, &out.ModifiedNS)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, out, ErrNotFound
	}
	return source, out, err
}
