// Package webhooks sends the owner's chosen events to the owner's chosen URLs.
//
// It is the local server's own doing: nothing here involves the Portico Account
// service. An event becomes a row in webhook_deliveries (most are written by
// triggers in the schema, in the transaction that made the change; a few are
// emitted from Go). The dispatcher sends each row, signs it, retries with
// backoff, and leaves the row behind as the delivery log.
//
// Secrets: a webhook's signing secret is shown when it is made or rotated and
// never again; lists carry a hint. Neither the secret nor the URL is ever
// written to a log line, an event or a payload.
package webhooks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Event types. The test event is sent only by "send test" and cannot be
// subscribed to.
const (
	TitleAdded        = "title.added"
	TitleUpdated      = "title.updated"
	TitleRemoved      = "title.removed"
	ScanStarted       = "scan.started"
	ScanFinished      = "scan.finished"
	PlaybackStarted   = "playback.started"
	PlaybackPaused    = "playback.paused"
	PlaybackResumed   = "playback.resumed"
	PlaybackStopped   = "playback.stopped"
	RecordingFinished = "recording.finished"
	RecordingFailed   = "recording.failed"
	MemberSignedIn    = "member.signed_in"
	BackupSucceeded   = "backup.succeeded"
	BackupFailed      = "backup.failed"
	StorageLow        = "storage.low"
	UpdateAvailable   = "update.available"
	ServerStarted     = "server.started"
	Test              = "webhook.test"
)

// Events is every type an owner may choose, in the order a console lists them.
var Events = []string{
	TitleAdded, TitleUpdated, TitleRemoved,
	ScanStarted, ScanFinished,
	PlaybackStarted, PlaybackPaused, PlaybackResumed, PlaybackStopped,
	RecordingFinished, RecordingFailed,
	MemberSignedIn,
	BackupSucceeded, BackupFailed,
	StorageLow, UpdateAvailable, ServerStarted,
}

const (
	// MaxWebhooks bounds how many an owner may configure.
	MaxWebhooks = 20
	// MaxQueued is how many deliveries may wait for one webhook; the schema's
	// fan-out trigger enforces the same number.
	MaxQueued = 1000
	// LogKept is how many finished deliveries are kept per webhook.
	LogKept    = 200
	maxURL     = 2048
	maxName    = 80
	secretSize = 32
)

var (
	ErrInvalid  = errors.New("invalid webhook")
	ErrCapacity = errors.New("too many webhooks")
	// ErrIdempotencyKeyReused is one operation identifier sent again with a
	// different request. The first attempt stands; this one is refused.
	ErrIdempotencyKeyReused = errors.New("webhook operation reused for a different request")
	// ErrCreateGone answers a repeated create whose webhook has since been
	// deleted: the request was already made, so it cannot make another.
	ErrCreateGone = errors.New("the webhook this request made no longer exists")
)

// ValidationError names the fields a request got wrong.
type ValidationError struct{ Fields []string }

// RevisionError is a change made against an older revision of the webhook.
// Current is the webhook as it is now, so the editor can carry its change over.
type RevisionError struct{ Current Webhook }

func (e *RevisionError) Error() string { return "webhook changed since it was read" }

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// Webhook is one configured destination as it is listed.
type Webhook struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
	// A transferred integration cannot send, including tests, until its new
	// owner explicitly reviews the destination/events and replaces the secret.
	RequiresOwnerReview bool `json:"requiresOwnerReview,omitempty"`
	// SecretHint is the last characters of the signing secret.
	SecretHint string `json:"secretHint"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	// Waiting is how many deliveries are queued. Failing is how many in a row
	// could not be delivered; it returns to zero on the next success. Dropped
	// counts events that arrived while the queue was full and were not kept.
	Waiting int `json:"waiting"`
	Failing int `json:"failing"`
	Dropped int `json:"dropped"`
	// Revision moves with every change the owner makes (not with deliveries);
	// a change names the revision it was made against.
	Revision int64 `json:"revision"`
}

// Issued is the answer to creating a webhook or rotating its secret: the one
// time the secret is shown.
type Issued struct {
	Webhook Webhook `json:"webhook"`
	Secret  string  `json:"secret,omitempty"`
}

type List struct {
	Items []Webhook `json:"items"`
	// Events is every event type that can be chosen.
	Events []string `json:"events"`
	// Maximum is how many webhooks may exist.
	Maximum int `json:"maximum"`
}

// Create is the body for a new webhook. Secret may be supplied (at least 16
// characters); leave it out and the server makes one.
type Create struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Secret  string   `json:"secret,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
	// OperationID makes one create retry-safe. A request that names an operation
	// and repeats it, unchanged, gets the webhook the first attempt made instead
	// of a second one; a repeat with a different body is refused. It is optional:
	// a caller that does not name one creates exactly as before.
	OperationID string `json:"operationId,omitempty"`
	// Actor is who asked. The route supplies it and the body can never set it, so
	// it stays out of the published document and out of the request digest.
	Actor string `json:"-"`
}

