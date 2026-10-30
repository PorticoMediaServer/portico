package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// The notification inbox.
//
// One monotonic counter orders every change to every inbox (see internal/notify).
// A client therefore holds a single number that simultaneously:
//
//   - fences a batch write (expectedRevision),
//   - resumes an event stream (Last-Event-ID), and
//   - parks a long poll (?revision=).
//
// Reading never writes. The previous build pruned retention on every inbox GET,
// which turned a read into a delete transaction and discarded its error;
// retention here runs only from the maintenance tick.

// NoticeAction and NoticeTarget are the wire shapes of an action. They are
// aliases of the producer-side types so the allowlist has exactly one authority.
type NoticeAction = notify.Action
type NoticeTarget = notify.NavigateTarget

// Notice is one notification as a client sees it. Scope never leaves the server.
type Notice struct {
	ID         string            `json:"id"`
	Sequence   int64             `json:"-"`
	Audience   string            `json:"audience"`
	Severity   string            `json:"severity"`
	Source     string            `json:"source"`
	Category   string            `json:"category"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Arguments  map[string]string `json:"arguments"`
	Actions    []NoticeAction    `json:"actions"`
	DedupeKey  string            `json:"dedupeKey"`
	Revision   int64             `json:"revision"`
	CreatedAt  apiwire.Instant   `json:"createdAt"`
	UpdatedAt  apiwire.Instant   `json:"updatedAt"`
	ExpiresAt  apiwire.Instant   `json:"expiresAt"`
	ReadAt     *apiwire.Instant  `json:"readAt"`
	ArchivedAt *apiwire.Instant  `json:"archivedAt"`
	Read       bool              `json:"read"`
	Archived   bool              `json:"archived"`
	Cursor     string            `json:"cursor"`
}

// NoticeCounts are computed inside the reading transaction, so the counts, the
// items and the revision in one response always describe the same instant.
type NoticeCounts struct {
	Unread   int `json:"unread"`
	Read     int `json:"read"`
	Archived int `json:"archived"`
	Total    int `json:"total"`
}

// NoticeInbox is the one-document read: vocabulary, counts, page and revision
// together, so a client needs no second request to render its inbox.
type NoticeInbox struct {
	Revision      int64           `json:"revision"`
	Audience      string          `json:"audience"`
	Audiences     []string        `json:"audiences"`
	State         string          `json:"state"`
	Counts        NoticeCounts    `json:"counts"`
	Items         []Notice        `json:"items"`
	NextCursor    string          `json:"nextCursor"`
	ObservedAt    apiwire.Instant `json:"observedAt"`
	RetentionDays int             `json:"retentionDays"`
}

// NoticeQuery is the list request. Audience and State are validated against the
// published vocabularies; an unknown value is a 400, never a silent default.
type NoticeQuery struct {
	Audience string
	State    string
	Cursor   string
	Limit    int
}

type NoticeOperation struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids,omitempty"`
}

// NoticeBatch is the single write surface for inbox state. Every state change —
// read, unread, archive, unarchive, read-all — travels through it, so a client
// changing five notices makes one request and gets one revision back.
type NoticeBatch struct {
	OperationID string `json:"operationId"`
	// ExpectedRevision fences the write against the inbox document the client
	// last read. Zero means "apply regardless", for a client that does not hold a
	// revision yet; any other value must match or the write is refused with the
	// current revision.
	ExpectedRevision int64             `json:"expectedRevision"`
	Audience         string            `json:"audience"`
	Operations       []NoticeOperation `json:"operations"`
}

type NoticeReceipt struct {
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Outcome string `json:"outcome"`
}

type NoticeBatchResult struct {
	Revision int64           `json:"revision"`
	Audience string          `json:"audience"`
	Applied  int             `json:"applied"`
	Receipts []NoticeReceipt `json:"receipts"`
	Counts   NoticeCounts    `json:"counts"`
}

// NoticeUnread is the cheap poll: two indexed counts and a revision, nothing else.
type NoticeUnread struct {
	Revision   int64           `json:"revision"`
	Audience   string          `json:"audience"`
	Counts     NoticeCounts    `json:"counts"`
	ObservedAt apiwire.Instant `json:"observedAt"`
}

// NoticeDelta is what an event stream or a long poll hands back: the records
// that changed since the client's revision, or a resync instruction when the
// gap is wider than one frame can carry.
type NoticeDelta struct {
	Revision int64        `json:"revision"`
	Since    int64        `json:"since"`
	Resync   bool         `json:"resync"`
	Items    []Notice     `json:"items"`
	Counts   NoticeCounts `json:"counts"`
}

// NoticeBroadcast is the owner's server-wide notice.
type NoticeBroadcast struct {
	OperationID   string         `json:"operationId"`
	Audience      string         `json:"audience"`
	Severity      string         `json:"severity"`
	DedupeKey     string         `json:"dedupeKey"`
	Title         string         `json:"title"`
	Body          string         `json:"body"`
	Actions       []NoticeAction `json:"actions"`
	ExpiresInDays int            `json:"expiresInDays"`
}

