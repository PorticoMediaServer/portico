// Package administration carries the owner administration pages that sit on top
// of the catalog, live source, DVR, library channel and restore domains: media
// deletion with a trash, library configuration (providers, the analysis
// operation matrix, generated navigation), live source configuration, DVR
// defaults, library channel presets and server maintenance.
//
// Nothing here owns domain state. Every page reads the domain's own tables and
// writes exactly one revision-fenced configuration document, so a configuration
// write never races a scan, a recording or a schedule generation.
package administration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInput is any malformed or out-of-range request field.
	ErrInput = errors.New("the administration request could not be accepted")
	// ErrConflict means the document moved under the caller's expectedRevision.
	ErrConflict             = errors.New("the configuration changed; reload and retry")
	ErrIdempotencyKeyReused = fmt.Errorf("%w: idempotency key reused", ErrConflict)
	// ErrNotFound is an addressed resource that does not exist.
	ErrNotFound = errors.New("the administration resource does not exist")
	// ErrDenied is a policy refusal, such as deletion on a library that has not
	// enabled it.
	ErrDenied = errors.New("this operation is not allowed here")
	// ErrConfirmation means the typed-back confirmation did not match.
	ErrConfirmation = errors.New("the confirmation text does not match")
	// ErrUnavailable is a dependency (state directory, backup key) the server
	// cannot reach right now.
	ErrUnavailable = errors.New("the administration service is unavailable")
	// ErrImage is an upload whose bytes are not an image the server accepts.
	ErrImage = errors.New("the image could not be accepted")
	// ErrCursor is a continuation token that does not belong to this listing.
	ErrCursor = errors.New("the page cursor is no longer valid")
	// ErrBusy means another filesystem-moving operation holds the lock.
	ErrBusy = errors.New("another administration operation is running")
)

// Authorize re-checks owner authority inside the same transaction as the write.
// The HTTP layer supplies the server's real owner check; the package never
// decides authority on its own.
type Authorize func(context.Context, *sql.Tx) error

// Service is the administration seam. Fetch and Discover are injected so update
// checks and tuner discovery are testable and so neither touches the network
// unless the owner configured it.
type Service struct {
	// SonicRuntimeAvailable is a cached readiness read; never a probe or SQL call.
	SonicRuntimeAvailable func() bool
	// ConfigurationCommitted drains the shared DB-to-file outbox after a committed domain edit.
	ConfigurationCommitted func(context.Context) error
	db                     *sql.DB
	DVR                    *dvr.Store
	// PickerRoots overrides the optional PORTICO_MEDIA_ROOTS picker limit.
	PickerRoots           []string
	ManagedMountDirectory string
	// state is the server state directory. It is resolved lazily, on the first
	// request that needs a path, because constructing this service must not touch
	// the database: the composition root builds it while other work may hold the
	// single connection.
	backgroundMu sync.Mutex
	stateMu      sync.Mutex
	state        string
	resolved     bool
	now          func() time.Time
	// files serializes operations that move bytes (trash, restore, cleanup,
	// backup creation) so two owners cannot interleave on the same tree.
	files       sync.Mutex
	Fetch       func(context.Context, string) ([]byte, error)
	LogoFetch   func(context.Context, string) ([]byte, error)
	Discover    func(context.Context, time.Duration) ([]TunerDevice, error)
	updateMu    sync.Mutex
	updateFeeds map[string]*cachedUpdateFeed
	// DataUsage measures the data and transcode folders for the data storage
	// page; the storage snapshot measures it in the same pass as the storage
	// care categories, so both pages read one observation.
	DataUsage func(context.Context) (storagepaths.Usage, error)
	usage     storageUsageSnapshot
}

func (s *Service) pickerPolicy() storage.SourcePolicy {
	roots := s.PickerRoots
	if roots == nil {
		roots = storage.MediaRoots()
	}
	return storage.SourcePolicy{Roots: roots, StateDirectory: s.StateDirectory(), ManagedMountDirectory: s.ManagedMountDirectory}
}

// New builds the service without reading anything. The state directory is
// derived from the open database file the first time a page needs a path, so the
// composition root does not have to thread another path through.
func New(db *sql.DB) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// NewAt is New with an explicit state directory, for tests and for a root that
// already knows where server state lives.
func NewAt(db *sql.DB, state string) *Service {
	return &Service{db: db, state: state, resolved: true, now: func() time.Time { return time.Now().UTC() }}
}

// StateDirectory is the root the maintenance pages measure and clean. A database
// with no file behind it (in-memory) leaves this empty, and the filesystem-backed
// pages then report themselves unavailable rather than guessing at a location.
func (s *Service) StateDirectory() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.resolved || s.db == nil {
		return s.state
	}
	s.resolved = true
	rows, err := s.db.Query(`PRAGMA database_list`)
	if err != nil {
		return s.state
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if rows.Scan(&seq, &name, &file) == nil && name == "main" && file != "" {
			s.state = filepath.Dir(file)
			if filepath.Base(s.state) == "Database" {
				s.state = filepath.Dir(s.state)
			}
		}
	}
	return s.state
}

