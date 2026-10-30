// Package usermutation stores tiny, transactionally reserved mutation receipts.
// Callers prove current authority before Check/Lookup, reserve before any domain
// effect, Save in that same transaction, and roll back the entire transaction on
// every error. The package does not authenticate accounts or session families.
package usermutation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var (
	ErrInvalidKey = errors.New("invalid mutation key")
	ErrExpired    = errors.New("mutation receipt expired")
	ErrConflict   = errors.New("mutation key conflicts with original request")
	ErrCapacity   = errors.New("mutation receipt capacity exhausted")
)

const (
	RetentionMs           int64 = 30 * 24 * 60 * 60 * 1000
	FutureSkewMs          int64 = 5 * 60 * 1000
	MaxIDBytes                  = 128
	MaxOperationBytes           = 96
	MaxStatusBytes              = 32
	ReceiptChargeBytes          = 4096
	AccountChargeBytes          = 1024
	BaseChargeBytes             = 65536
	GlobalByteLimit             = 32 * 1024 * 1024
	OrdinaryByteLimit           = 30 * 1024 * 1024
	ReservedStopBytes           = 2 * 1024 * 1024
	PruneLimit                  = 32
	MaxReceiptRecordBytes       = 1024
)

// Binding is the authenticated owner a receipt belongs to. RootID is the
// optional P2-62 mutation root the client presented; an empty RootID is an
// existing non-generic domain receipt, which stays rootless. The lifetimes
// themselves are never taken from the caller: Check, Save and Lookup resolve the
// current membership incarnation and profile lifetime from the transaction.
type Binding struct {
	Authority, AccountID, ProfileID, FamilyID string
	RootID                                    string
}
type Envelope struct {
	Key, Operation, Digest string
	Stop                   bool
}

// Result is deliberately closed: no arbitrary response body or JSON field.
type Result struct {
	Status         string `json:"status"`
	TargetID       string `json:"targetId,omitempty"`
	Revision       int64  `json:"revision,omitempty"`
	Generation     int    `json:"generation,omitempty"`
	PositionMs     int64  `json:"positionMs,omitempty"`
	PositionStatus string `json:"positionStatus,omitempty"`
}
type Receipt struct {
	Key       string `json:"key"`
	Operation string `json:"operation"`
	Result
	CommittedMs int64 `json:"committedMs"`
	ExpiresMs   int64 `json:"expiresMs"`
}