// Change is a partial edit: a field left out is left alone. It is made
// against the revision the editor read; when the webhook has changed since,
// nothing is written and the answer is the webhook as it is now.
type Change struct {
	ExpectedRevision int64     `json:"expectedRevision"`
	Name             *string   `json:"name,omitempty"`
	URL              *string   `json:"url,omitempty"`
	Events           *[]string `json:"events,omitempty"`
	Enabled          *bool     `json:"enabled,omitempty"`
	// RotateSecret replaces the signing secret with a new one, which the answer
	// shows once. Secret sets a chosen one instead.
	RotateSecret bool   `json:"rotateSecret,omitempty"`
	Secret       string `json:"secret,omitempty"`
}

// Delivery is one attempt history, as the delivery log lists it.
type Delivery struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	// State is pending, delivered or failed (given up).
	State       string `json:"state"`
	Attempts    int    `json:"attempts"`
	CreatedAt   string `json:"createdAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	NextAttempt string `json:"nextAttemptAt,omitempty"`
	// Status is the HTTP status of the last attempt, 0 when there was none.
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

type DeliveryPage struct {
	Items      []Delivery `json:"items"`
	Limit      int        `json:"limit"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// Authorize runs in the store's transaction: the caller's live authority.
type Authorize func(context.Context, *sql.Tx) error

type Store struct {
	DB  *sql.DB
	Now func() time.Time
	// Wake, when set, tells the dispatcher there is something to send.
	Wake     func()
	activeMu sync.Mutex
	active   map[string]*sendAdmission
}

func New(db *sql.DB) *Store { return &Store{DB: db, Now: time.Now} }

func (s *Store) now() int64 { return s.Now().UTC().UnixMilli() }

