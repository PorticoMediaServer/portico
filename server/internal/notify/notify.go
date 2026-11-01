// Package notify is the producer seam for the notification inbox.
//
// It deliberately depends on nothing but the standard library. Producers live
// all over the server — identity, ingestion, networking, the DVR worker, the
// console — and several of them are imported by internal/operations, so the
// writer cannot live there without an import cycle. Reading, paging, receipts,
// broadcast and retention are built on top of this package in
// internal/operations/notifications.go.
//
// Everything a producer can put on the wire is allowlisted here: the audience,
// the severity, the action kinds, the command identifiers and the navigation
// views. A producer cannot invent a client instruction.
package notify

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

// ErrInvalid is returned for a draft that does not satisfy the allowlists. A
// producer that trips it has a bug; it is never a transient condition.
var ErrInvalid = errors.New("invalid notification draft")

const (
	// AudienceProfile addresses one viewer profile's own inbox.
	AudienceProfile = "profile"
	// AudienceAccountAdmin addresses the administrators of one account. It is
	// keyed by account, not by profile, so any admin sees and can clear it.
	AudienceAccountAdmin = "account-admin"
)

const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Sources name the producer. They are part of the dedupe identity, so two
// producers cannot collide on a shared key.
const (
	SourceFeedback   = "feedback"
	SourceDownloads  = "downloads"
	SourceDVR        = "dvr"
	SourceSecurity   = "security"
	SourceScan       = "scan"
	SourceNetworking = "networking"
	SourceStorage    = "storage"
	SourceBroadcast  = "broadcast"
)

// Commands is the complete client command allowlist. A client that does not
// recognise one of these must render the action as disabled rather than guess.
var Commands = []string{"retry-job", "open-download", "dismiss-conflict", "review-device", "open-feedback", "run-scan"}

// Views is the complete navigation allowlist for a navigate action.
var Views = []string{
	"home", "library", "item", "show", "season", "episode", "person", "collection",
	"downloads", "recordings", "recording", "live", "search", "feedback", "notifications",
	"settings", "settings-storage", "settings-network", "settings-security", "settings-jobs",
	"admin-jobs", "admin-feedback", "admin-devices",
}

// Severities and audiences are published so a client can validate a response
// against the same vocabulary the server enforces.
var (
	Severities = []string{SeverityInfo, SeverityWarning, SeverityCritical}
	Audiences  = []string{AudienceProfile, AudienceAccountAdmin}
	Sources    = []string{SourceFeedback, SourceDownloads, SourceDVR, SourceSecurity, SourceScan, SourceNetworking, SourceStorage, SourceBroadcast}
)

// DefaultRetentionDays mirrors the settings registry default for
// notificationDays. The registry field remains the single authority; this value
// applies only before an owner has written the settings document.
const DefaultRetentionDays = 180

// perInboxCap bounds one inbox so a single noisy producer cannot evict another
// viewer's notices or grow the database without limit.
const perInboxCap = 1000

// NavigateTarget is the destination of a navigate action. View is allowlisted;
// EntityID and LibraryID are opaque identifiers the client resolves itself.
type NavigateTarget struct {
	View      string `json:"view"`
	EntityID  string `json:"entityId,omitempty"`
	LibraryID string `json:"libraryId,omitempty"`
}