// JSONSchema keeps the compact receipt wire shape closed, including outcome
// enums. Strings stay assignable from existing domain adapters; Save and SQL
// independently enforce these same closed sets before anything is retained.
func (Receipt) JSONSchema() map[string]any {
	boundedString := func(n int) map[string]any { return map[string]any{"type": "string", "maxLength": n} }
	number := func(n int64) map[string]any { return map[string]any{"type": "integer", "minimum": 0, "maximum": n} }
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"key", "operation", "status", "committedMs", "expiresMs"},
		"properties": map[string]any{
			"key":       map[string]any{"type": "string", "pattern": `^p251_[0-9]{13}_[0-9a-f]{32}$`, "minLength": 51, "maxLength": 51},
			"operation": boundedString(MaxOperationBytes),
			"status":    map[string]any{"type": "string", "enum": []string{"applied", "already_stopped", "conflict", "invalid", "terminal", "deleted", "removed", "partial"}},
			"targetId":  boundedString(MaxIDBytes), "revision": number(9007199254740991), "generation": number(2147483647), "positionMs": number(9007199254740991),
			"positionStatus": map[string]any{"type": "string", "enum": []string{"", "applied", "already_stopped", "not_renderer", "policy_rejected", "unchanged"}},
			"committedMs":    number(9999999999999), "expiresMs": number(9999999999999 + RetentionMs),
		},
	}
}

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validStatus(s string) bool {
	switch s {
	case "applied", "already_stopped", "conflict", "invalid", "terminal", "deleted", "removed", "partial":
		return true
	}
	return false
}
func validPositionStatus(s string) bool {
	switch s {
	case "", "applied", "already_stopped", "not_renderer", "policy_rejected", "unchanged":
		return true
	}
	return false
}
func bounded(s string, max int, empty bool) bool {
	return (empty || s != "") && len(s) <= max && !strings.ContainsRune(s, 0)
}
func validBinding(b Binding) bool {
	return (b.Authority == "local" || b.Authority == "hosted") && bounded(b.AccountID, MaxIDBytes, false) && bounded(b.ProfileID, MaxIDBytes, false) && bounded(b.FamilyID, MaxIDBytes, false) && (b.RootID == "" || ValidRootID(b.RootID))
}
func hexLower(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func parseKey(key string) (int64, error) {
	if len(key) != 51 || !strings.HasPrefix(key, "p251_") || key[18] != '_' || !hexLower(key[19:], 32) {
		return 0, ErrInvalidKey
	}
	for _, c := range key[5:18] {
		if c < '0' || c > '9' {
			return 0, ErrInvalidKey
		}
	}
	issued, _ := strconv.ParseInt(key[5:18], 10, 64)
	if issued <= 0 {
		return 0, ErrInvalidKey
	}
	return issued, nil
}

// IssuedMs checks the wire grammar and wall-clock window. Persisted retirement
// fences must also be checked; use Check/Lookup rather than this parser alone.
func IssuedMs(key string, now int64) (int64, error) {
	issued, err := parseKey(key)
	if err != nil {
		return 0, err
	}
	if now < 0 || now > 9999999999999 || issued > now+FutureSkewMs {
		return 0, ErrInvalidKey
	}
	if issued < now-RetentionMs {
		return 0, ErrExpired
	}
	return issued, nil
}
func validate(b Binding, e Envelope, now int64) (int64, error) {
	if !validBinding(b) || !bounded(e.Operation, MaxOperationBytes, false) || !hexLower(e.Digest, 64) || now < 0 || now > 9999999999999 {
		return 0, ErrInvalidKey
	}
	return parseKey(e.Key)
}
func cutoff(ctx context.Context, q reader, b Binding, now int64) (int64, error) {
	var cut int64
	err := q.QueryRowContext(ctx, `SELECT max(g.retired_before_ms,coalesce((SELECT retired_before_ms FROM user_mutation_accounts WHERE authority=? AND account_id=?),0),?) FROM user_mutation_global g WHERE singleton=1`, b.Authority, b.AccountID, max(int64(0), now-RetentionMs)).Scan(&cut)
	return cut, err
}
func advance(ctx context.Context, tx *sql.Tx, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE user_mutation_global SET retired_before_ms=max(retired_before_ms,?) WHERE singleton=1`, max(int64(0), now-RetentionMs))
	return err
}

type stored struct {
	Receipt
	scope                   int64
	profile                 string
	family                  string
	digest                  string
	profileIncarnation      string
	installationIncarnation string
	rootID                  string
	stop                    bool
}

func read(ctx context.Context, q reader, key string) (*stored, error) {
	var r stored
	err := q.QueryRowContext(ctx, `SELECT key,operation,status,target_id,revision,generation,position_ms,position_status,committed_ms,issued_ms+2592000000,scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,digest,is_stop FROM user_mutation_receipts WHERE key=?`, key).Scan(&r.Key, &r.Operation, &r.Status, &r.TargetID, &r.Revision, &r.Generation, &r.PositionMs, &r.PositionStatus, &r.CommittedMs, &r.ExpiresMs, &r.scope, &r.profile, &r.family, &r.profileIncarnation, &r.installationIncarnation, &r.rootID, &r.digest, &r.stop)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}
func scopeID(ctx context.Context, q reader, b Binding) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM user_mutation_accounts WHERE authority=? AND account_id=?`, b.Authority, b.AccountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func matches(r *stored, id int64, b Binding, e Envelope, l lifetime) bool {
	return bound(r, id, b, l) && r.Operation == e.Operation && r.digest == e.Digest && r.stop == e.Stop
}
func capacity(err error) error {
	if err != nil && strings.Contains(err.Error(), "user_mutation_capacity") {
		return fmt.Errorf("%w: %v", ErrCapacity, err)
	}
	return err
}

// Check returns a matching completed receipt, or reserves a fixed worst-case
// receipt slot and returns nil. The reservation and all effects MUST share tx.
// A committed 'reserved' row is a caller bug, and is never reported as success.
//
// The receipt is bound to the current membership incarnation, profile lifetime,
// installation lifetime and mutation root resolved inside tx. A receipt from a
// different lifetime is a conflict, never a replay, and a new intent under a
// draining or gone root is refused rather than applied.
func Check(ctx context.Context, tx *sql.Tx, b Binding, e Envelope, now int64) (*Receipt, error) {
	issued, err := validate(b, e, now)
	if err != nil {
		return nil, err
	}
	l, err := resolveLifetime(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	if !l.profileLive {
		// An erased profile (or a membership that is no longer current) accepts
		// nothing new; its retained history is read-only.
		return nil, ErrConflict
	}
	if err = advance(ctx, tx, now); err != nil {
		return nil, err
	}
	cut, err := cutoff(ctx, tx, b, now)
	if err != nil {
		return nil, err
	}
	if issued < cut {
		return nil, ErrExpired
	}
	if _, err = IssuedMs(e.Key, now); err != nil {
		return nil, err
	}
	id, err := scopeID(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	old, err := read(ctx, tx, e.Key)
	if err != nil {
		return nil, err
	}
	if old != nil {
		// A committed result replays only under the lifetimes it was committed
		// with. The stored profile lifetime is compared with the one read from
		// this transaction, so reusing a profile id under a new lifetime never
		// re-returns the old result.
		if !matches(old, id, b, e, l) || old.Status == "reserved" {
			return nil, ErrConflict
		}
		return &old.Receipt, nil
	}
	// A new intent needs a live root. Draining roots keep their recorded
	// results readable, but nothing new is accepted through them.
	if b.RootID != "" && l.rootState != "active" {
		return nil, ErrConflict
	}
	// The savepoint also removes a newly allocated account fact on refusal, even
	// if a caller inspects the error before rolling its outer transaction back.
	if _, err = tx.ExecContext(ctx, `SAVEPOINT user_mutation_reserve`); err != nil {
		return nil, err
	}
	fail := func(err error) (*Receipt, error) {
		_, _ = tx.ExecContext(ctx, `ROLLBACK TO user_mutation_reserve`)
		_, _ = tx.ExecContext(ctx, `RELEASE user_mutation_reserve`)
		return nil, capacity(err)
	}
	if id == 0 {
		var owner string
		if b.Authority == "local" {
			err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=?`, b.AccountID).Scan(&owner)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT account_id FROM account_portico_links WHERE hosted_account_id=?`, b.AccountID).Scan(&owner)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrConflict
			}
			return fail(err)
		}
		row, er := tx.ExecContext(ctx, `INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES(?,?,?,?)`, b.Authority, b.AccountID, owner, e.Stop)
		if er != nil {
			return fail(er)
		}
		id, err = row.LastInsertId()
		if err != nil {
			return fail(err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,operation,digest,is_stop,issued_ms,status) VALUES(?,?,?,?,?,?,?,?,?,?,?, 'reserved')`, e.Key, id, b.ProfileID, b.FamilyID, l.profile, l.installation, b.RootID, e.Operation, e.Digest, e.Stop, issued)
	if err != nil {
		return fail(err)
	}
	_, err = tx.ExecContext(ctx, `RELEASE user_mutation_reserve`)
	if err != nil {
		return nil, err
	}
	err = dbwork.BeforeCommit(tx, func() error {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM user_mutation_receipts WHERE key=?`, e.Key).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status == "reserved" {
			return fmt.Errorf("%w: unfinalized receipt reservation", ErrConflict)
		}
		return nil
	})
	return nil, err
}

// Save finalizes Check's reservation. It never reserves after domain effects.
func Save(ctx context.Context, tx *sql.Tx, b Binding, e Envelope, r Result, now int64) error {
	issued, err := validate(b, e, now)
	if err != nil {
		return err
	}
	if !validStatus(r.Status) || !validPositionStatus(r.PositionStatus) || !bounded(r.TargetID, MaxIDBytes, true) || !bounded(r.PositionStatus, MaxStatusBytes, true) || r.Revision < 0 || r.Generation < 0 || r.PositionMs < 0 || r.PositionMs > 9007199254740991 || r.Revision > 9007199254740991 || r.Generation > 2147483647 {
		return ErrConflict
	}
	l, err := resolveLifetime(ctx, tx, b)
	if err != nil {
		return err
	}
	cut, err := cutoff(ctx, tx, b, now)
	if err != nil {
		return err
	}
	if issued < cut {
		return ErrExpired
	}
	if _, err = IssuedMs(e.Key, now); err != nil {
		return err
	}
	id, err := scopeID(ctx, tx, b)
	if err != nil {
		return err
	}
	old, err := read(ctx, tx, e.Key)
	if err != nil {
		return err
	}
	if old == nil || !matches(old, id, b, e, l) {
		return ErrConflict
	}
	if old.Status != "reserved" {
		if old.Result == r {
			return nil
		}
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE user_mutation_receipts SET status=?,target_id=?,revision=?,generation=?,position_ms=?,position_status=?,committed_ms=? WHERE key=?`, r.Status, r.TargetID, r.Revision, r.Generation, r.PositionMs, r.PositionStatus, now, e.Key)
	return err
}

// Lookup joins a dbwork snapshot already carried by ctx (including the caller's
// current-authority check), or opens one bounded read snapshot of its own.
func Lookup(ctx context.Context, db *sql.DB, b Binding, key string, now int64) (out *Receipt, err error) {
	err = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
		var e error
		out, e = LookupTx(ctx, dbwork.Snapshot(ctx), b, key, now)
		return e
	})
	return
}
func LookupTx(ctx context.Context, tx *sql.Tx, b Binding, key string, now int64) (*Receipt, error) {
	if !validBinding(b) || now < 0 || now > 9999999999999 {
		return nil, ErrInvalidKey
	}
	issued, err := parseKey(key)
	if err != nil {
		return nil, err
	}
	l, err := resolveLifetime(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	cut, err := cutoff(ctx, tx, b, now)
	if err != nil {
		return nil, err
	}
	if issued < cut {
		return nil, ErrExpired
	}
	if _, err = IssuedMs(key, now); err != nil {
		return nil, err
	}
	id, err := scopeID(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	r, err := read(ctx, tx, key)
	if err != nil || r == nil {
		return nil, err
	}
	// A recorded result is readable only under the lifetimes it was committed
	// with. A drained root keeps its results readable; a gone root or a rotated
	// lifetime does not, and never reports the old receipt.
	if !bound(r, id, b, l) {
		return nil, nil
	}
	if r.Status == "reserved" {
		return nil, ErrConflict
	}
	return &r.Receipt, nil
}
func Policy(ctx context.Context, db *sql.DB, b Binding, now int64) (out int64, err error) {
	if !validBinding(b) || now < 0 || now > 9999999999999 {
		return 0, ErrInvalidKey
	}
	err = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
		var e error
		out, e = cutoff(ctx, dbwork.Snapshot(ctx), b, now)
		return e
	})
	return
}

// RetireAccount is owner-authorized destructive history recovery, never a
// user-data mutation. It consumes no receipt slot, advances before pruning,
// and removes at most PruneLimit receipts. Call one cleanup API per writer
// turn; repeat in separate transactions to drain the retired cohort.
func RetireAccount(ctx context.Context, tx *sql.Tx, authority, account string, cut int64) error {
	if (authority != "local" && authority != "hosted") || !bounded(account, MaxIDBytes, false) || cut < 0 || cut > 9999999999999 {
		return ErrInvalidKey
	}
	// A recovery fact is constant per real account and consumes no receipt slot.
	// Never retire unrelated accounts when this account has no previous receipt.
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM user_mutation_accounts WHERE authority=? AND account_id=?`, authority, account).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var owner string
		if authority == "local" {
			err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=?`, account).Scan(&owner)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT account_id FROM account_portico_links WHERE hosted_account_id=?`, account).Scan(&owner)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		row, e := tx.ExecContext(ctx, `INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop,retired_before_ms) VALUES(?,?,?,1,?)`, authority, account, owner, cut)
		if e != nil {
			return capacity(e)
		}
		id, err = row.LastInsertId()
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE user_mutation_accounts SET retired_before_ms=max(retired_before_ms,?),cleanup_pending=1 WHERE id=?`, cut, id); err != nil {
		return err
	}
	_, err = pruneAccount(ctx, tx, id)
	return err
}

// pruneAccount drains a maintained per-account cutoff cohort, never a stored
// query result. The pending bit lets ordinary maintenance finish owner recovery.
func pruneAccount(ctx context.Context, tx *sql.Tx, id int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM user_mutation_receipts WHERE key IN (SELECT key FROM user_mutation_receipts WHERE scope_id=? AND issued_ms<(SELECT retired_before_ms FROM user_mutation_accounts WHERE id=?) ORDER BY issued_ms,key LIMIT 32)`, id, id)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE user_mutation_accounts SET cleanup_pending=EXISTS(SELECT 1 FROM user_mutation_receipts WHERE scope_id=? AND issued_ms<user_mutation_accounts.retired_before_ms) WHERE id=?`, id, id)
	return n, err
}

// RetireFamily requires the caller to revoke that family's current authority
// first. No per-family tombstone or registration is created; replay authority
// stays with the existing identity system. Live rows remain charged until a
// durable account/global cutoff retires their keys; otherwise another family
// could reuse a pruned key inside its window. Each call deletes at most 32 rows.
func RetireFamily(ctx context.Context, tx *sql.Tx, b Binding) error {
	if !validBinding(b) {
		return ErrInvalidKey
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM user_mutation_receipts WHERE key IN (SELECT key FROM user_mutation_receipts WHERE scope_id=(SELECT id FROM user_mutation_accounts WHERE authority=? AND account_id=?) AND family_id=? AND issued_ms<(SELECT max(a.retired_before_ms,g.retired_before_ms) FROM user_mutation_accounts a CROSS JOIN user_mutation_global g WHERE a.authority=? AND a.account_id=? AND g.singleton=1) ORDER BY issued_ms,key LIMIT 32)`, b.Authority, b.AccountID, b.FamilyID, b.Authority, b.AccountID)
	return err
}