type NoticeBroadcastResult struct {
	Audience  string   `json:"audience"`
	Delivered int      `json:"delivered"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Revision  int64    `json:"revision"`
	DedupeKey string   `json:"dedupeKey"`
	IDs       []string `json:"ids"`
}

// NoticeVocabulary publishes every allowlist a client needs to render and
// validate notifications, so a client never has to hard-code the server's sets.
type NoticeVocabulary struct {
	Audiences  []string `json:"audiences"`
	Severities []string `json:"severities"`
	States     []string `json:"states"`
	Sources    []string `json:"sources"`
	Commands   []string `json:"commands"`
	Views      []string `json:"views"`
	MaxLimit   int      `json:"maxLimit"`
	Heartbeat  int      `json:"heartbeatMs"`
	MaxWait    int      `json:"maxWaitMs"`
}

const (
	noticeMaxLimit      = 100
	noticeHeartbeat     = 20
	noticeMaxWait       = 60
	noticeDeltaCap      = 50
	noticeBatchIDCap    = 200
	noticeBroadcastCap  = 50
	noticeStreamBackoff = 2 * time.Second
)

func NoticeVocabularyDocument() NoticeVocabulary {
	return NoticeVocabulary{
		Audiences:  append(append([]string{}, notify.Audiences...), "all"),
		Severities: append([]string{}, notify.Severities...),
		States:     []string{"unread", "read", "archived", "all"},
		Sources:    append([]string{}, notify.Sources...),
		Commands:   append([]string{}, notify.Commands...),
		Views:      append([]string{}, notify.Views...),
		MaxLimit:   noticeMaxLimit,
		Heartbeat:  noticeHeartbeat * 1000,
		MaxWait:    noticeMaxWait * 1000,
	}
}

// noticeWake is an in-process wake-up for parked readers. It is an optimisation
// only: every parked reader also re-reads the revision on a short timer, so a
// producer in another package that never calls Wake still wakes its readers
// within one backoff period rather than never.
type noticeHub struct {
	mu      sync.Mutex
	waiters map[chan struct{}]struct{}
}

var noticeWake = &noticeHub{waiters: map[chan struct{}]struct{}{}}

func (h *noticeHub) subscribe() (chan struct{}, func()) {
	c := make(chan struct{}, 1)
	h.mu.Lock()
	h.waiters[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.waiters, c)
		h.mu.Unlock()
	}
}

func (h *noticeHub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.waiters {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// WakeNotifications releases parked event streams and long polls immediately.
// Call it after committing a transaction that raised or changed a notification.
func WakeNotifications() { noticeWake.broadcast() }

// SubscribeNotifications parks a caller on the same wake channel. The event
// stream uses it so a connected viewer costs nothing while nothing is
// happening, instead of asking the database twice a second forever.
func SubscribeNotifications() (<-chan struct{}, func()) { return noticeWake.subscribe() }

// noticeAudiences resolves which inboxes this principal may read, and rejects a
// request for one it may not. The account-admin inbox is readable only by an
// owner or administrator of that same account, whose membership the caller's
// authorization has already checked is enabled: audience is enforced here, not
// by the caller and not by the client. An administrator's inbox only ever holds
// the server notices the owner routed to administrators.
func noticeAudiences(p identity.Principal, requested string) (string, []string, []string, error) {
	profile, account := ViewerKey(p), AccountKey(p)
	admin := p.Authority == "local" && (p.Role == "owner" || p.Role == "admin")
	switch requested {
	case "", "all":
		requested = "all"
		scopes := []string{profile}
		audiences := []string{notify.AudienceProfile}
		if admin {
			scopes = append(scopes, account)
			audiences = append(audiences, notify.AudienceAccountAdmin)
		}
		return requested, scopes, audiences, nil
	case notify.AudienceProfile:
		return requested, []string{profile}, []string{notify.AudienceProfile}, nil
	case notify.AudienceAccountAdmin:
		if !admin {
			return "", nil, nil, identity.ErrUnauthorized
		}
		return requested, []string{account}, []string{notify.AudienceAccountAdmin}, nil
	}
	return "", nil, nil, invalidFields("audience")
}

// noticeFilter builds the scope predicate. Scopes and audiences are positional
// pairs, so the profile inbox can never match an account-admin row.
func noticeFilter(scopes, audiences []string) (string, []any) {
	parts := make([]string, len(scopes))
	args := make([]any, 0, len(scopes)*2)
	for i := range scopes {
		parts[i] = "(scope=? AND audience=?)"
		args = append(args, scopes[i], audiences[i])
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func noticeStateClause(state string) (string, error) {
	switch state {
	case "", "all":
		return "", nil
	case "unread":
		return " AND archived_ms IS NULL AND read_ms IS NULL", nil
	case "read":
		return " AND archived_ms IS NULL AND read_ms IS NOT NULL", nil
	case "archived":
		return " AND archived_ms IS NOT NULL", nil
	}
	return "", invalidFields("state")
}

func noticeRevisionTx(tx *sql.Tx, scopes, audiences []string) (int64, error) {
	where, args := noticeFilter(scopes, audiences)
	var revision sql.NullInt64
	if e := tx.QueryRow(`SELECT max(revision) FROM notification_inbox WHERE `+where, args...).Scan(&revision); e != nil {
		return 0, e
	}
	if !revision.Valid {
		return 0, nil
	}
	return revision.Int64, nil
}

func noticeCountsTx(tx *sql.Tx, now int64, scopes, audiences []string) (NoticeCounts, error) {
	var out NoticeCounts
	where, args := noticeFilter(scopes, audiences)
	args = append([]any{now}, args...)
	e := tx.QueryRow(`SELECT
 COALESCE(sum(archived_ms IS NULL AND read_ms IS NULL),0),
 COALESCE(sum(archived_ms IS NULL AND read_ms IS NOT NULL),0),
 COALESCE(sum(archived_ms IS NOT NULL),0),
 count(*) FROM notification_records WHERE expires_ms>? AND `+where, args...).
		Scan(&out.Unread, &out.Read, &out.Archived, &out.Total)
	return out, e
}

// noticeIDsTx lists the identifiers a bulk update is about to move, so the
// revision stamp afterwards touches exactly those rows. It is bounded by the
// per-inbox cap, so it cannot grow without limit.
func noticeIDsTx(tx *sql.Tx, now int64, where string, args []any, clause string) ([]string, error) {
	rows, e := tx.Query(`SELECT id FROM notification_records WHERE expires_ms>? AND `+where+clause+` LIMIT 2000`, append([]any{now}, args...)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const noticeColumns = `id,sequence,audience,severity,source,category,title,body,arguments,actions,dedupe_key,revision,created_ms,updated_ms,read_ms,archived_ms,expires_ms`

func scanNotice(row interface{ Scan(...any) error }) (Notice, error) {
	var n Notice
	var arguments, actions string
	e := row.Scan(&n.ID, &n.Sequence, &n.Audience, &n.Severity, &n.Source, &n.Category, &n.Title, &n.Body,
		&arguments, &actions, &n.DedupeKey, &n.Revision, &n.CreatedAt, &n.UpdatedAt, &n.ReadAt, &n.ArchivedAt, &n.ExpiresAt)
	if e != nil {
		return n, e
	}
	n.Arguments = map[string]string{}
	n.Actions = []NoticeAction{}
	if e = json.Unmarshal([]byte(arguments), &n.Arguments); e != nil {
		return n, errors.New("invalid stored notification arguments")
	}
	if e = json.Unmarshal([]byte(actions), &n.Actions); e != nil {
		return n, errors.New("invalid stored notification actions")
	}
	n.Read, n.Archived = n.ReadAt != nil, n.ArchivedAt != nil
	n.Cursor = strconv.FormatInt(n.Sequence, 10)
	return n, nil
}

// Notices answers the inbox document: vocabulary-checked filters, one page of
// records, the counts and the revision, all from one transaction.
func (s *Store) Notices(ctx context.Context, p identity.Principal, auth Authorize, q NoticeQuery) (out NoticeInbox, e error) {
	out.Items = []Notice{}
	audience, scopes, audiences, e := noticeAudiences(p, q.Audience)
	if e != nil {
		return out, e
	}
	state := q.State
	if state == "" {
		state = "all"
	}
	clause, e := noticeStateClause(state)
	if e != nil {
		return out, e
	}
	before, e := Cursor(q.Cursor)
	if e != nil {
		return out, e
	}
	limit := q.Limit
	if limit == 0 {
		limit = 40
	}
	if limit < 1 || limit > noticeMaxLimit {
		return out, invalidFields("limit")
	}
	out.Audience, out.State = audience, state
	out.Audiences = []string{notify.AudienceProfile}
	if len(audiences) > 1 || audience == notify.AudienceAccountAdmin {
		out.Audiences = append([]string{}, notify.Audiences...)
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		out.ObservedAt, out.RetentionDays = apiwire.Instant(now), notify.RetentionDays(tx)
		where, args := noticeFilter(scopes, audiences)
		query := `SELECT ` + noticeColumns + ` FROM notification_records WHERE expires_ms>? AND sequence<? AND ` + where + clause + ` ORDER BY sequence DESC LIMIT ?`
		rows, err := tx.Query(query, append(append([]any{now, before}, args...), limit+1)...)
		if err != nil {
			return err
		}
		items := []Notice{}
		for rows.Next() {
			n, err := scanNotice(rows)
			if err != nil {
				rows.Close()
				return err
			}
			items = append(items, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(items) > limit {
			items = items[:limit]
			out.NextCursor = items[len(items)-1].Cursor
		}
		out.Items = items
		if out.Counts, err = noticeCountsTx(tx, now, scopes, audiences); err != nil {
			return err
		}
		out.Revision, err = noticeRevisionTx(tx, scopes, audiences)
		return err
	})
	return
}

// NoticeSummary is the poll endpoint: one revision and four counts. It is the
// cheapest read in the feature and is safe to call every few seconds.
func (s *Store) NoticeSummary(ctx context.Context, p identity.Principal, auth Authorize, requested string) (out NoticeUnread, e error) {
	audience, scopes, audiences, e := noticeAudiences(p, requested)
	if e != nil {
		return out, e
	}
	out.Audience = audience
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		out.ObservedAt = apiwire.Instant(now)
		var err error
		if out.Counts, err = noticeCountsTx(tx, now, scopes, audiences); err != nil {
			return err
		}
		out.Revision, err = noticeRevisionTx(tx, scopes, audiences)
		return err
	})
	return
}

// NoticeChanges answers an event-stream resume or a long poll. Records whose
// revision is above the client's are returned oldest-first so the client can
// advance its cursor frame by frame; a gap wider than one frame asks for a
// resync instead of silently dropping records.
func (s *Store) NoticeChanges(ctx context.Context, p identity.Principal, auth Authorize, requested string, since int64) (out NoticeDelta, e error) {
	out.Items, out.Since = []Notice{}, since
	_, scopes, audiences, e := noticeAudiences(p, requested)
	if e != nil {
		return out, e
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		where, args := noticeFilter(scopes, audiences)
		var err error
		if out.Revision, err = noticeRevisionTx(tx, scopes, audiences); err != nil {
			return err
		}
		if out.Counts, err = noticeCountsTx(tx, now, scopes, audiences); err != nil {
			return err
		}
		if since <= 0 || since >= out.Revision {
			return nil
		}
		query := `SELECT ` + noticeColumns + ` FROM notification_records WHERE expires_ms>? AND revision>? AND ` + where + ` ORDER BY revision ASC LIMIT ?`
		rows, err := tx.Query(query, append(append([]any{now, since}, args...), noticeDeltaCap+1)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			n, err := scanNotice(rows)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, n)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		// More changed than one frame can carry, or changes that left no row
		// behind (a prune). Tell the client to re-read rather than guess.
		if len(out.Items) > noticeDeltaCap {
			out.Items, out.Resync = []Notice{}, true
		}
		return nil
	})
	return
}

// NoticeWait parks until the inbox revision moves past the client's, the
// deadline passes, or the request is cancelled. It is the long-poll variant for
// clients without an event-stream reader.
func (s *Store) NoticeWait(ctx context.Context, p identity.Principal, auth Authorize, requested string, since int64, wait time.Duration) (NoticeDelta, error) {
	wake, release := noticeWake.subscribe()
	defer release()
	deadline := time.Now().Add(wait)
	for {
		out, e := s.NoticeChanges(ctx, p, auth, requested, since)
		if e != nil {
			return out, e
		}
		if since <= 0 || out.Revision > since || !time.Now().Before(deadline) {
			return out, nil
		}
		timer := time.NewTimer(noticeStreamBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func noticeAction(action string) bool {
	switch action {
	case "read", "unread", "archive", "unarchive", "read-all":
		return true
	}
	return false
}

// ApplyNotices is the batch write. It is fenced, idempotent and reports a
// per-identifier outcome, so a client that retries after a dropped response
// gets the original answer back rather than applying the change twice.
func (s *Store) ApplyNotices(ctx context.Context, p identity.Principal, auth Authorize, c NoticeBatch) (out NoticeBatchResult, e error) {
	audience, scopes, audiences, e := noticeAudiences(p, c.Audience)
	if e != nil {
		return out, e
	}
	if len(c.Operations) < 1 || len(c.Operations) > 8 {
		return out, invalidFields("operations")
	}
	total := 0
	for _, op := range c.Operations {
		if !noticeAction(op.Action) {
			return out, invalidFields("operations.action")
		}
		if op.Action == "read-all" {
			if len(op.IDs) != 0 {
				return out, invalidFields("operations.ids")
			}
			continue
		}
		if len(op.IDs) < 1 {
			return out, invalidFields("operations.ids")
		}
		for _, id := range op.IDs {
			if !validID(id) {
				return out, invalidFields("operations.ids")
			}
		}
		total += len(op.IDs)
	}
	if total > noticeBatchIDCap {
		return out, ErrCapacity
	}
	out.Audience = audience
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		receiptScope := "notices:" + ViewerKey(p)
		raw, digest, err := Receipt(tx, receiptScope, c.OperationID, c, now)
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		current, err := noticeRevisionTx(tx, scopes, audiences)
		if err != nil {
			return err
		}
		if c.ExpectedRevision != 0 && c.ExpectedRevision != current {
			return &ConflictError{current}
		}
		where, args := noticeFilter(scopes, audiences)
		receipts := []NoticeReceipt{}
		moved := []string{}
		// Every mutation is one UPDATE guarded by the same scope predicate, so an
		// identifier borrowed from another inbox simply does not match. The guard
		// also separates "already in that state" from "not yours / not there".
		mark := func(action, set string, setArgs []any, guard string, ids []string) error {
			for _, id := range ids {
				// Placeholder order: the SET clause's own values, then updated_ms,
				// then the identifier, the expiry bound and the scope predicate.
				parameters := append(append([]any{}, setArgs...), append([]any{now, id, now}, args...)...)
				result, err := tx.Exec(`UPDATE notification_records SET `+set+`,updated_ms=? WHERE id=? AND expires_ms>? AND `+where+guard, parameters...)
				if err != nil {
					return err
				}
				n, _ := result.RowsAffected()
				outcome := "applied"
				switch {
				case n > 0:
					moved = append(moved, id)
				default:
					outcome = "unchanged"
					var exists int
					if err = tx.QueryRow(`SELECT count(*) FROM notification_records WHERE id=? AND expires_ms>? AND `+where,
						append([]any{id, now}, args...)...).Scan(&exists); err != nil {
						return err
					}
					if exists == 0 {
						outcome = "not-found"
					}
				}
				receipts = append(receipts, NoticeReceipt{Action: action, ID: id, Outcome: outcome})
			}
			return nil
		}
		for _, op := range c.Operations {
			switch op.Action {
			case "read":
				err = mark("read", "read_ms=?", []any{now}, " AND read_ms IS NULL", op.IDs)
			case "unread":
				err = mark("unread", "read_ms=NULL", nil, " AND read_ms IS NOT NULL", op.IDs)
			case "archive":
				// Archiving also marks read: a notice filed away is one that was seen.
				err = mark("archive", "archived_ms=?,read_ms=COALESCE(read_ms,?)", []any{now, now}, " AND archived_ms IS NULL", op.IDs)
			case "unarchive":
				err = mark("unarchive", "archived_ms=NULL", nil, " AND archived_ms IS NOT NULL", op.IDs)
			case "read-all":
				pending, e := noticeIDsTx(tx, now, where, args, " AND read_ms IS NULL AND archived_ms IS NULL")
				if e != nil {
					return e
				}
				outcome := "unchanged"
				if len(pending) > 0 {
					outcome = "applied"
					if _, e = tx.Exec(`UPDATE notification_records SET read_ms=?,updated_ms=? WHERE read_ms IS NULL AND archived_ms IS NULL AND expires_ms>? AND `+where,
						append([]any{now, now, now}, args...)...); e != nil {
						return e
					}
					moved = append(moved, pending...)
				}
				receipts = append(receipts, NoticeReceipt{Action: "read-all", Outcome: outcome})
			}
			if err != nil {
				return err
			}
		}
		revision := current
		if len(moved) > 0 {
			for i := range scopes {
				if revision, err = notify.Bump(tx, now, scopes[i], audiences[i]); err != nil {
					return err
				}
			}
			// Records that moved carry the new revision, so a stream resuming from
			// an older revision replays exactly the notices whose state changed.
			for start := 0; start < len(moved); start += 100 {
				end := start + 100
				if end > len(moved) {
					end = len(moved)
				}
				chunk := moved[start:end]
				placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
				parameters := []any{revision}
				for _, id := range chunk {
					parameters = append(parameters, id)
				}
				if _, err = tx.Exec(`UPDATE notification_records SET revision=? WHERE id IN(`+placeholders+`)`, parameters...); err != nil {
					return err
				}
			}
		}
		applied := len(moved)
		out.Revision, out.Receipts, out.Applied = revision, receipts, applied
		if out.Counts, err = noticeCountsTx(tx, now, scopes, audiences); err != nil {
			return err
		}
		return SaveReceipt(tx, receiptScope, c.OperationID, digest, out, now)
	})
	if e == nil {
		WakeNotifications()
	}
	return
}

// BroadcastNotice is the owner's server-wide message. It is deduped like any
// other producer, so re-sending the same key edits the existing notice in every
// inbox instead of posting a second copy.
func (s *Store) BroadcastNotice(ctx context.Context, p identity.Principal, auth Authorize, c NoticeBroadcast) (out NoticeBroadcastResult, e error) {
	if p.Authority != "local" || p.Role != "owner" {
		return out, identity.ErrUnauthorized
	}
	if c.Audience != notify.AudienceProfile && c.Audience != notify.AudienceAccountAdmin {
		return out, invalidFields("audience")
	}
	if c.ExpiresInDays < 0 || c.ExpiresInDays > 180 {
		return out, invalidFields("expiresInDays")
	}
	if e = notify.ValidateActions(c.Actions); e != nil {
		return out, invalidFields("actions")
	}
	out.Audience, out.DedupeKey, out.IDs = c.Audience, c.DedupeKey, []string{}

	scope := "broadcast:" + AccountKey(p)
	for {
		finished := false
		e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
			now := s.now()
			raw, digest, err := Receipt(tx, scope, c.OperationID, c, now)
			if err != nil {
				return err
			}
			if raw != "" {
				finished = true
				return decodeDocument(raw, &out)
			}
			var prior, lastAccount, lastProfile, upperAccount, upperProfile, ids string
			var expiry int64
			err = tx.QueryRow(`SELECT digest,last_account,last_profile,upper_account,upper_profile,delivered,created,updated,revision,ids,expires_ms FROM notice_broadcast_work WHERE scope=? AND operation_id=?`, scope, c.OperationID).Scan(&prior, &lastAccount, &lastProfile, &upperAccount, &upperProfile, &out.Delivered, &out.Created, &out.Updated, &out.Revision, &ids, &expiry)
			if errors.Is(err, sql.ErrNoRows) {
				var pending int
				if err = tx.QueryRow(`SELECT (SELECT count(*) FROM notice_broadcast_work WHERE scope=?)+(SELECT count(*) FROM console_receipts WHERE scope=? AND expires_ms>?)`, scope, scope, now).Scan(&pending); err != nil {
					return err
				}
				if pending >= 2048 {
					return ErrCapacity
				}
				err = tx.QueryRow(`SELECT account_id,id FROM direct_profiles WHERE deleted=0 ORDER BY account_id DESC,id DESC LIMIT 1`).Scan(&upperAccount, &upperProfile)
				if errors.Is(err, sql.ErrNoRows) {
					err = nil
				}
				if err != nil {
					return err
				}
				expiry = now + int64(14*24*time.Hour/time.Millisecond)
				_, err = tx.Exec(`INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,expires_ms) VALUES(?,?,?,?,?,?)`, scope, c.OperationID, digest, upperAccount, upperProfile, expiry)
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				if prior != digest {
					return ErrIdempotencyKeyReused
				}
				if expiry <= now {
					return ErrExpired
				}
				if err = json.Unmarshal([]byte(ids), &out.IDs); err != nil {
					return err
				}
			}
			type target struct{ account, profile, scope string }
			targets := []target{}
			var rows *sql.Rows
			if c.Audience == notify.AudienceAccountAdmin {
				rows, err = tx.Query(`SELECT account_id,'' FROM direct_memberships WHERE role='owner' AND disabled=0 AND account_id>? ORDER BY account_id LIMIT ?`, lastAccount, noticeBroadcastCap)
			} else {
				rows, err = tx.Query(`SELECT account_id,id FROM direct_profiles WHERE deleted=0 AND (account_id,id)>(?,?) AND (account_id,id)<=(?,?) ORDER BY account_id,id LIMIT ?`, lastAccount, lastProfile, upperAccount, upperProfile, noticeBroadcastCap)
			}
			if err != nil {
				return err
			}
			for rows.Next() {
				var t target
				if err = rows.Scan(&t.account, &t.profile); err != nil {
					rows.Close()
					return err
				}
				t.scope = notify.ProfileScope("local", t.account, t.profile)
				if c.Audience == notify.AudienceAccountAdmin {
					t.scope = notify.AdminScope("local", t.account)
				}
				targets = append(targets, t)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			noticeExpiry := int64(0)
			// Keep one expiry across resumptions rather than extending each batch.
			if c.ExpiresInDays > 0 {
				noticeExpiry = expiry - int64(14*24*time.Hour/time.Millisecond) + days(c.ExpiresInDays)
			}
			draft := notify.Draft{Audience: c.Audience, Severity: c.Severity, Source: notify.SourceBroadcast, Category: "server.message", Title: c.Title, Body: c.Body, Actions: c.Actions, DedupeKey: c.DedupeKey, ExpiresAt: noticeExpiry}
			for _, target := range targets {
				draft.Scope = target.scope
				result, err := notify.Raise(tx, now, draft)
				if errors.Is(err, notify.ErrInvalid) {
					return ErrInvalid
				}
				if err != nil {
					return err
				}
				out.Delivered++
				if result.Created {
					out.Created++
				} else {
					out.Updated++
				}
				out.Revision = result.Revision
				if len(out.IDs) < 50 {
					out.IDs = append(out.IDs, result.ID)
				}
				lastAccount, lastProfile = target.account, target.profile
			}
			if len(targets) < noticeBroadcastCap {
				if err = Audit(tx, now, AccountKey(p), "notifications.broadcast", c.DedupeKey, int64(out.Delivered)); err != nil {
					return err
				}
				if err = SaveReceipt(tx, scope, c.OperationID, digest, out, now); err != nil {
					return err
				}
				if _, err = tx.Exec(`DELETE FROM notice_broadcast_work WHERE scope=? AND operation_id=?`, scope, c.OperationID); err != nil {
					return err
				}
				finished = true
				return nil
			}
			encoded, err := json.Marshal(out.IDs)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE notice_broadcast_work SET last_account=?,last_profile=?,delivered=?,created=?,updated=?,revision=?,ids=? WHERE scope=? AND operation_id=?`, lastAccount, lastProfile, out.Delivered, out.Created, out.Updated, out.Revision, string(encoded), scope, c.OperationID)
			return err
		})
		if e != nil {
			return
		}
		WakeNotifications()
		if finished {
			return
		}
		// Each checkpoint releases the writer and rechecks the owner's authority.
		if e = ctx.Err(); e != nil {
			return
		}
	}
}

