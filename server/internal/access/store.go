// Package access owns server administration of people: the admin tier, account
// invitations, per-member limits, the device inventory and API keys. It is the
// single authority for what a member may do at admission time; the HTTP layer
// only translates its documents and errors.
package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var (
	ErrIdempotencyKeyReused = errors.New("idempotency key reused")
	ErrInvalid              = errors.New("invalid administration request")
	ErrConflict             = errors.New("This record changed. Reload and try again.")
	ErrExpired              = errors.New("This invitation is no longer usable.")
	ErrCapacity             = errors.New("Too many records of this kind already exist.")
)

// ConflictError carries the revision a caller must resubmit against, so a client
// recovers from a lost race without a second read.
type ConflictError struct{ CurrentRevision int64 }

func (e *ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return ErrConflict }

// ValidationError names the rejected fields in the wire document's own shape.
type ValidationError struct{ Fields []string }

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func invalid(fields ...string) error { return &ValidationError{Fields: fields} }

// Authorize runs inside the store's transaction. It is the caller's live
// authority check: the store never trusts a role carried on a token alone.
type Authorize func(context.Context, *sql.Tx) error

type Store struct {
	DB            *sql.DB
	Now           func() time.Time
	invitationKey []byte
}

// New uses an ephemeral key until the server loads its durable key before serving.
func New(db *sql.DB) *Store {
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		panic(e)
	}
	return &Store{DB: db, Now: time.Now, invitationKey: key}
}

// LoadInvitationKey is called once before HTTP starts. The database never contains
// this key. Loss or rotation invalidates pending invitations; create new links.
func (s *Store) LoadInvitationKey(state string) error {
	return s.loadInvitationKey(state, invitationKeyIO{
		write: (*os.File).Write, syncFile: (*os.File).Sync, closeFile: (*os.File).Close,
		install: os.Link, syncDirectory: dirsync.Sync,
	})
}

// Each initializer prepares a complete private inode before publishing its name.
// Link is atomic and cannot replace another initializer's winner. The I/O steps
// are passed per call so failure tests never alter process-global filesystem hooks.
type invitationKeyIO struct {
	write         func(*os.File, []byte) (int, error)
	syncFile      func(*os.File) error
	closeFile     func(*os.File) error
	install       func(string, string) error
	syncDirectory func(string) error
}

func (s *Store) loadInvitationKey(state string, files invitationKeyIO) error {
	path := filepath.Join(state, "invitation-link.key")
	key, err := readInvitationKey(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = createInvitationKey(path, files); err != nil {
			return err
		}
		// Always read the installed inode: a concurrent creator may have won.
		key, err = readInvitationKey(path)
	}
	if err != nil {
		return err
	}
	// Also sync existing keys so retry after a directory-sync failure acknowledges
	// durability only after the installed entry has actually been flushed.
	if err = files.syncDirectory(state); err != nil {
		return err
	}
	s.invitationKey = append([]byte(nil), key...)
	return nil
}

func createInvitationKey(path string, files invitationKeyIO) (err error) {
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	f, err := os.CreateTemp(directory, ".invitation-link-key-*") // private 0600, same filesystem
	if err != nil {
		return err
	}
	defer func() {
		f.Close() // release the handle even when write/sync/close failed
		if cleanup := os.Remove(f.Name()); !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, cleanup)
		}
	}()
	n, err := files.write(f, key)
	if err != nil {
		return err
	}
	if n != len(key) {
		return io.ErrShortWrite
	}
	if err = files.syncFile(f); err != nil {
		return err
	}
	if err = files.closeFile(f); err != nil {
		return err
	}
	// Link is atomic and never overwrites an established key. Rename would
	// overwrite another creator's key and invalidate its pending invitations.
	if err = files.install(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

func readInvitationKey(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 32 {
		return nil, errors.New("invitation key must be a private 32-byte regular file")
	}
	key, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, errors.New("invitation key must contain exactly 32 bytes")
	}
	return key, nil
}

func (s *Store) now() int64 { return s.Now().UTC().UnixMilli() }

func (s *Store) transaction(ctx context.Context, auth Authorize, fn func(*sql.Tx) error) error {
	if s == nil || s.DB == nil {
		return identity.ErrUnauthorized
	}
	gated, err := dbwork.Begin(ctx, s.DB, dbwork.ClassSecurityFence)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	return gated.Commit()
}

func (s *Store) snapshot(ctx context.Context, auth Authorize, fn func(*sql.Tx) error) error {
	if s == nil || s.DB == nil {
		return identity.ErrUnauthorized
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	return gated.Commit()
}

func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// digest is the at-rest form of every secret this package issues. Invitation
// codes and API key secrets are stored only as this digest. Invitation codes
// can be derived again using a separate server-only key.
func digest(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

func safeText(v string, max int) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > max {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func validID(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// validOperationID bounds the caller-supplied idempotency key. Every mutation in
// this package takes one so a retried request replays its receipt instead of
// applying twice.
func validOperationID(v string) bool { return len(v) >= 8 && len(v) <= 128 && safeText(v, 128) }

// receipt replays a completed mutation. It returns the stored response body when
// this operation identifier has already run with the same request digest, and an
// error when the same identifier is reused for a different request.
func receipt(tx *sql.Tx, scope, operation string, request any) (string, string, error) {
	raw, _ := json.Marshal(request)
	want := digest(string(raw))
	var have, body string
	err := tx.QueryRow(`SELECT digest,body FROM access_receipts WHERE scope=? AND operation_id=?`, scope, operation).Scan(&have, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", want, nil
	}
	if err != nil {
		return "", want, err
	}
	if have != want {
		return "", want, ErrIdempotencyKeyReused
	}
	return body, want, nil
}

func saveReceipt(tx *sql.Tx, scope, operation, want string, out any, now int64) error {
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO access_receipts VALUES(?,?,?,?,?) ON CONFLICT(scope,operation_id) DO NOTHING`, scope, operation, want, string(body), now)
	if err == nil {
		// Receipts are a retry aid, not history: keep the newest 2000.
		_, err = tx.Exec(`DELETE FROM access_receipts WHERE rowid NOT IN(SELECT rowid FROM access_receipts ORDER BY created_ms DESC LIMIT 2000)`)
	}
	return err
}

// Page is the shared list envelope. Every list in this area pages by an opaque
// cursor rather than an offset, so a concurrent insert cannot skip a row.
type Page struct {
	Limit      int    `json:"limit"`
	NextCursor string `json:"nextCursor,omitempty"`
}

const maxPage = 100

func pageLimit(raw string) (int, error) {
	if raw == "" {
		return 25, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPage {
		return 0, invalid("limit")
	}
	return n, nil
}

// cursor encodes the (sort key, id) pair a list stopped at. It is opaque to
// clients and validated on the way back in.
func encodeCursor(sortKey int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sortKey, 10) + ":" + id))
}

func decodeCursor(raw string) (int64, string, error) {
	if raw == "" {
		return 0, "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) > 256 {
		return 0, "", invalid("cursor")
	}
	key, id, ok := strings.Cut(string(b), ":")
	n, err := strconv.ParseInt(key, 10, 64)
	if !ok || err != nil || !validID(id) {
		return 0, "", invalid("cursor")
	}
	return n, id, nil
}