// Action is either a navigation or an allowlisted client command. Exactly one
// of Target and Command is set, matched to Kind.
type Action struct {
	Kind      string            `json:"kind"`
	Label     string            `json:"label"`
	Target    *NavigateTarget   `json:"target,omitempty"`
	Command   string            `json:"command,omitempty"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// Draft is what a producer raises. Scope must come from ProfileScope or
// AdminScope so the durable key never contains a bare profile identifier.
type Draft struct {
	Audience  string
	Scope     string
	Severity  string
	Source    string
	Category  string
	Title     string
	Body      string
	Arguments map[string]string
	Actions   []Action
	DedupeKey string
	// ExpiresAt is absolute milliseconds. Zero means "apply the retention
	// setting", which is the normal case.
	ExpiresAt int64
}

// Result reports what Raise did, so a producer can tell a new notice from a
// re-raise of one the viewer has already seen.
type Result struct {
	ID       string
	Revision int64
	Created  bool
}

// ProfileScope and AdminScope build the durable inbox key. The encoding matches
// operations.ViewerKey and operations.AccountKey exactly: a JSON string array.
// It is server-bound and never a bare profile identifier.
func ProfileScope(authority, account, profile string) string {
	b, _ := json.Marshal([]string{authority, account, profile})
	return string(b)
}

func AdminScope(authority, account string) string {
	b, _ := json.Marshal([]string{authority, account})
	return string(b)
}

func token() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func allowed(v string, set []string) bool {
	for _, a := range set {
		if a == v {
			return true
		}
	}
	return false
}

// text rejects control characters and bidirectional overrides so a producer
// cannot smuggle a spoofed line into a client's notification list.
func text(v string, max int, required bool) bool {
	if v == "" {
		return !required
	}
	if !utf8.ValidString(v) || len(v) > max {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) && c != '\n' || c >= 0x202a && c <= 0x202e || c >= 0x2066 && c <= 0x2069 {
			return false
		}
	}
	return strings.TrimSpace(v) != ""
}

func identifier(v string, max int) bool {
	if len(v) < 1 || len(v) > max {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}

// ValidateActions checks an action list against the allowlists. It is exported
// so the owner broadcast route can reject a bad request with a 400 before it
// reaches a transaction.
func ValidateActions(actions []Action) error {
	if len(actions) > 3 {
		return ErrInvalid
	}
	for _, a := range actions {
		if !text(a.Label, 80, true) {
			return ErrInvalid
		}
		if len(a.Arguments) > 8 {
			return ErrInvalid
		}
		for k, v := range a.Arguments {
			if !identifier(k, 48) || !text(v, 200, true) {
				return ErrInvalid
			}
		}
		switch a.Kind {
		case "navigate":
			if a.Command != "" || a.Target == nil || !allowed(a.Target.View, Views) {
				return ErrInvalid
			}
			if a.Target.EntityID != "" && !identifier(a.Target.EntityID, 160) {
				return ErrInvalid
			}
			if a.Target.LibraryID != "" && !identifier(a.Target.LibraryID, 160) {
				return ErrInvalid
			}
		case "command":
			if a.Target != nil || !allowed(a.Command, Commands) {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

func (d Draft) validate() error {
	if !allowed(d.Audience, Audiences) || !allowed(d.Severity, Severities) {
		return ErrInvalid
	}
	if !identifier(d.Source, 40) || !identifier(d.Category, 60) || !identifier(d.DedupeKey, 200) {
		return ErrInvalid
	}
	if d.Scope == "" || len(d.Scope) > 400 {
		return ErrInvalid
	}
	if !text(d.Title, 200, true) || !text(d.Body, 1000, false) {
		return ErrInvalid
	}
	if len(d.Arguments) > 12 {
		return ErrInvalid
	}
	for k, v := range d.Arguments {
		if !identifier(k, 48) || !text(v, 300, true) {
			return ErrInvalid
		}
	}
	return ValidateActions(d.Actions)
}

// RetentionDays reads the notificationDays settings field. The notification
// retention setting is not duplicated: notifications.retentionDays in the
// documented API is this one registry field, read here without importing the
// settings package.
func RetentionDays(tx *sql.Tx) int {
	var value sql.NullInt64
	if e := tx.QueryRow(`SELECT settings."notificationDays" FROM server_settings AS settings WHERE settings.singleton=1`).Scan(&value); e != nil || !value.Valid {
		return DefaultRetentionDays
	}
	if value.Int64 < 1 || value.Int64 > 180 {
		return DefaultRetentionDays
	}
	return int(value.Int64)
}

func days(n int) int64 { return int64(n) * 24 * 60 * 60 * 1000 }

// Bump advances the single monotonic counter and stamps the inbox with it. Every
// change to an inbox — a new record, a read receipt, an archive, a prune —
// passes through here, so one number orders the whole feature: it fences a batch
// write, resumes an event stream and answers a long poll.
func Bump(tx *sql.Tx, now int64, scope, audience string) (int64, error) {
	if _, e := tx.Exec(`UPDATE notification_revision SET value=value+1 WHERE singleton=1`); e != nil {
		return 0, e
	}
	var revision int64
	if e := tx.QueryRow(`SELECT value FROM notification_revision WHERE singleton=1`).Scan(&revision); e != nil {
		return 0, e
	}
	_, e := tx.Exec(`INSERT INTO notification_inbox(scope,audience,revision,updated_ms) VALUES(?,?,?,?)
 ON CONFLICT(scope,audience) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms`, scope, audience, revision, now)
	if e != nil {
		return 0, e
	}
	e = apievents.Append(tx, apievents.InboxAudience(scope, audience), "notification.changed", "inbox", "inbox", fmt.Sprint(revision), nil)
	return revision, e
}

// Raise writes or refreshes one notification inside the caller's transaction.
//
// A producer re-raising the same (source, dedupeKey) for the same inbox updates
// the existing record in place — new text, new actions, refreshed expiry, moved
// back to unread and un-archived — instead of stacking duplicates. That is the
// behaviour a retrying job or a repeating condition needs: one live row per
// condition, whose revision advances every time the condition is restated.
func Raise(tx *sql.Tx, now int64, d Draft) (Result, error) {
	var out Result
	if e := d.validate(); e != nil {
		return out, e
	}
	expiry := d.ExpiresAt
	if expiry == 0 {
		expiry = now + days(RetentionDays(tx))
	}
	if expiry <= now {
		return out, ErrInvalid
	}
	arguments := d.Arguments
	if arguments == nil {
		arguments = map[string]string{}
	}
	actions := d.Actions
	if actions == nil {
		actions = []Action{}
	}
	encodedArguments, e := json.Marshal(arguments)
	if e != nil {
		return out, ErrInvalid
	}
	encodedActions, e := json.Marshal(actions)
	if e != nil {
		return out, ErrInvalid
	}
	revision, e := Bump(tx, now, d.Scope, d.Audience)
	if e != nil {
		return out, e
	}
	out.Revision = revision
	e = tx.QueryRow(`SELECT id FROM notification_records WHERE scope=? AND audience=? AND source=? AND dedupe_key=?`,
		d.Scope, d.Audience, d.Source, d.DedupeKey).Scan(&out.ID)
	if e == nil {
		_, e = tx.Exec(`UPDATE notification_records SET severity=?,category=?,title=?,body=?,arguments=?,actions=?,
 revision=?,updated_ms=?,read_ms=NULL,archived_ms=NULL,expires_ms=? WHERE id=?`,
			d.Severity, d.Category, d.Title, d.Body, string(encodedArguments), string(encodedActions),
			revision, now, expiry, out.ID)
		return out, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	out.ID, out.Created = token(), true
	if out.ID == "" {
		return out, errors.New("notification identifier unavailable")
	}
	if _, e = tx.Exec(`INSERT INTO notification_records
 (id,scope,audience,severity,source,category,title,body,arguments,actions,dedupe_key,revision,created_ms,updated_ms,expires_ms)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		out.ID, d.Scope, d.Audience, d.Severity, d.Source, d.Category, d.Title, d.Body,
		string(encodedArguments), string(encodedActions), d.DedupeKey, revision, now, now, expiry); e != nil {
		return out, e
	}
	// Per-inbox rotation, evaluated only on insert. Archived and expired rows go
	// first; only then does the oldest live notice fall off the end.
	_, e = tx.Exec(`DELETE FROM notification_records WHERE scope=? AND audience=? AND sequence NOT IN(
 SELECT sequence FROM notification_records WHERE scope=? AND audience=?
 ORDER BY (archived_ms IS NOT NULL) ASC,(expires_ms<=?) ASC,sequence DESC LIMIT ?)`,
		d.Scope, d.Audience, d.Scope, d.Audience, now, perInboxCap)
	return out, e
}