func (s *Service) milliseconds() int64 { return s.now().UnixMilli() }

// identifier returns an opaque, URL-safe id. Backup identifiers must satisfy the
// restore package's own opaque pattern, which this length comfortably meets.
func identifier() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func digestOf(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// safeText rejects control characters and over-long values everywhere an owner
// supplies a name, a template or a filter term.
func safeText(v string, max int) bool {
	if !utf8.ValidString(v) || len(v) > max {
		return false
	}
	for _, r := range v {
		if r != '\t' && (unicode.IsControl(r) || r == '�') {
			return false
		}
	}
	return true
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

func clampInt(v *int, low, high int) {
	if *v < low {
		*v = low
	}
	if *v > high {
		*v = high
	}
}

// Cursors are opaque to clients and fenced by the listing they came from, so a
// cursor minted for one listing cannot be replayed against another.
type cursor struct {
	Listing string `json:"l"`
	Order   int64  `json:"o"`
	ID      string `json:"i"`
}

func encodeCursor(listing string, order int64, id string) string {
	raw, _ := json.Marshal(cursor{listing, order, id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(listing, token string) (cursor, error) {
	if token == "" {
		return cursor{}, nil
	}
	if len(token) > 512 {
		return cursor{}, ErrCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, ErrCursor
	}
	var c cursor
	if json.Unmarshal(raw, &c) != nil || c.Listing != listing {
		return cursor{}, ErrCursor
	}
	return c, nil
}

// Page bounds every listing this package publishes.
const (
	DefaultPageSize = 40
	MaxPageSize     = 200
)

func pageSize(limit int) (int, error) {
	if limit == 0 {
		return DefaultPageSize, nil
	}
	if limit < 1 || limit > MaxPageSize {
		// Named, so a client that asks for more than a page (CD-05: the folder
		// picker asked for 500) is told which value is at fault.
		return 0, invalid("limit")
	}
	return limit, nil
}

// Document is the envelope every configuration page returns: the revision the
// next write must present, plus the values themselves.
type Document[T any] struct {
	Scope    string `json:"scope"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
	Settings T      `json:"settings"`
}

// Change is the envelope every configuration write accepts.
type Change[T any] struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	// OperationID makes a write idempotent: replaying the same identifier with
	// the same body returns the first result rather than writing twice.
	OperationID string `json:"operationId"`
	Settings    T      `json:"settings"`
}

func (s *Service) transaction(ctx context.Context, auth Authorize, work func(*sql.Tx) error) error {
	return s.runTransaction(ctx, auth, false, work)
}

func (s *Service) snapshot(ctx context.Context, auth Authorize, work func(*sql.Tx) error) error {
	return s.runTransaction(ctx, auth, true, work)
}

func (s *Service) runTransaction(ctx context.Context, auth Authorize, readOnly bool, work func(*sql.Tx) error) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	var gated *dbwork.Write
	var err error
	if readOnly {
		gated, err = dbwork.BeginSnapshot(ctx, s.db)
	} else {
		gated, err = dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	}
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
	if err = work(tx); err != nil {
		return err
	}
	return gated.Commit()
}

// readDocument returns the stored document for a scope, or the caller's defaults
// at revision 1 when nothing has been written yet. Unknown fields in a stored
// body are ignored so a pre-release document never blocks a newer server.
func readDocument[T any](ctx context.Context, tx *sql.Tx, scope string, defaults T) (T, int64, error) {
	var body string
	revision := int64(1)
	err := tx.QueryRowContext(ctx, `SELECT revision,body FROM admin_documents WHERE scope=?`, scope).Scan(&revision, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return defaults, 1, nil
	}
	if err != nil {
		return defaults, 0, err
	}
	return decodeDocument(body, defaults), revision, nil
}

// decodeDocument is the stored-document rule without a query, for a reader that
// batches the bodies. A body this build cannot read is treated as absent rather
// than fatal: the owner sees the whole defaults document and the next write
// replaces it. A stored field of the wrong type makes the whole body unreadable,
// so a reader must never infer one field from the raw JSON instead.
func decodeDocument[T any](body string, defaults T) T {
	value := defaults
	if json.Unmarshal([]byte(body), &value) != nil {
		return defaults
	}
	return value
}

func writeDocument[T any](ctx context.Context, tx *sql.Tx, scope string, revision int64, value T, at int64) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return ErrInput
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_documents VALUES(?,?,?,?) ON CONFLICT(scope) DO UPDATE SET revision=excluded.revision,body=excluded.body,updated_ms=excluded.updated_ms`, scope, revision, string(raw), at)
	return err
}

// receipt returns a previously stored result for an operation identifier. A
// replay that carries a different body is refused rather than silently answered
// with the earlier outcome.
func receipt[T any](ctx context.Context, tx *sql.Tx, scope, operationID, digest string) (T, bool, error) {
	var out T
	if strings.HasPrefix(operationID, "p251_") {
		if _, _, ok := usermutation.RequestFrom(ctx); !ok {
			return out, false, usermutation.ErrInvalidKey
		}
		return out, false, nil
	}
	if operationID == "" {
		return out, false, nil
	}
	var stored, result string
	err := tx.QueryRowContext(ctx, `SELECT digest,result FROM admin_receipts WHERE scope=? AND operation_id=?`, scope, operationID).Scan(&stored, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if stored != digest {
		return out, false, ErrIdempotencyKeyReused
	}
	if json.Unmarshal([]byte(result), &out) != nil {
		return out, false, ErrUnavailable
	}
	return out, true, nil
}

func saveReceipt(ctx context.Context, tx *sql.Tx, scope, operationID, digest string, result any, at int64) error {
	if strings.HasPrefix(operationID, "p251_") {
		return usermutation.ErrInvalidKey
	}
	if operationID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_receipts VALUES(?,?,?,?,?) ON CONFLICT(scope,operation_id) DO NOTHING`, scope, operationID, digest, string(raw), at)
	return err
}

func validOperationID(v string) bool {
	if v == "" {
		return false
	}
	if len(v) < 8 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// loadDocument is the shared read path for every configuration page.
func loadDocument[T any](ctx context.Context, s *Service, auth Authorize, scope string, defaults T, normalize func(*T)) (Document[T], error) {
	out := Document[T]{Scope: scope}
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		value, revision, err := readDocument(ctx, tx, scope, defaults)
		if err != nil {
			return err
		}
		if normalize != nil {
			normalize(&value)
		}
		out.Revision, out.Settings, out.Digest = revision, value, digestOf(value)
		return nil
	})
	return out, err
}

// saveDocument is the shared write path: fence on expectedRevision, merge any
// preserved fields inside the accepting transaction, validate, store, and
// return the document the caller should now hold. The receipt identity is the
// caller's request and expected revision, before the current-state merge, so a
// replay returns the first result rather than recomputing it.
func saveDocument[T any](ctx context.Context, s *Service, auth Authorize, scope string, defaults T, change Change[T], validate func(*T) error, normalize func(*T), merge func(current, incoming *T)) (Document[T], error) {
	out := Document[T]{Scope: scope}
	if !validOperationID(change.OperationID) {
		return out, ErrInput
	}
	value := change.Settings
	digest := digestOf(struct {
		Expected int64 `json:"expected"`
		Settings T     `json:"settings"`
	}{change.ExpectedRevision, value})
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		r, err := usermutation.CheckContext(ctx, tx, s.milliseconds())
		if err != nil {
			return err
		}
		if r != nil {
			value, revision, err := readDocument(ctx, tx, scope, defaults)
			if err != nil {
				return err
			}
			if normalize != nil {
				normalize(&value)
			}
			out.Revision, out.Settings, out.Digest = revision, value, digestOf(value)
			return nil
		}

		stored, replayed, err := receipt[Document[T]](ctx, tx, scope, change.OperationID, digest)
		if err != nil {
			return err
		}
		if replayed {
			out = stored
			return nil
		}
		current, revision, err := readDocument(ctx, tx, scope, defaults)
		if err != nil {
			return err
		}
		if revision != change.ExpectedRevision {
			return ErrConflict
		}
		if merge != nil {
			merge(&current, &value)
		}
		if normalize != nil {
			normalize(&value)
		}
		if validate != nil {
			if err := validate(&value); err != nil {
				return err
			}
		}
		at := s.milliseconds()
		if err = writeDocument(ctx, tx, scope, revision+1, value, at); err != nil {
			return err
		}
		out.Revision, out.Settings, out.Digest = revision+1, value, digestOf(value)
		return saveSettingsReceipt(ctx, tx, scope, change.OperationID, digest, out, scope, out.Revision, at)
	})
	return out, err
}

// ValidationError names the exact fields a write could not accept, so a client
// can mark them rather than showing one opaque failure.
type ValidationError struct{ Fields []string }

func (e *ValidationError) Error() string {
	return "invalid values: " + strings.Join(e.Fields, ", ")
}
func (e *ValidationError) Is(target error) bool { return target == ErrInput }

func invalid(fields ...string) error {
	sort.Strings(fields)
	return &ValidationError{Fields: fields}
}

func scopeFor(kind, id string) string { return kind + ":" + id }

// libraryExists keeps every library-scoped page honest about which libraries the
// catalog actually holds.
func libraryExists(ctx context.Context, tx *sql.Tx, id string) (string, string, error) {
	var name, kind string
	err := tx.QueryRowContext(ctx, `SELECT name,kind FROM libraries WHERE id=?`, id).Scan(&name, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return name, kind, err
}

func formatBytes(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value, index := float64(n), 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d %s", n, units[0])
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}

// timeFromMilliseconds renders a stored millisecond stamp for the wire. Every
// timestamp this package publishes is UTC RFC 3339.
func timeFromMilliseconds(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