func token(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func identifier() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

func safeText(v string, max int) bool {
	if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > max {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// ValidURL accepts an absolute http or https URL with a host. Credentials in
// the URL are refused: they would be listed wherever the URL is. A private or
// loopback address is the owner's to choose.
func ValidURL(raw string) bool {
	if raw == "" || len(raw) > maxURL || !utf8.ValidString(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return false
	}
	return true
}

func validSecret(secret string) bool {
	if len(secret) < 16 || len(secret) > 128 {
		return false
	}
	for _, r := range secret {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// normalizeEvents validates a chosen event list and returns it in catalogue
// order without duplicates.
func normalizeEvents(chosen []string) ([]string, bool) {
	if len(chosen) == 0 || len(chosen) > len(Events) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, event := range chosen {
		known := false
		for _, candidate := range Events {
			known = known || candidate == event
		}
		if !known || seen[event] {
			return nil, false
		}
		seen[event] = true
	}
	out := make([]string, 0, len(seen))
	for _, event := range Events {
		if seen[event] {
			out = append(out, event)
		}
	}
	return out, true
}

func packEvents(events []string) string { return "," + strings.Join(events, ",") + "," }
func unpackEvents(packed string) []string {
	out := []string{}
	for _, event := range strings.Split(strings.Trim(packed, ","), ",") {
		if event != "" {
			out = append(out, event)
		}
	}
	return out
}

func hint(secret string) string {
	if len(secret) <= 4 {
		return ""
	}
	return secret[len(secret)-4:]
}

func stamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func (s *Store) write(ctx context.Context, auth Authorize, fn func(*sql.Tx) error) error {
	if s == nil || s.DB == nil {
		return ErrInvalid
	}
	return dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		if auth != nil {
			if err := auth(ctx, tx); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

const webhookColumns = `w.id,w.name,w.url,w.events,w.enabled,w.secret,w.created_ms,w.updated_ms,w.failures,w.dropped,(SELECT count(*) FROM webhook_deliveries d WHERE d.webhook_id=w.id AND d.state='pending'),w.revision,w.owner_review_required`

func scanWebhook(row interface{ Scan(...any) error }) (Webhook, error) {
	var w Webhook
	var events, secret string
	var created, updated int64
	if err := row.Scan(&w.ID, &w.Name, &w.URL, &events, &w.Enabled, &secret, &created, &updated, &w.Failing, &w.Dropped, &w.Waiting, &w.Revision, &w.RequiresOwnerReview); err != nil {
		return w, err
	}
	w.Events, w.SecretHint, w.CreatedAt, w.UpdatedAt = unpackEvents(events), hint(secret), stamp(created), stamp(updated)
	return w, nil
}

func (s *Store) List(ctx context.Context, auth Authorize) (List, error) {
	out := List{Items: []Webhook{}, Events: append([]string{}, Events...), Maximum: MaxWebhooks}
	if s == nil || s.DB == nil {
		return out, ErrInvalid
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w ORDER BY w.created_ms,w.id LIMIT ?`, MaxWebhooks)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return List{Items: []Webhook{}, Events: out.Events, Maximum: MaxWebhooks}, err
		}
		out.Items = append(out.Items, w)
	}
	return out, rows.Err()
}

// validOperationID bounds a caller-supplied idempotency key the way every other
// owner write in this server does.
func validOperationID(v string) bool {
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

func requestDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// resolveCreate answers whether this owner's identifier already names an effect,
// before anything is created and before the capacity check. The webhook row is
// the effect and lives exactly as long as the webhook does, so it answers first;
// the receipt only remembers a request whose webhook has since been deleted, so
// that a retry stays a refusal rather than making a second endpoint. Neither
// answer carries a secret: it was shown once, when the webhook was made.
func (s *Store) resolveCreate(tx *sql.Tx, actor, operation, want string) (Webhook, bool, error) {
	var id, digest string
	err := tx.QueryRow(`SELECT id,create_digest FROM webhooks WHERE create_actor=? AND create_operation=?`, actor, operation).Scan(&id, &digest)
	switch {
	case err == nil:
		if digest != want {
			return Webhook{}, false, ErrIdempotencyKeyReused
		}
		webhook, err := scanWebhook(tx.QueryRow(`SELECT `+webhookColumns+` FROM webhooks w WHERE w.id=?`, id))
		return webhook, err == nil, err
	case !errors.Is(err, sql.ErrNoRows):
		return Webhook{}, false, err
	}
	err = tx.QueryRow(`SELECT digest FROM webhook_create_receipts WHERE actor=? AND operation=?`, actor, operation).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, false, nil
	}
	if err != nil {
		return Webhook{}, false, err
	}
	if digest != want {
		return Webhook{}, false, ErrIdempotencyKeyReused
	}
	// The request was made and its webhook has since been deleted, so the effect
	// it names is gone. That stays a refusal, not a new endpoint.
	return Webhook{}, false, ErrCreateGone
}

// recordCreateReceipt remembers one create request after its webhook exists, so
// the request can still be answered truthfully once that webhook is deleted. It
// is written in the same transaction as the webhook, and this table is bounded
// by this package alone: nothing here deletes another owner's receipts.
func recordCreateReceipt(tx *sql.Tx, actor, operation, want, id string, now int64) error {
	if _, err := tx.Exec(`INSERT INTO webhook_create_receipts(actor,operation,digest,webhook_id,created_ms) VALUES(?,?,?,?,?) ON CONFLICT(actor,operation) DO NOTHING`, actor, operation, want, id, now); err != nil {
		return err
	}
	// A retry aid, not history: keep the newest 2000 of these, and nothing else.
	_, err := tx.Exec(`DELETE FROM webhook_create_receipts WHERE rowid NOT IN(SELECT rowid FROM webhook_create_receipts ORDER BY created_ms DESC LIMIT 2000)`)
	return err
}

func (s *Store) Create(ctx context.Context, auth Authorize, q Create) (Issued, error) {
	var out Issued
	fields := []string{}
	name := strings.TrimSpace(q.Name)
	if !safeText(name, maxName) {
		fields = append(fields, "name")
	}
	if !ValidURL(q.URL) {
		fields = append(fields, "url")
	}
	events, ok := normalizeEvents(q.Events)
	if !ok {
		fields = append(fields, "events")
	}
	secret := q.Secret
	if secret == "" {
		secret = "whsec_" + token(secretSize)
	} else if !validSecret(secret) {
		fields = append(fields, "secret")
	}
	// An identifier with no actor cannot be bound to one caller's intent, so it
	// is refused rather than stored unbound. Every other request keeps the
	// original create, with no identifier at all.
	if q.OperationID != "" && (!validOperationID(q.OperationID) || q.Actor == "") {
		fields = append(fields, "operationId")
	}
	if len(fields) > 0 {
		return out, &ValidationError{fields}
	}
	enabled := q.Enabled == nil || *q.Enabled
	id := identifier()
	// A create that named an identifier stores the whole identity; one that did
	// not stores none of it, so no caller identity is kept for a legacy create and
	// the row's shape is closed by the table's own check.
	actor, operation, digest := "", "", ""
	if q.OperationID != "" {
		actor, operation, digest = q.Actor, q.OperationID, requestDigest(q)
	}
	created := false
	err := s.write(ctx, auth, func(tx *sql.Tx) error {
		// One create request's identity is resolved before capacity: a server that
		// filled up because of this very request still answers its retry, and an
		// identifier that already names an effect never makes a second one.
		if q.OperationID != "" {
			prior, found, err := s.resolveCreate(tx, actor, operation, digest)
			if err != nil {
				return err
			}
			if found {
				out.Webhook = prior
				return nil
			}
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM webhooks`).Scan(&count); err != nil {
			return err
		}
		if count >= MaxWebhooks {
			return ErrCapacity
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhooks(id,name,url,secret,events,enabled,created_ms,updated_ms,create_actor,create_operation,create_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, name, q.URL, secret, packEvents(events), enabled, now, now, actor, operation, digest); err != nil {
			return err
		}
		var err error
		if out.Webhook, err = scanWebhook(tx.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.id=?`, id)); err != nil {
			return err
		}
		created = true
		if operation == "" {
			return nil
		}
		return recordCreateReceipt(tx, actor, operation, digest, id, now)
	})
	if err != nil {
		return Issued{}, err
	}
	// Only an attempt that made the webhook shows its secret; a replay answers
	// with the webhook alone.
	if created {
		out.Secret = secret
	}
	return out, nil
}

func (s *Store) Update(ctx context.Context, auth Authorize, id string, q Change) (Issued, error) {
	var out Issued
	fields := []string{}
	if q.Name != nil {
		trimmed := strings.TrimSpace(*q.Name)
		q.Name = &trimmed
		if !safeText(trimmed, maxName) {
			fields = append(fields, "name")
		}
	}
	if q.URL != nil && !ValidURL(*q.URL) {
		fields = append(fields, "url")
	}
	var events []string
	if q.Events != nil {
		var ok bool
		if events, ok = normalizeEvents(*q.Events); !ok {
			fields = append(fields, "events")
		}
	}
	secret := ""
	switch {
	case q.Secret != "" && q.RotateSecret:
		fields = append(fields, "secret")
	case q.Secret != "":
		if secret = q.Secret; !validSecret(secret) {
			fields = append(fields, "secret")
		}
	case q.RotateSecret:
		secret = "whsec_" + token(secretSize)
	}
	if len(fields) > 0 {
		return out, &ValidationError{fields}
	}
	err := s.write(ctx, auth, func(tx *sql.Tx) error {
		current, err := scanWebhook(tx.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.id=?`, id))
		if err != nil {
			return err
		}
		if current.Revision != q.ExpectedRevision {
			return &RevisionError{Current: current}
		}
		var reviewed, replaced bool
		var generation int64
		var previousSecret string
		var retiredSecretDigest string
		if err = tx.QueryRowContext(ctx, `SELECT owner_reviewed,owner_secret_replaced,authority_generation,secret,retired_secret_digest FROM webhooks WHERE id=?`, id).Scan(&reviewed, &replaced, &generation, &previousSecret, &retiredSecretDigest); err != nil {
			return err
		}
		if secret != "" && retiredSecretDigest != "" && secretDigest(secret) == retiredSecretDigest {
			return &ValidationError{[]string{"secret"}}
		}
		if current.RequiresOwnerReview {
			// Explicitly saving both fields records review, even when the new
			// owner deliberately keeps their values. A bare enable is never review.
			if q.URL != nil && q.Events != nil {
				reviewed = true
			}
			if secret != "" {
				if secret == previousSecret {
					return &ValidationError{[]string{"secret"}}
				}
				replaced = true
			}
			if q.Enabled != nil && *q.Enabled && (!reviewed || !replaced) {
				return &ValidationError{[]string{"requiresOwnerReview"}}
			}
		}
		now := s.now()
		authorityChanged := q.URL != nil || q.Events != nil || q.Enabled != nil || secret != ""
		if authorityChanged {
			generation++
			requiresReview := current.RequiresOwnerReview && !(q.Enabled != nil && *q.Enabled && reviewed && replaced)
			if _, err = tx.ExecContext(ctx, `UPDATE webhooks SET authority_generation=?,owner_review_required=?,owner_reviewed=?,owner_secret_replaced=? WHERE id=?`, generation, requiresReview, reviewed, replaced, id); err != nil {
				return err
			}
			// Cached tests and retries keep their original generation. Changing
			// configuration never repurposes that old backlog for a new target.
			if _, err = tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='failed',finished_ms=?,error='configuration_changed' WHERE webhook_id=? AND state='pending'`, now, id); err != nil {
				return err
			}
			if err = pruneDeliveryLog(ctx, tx, id); err != nil {
				return err
			}
			if err = dbwork.AfterCommit(tx, func() { s.cancelSends(id, generation) }); err != nil {
				return err
			}
		}
		// One change is one revision, whatever it touches.
		if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET updated_ms=?,revision=revision+1 WHERE id=?`, now, id); err != nil {
			return err
		}
		if q.Name != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET name=? WHERE id=?`, *q.Name, id); err != nil {
				return err
			}
		}
		if q.URL != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET url=?,failures=0 WHERE id=?`, *q.URL, id); err != nil {
				return err
			}
		}
		if q.Events != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET events=? WHERE id=?`, packEvents(events), id); err != nil {
				return err
			}
		}
		if q.Enabled != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET enabled=?,failures=0 WHERE id=?`, *q.Enabled, id); err != nil {
				return err
			}
			if !*q.Enabled {
				// What was waiting is not sent later as stale news.
				if _, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='failed',finished_ms=?,error='webhook_turned_off' WHERE webhook_id=? AND state='pending'`, now, id); err != nil {
					return err
				}
			}
		}
		if secret != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET secret=? WHERE id=?`, secret, id); err != nil {
				return err
			}
		}
		out.Webhook, err = scanWebhook(tx.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.id=?`, id))
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	// Only a rotation shows a secret; a secret the caller chose is theirs already.
	if q.RotateSecret {
		out.Secret = secret
	}
	s.wake()
	return out, nil
}

// Get reads one webhook, as the list shows it.
func (s *Store) Get(ctx context.Context, auth Authorize, id string) (Webhook, error) {
	if s == nil || s.DB == nil {
		return Webhook{}, ErrInvalid
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return Webhook{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return Webhook{}, err
		}
	}
	return scanWebhook(tx.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.id=?`, id))
}

// Delete removes a webhook with its queue and its log. Deleting one that is
// not there is sql.ErrNoRows.
func (s *Store) Delete(ctx context.Context, auth Authorize, id string) error {
	return s.write(ctx, auth, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM webhooks WHERE id=?`, id)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return dbwork.AfterCommit(tx, func() { s.cancelSends(id, 0) })
	})
}

func (s *Store) wake() {
	if s != nil && s.Wake != nil {
		s.Wake()
	}
}

func scanDelivery(row interface{ Scan(...any) error }) (Delivery, int64, error) {
	var d Delivery
	var created, finished, next int64
	if err := row.Scan(&d.ID, &d.Event, &d.State, &d.Attempts, &created, &finished, &next, &d.Status, &d.DurationMs, &d.Error); err != nil {
		return d, 0, err
	}
	d.CreatedAt, d.FinishedAt = stamp(created), stamp(finished)
	if d.State == "pending" {
		d.NextAttempt = stamp(next)
	}
	return d, created, nil
}

const deliveryColumns = `id,type,state,attempts,created_ms,finished_ms,next_attempt_ms,status,duration_ms,error`

// Deliveries is the delivery log of one webhook, newest first.
func (s *Store) Deliveries(ctx context.Context, auth Authorize, id, cursor string, limit int) (DeliveryPage, error) {
	out := DeliveryPage{Items: []Delivery{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return out, &ValidationError{[]string{"limit"}}
	}
	out.Limit = limit
	var beforeMs int64
	beforeID := ""
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var at struct {
			Created int64  `json:"c"`
			ID      string `json:"i"`
		}
		if err != nil || len(raw) > 256 || json.Unmarshal(raw, &at) != nil || at.Created <= 0 || at.ID == "" {
			return out, &ValidationError{[]string{"cursor"}}
		}
		beforeMs, beforeID = at.Created, at.ID
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return out, err
		}
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM webhooks WHERE id=?`, id).Scan(&exists); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries WHERE webhook_id=? AND (?=0 OR (created_ms,id)<(?,?)) ORDER BY created_ms DESC,id DESC LIMIT ?`, id, beforeMs, beforeMs, beforeID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lastCreated int64
	for rows.Next() {
		d, created, err := scanDelivery(rows)
		if err != nil {
			return DeliveryPage{Items: []Delivery{}}, err
		}
		if len(out.Items) == limit {
			raw, _ := json.Marshal(map[string]any{"c": lastCreated, "i": out.Items[limit-1].ID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		out.Items, lastCreated = append(out.Items, d), created
	}
	return out, rows.Err()
}

// SendTest queues a test event for one webhook, whether or not it is enabled
// or subscribed to anything, and returns the delivery to look for in the log.
func (s *Store) SendTest(ctx context.Context, auth Authorize, id string) (Delivery, error) {
	var out Delivery
	err := s.write(ctx, auth, func(tx *sql.Tx) error {
		var retired bool
		var generation int64
		if err := tx.QueryRowContext(ctx, `SELECT owner_review_required,authority_generation FROM webhooks WHERE id=?`, id).Scan(&retired, &generation); err != nil {
			return err
		}
		if retired {
			return &ValidationError{[]string{"requiresOwnerReview"}}
		}
		var waiting int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE webhook_id=? AND state='pending' AND type=?`, id, Test).Scan(&waiting); err != nil {
			return err
		}
		if waiting >= 5 {
			return ErrCapacity
		}
		delivery, now := identifier(), s.now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,webhook_id,authority_generation,type,data,next_attempt_ms,created_ms) VALUES(?,?,?,?,?,?,?)`, delivery, id, generation, Test, `{"message":"This is a test from Portico."}`, now, now); err != nil {
			return err
		}
		var err error
		out, _, err = scanDelivery(tx.QueryRowContext(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries WHERE id=?`, delivery))
		return err
	})
	if err != nil {
		return Delivery{}, err
	}
	s.wake()
	return out, nil
}