// pruneNotifications enforces retention. It runs from the maintenance tick, not
// from a read, and bumps the revision of every inbox it actually changed so a
// parked client learns its counts moved.
// pruneNotifications removes one batch of expired notification records and
// bumps the revision of each inbox it touched. It reports how many records it
// removed; the caller repeats it until a batch comes back short.
func (s *Store) pruneNotifications(tx *sql.Tx, now int64, retentionDays int) (int, error) {
	if _, err := tx.Exec(`DELETE FROM notice_broadcast_work WHERE (scope,operation_id) IN(SELECT scope,operation_id FROM notice_broadcast_work WHERE expires_ms<=? ORDER BY expires_ms LIMIT 100)`, now); err != nil {
		return 0, err
	}

	cutoff := now - days(retentionDays)
	rows, e := tx.Query(`SELECT rowid,scope,audience FROM notification_records WHERE expires_ms<=? OR created_ms<? LIMIT ?`, now, cutoff, retentionBatch)
	if e != nil {
		return 0, e
	}
	type inbox struct{ scope, audience string }
	affected := []inbox{}
	seen := map[inbox]bool{}
	ids := []any{}
	for rows.Next() {
		var id int64
		var v inbox
		if e = rows.Scan(&id, &v.scope, &v.audience); e != nil {
			rows.Close()
			return 0, e
		}
		ids = append(ids, id)
		if !seen[v] {
			seen[v] = true
			affected = append(affected, v)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(ids) == 0 {
		return 0, e
	}
	if _, e = tx.Exec(`DELETE FROM notification_records WHERE rowid IN(`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, ids...); e != nil {
		return 0, e
	}
	for _, v := range affected {
		if _, e = notify.Bump(tx, now, v.scope, v.audience); e != nil {
			return 0, e
		}
	}
	// An inbox with no records left keeps its revision row: it is a few bytes and
	// it is what lets a client tell "nothing yet" from "everything expired".
	return len(ids), nil
}

// NotificationSettings folds the notification retention control onto the single
// existing registry field. notifications.retentionDays in the documented API is
// notificationDays in the settings registry — one value, one write surface, read
// here alongside the storage thresholds this feature owns.
type NotificationSettings struct {
	Revision                int64  `json:"revision"`
	RetentionDays           int    `json:"retentionDays"`
	RetentionSettingsField  string `json:"retentionSettingsField"`
	StorageWarningPercent   int    `json:"storageWarningPercent"`
	StorageCriticalPercent  int    `json:"storageCriticalPercent"`
	CertificateWarningDays  int    `json:"certificateWarningDays"`
	MaintenanceIntervalMins int    `json:"maintenanceIntervalMinutes"`
	// NoticeAudience is who receives each server notice (notify.NoticeKinds):
	// "both" (the owner and the administrators), "owner" or "off". Every kind
	// is always present in a read.
	NoticeAudience map[string]string `json:"noticeAudience"`
}

// NotificationSettingsChange is fenced by the document revision rather than by
// an idempotency key: it is a complete replacement of three bounded values, so a
// replayed write either matches the fence and is the same write, or is refused.
type NotificationSettingsChange struct {
	ExpectedRevision       int64 `json:"expectedRevision"`
	StorageWarningPercent  int   `json:"storageWarningPercent"`
	StorageCriticalPercent int   `json:"storageCriticalPercent"`
	CertificateWarningDays int   `json:"certificateWarningDays"`
	// NoticeAudience replaces the routing for the kinds it names; absent keeps
	// what is saved.
	NoticeAudience map[string]string `json:"noticeAudience,omitempty"`
}

func defaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		Revision: 1, RetentionSettingsField: "notificationDays",
		StorageWarningPercent: 10, StorageCriticalPercent: 5,
		CertificateWarningDays: 14, MaintenanceIntervalMins: 15,
		NoticeAudience: defaultNoticeAudience(),
	}
}

