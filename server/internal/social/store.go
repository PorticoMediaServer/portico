// Package social implements Watch Together groups and remote playback: the
// Portico-to-Portico receiver handoff and the Google Cast bootstrap protocol.
//
// The package owns no playback transport. It owns the shared *intent* — who is
// in a group, what the group's authoritative timeline is, which receiver a
// viewer may hand playback to — and reads Playback v1 sessions only through the
// Playback seam below (implemented in internal/playbackv1/social.go).
package social

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Protocol is the wire version every request and response in this group carries.
const Protocol = "1.0"

const MaxPositionUS int64 = (1 << 40) * 1000

// Caps and timings. The contract fixes the host-reconnect boundaries (two
// minutes to pause, ten minutes to end) and the group size ceiling; the rest are
// this implementation's bounded defaults.
const (
	MaxMembers            = 32
	MaxQueueEntries       = 256
	MaxReceipts           = 128
	MaxRetainedEvents     = 512
	MemberStaleSeconds    = 120
	ReadinessStaleSeconds = 30
	HostPauseSeconds      = 120
	HostEndSeconds        = 600
	InviteTTLSeconds      = 900
	InviteMaxTTLSeconds   = 3600
	InviteDefaultUses     = 8
	InviteMaxUses         = 32
	InvitesPerGroupHour   = 12
	JoinAttemptsPerHour   = 20
	GrantTTLSeconds       = 900
	ReceiverPresenceTTL   = 120
	HandoffTTLSeconds     = 120
	CastCodeTTLSeconds    = 300
	CastSessionTTLSeconds = 86400
	CastRedeemPerHour     = 30
	CastBootstrapPerHour  = 20
)

// Fault is a refusal with a stable machine code. Codes follow the contract's
// error families (§15) and its invitation codes (§17).
type Fault struct {
	Code       string
	Status     int
	Message    string
	Detail     map[string]any
	Retryable  bool
	RetryAfter int
	// credential marks a refused Cast code or device token, which is charged to
	// the failed-attempt budgets (castAttempt).
	credential bool
}

func credentialFault(code string, status int, message string) *Fault {
	return &Fault{Code: code, Status: status, Message: message, credential: true}
}

func (f *Fault) Error() string { return f.Code }

func fault(code string, status int, message string) *Fault {
	return &Fault{Code: code, Status: status, Message: message}
}

var (
	errInvalid    = fault("invalid_request", 400, "The request could not be understood.")
	errNotFound   = fault("not_found", 404, "No such resource.")
	errConflict   = fault("revision_conflict", 409, "The resource changed since the revision you sent.")
	errHost       = fault("host_required", 403, "Only the group host may do that.")
	errGroupEnded = fault("group_ended", 409, "The group has ended.")
	errGroupFull  = fault("group_full", 409, "The group is full.")
	errNotMember  = fault("membership_revoked", 403, "You are not a member of this group.")
)

// Store is the social playback service. Every dependency is an explicit seam so
// tests can drive the clock and the visibility policy directly.
type Store struct {
	DB *sql.DB
	// Now is the injectable clock. Every expiry, timeline boundary and heartbeat
	// in this package reads it; nothing calls time.Now directly.
	Now func() time.Time
	// Playback reads and ends the viewer's Playback v1 sessions: a group's host
	// session, a handoff's source (B8a). Nil: groups work without an authority
	// projection, and handoffs are unavailable.
	Playback Playback
	// Visible reports whether a viewer may see one catalog item. It is wired by
	// HTTP composition to the same library policy every other route uses. A nil
	// Visible treats everything as visible, which is only ever correct in a
	// composition with no library restrictions at all.
	Visible func(ctx context.Context, tx *sql.Tx, v identity.Viewer, itemID string) (bool, error)
	// Identity issues the ordinary viewer session a redeemed Cast receiver plays
	// with. Nil refuses redemption rather than inventing a credential.
	Identity *identity.Service

	// CastApplicationID reads the published Google Cast application id from the
	// `cast.applicationId` setting. It takes the caller's transaction because the
	// server runs SQLite on a single connection: a second connection opened while
	// a transaction is held would deadlock, not queue.
	CastApplicationID func(context.Context, *sql.Tx) string
	events            *hub
	// sweeps is the process-wide host-timeline sweeper and its holders. See
	// sweeper.go for why there is exactly one of it rather than one per stream.
	sweeps sweeper
}

// New builds a store with the production clock and an empty event hub.
func New(db *sql.DB) *Store {
	return &Store{DB: db, Now: time.Now, events: newHub()}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *Store) ms() int64 { return s.now().UnixMilli() }
func (s *Store) hub() *hub {
	if s.events == nil {
		s.events = newHub()
	}
	return s.events
}

// begin opens the store's gated write transaction. Social mutations are ordinary
// request-path work unless the calling context says otherwise.
func (s *Store) begin(ctx context.Context) (*dbwork.Write, error) {
	return dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
}

// visible answers the all-participant visibility question for one member.
func (s *Store) visible(ctx context.Context, tx *sql.Tx, v identity.Viewer, itemID string) (bool, error) {
	if itemID == "" {
		return true, nil
	}
	if s.Visible == nil {
		return true, nil
	}
	return s.Visible(ctx, tx, v, itemID)
}

// --- identifiers, codes and digests -----------------------------------------

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ValidID reports whether a client-supplied identifier is in the accepted shape.
func ValidID(v string) bool { return idPattern.MatchString(v) }