// The server notices an owner routes in Settings › General › Who sees each
// notice. Each producer that raises for the admins names one of these, and the
// owner's choice for it decides which admin inboxes receive it.
const (
	NoticeScan        = "scan"
	NoticeStorage     = "storage"
	NoticeCertificate = "certificate"
	NoticeRecording   = "recording"
	NoticeFeedback    = "feedback"
	NoticeSecurity    = "security"
)

// Who receives a notice: the owner and the administrators, the owner alone, or no one.
const (
	NoticeOwnerAndAdmins = "both"
	NoticeOwnerOnly      = "owner"
	NoticeNoOne          = "off"
)

var (
	NoticeKinds     = []string{NoticeScan, NoticeStorage, NoticeCertificate, NoticeRecording, NoticeFeedback, NoticeSecurity}
	NoticeAudiences = []string{NoticeOwnerAndAdmins, NoticeOwnerOnly, NoticeNoOne}
)

// NoticeRecords is the predicate that selects one notice kind's records, so a
// narrowed audience can withdraw what it no longer allows.
func NoticeRecords(kind string) (string, []any) {
	switch kind {
	case NoticeCertificate:
		return `source=? AND category LIKE 'networking.certificate%'`, []any{SourceNetworking}
	case NoticeRecording:
		return `source=? AND category='dvr.conflict'`, []any{SourceDVR}
	case NoticeFeedback:
		return `source=? AND category='feedback.received'`, []any{SourceFeedback}
	case NoticeScan:
		return `source=?`, []any{SourceScan}
	case NoticeStorage:
		return `source=?`, []any{SourceStorage}
	case NoticeSecurity:
		// Only the administrators' copies are in scope here; a viewer's own
		// security notice is a profile record, not an account-admin one.
		return `source=? AND category LIKE 'security.%'`, []any{SourceSecurity}
	}
	return `0`, nil
}

