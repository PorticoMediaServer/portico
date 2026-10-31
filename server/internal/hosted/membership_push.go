package hosted

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// This server decides who its members are; Hosted only keeps an index of
// "which servers is this Portico Account a member of" so a person's devices can
// find their servers (Spec — Hosted at Scale). The index is kept current by
// pushing changes, never by Hosted asking: the journal triggers of migration
// 0120 record every change to a linked account's standing in the writer's own
// transaction, and this pushes whatever is unacknowledged.
//
// A push carries the current standing of each account the journal names, so
// any number of journal rows for one account coalesce, together with the
// account's own identity assertion for this server: Hosted lists a server for
// an account only with that consent (INT M6). Hosted applies a push only on
// top of the sequence it holds ("after"); anything else it answers with
// applied=false, and the next push is the full list, in pages. The check-in
// carries a digest of the full list as well, so a server restored from a
// backup, or a Hosted restored from one, converges without a heartbeat.

// MemberStanding is one Portico Account's standing here, as Hosted indexes it.
type MembershipEnding struct {
	Sequence  int64     `json:"sequence"`
	Cause     string    `json:"cause"`
	EndedAt   time.Time `json:"endedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type MemberStanding struct {
	MembershipID string            `json:"membershipId,omitempty"`
	Ending       *MembershipEnding `json:"ending,omitempty"`
	AccountID    string            `json:"accountId"`
	Role         string            `json:"role"`
	Active       bool              `json:"active"`
	Consent      json.RawMessage   `json:"consent,omitempty"`
}

// MemberPush is POST /v1/servers/{id}/members. A full list is sent in pages:
// Page counts from 0 and More says another follows; Hosted records Through
// only with the last page.
type MemberPush struct {
	After   int64            `json:"after"`
	Through int64            `json:"through"`
	Full    bool             `json:"full"`
	Page    int              `json:"page,omitempty"`
	More    bool             `json:"more,omitempty"`
	Members []MemberStanding `json:"members"`
}

// MemberPushResult is Hosted's answer: the sequence it now holds, whether this
// push was applied (false: a gap, send the full list), and the accounts it
// would not list for want of their consent.
type MemberPushResult struct {
	Sequence int64    `json:"sequence"`
	Applied  bool     `json:"applied"`
	Skipped  []string `json:"skipped,omitempty"`
	// Custodian is the Hosted account holding custody of this server's
	// claim, "" while custody is released (INT M10).
	Custodian string `json:"custodian"`
}

// Every push stays well under Hosted's 1 MiB body limit (INT M14): a delta
// names at most memberPushRows journal rows and memberPushPage accounts, a
// full list goes in pages of at most memberPushPage accounts, and either stops
// early at memberPushBytes of members (each carries its consent, about a
// kilobyte and at most 8 KiB). The rest go in the next pass. After
// memberPushFallback failed pushes in a row a delta gives way to the full
// list, so a push Hosted keeps refusing cannot wedge the index.
const (
	memberPushRows     = 2000
	memberPushPage     = 200
	memberPushBytes    = 640 << 10
	memberPushFallback = 4
)

// standingSize is what one member adds to a push body, near enough.
func standingSize(m MemberStanding) int {
	return len(m.AccountID) + len(m.Role) + len(m.Consent) + 512
}

// MembershipChanged asks the control loop to push now instead of at its next
// deadline. The change itself is already journalled.
func (s *Service) MembershipChanged() { s.wakeControl() }

type memberSync struct {
	acked, head    int64
	full           bool
	nextAt         time.Time
	attempts       int
	journalPending bool
	fullThrough    int64
	fullPage       int
	fullCursor     string
}

func (s *Service) memberSyncState(ctx context.Context) (memberSync, error) {
	var q memberSync
	var next int64
	e := s.db.QueryRowContext(ctx, `SELECT acked,full_pending,next_at,attempts,full_through,full_page,full_cursor,(SELECT COALESCE(MAX(sequence),0) FROM hosted_membership_journal) FROM hosted_membership_sync WHERE singleton=1`).Scan(&q.acked, &q.full, &next, &q.attempts, &q.fullThrough, &q.fullPage, &q.fullCursor, &q.head)
	if e != nil {
		return q, e
	}
	if next > 0 {
		q.nextAt = time.UnixMilli(next)
	}
	if q.head < q.acked {
		q.head = q.acked
	}
	q.journalPending = q.full || q.head > q.acked
	return q, nil
}

const standingColumns = `l.hosted_account_id,m.role,m.disabled=0,l.consent,m.incarnation`

func scanStandings(rows *sql.Rows) ([]MemberStanding, error) {
	defer rows.Close()
	var out []MemberStanding
	for rows.Next() {
		var m MemberStanding
		var consent string
		if e := rows.Scan(&m.AccountID, &m.Role, &m.Active, &consent, &m.MembershipID); e != nil {
			return nil, e
		}
		if m.Active && consent != "" && json.Valid([]byte(consent)) {
			m.Consent = json.RawMessage(consent)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// namedStandings reads the current standing of the given Hosted accounts; an
// account no longer linked here is reported inactive.
func namedStandings(ctx context.Context, db *sql.DB, accounts []string) ([]MemberStanding, error) {
	if len(accounts) == 0 {
		return []MemberStanding{}, nil
	}
	args := make([]any, len(accounts))
	for i, a := range accounts {
		args[i] = a
	}
	rows, e := db.QueryContext(ctx, `SELECT `+standingColumns+` FROM account_portico_links l JOIN direct_memberships m ON m.account_id=l.account_id WHERE l.hosted_account_id IN(?`+strings.Repeat(",?", len(accounts)-1)+`)`, args...)
	if e != nil {
		return nil, e
	}
	found, e := scanStandings(rows)
	if e != nil {
		return nil, e
	}
	current := map[string]MemberStanding{}
	for _, m := range found {
		current[m.AccountID] = m
	}
	out := make([]MemberStanding, 0, len(accounts))
	for _, id := range accounts {
		m, ok := current[id]
		if !ok {
			// Unlinked or deleted: gone from this server.
			m = MemberStanding{AccountID: id}
		}
		out = append(out, m)
	}
	return out, nil
}

// MemberDigest is the digest of the full active list that the check-in sends,
// "accountId:role" lines sorted and newline-joined, SHA-256, hex. Hosted
// computes the same over its index rows for this server.
func MemberDigest(members []MemberStanding) string {
	lines := make([]string, 0, len(members))
	for _, m := range members {
		if m.Active {
			lines = append(lines, m.AccountID+":"+m.Role)
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func (s *Service) buildMemberPush(ctx context.Context, q memberSync) (MemberPush, error) {
	if q.full {
		through := q.head
		if q.fullPage > 0 {
			through = q.fullThrough
		}
		rows, e := s.db.QueryContext(ctx, `SELECT hosted_account_id FROM (
 SELECT l.hosted_account_id FROM account_portico_links l JOIN direct_memberships m ON m.account_id=l.account_id WHERE m.disabled=0
 UNION SELECT hosted_account_id FROM hosted_membership_endings WHERE sequence<=? AND expires_ms>?
 ) WHERE hosted_account_id>? ORDER BY hosted_account_id LIMIT ?`, through, time.Now().UnixMilli(), q.fullCursor, memberPushPage+1)
		if e != nil {
			return MemberPush{}, e
		}
		var accounts []string
		for rows.Next() {
			var account string
			if e = rows.Scan(&account); e != nil {
				rows.Close()
				return MemberPush{}, e
			}
			accounts = append(accounts, account)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return MemberPush{}, e
		}
		members, e := namedStandings(ctx, s.db, accounts)
		if e != nil {
			return MemberPush{}, e
		}
		if e = attachMembershipEndings(ctx, s.db, members, through); e != nil {
			return MemberPush{}, e
		}
		keep, size := 0, 0
		for keep < len(members) && keep < memberPushPage {
			size += standingSize(members[keep])
			if keep > 0 && size > memberPushBytes {
				break
			}
			keep++
		}
		more := len(members) > keep
		return MemberPush{Through: through, Full: true, Page: q.fullPage, More: more, Members: members[:keep]}, nil
	}
	rows, e := s.db.QueryContext(ctx, `SELECT sequence,hosted_account_id FROM hosted_membership_journal WHERE sequence>? ORDER BY sequence LIMIT ?`, q.acked, memberPushRows)
	if e != nil {
		return MemberPush{}, e
	}
	defer rows.Close()
	type journalRow struct {
		sequence int64
		account  string
	}
	var journal []journalRow
	for rows.Next() {
		var r journalRow
		if e = rows.Scan(&r.sequence, &r.account); e != nil {
			return MemberPush{}, e
		}
		journal = append(journal, r)
	}
	if e = rows.Err(); e != nil {
		return MemberPush{}, e
	}
	rows.Close()
	var accounts []string
	seen := map[string]bool{}
	for _, r := range journal {
		if !seen[r.account] {
			seen[r.account] = true
			accounts = append(accounts, r.account)
		}
	}
	standings, e := namedStandings(ctx, s.db, accounts)
	if e != nil {
		return MemberPush{}, e
	}
	sizes := map[string]int{}
	for _, m := range standings {
		sizes[m.AccountID] = standingSize(m)
	}
	// Take journal rows in order while the accounts they name fit; the push
	// covers exactly those rows (through), so nothing is acknowledged unsent.
	through := q.acked
	included := map[string]bool{}
	size := 0
	for _, r := range journal {
		if !included[r.account] {
			if len(included) == memberPushPage || (len(included) > 0 && size+sizes[r.account] > memberPushBytes) {
				break
			}
			included[r.account] = true
			size += sizes[r.account]
		}
		through = r.sequence
	}
	members := make([]MemberStanding, 0, len(included))
	for _, m := range standings {
		if included[m.AccountID] {
			members = append(members, m)
		}
	}
	if e = attachMembershipEndings(ctx, s.db, members, through); e != nil {
		return MemberPush{}, e
	}
	return MemberPush{After: q.acked, Through: through, Members: members}, nil
}

// memberPushPlan bounds recording Portico Services' answer to one member push:
// up to memberPushPage accounts' index flags.
var memberPushPlan = dbwork.WritePlan{Name: "member-push", Rows: 2048, Pages: 128, VMOps: 500_000, Why: "Portico Services acknowledges a page of up to 200 members at once, and the answer is recorded atomically"}

// pushMembers sends one push (or one page of a full list) and records
// Hosted's answer. It is called from the control step, under the
// installed-claim runner.
func (s *Service) pushMembers(ctx context.Context, v networking.Intent, q memberSync) error {
	push, e := s.buildMemberPush(ctx, q)
	if e != nil {
		return e
	}
	if push.Members == nil {
		push.Members = []MemberStanding{}
	}
	var result MemberPushResult
	if e = s.current.transport.CallServer(ctx, v, networking.PushMembers, push, &result); e != nil {
		return e
	}
	return s.current.store.WithInstalledTransaction(dbwork.WithPlan(ctx, memberPushPlan), v, func(ctx context.Context, tx *sql.Tx) error {
		if !result.Applied {
			// Hosted holds something else: replace it with the full list.
			_, e := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET full_pending=1,full_page=0,full_cursor='',full_through=0,attempts=0,next_at=0 WHERE singleton=1`)
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET custodian=? WHERE singleton=1`, result.Custodian); e != nil {
			return e
		}
		for _, account := range result.Skipped {
			if _, e := tx.ExecContext(ctx, `UPDATE account_portico_links SET unindexed=1 WHERE hosted_account_id=?`, account); e != nil {
				return e
			}
		}
		for _, m := range push.Members {
			if !containsString(result.Skipped, m.AccountID) {
				if _, e := tx.ExecContext(ctx, `UPDATE account_portico_links SET unindexed=0 WHERE hosted_account_id=? AND unindexed=1`, m.AccountID); e != nil {
					return e
				}
			}
		}
		if push.Full && push.More {
			_, e := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET full_page=?,full_cursor=?,full_through=?,attempts=0,next_at=0 WHERE singleton=1`, push.Page+1, push.Members[len(push.Members)-1].AccountID, push.Through)
			return e
		}
		if result.Sequence != push.Through {
			return networking.ErrInvalid
		}
		// A full push covers everything journalled up to its head; rows written
		// since (a concurrent change) stay and go in the next delta.
		if _, e := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET acked=max(acked,?),full_pending=CASE WHEN ? THEN 0 ELSE full_pending END,full_page=0,full_cursor='',full_through=0,attempts=0,next_at=0 WHERE singleton=1`, push.Through, push.Full); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM hosted_membership_journal WHERE sequence<=?`, push.Through)
		return e
	})
}