func defaultNoticeAudience() map[string]string {
	out := map[string]string{}
	for _, kind := range notify.NoticeKinds {
		out[kind] = notify.NoticeOwnerAndAdmins
	}
	return out
}

// normalizeNoticeAudience fills every kind a stored document lacks, or holds
// an unknown value for, with the shipped default and drops anything else.
func normalizeNoticeAudience(stored map[string]string) map[string]string {
	out := defaultNoticeAudience()
	for kind, value := range stored {
		if _, known := out[kind]; known && slices.Contains(notify.NoticeAudiences, value) {
			out[kind] = value
		}
	}
	return out
}

func readNotificationSettings(tx *sql.Tx) (NotificationSettings, error) {
	out := defaultNotificationSettings()
	var body string
	var revision int64
	e := tx.QueryRow(`SELECT revision,body FROM console_documents WHERE scope='notifications'`).Scan(&revision, &body)
	if e == nil {
		if e = decodeDocument(body, &out); e != nil {
			return out, e
		}
		out.Revision = revision
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	out.RetentionDays, out.RetentionSettingsField = notify.RetentionDays(tx), "notificationDays"
	out.NoticeAudience = normalizeNoticeAudience(out.NoticeAudience)
	return out, nil
}

func (s *Store) NotificationSettings(ctx context.Context, auth Authorize) (out NotificationSettings, e error) {
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		var err error
		out, err = readNotificationSettings(tx)
		return err
	})
	return
}

