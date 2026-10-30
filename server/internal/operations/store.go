package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/configfile"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var ErrInvalid = requestinput.New("invalid console request")
var ErrConflict = errors.New("revision or idempotency conflict; refresh before applying")
var ErrIdempotencyKeyReused = fmt.Errorf("%w: idempotency key reused", ErrConflict)
var ErrCapacity = errors.New("local storage quota reached; retry after retention cleanup")
var ErrExpired = errors.New("private artifact expired")

type ConflictError struct{ CurrentRevision int64 }

func (e *ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return ErrConflict }

type ValidationError struct{ Fields []string }

func (e *ValidationError) Error() string   { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error   { return ErrInvalid }
func invalidFields(fields ...string) error { return &ValidationError{Fields: fields} }

type Authorize func(context.Context, *sql.Tx, string) error

type AuditAnchor struct {
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

func auditAnchor(tx *sql.Tx) (AuditAnchor, error) {
	var a AuditAnchor
	var body string
	e := tx.QueryRow(`SELECT body FROM console_documents WHERE scope='audit-anchor'`).Scan(&body)
	if errors.Is(e, sql.ErrNoRows) {
		return a, nil
	}
	if e != nil {
		return a, e
	}
	e = decodeDocument(body, &a)
	return a, e
}

type Store struct {
	RecordingRoot        *RecordingRootRuntime
	ConfigFile           *configfile.Loader
	ConfigPath           string
	ConfigurationDomains []ConfigurationDomain
	ConfigChanged        func(context.Context)
	configMu             sync.Mutex
	FFmpegValidation     func(context.Context, string) FFmpegStatus
	// Set once at startup by the conversion consumer. Nil is a store without a running converter.
	AppliedTemporaryDirectory *string
	// Optional startup policy for the configurable disk conversion folder.
	TranscodeDirectoryAllowed func(string) bool
	// Set once by the listener at startup; zero means the automatic ceiling.
	AppliedConnectionLimit *int
	AppliedFFmpegPath      *string
	DB                     *sql.DB
	Now                    func() time.Time
}

func New(db *sql.DB) *Store { return &Store{DB: db, Now: time.Now} }
func (s *Store) now() int64 { return s.Now().UnixMilli() }

// The database is server-bound. Never use a raw profile ID as a durable owner.
func ViewerKey(p identity.Principal) string {
	b, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
	return string(b)
}
func AccountKey(p identity.Principal) string {
	b, _ := json.Marshal([]string{p.Authority, p.AccountID})
	return string(b)
}
func Hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func validID(v string) bool {
	if len(v) < 1 || len(v) > 160 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.-", c)) {
			return false
		}
	}
	return true
}
func SafeText(v string, max int) bool {
	if !utf8.ValidString(v) || len(v) > max || strings.TrimSpace(v) == "" {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) && c != '\n' && c != '\t' || c >= 0x202a && c <= 0x202e || c >= 0x2066 && c <= 0x2069 {
			return false
		}
	}
	return true
}
func Cursor(v string) (int64, error) {
	if v == "" {
		return 9223372036854775807, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 1 {
		return 0, ErrInvalid
	}
	return n, nil
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
}

func (s *Store) transaction(ctx context.Context, auth Authorize, item string, fn func(*sql.Tx) error) error {
	gated, e := dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if auth == nil {
		return identity.ErrUnauthorized
	}
	if e = auth(ctx, tx, item); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return gated.Commit()
}
func (s *Store) snapshot(ctx context.Context, auth Authorize, item string, fn func(*sql.Tx) error) error {
	gated, e := dbwork.BeginSnapshot(ctx, s.DB)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if auth == nil {
		return identity.ErrUnauthorized
	}
	if e = auth(ctx, tx, item); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return gated.Commit()
}
func Receipt(tx *sql.Tx, scope, key string, body any, now int64) (string, string, error) {
	if strings.HasPrefix(key, "p251_") {
		return "", "", usermutation.ErrInvalidKey
	}
	if !validID(key) {
		return "", "", ErrInvalid
	}
	b, e := json.Marshal(body)
	if e != nil {
		return "", "", ErrInvalid
	}
	digest := Hash(string(b))
	var stored, out string
	var expiry int64
	e = tx.QueryRow(`SELECT digest,result,expires_ms FROM console_receipts WHERE scope=? AND key=?`, scope, key).Scan(&stored, &out, &expiry)
	if e == nil {
		if expiry <= now {
			return "", "", ErrExpired
		}
		if stored != digest {
			return "", "", ErrIdempotencyKeyReused
		}
		return out, digest, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", "", e
	}
	return "", digest, nil
}
func SaveReceipt(tx *sql.Tx, scope, key, digest string, out any, now int64) error {
	if strings.HasPrefix(key, "p251_") {
		return usermutation.ErrInvalidKey
	}
	var n int
	if e := tx.QueryRow(`SELECT count(*) FROM console_receipts WHERE scope=? AND expires_ms>?`, scope, now).Scan(&n); e != nil {
		return e
	}
	if n >= 2048 {
		return ErrCapacity
	}
	b, e := json.Marshal(out)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO console_receipts VALUES(?,?,?,?,?)`, scope, key, digest, string(b), now+int64(14*24*time.Hour/time.Millisecond))
	return e
}

// Hash chaining provides tamper evidence within the trusted host boundary,
// not protection from a host operator able to rewrite both data and anchors.
func Audit(tx *sql.Tx, now int64, actor, action, target string, revision int64) error {
	var prev string
	e := tx.QueryRow(`SELECT hash FROM console_audit ORDER BY sequence DESC LIMIT 1`).Scan(&prev)
	if errors.Is(e, sql.ErrNoRows) {
		a, err := auditAnchor(tx)
		if err != nil {
			return err
		}
		prev = a.Hash
	} else if e != nil {
		return e
	}
	id := identity.Token()
	actor = Hash(actor)
	payload, _ := json.Marshal([]any{id, now, actor, action, target, revision, prev})
	_, e = tx.Exec(`INSERT INTO console_audit(id,time_ms,actor,action,target,revision,previous_hash,hash) VALUES(?,?,?,?,?,?,?,?)`, id, now, actor, action, target, revision, prev, Hash(string(payload)))
	return e
}
func decodeDocument(raw string, dst any) error {
	if e := json.Unmarshal([]byte(raw), dst); e != nil {
		return fmt.Errorf("invalid saved console document")
	}
	return nil
}