// ownerAdmitted reports whether this server has any account: false only while a
// first-run setup's owner is still to be admitted (setupflow phases before completed).
func (s *Service) ownerAdmitted(ctx context.Context) (bool, error) {
	var any bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts)`).Scan(&any)
	return any, err
}

func containsString(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

// memberStep pushes if anything is unacknowledged and its retry deadline has
// passed. It reports when it next needs to run (zero: nothing pending).
func (s *Service) memberStep(ctx context.Context, v networking.Intent) (time.Time, error) {
	q, e := s.memberSyncState(ctx)
	if e != nil || !q.journalPending {
		return time.Time{}, e
	}
	// A Portico Account setup whose claim installed but whose owner the Return hasn't
	// admitted yet has no accounts. Its full list would be empty, and Portico Services
	// would drop the claim's owner from its index and refuse the Return. Nothing is
	// pushed until the owner is admitted: admission journals the owner and wakes this
	// step (MembershipChanged), so the first push is the full list naming them. Only a
	// server with no accounts at all waits; owner moves and switches always have some.
	if admitted, err := s.ownerAdmitted(ctx); err != nil || !admitted {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	if now.Before(q.nextAt) {
		return q.nextAt, nil
	}
	if e = s.pushMembers(ctx, v, q); e == nil {
		// Another pass picks up a gap answer, the next page or rows that
		// arrived meanwhile.
		if again, err := s.memberSyncState(ctx); err == nil && again.journalPending {
			return now, nil
		}
		return time.Time{}, nil
	}
	// The same backoff as every other Hosted call: exponential, capped, with
	// proportional jitter and Retry-After as a floor. The journal keeps
	// collecting meanwhile, so the eventual push carries everything.
	attempts := min(q.attempts+1, 8)
	next := networking.RetryAt(now, 5*time.Second, attempts, 8, networking.ControlRetryAt(e))
	// A delta Hosted keeps refusing gives way to the full list (INT M14).
	fallback := !q.full && attempts >= memberPushFallback
	if err := s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET attempts=?,next_at=?,full_pending=CASE WHEN ? THEN 1 ELSE full_pending END WHERE singleton=1`, attempts, next.UnixMilli(), fallback)
		return err
	}); err != nil {
		return next, errors.Join(e, err)
	}
	return next, e
}