func (s *Store) ApplyNotificationSettings(ctx context.Context, p identity.Principal, auth Authorize, c NotificationSettingsChange) (out NotificationSettings, e error) {
	if c.StorageWarningPercent < 1 || c.StorageWarningPercent > 50 {
		return out, invalidFields("storageWarningPercent")
	}
	if c.StorageCriticalPercent < 1 || c.StorageCriticalPercent >= c.StorageWarningPercent {
		return out, invalidFields("storageCriticalPercent")
	}
	if c.CertificateWarningDays < 1 || c.CertificateWarningDays > 90 {
		return out, invalidFields("certificateWarningDays")
	}
	for kind, value := range c.NoticeAudience {
		if !slices.Contains(notify.NoticeKinds, kind) || !slices.Contains(notify.NoticeAudiences, value) {
			return out, invalidFields("noticeAudience")
		}
	}
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		receipt, err := usermutation.CheckContext(ctx, tx, now)
		if err != nil {
			return err
		}
		if receipt != nil {
			out, err = readNotificationSettings(tx)
			return err
		}
		current, err := readNotificationSettings(tx)
		if err != nil {
			return err
		}
		if c.ExpectedRevision != current.Revision {
			return &ConflictError{current.Revision}
		}
		current.StorageWarningPercent = c.StorageWarningPercent
		current.StorageCriticalPercent = c.StorageCriticalPercent
		current.CertificateWarningDays = c.CertificateWarningDays
		before := current.NoticeAudience
		current.NoticeAudience = map[string]string{}
		for kind, value := range before {
			current.NoticeAudience[kind] = value
			if next, ok := c.NoticeAudience[kind]; ok {
				current.NoticeAudience[kind] = next
			}
		}
		if err = withdrawNarrowedNotices(tx, now, before, current.NoticeAudience); err != nil {
			return err
		}
		current.Revision++
		body, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO console_documents VALUES('notifications',?,?,?)
 ON CONFLICT(scope) DO UPDATE SET revision=excluded.revision,body=excluded.body,updated_ms=excluded.updated_ms`,
			current.Revision, string(body), now); err != nil {
			return err
		}
		if err = Audit(tx, now, AccountKey(p), "notifications.settings", "notifications", current.Revision); err != nil {
			return err
		}
		out = current
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: "notifications", Revision: out.Revision}, now)
	})
	return
}

// withdrawNarrowedNotices removes the notices an owner's new routing no longer
// sends to an inbox, so an administrator taken off a kind of notice stops
// seeing the copies already delivered rather than keeping them until expiry.
func withdrawNarrowedNotices(tx *sql.Tx, now int64, before, after map[string]string) error {
	// Only owner and administrator inboxes can hold these notices, and only the
	// ones the new choice leaves out lose anything: each is walked through its
	// own (scope, audience) index, bounded by the per-inbox cap.
	reachable, e := notify.AudienceScopes(tx, notify.NoticeOwnerAndAdmins)
	if e != nil {
		return e
	}
	for _, kind := range notify.NoticeKinds {
		if before[kind] == after[kind] {
			continue
		}
		allowed, e := notify.AudienceScopes(tx, after[kind])
		if e != nil {
			return e
		}
		predicate, kindArgs := notify.NoticeRecords(kind)
		for _, scope := range reachable {
			if slices.Contains(allowed, scope) {
				continue
			}
			args := append([]any{scope, notify.AudienceAccountAdmin}, kindArgs...)
			result, e := tx.Exec(`DELETE FROM notification_records WHERE scope=? AND audience=? AND `+predicate, args...)
			if e != nil {
				return e
			}
			if n, _ := result.RowsAffected(); n > 0 {
				if _, e = notify.Bump(tx, now, scope, notify.AudienceAccountAdmin); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

// NotificationMaintenance is the periodic producer tick: the conditions that are
// true continuously rather than at a moment. It raises (and re-raises) storage
// warnings from the measured free space on the state volume, and clears the
// notice when the volume recovers.
//
// freeBytes/totalBytes come from the caller because measuring a volume is a
// platform call, not a database one; pass 0,0 when the platform cannot answer
// and the storage check is skipped rather than reported as full.
func (s *Store) NotificationMaintenance(ctx context.Context, freeBytes, totalBytes int64) error {
	gated, e := dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.now()
	settings, e := readNotificationSettings(tx)
	if e != nil {
		return e
	}
	changed := false
	if totalBytes > 0 && freeBytes >= 0 {
		percent := int(freeBytes * 100 / totalBytes)
		switch {
		case percent <= settings.StorageCriticalPercent:
			if _, e = notify.NotifyStorageWarning(tx, now, notify.SeverityCritical, percent, freeBytes); e != nil {
				return e
			}
			changed = true
		case percent <= settings.StorageWarningPercent:
			if _, e = notify.NotifyStorageWarning(tx, now, notify.SeverityWarning, percent, freeBytes); e != nil {
				return e
			}
			changed = true
		default:
			// Recovered: retire the standing notice instead of leaving a stale alarm.
			result, e := tx.Exec(`DELETE FROM notification_records WHERE source=? AND dedupe_key='volume:state'`, notify.SourceStorage)
			if e != nil {
				return e
			}
			if n, _ := result.RowsAffected(); n > 0 {
				scopes, e := notify.AudienceScopes(tx, notify.NoticeOwnerAndAdmins)
				if e != nil {
					return e
				}
				for _, scope := range scopes {
					if _, e = notify.Bump(tx, now, scope, notify.AudienceAccountAdmin); e != nil {
						return e
					}
				}
				changed = true
			}
		}
	}
	certificates, e := noticeCertificateExpiry(tx, now, settings.CertificateWarningDays)
	if e != nil {
		return e
	}
	for scope, remaining := range certificates {
		if _, e = notify.NotifyCertificateExpiry(tx, now, scope, remaining); e != nil {
			return e
		}
		changed = true
	}
	if _, e = s.pruneNotifications(tx, now, settings.RetentionDays); e != nil {
		return e
	}
	if e = gated.Commit(); e != nil {
		return e
	}
	if changed {
		WakeNotifications()
	}
	return nil
}

// noticeCertificateExpiry reads the networking certificate table directly rather
// than asking the networking package, so the maintenance tick stays a pure
// database pass with no import back into a service that imports this one. A
// server that has never configured TLS has no such table and simply reports
// nothing, which is why the lookup is guarded instead of assumed.
func noticeCertificateExpiry(tx *sql.Tx, now int64, warningDays int) (map[string]int, error) {
	out := map[string]int{}
	var present int
	if e := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='networking_certificates'`).Scan(&present); e != nil {
		return out, e
	}
	if present == 0 {
		return out, nil
	}
	horizon := now + days(warningDays)
	rows, e := tx.Query(`SELECT scope_id,min(not_after) FROM networking_certificates
 WHERE state='issued' AND not_after>0 GROUP BY scope_id HAVING min(not_after)<=?`, horizon)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var notAfter int64
		if e = rows.Scan(&scope, &notAfter); e != nil {
			return out, e
		}
		remaining := int((notAfter - now) / (24 * 60 * 60 * 1000))
		if notAfter <= now {
			remaining = 0
		}
		out[scope] = remaining
	}
	return out, rows.Err()
}