func token(prefix string) string { return prefix + identity.Token() }

func digest(v string) string { return identity.Digest(v) }

func digestJSON(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// codeAlphabet excludes I, L, O, 0 and 1: a pairing code is read off a TV across
// a room and typed on a phone, so visually confusable glyphs are not in it.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newCode draws n characters uniformly from codeAlphabet with rejection
// sampling, so no character is more likely than another.
func newCode(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	limit := byte(256 - (256 % len(codeAlphabet)))
	for len(out) < n {
		if _, e := rand.Read(buf); e != nil {
			return "", e
		}
		if buf[0] >= limit {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out), nil
}

var codePattern = regexp.MustCompile(`^[A-Z2-9]{4,16}$`)

func normalizeCode(v string) (string, bool) {
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z':
			c -= 32
		case c == ' ' || c == '-':
			continue
		}
		out = append(out, c)
	}
	s := string(out)
	return s, codePattern.MatchString(s)
}

func sameSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// --- small helpers ----------------------------------------------------------

func counter(v int64) string { return strconv.FormatInt(v, 10) }

func parseCounter(v string) (int64, bool) {
	if v == "" || len(v) > 19 {
		return 0, false
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func stamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

func optionalStamp(ms int64) *string {
	if ms <= 0 {
		return nil
	}
	v := stamp(ms)
	return &v
}

func text(v string, max int) bool {
	if len(v) == 0 || !utf8.ValidString(v) || len([]rune(v)) > max {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return false
		}
	}
	return true
}

// --- rate limiting ----------------------------------------------------------

// limit applies a fixed-window counter to one bucket. It returns a 429 fault
// when the window is already full. Windows are stored, not held in memory, so a
// restart does not hand an attacker a fresh allowance.
func (s *Store) limit(ctx context.Context, tx *sql.Tx, bucket string, max int, window time.Duration) error {
	now := s.ms()
	start := now - window.Milliseconds()
	var windowStart int64
	var count int
	e := tx.QueryRowContext(ctx, `SELECT window_start_ms,count FROM social_rate WHERE bucket=?`, bucket).Scan(&windowStart, &count)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.ExecContext(ctx, `INSERT INTO social_rate(bucket,window_start_ms,count) VALUES(?,?,1)`, bucket, now)
		return e
	}
	if e != nil {
		return e
	}
	if windowStart <= start {
		_, e = tx.ExecContext(ctx, `UPDATE social_rate SET window_start_ms=?,count=1 WHERE bucket=?`, now, bucket)
		return e
	}
	if count >= max {
		retry := int((windowStart+window.Milliseconds()-now)/1000) + 1
		return &Fault{Code: "rate_limited", Status: 429, Message: "Too many attempts. Try again shortly.", Retryable: true, RetryAfter: retry}
	}
	_, e = tx.ExecContext(ctx, `UPDATE social_rate SET count=count+1 WHERE bucket=?`, bucket)
	return e
}

type rateBucket struct {
	name string
	max  int
}

// rateFull is limit's check without the charge: a 429 fault when the bucket's
// current window is already full.
func (s *Store) rateFull(ctx context.Context, tx *sql.Tx, bucket string, max int, window time.Duration) error {
	now := s.ms()
	var windowStart int64
	var count int
	e := tx.QueryRowContext(ctx, `SELECT window_start_ms,count FROM social_rate WHERE bucket=?`, bucket).Scan(&windowStart, &count)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if windowStart > now-window.Milliseconds() && count >= max {
		retry := int((windowStart+window.Milliseconds()-now)/1000) + 1
		return &Fault{Code: "rate_limited", Status: 429, Message: "Too many attempts. Try again shortly.", Retryable: true, RetryAfter: retry}
	}
	return nil
}

// chargeFailure records one failed attempt in every bucket, in a transaction of
// its own that outlives the caller's cancellation.
func (s *Store) chargeFailure(ctx context.Context, buckets []rateBucket) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	gated, e := s.begin(ctx)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, b := range buckets {
		// A full bucket stays full; its refusal is the caller's answer next time.
		var f *Fault
		if e = s.limit(ctx, tx, b.name, b.max, time.Hour); e != nil && !errors.As(e, &f) {
			return e
		}
	}
	return gated.Commit()
}

func viewerKey(v identity.Viewer) string {
	return digest(v.Authority + "\x00" + v.AccountID + "\x00" + v.ProfileID)
}

// Playback is what social playback needs of Playback v1 (B8a): the viewer's
// sessions, never a v2 occurrence or controller lane.
type Playback interface {
	// SessionTx reads one v1 session the principal owns; sql.ErrNoRows when it
	// doesn't exist or belongs to someone else (indistinguishable on purpose).
	SessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (SessionFacts, error)
	// DeviceSessionTx reads a device's live session, if any, and whether the
	// device is still signed in (a signed-out host can't drive the group).
	DeviceSessionTx(ctx context.Context, tx *sql.Tx, device string) (SessionFacts, bool, error)
	// EndSessionTx ends a session the principal owns inside the caller's
	// transaction, with a reason; after runs once that transaction commits (it
	// fences the session's media).
	EndSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id, reason string) (after func(), err error)
}

// SessionFacts describe one v1 session for social playback.
type SessionFacts struct {
	ID, ItemID, DeviceID, State string
	Live                        bool
	Revision                    int64
	PositionUS                  int64
}