// currentMemberDigest is the digest of this server's active list as Hosted
// should hold it: without the accounts it declined for want of consent.
func (s *Service) currentMemberDigest(ctx context.Context) (string, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+standingColumns+` FROM account_portico_links l JOIN direct_memberships m ON m.account_id=l.account_id WHERE m.disabled=0 AND l.unindexed=0`)
	if e != nil {
		return "", e
	}
	members, e := scanStandings(rows)
	if e != nil {
		return "", e
	}
	return MemberDigest(members), nil
}

// requestFullMemberPush is what a check-in whose digest disagrees does.
func (s *Service) requestFullMemberPush(ctx context.Context, v networking.Intent) error {
	return s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE hosted_membership_sync SET full_pending=1,full_page=0,full_cursor='',full_through=0,next_at=0,attempts=0 WHERE singleton=1`)
		return e
	})
}

// Departure is a check-in's note that a Portico Account removed this server
// from its list at Hosted, and when.
type Departure struct {
	AccountID string    `json:"accountId"`
	LeftAt    time.Time `json:"leftAt"`
}

// consentIssuedAt is when the account's stored consent (its latest assertion
// for this server) was issued; zero if there is none to read.
func consentIssuedAt(consent string) time.Time {
	var env Signed
	if consent == "" || json.Unmarshal([]byte(consent), &env) != nil {
		return time.Time{}
	}
	raw, e := base64.RawURLEncoding.DecodeString(env.Payload)
	if e != nil {
		return time.Time{}
	}
	var c IdentityClaims
	if json.Unmarshal(raw, &c) != nil {
		return time.Time{}
	}
	at, e := time.Parse(time.RFC3339Nano, c.IssuedAt)
	if e != nil {
		return time.Time{}
	}
	return at
}

// applyDepartures is a check-in's list of Portico Accounts that removed this
// server from their list at Hosted: each leaves, exactly as if the owner had
// disabled it (sessions end at once). The journal then pushes the result, which
// is what Hosted waits for. An account whose consent here is newer than its
// departure joined again since (a fresh invitation, a sign-in): it stays, and
// is pushed again so Hosted lists it and forgets the departure.
func (s *Service) applyDepartures(ctx context.Context, v networking.Intent, departures []Departure) error {
	if len(departures) == 0 {
		return nil
	}
	return s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
		for _, d := range departures {
			hosted := d.AccountID
			var account, role, consent string
			e := tx.QueryRowContext(ctx, `SELECT l.account_id,m.role,l.consent FROM account_portico_links l JOIN direct_memberships m ON m.account_id=l.account_id WHERE l.hosted_account_id=?`, hosted).Scan(&account, &role, &consent)
			if errors.Is(e, sql.ErrNoRows) {
				// Already gone here; the next push says so.
				if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?)`, hosted); e != nil {
					return e
				}
				continue
			}
			if e != nil {
				return e
			}
			if role == identity.TierOwner {
				// The owner leaves by transferring ownership or retiring the
				// server; Hosted refuses the request, so this is never reached.
				continue
			}
			if at := consentIssuedAt(consent); !d.LeftAt.IsZero() && at.After(d.LeftAt) {
				if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?)`, hosted); e != nil {
					return e
				}
				continue
			}
			if e = s.identity.RevokeMemberSessionsTx(ctx, tx, account); e != nil {
				return e
			}
			// Recorded as the person leaving, at the time they left (not when this
			// server heard), so an invitation sent after that still lets them back.
			left := d.LeftAt
			if left.IsZero() {
				left = time.Now()
			}
			changed, e := tx.ExecContext(ctx, `UPDATE direct_memberships SET disabled=1,disabled_ms=?,disabled_reason='left',revision=revision+1 WHERE account_id=? AND disabled=0`, left.UnixMilli(), account)
			if e != nil {
				return e
			}
			n, e := changed.RowsAffected()
			if e != nil {
				return e
			}
			// Replayed departures cannot advance the account's epoch again.
			if n != 0 {
				if _, e = tx.ExecContext(ctx, `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, account); e != nil {
					return e
				}
			}
			// An already-disabled member makes no journal row by trigger.
			if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?)`, hosted); e != nil {
				return e
			}
		}
		return nil
	})
}

// linkClaimOwnerTx links the Portico Account that installed this claim to the
// local owner who consented to it, so the owner signs in with that account like
// any other member. An owner already linked (to this or an earlier claim's
// account) stays as it is. It runs inside the credential installation transaction
// so acknowledgment never precedes admission readiness.
func (s *Service) linkClaimOwnerTx(ctx context.Context, tx *sql.Tx, v networking.Intent) error {
	if tx == nil || v.ServerID != s.identity.ID() || v.Stage != networking.Installed {
		return networking.ErrStale
	}
	if v.Purpose == "switch" && v.CurrentOwnerAccountID == "" {
		err := s.identity.InstallOwnerClaimTx(ctx, tx, v.OperationID)
		switch {
		case errors.Is(err, identity.ErrMoveCollision):
			return networking.ErrOwnerCollision
		case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrProfileChanged), errors.Is(err, identity.ErrMoveConfirmation), errors.Is(err, identity.ErrSignInKindLocal), errors.Is(err, identity.ErrSignInKindPortico):
			// The central commit may already exist. Classify a rejected local
			// capture as stale so the caller reconciles its durable deny-only
			// intent rather than presenting a retry as a fresh authorization.
			return networking.ErrStale
		default:
			return err
		}
	}
	if v.Purpose != "setup" {
		return nil
	}
	if v.AccountID == "" || v.AccountID == networking.UnboundAccount {
		return nil
	}
	var linked bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_portico_links WHERE hosted_account_id=?) OR EXISTS(SELECT 1 FROM networking_claim_intents i JOIN account_portico_links l ON l.account_id=i.owner_id WHERE i.operation_id=?) OR NOT EXISTS(SELECT 1 FROM networking_claim_intents i JOIN accounts a ON a.id=i.owner_id WHERE i.operation_id=?)`, v.AccountID, v.OperationID, v.OperationID).Scan(&linked)
	if e != nil || linked {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO account_portico_links(account_id,hosted_account_id) SELECT owner_id,? FROM networking_claim_intents WHERE operation_id=?`, v.AccountID, v.OperationID)
	return e
}