// Prune durably advances the global wall-clock fence before bounded cleanup.
// Orphan receipts of erased accounts retain no account binding (only an opaque
// non-reused scope number), remain charged globally, and drain in this budget.
// Call at most one cleanup API per bounded writer transaction.
func Prune(ctx context.Context, tx *sql.Tx, now int64) (int64, error) {
	if now < 0 || now > 9999999999999 {
		return 0, ErrInvalidKey
	}
	if err := advance(ctx, tx, now); err != nil {
		return 0, err
	}
	// Drain one erased account through indexed owner/scope lookups first.
	var orphan int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM user_mutation_accounts WHERE owner_account_id IS NULL LIMIT 1`).Scan(&orphan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if orphan != 0 {
		result, e := tx.ExecContext(ctx, `DELETE FROM user_mutation_receipts WHERE key IN (SELECT key FROM user_mutation_receipts WHERE scope_id=? ORDER BY issued_ms,key LIMIT 32)`, orphan)
		if e != nil {
			return 0, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return 0, e
		}
		// Count the orphan fact inside the same 32-row cleanup budget.
		if n < PruneLimit {
			_, e = tx.ExecContext(ctx, `DELETE FROM user_mutation_accounts WHERE id=? AND NOT EXISTS(SELECT 1 FROM user_mutation_receipts WHERE scope_id=?)`, orphan, orphan)
			if e != nil {
				return 0, e
			}
			n++
		}
		return n, nil
	}
	var pending int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM user_mutation_accounts WHERE cleanup_pending=1 LIMIT 1`).Scan(&pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if pending != 0 {
		return pruneAccount(ctx, tx, pending)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM user_mutation_receipts WHERE key IN (SELECT r.key FROM user_mutation_receipts r WHERE r.issued_ms<(SELECT retired_before_ms FROM user_mutation_global WHERE singleton=1) ORDER BY r.issued_ms,r.key LIMIT 32)`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