// NoticeAudience reads the owner's choice for one notice kind from the
// notification settings document. It is read here, without importing the
// settings package, the way RetentionDays is; anything unreadable is the
// shipped default, the owner and the administrators.
func NoticeAudience(tx *sql.Tx, kind string) string {
	var value sql.NullString
	if e := tx.QueryRow(`SELECT json_extract(body,'$.noticeAudience.'||?) FROM console_documents WHERE scope='notifications'`, kind).Scan(&value); e != nil || !value.Valid || !allowed(value.String, NoticeAudiences) {
		return NoticeOwnerAndAdmins
	}
	return value.String
}

// AdminScopes resolves the account-admin inboxes: the accounts that hold an
// enabled owner membership. Direct Sign-In allows exactly one, but the query
// does not assume that.
func AdminScopes(tx *sql.Tx) ([]string, error) { return AudienceScopes(tx, NoticeOwnerOnly) }

// AudienceScopes resolves the account-admin inboxes one audience reaches:
// enabled owners, enabled owners and administrators, or none.
func AudienceScopes(tx *sql.Tx, audience string) ([]string, error) {
	out := []string{}
	roles := `'owner'`
	switch audience {
	case NoticeNoOne:
		return out, nil
	case NoticeOwnerAndAdmins:
		roles = `'owner','admin'`
	case NoticeOwnerOnly:
	default:
		return nil, ErrInvalid
	}
	rows, e := tx.Query(`SELECT DISTINCT a.id FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.role IN(` + roles + `) AND m.disabled=0 ORDER BY a.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var account string
		if e = rows.Scan(&account); e != nil {
			return nil, e
		}
		out = append(out, AdminScope("local", account))
	}
	return out, rows.Err()
}

// ProfileScopes resolves every profile inbox on this server, for an owner
// broadcast. It is bounded by the profile table, which the account surface caps.
func ProfileScopes(tx *sql.Tx) ([]string, error) {
	rows, e := tx.Query(`SELECT account_id,id FROM direct_profiles WHERE deleted=0 ORDER BY account_id,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var account, profile string
		if e = rows.Scan(&account, &profile); e != nil {
			return nil, e
		}
		out = append(out, ProfileScope("local", account, profile))
	}
	return out, rows.Err()
}

// RaiseForAdmins is the common producer shape: one condition, delivered under
// the same dedupe key to every account-admin inbox the owner's choice for this
// kind of notice reaches.
func RaiseForAdmins(tx *sql.Tx, now int64, kind string, d Draft) ([]Result, error) {
	if !allowed(kind, NoticeKinds) {
		return nil, ErrInvalid
	}
	scopes, e := AudienceScopes(tx, NoticeAudience(tx, kind))
	if e != nil {
		return nil, e
	}
	out := []Result{}
	d.Audience = AudienceAccountAdmin
	for _, scope := range scopes {
		d.Scope = scope
		r, e := Raise(tx, now, d)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