// Emit records an event that did not come from a table change. data must
// marshal to a JSON object and must never contain a secret. subject, when set,
// folds repeats of the same thing that have not been sent yet into one.
func (s *Store) Emit(ctx context.Context, event, subject string, data map[string]any) error {
	if s == nil || s.DB == nil {
		return nil
	}
	known := false
	for _, candidate := range Events {
		known = known || candidate == event
	}
	if !known {
		return ErrInvalid
	}
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 16<<10 {
		return ErrInvalid
	}
	_, err = dbwork.ExecWrite(ctx, s.DB, dbwork.ClassMaintenance, `INSERT INTO webhook_events(type,subject,data) VALUES(?,?,?)`, event, subject, string(raw))
	if err == nil {
		s.wake()
	}
	return err
}

// subscribed lists the events at least one enabled webhook wants, for callers
// that would otherwise do work to build an event nobody receives.
func (s *Store) Subscribed(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := dbwork.Query(ctx, s.DB, `SELECT events FROM webhooks WHERE enabled=1`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var packed string
		if err = rows.Scan(&packed); err != nil {
			return out, err
		}
		for _, event := range unpackEvents(packed) {
			out[event] = true
		}
	}
	return out, rows.Err()
}

// SortedEvents returns the catalogue sorted by name, for documentation tests.
func SortedEvents() []string {
	out := append([]string{}, Events...)
	sort.Strings(out)
	return out
}

// State reads a small value the dispatcher's helpers keep between runs (the
// last update that was announced). A missing key is "".
func (s *Store) State(ctx context.Context, key string) (string, error) {
	var value string
	err := dbwork.QueryRow(ctx, s.DB, `SELECT value FROM webhook_state WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassMaintenance, `INSERT INTO webhook_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
