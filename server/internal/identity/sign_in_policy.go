package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var ErrFactorSetupRequired = errors.New("Set up two-step verification to finish signing in.")

type factorSetupContextKey struct{}

func factorSetupContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, factorSetupContextKey{}, true)
}

func requiresFactorTx(ctx context.Context, tx *sql.Tx, account string) (bool, error) {
	k, e := SignInKindTx(ctx, tx)
	if e != nil || k != SignInLocal {
		return false, e
	}
	p, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if p.Values.RequireTwoStep == "nobody" {
		return false, nil
	}
	var role string
	if e = tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=?`, account).Scan(&role); e != nil {
		return false, e
	}
	return p.Values.RequireTwoStep == "everyone" || Administrative(role), nil
}
func missingRequiredFactorTx(ctx context.Context, tx *sql.Tx, account string) (bool, error) {
	required, e := requiresFactorTx(ctx, tx, account)
	if e != nil || !required {
		return false, e
	}
	var confirmed bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_account_factors WHERE account_id=? AND confirmed=1)`, account).Scan(&confirmed)
	return !confirmed, e
}

type AttemptPolicy string

func (AttemptPolicy) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"relaxed", "standard", "strict"}}
}

type TwoStepRequirement string

func (TwoStepRequirement) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"nobody", "administrators", "everyone"}}
}

type StaySignedInDays int

func (StaySignedInDays) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{7, 30, 90, 365}}
}

type PINRememberDays int

func (PINRememberDays) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{0, 7, 30, 90}}
}

type PasswordMinimum int

func (PasswordMinimum) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{8, 10, 12, 16}}
}

type MemberResetMinutes int

func (MemberResetMinutes) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{15, 60, 1440}}
}

type EndedSessionsDays int

func (EndedSessionsDays) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{30, 90, 365}}
}

type SignInPolicyValues struct {
	StaySignedInDays       StaySignedInDays   `json:"staySignedInDays"`
	PINRememberDays        PINRememberDays    `json:"pinRememberDays"`
	PasswordAttempts       AttemptPolicy      `json:"passwordAttempts"`
	PINAttempts            AttemptPolicy      `json:"pinAttempts"`
	PasswordMinLength      PasswordMinimum    `json:"passwordMinLength"`
	MemberResetCodeMinutes MemberResetMinutes `json:"memberResetCodeMinutes"`
	RequireTwoStep         TwoStepRequirement `json:"requireTwoStep"`
	RememberAccounts       bool               `json:"rememberAccounts"`
	EndedSessionsDays      EndedSessionsDays  `json:"endedSessionsDays"`
}

// UnmarshalJSON requires the complete typed policy, including false-valued
// booleans. A partial replacement cannot silently turn remembering off.
func (v *SignInPolicyValues) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 9 {
		return ErrDirectInput
	}
	for _, key := range []string{"staySignedInDays", "pinRememberDays", "passwordAttempts", "pinAttempts", "passwordMinLength", "memberResetCodeMinutes", "requireTwoStep", "rememberAccounts", "endedSessionsDays"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrDirectInput
		}
	}
	type plain SignInPolicyValues
	var out plain
	if json.Unmarshal(raw, &out) != nil || !SignInPolicyValues(out).valid() {
		return ErrDirectInput
	}
	*v = SignInPolicyValues(out)
	return nil
}

type SignInPolicy struct {
	Revision int64              `json:"revision,string"`
	Values   SignInPolicyValues `json:"values"`
}
type SignInPolicyPatch struct {
	ExpectedRevision int64              `json:"expectedRevision,string"`
	Values           SignInPolicyValues `json:"values"`
}

func DefaultSignInPolicy() SignInPolicyValues {
	return SignInPolicyValues{90, 30, "standard", "standard", 8, 60, "nobody", true, 90}
}
func validAttemptPolicy(p AttemptPolicy) bool {
	return p == "relaxed" || p == "standard" || p == "strict"
}
func (v SignInPolicyValues) valid() bool {
	return (v.StaySignedInDays == 7 || v.StaySignedInDays == 30 || v.StaySignedInDays == 90 || v.StaySignedInDays == 365) && (v.PINRememberDays == 0 || v.PINRememberDays == 7 || v.PINRememberDays == 30 || v.PINRememberDays == 90) && validAttemptPolicy(v.PasswordAttempts) && validAttemptPolicy(v.PINAttempts) && (v.PasswordMinLength == 8 || v.PasswordMinLength == 10 || v.PasswordMinLength == 12 || v.PasswordMinLength == 16) && (v.MemberResetCodeMinutes == 15 || v.MemberResetCodeMinutes == 60 || v.MemberResetCodeMinutes == 1440) && (v.RequireTwoStep == "nobody" || v.RequireTwoStep == "administrators" || v.RequireTwoStep == "everyone") && (v.EndedSessionsDays == 30 || v.EndedSessionsDays == 90 || v.EndedSessionsDays == 365)
}

// ReadSignInPolicyTx reads the only policy authority, including in configuration
// imports. No operations document or process-local cache mirrors these values.
func ReadSignInPolicyTx(ctx context.Context, tx *sql.Tx) (out SignInPolicy, err error) {
	if tx == nil {
		return out, ErrDirectInput
	}
	return readSignInPolicy(ctx, tx)
}

func readSignInPolicy(ctx context.Context, reader identityReader) (out SignInPolicy, err error) {
	v := &out.Values
	err = reader.QueryRowContext(ctx, `SELECT revision,stay_signed_in_days,pin_remember_days,password_attempts,pin_attempts,password_min_length,member_reset_code_minutes,require_two_step,remember_accounts,ended_sessions_days FROM identity_sign_in_policy WHERE singleton=1`).Scan(&out.Revision, &v.StaySignedInDays, &v.PINRememberDays, &v.PasswordAttempts, &v.PINAttempts, &v.PasswordMinLength, &v.MemberResetCodeMinutes, &v.RequireTwoStep, &v.RememberAccounts, &v.EndedSessionsDays)
	return
}

func sessionWindowTx(ctx context.Context, tx *sql.Tx) (time.Duration, error) {
	p, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return 0, e
	}
	kind, e := SignInKindTx(ctx, tx)
	if e != nil {
		return 0, e
	}
	window := time.Duration(p.Values.StaySignedInDays) * 24 * time.Hour
	if kind == SignInPortico && window > LocalSignInWindow {
		window = LocalSignInWindow
	}
	return window, nil
}

func rememberAccountsTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	p, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return false, e
	}
	k, e := SignInKindTx(ctx, tx)
	return e == nil && k == SignInLocal && p.Values.RememberAccounts, e
}

// ReplaceSignInPolicyTx is compare-and-swap within the caller's authorized
// SQLite transaction. Side effects commit together with the policy.
func ReplaceSignInPolicyTx(ctx context.Context, tx *sql.Tx, q SignInPolicyPatch) (SignInPolicy, error) {
	if q.ExpectedRevision < 1 || !q.Values.valid() {
		return SignInPolicy{}, ErrDirectInput
	}
	old, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return old, e
	}
	if old.Revision != q.ExpectedRevision {
		return old, ErrProfileChanged
	}
	v := q.Values
	r, e := tx.ExecContext(ctx, `UPDATE identity_sign_in_policy SET revision=revision+1,stay_signed_in_days=?,pin_remember_days=?,password_attempts=?,pin_attempts=?,password_min_length=?,member_reset_code_minutes=?,require_two_step=?,remember_accounts=?,ended_sessions_days=?,updated_ms=? WHERE singleton=1 AND revision=?`, v.StaySignedInDays, v.PINRememberDays, v.PasswordAttempts, v.PINAttempts, v.PasswordMinLength, v.MemberResetCodeMinutes, v.RequireTwoStep, v.RememberAccounts, v.EndedSessionsDays, time.Now().UnixMilli(), q.ExpectedRevision)
	if e != nil {
		return old, e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return old, ErrProfileChanged
	}
	if v.PINRememberDays != old.Values.PINRememberDays {
		if _, e = tx.ExecContext(ctx, `DELETE FROM direct_profile_trust`); e != nil {
			return old, e
		}
	}
	if !v.RememberAccounts {
		if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts`); e != nil {
			return old, e
		}
	}
	return ReadSignInPolicyTx(ctx, tx)
}
func (s *Service) signInPolicyOwnerTx(ctx context.Context, tx *sql.Tx, bearer string) error {
	if _, e := s.moveOwnerTx(ctx, tx, bearer); e != nil {
		return e
	}
	record, e := s.familyTokenTx(ctx, tx, Digest(bearer))
	if e != nil {
		return e
	}
	return s.RequireMainProfileTx(ctx, tx, record.principal)
}

func (s *Service) ReadSignInPolicy(ctx context.Context, bearer string) (out SignInPolicy, err error) {
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer g.Rollback()
	if e = s.signInPolicyOwnerTx(ctx, g.Tx(), bearer); e != nil {
		return out, e
	}
	return ReadSignInPolicyTx(ctx, g.Tx())
}
func (s *Service) ChangeSignInPolicy(ctx context.Context, bearer string, q SignInPolicyPatch) (out SignInPolicy, err error) {
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := s.signInPolicyOwnerTx(ctx, tx, bearer); e != nil {
			return e
		}
		var e error
		out, e = ReplaceSignInPolicyTx(ctx, tx, q)
		return e
	})
	return
}
func ValidateNewLocalPasswordTx(ctx context.Context, tx *sql.Tx, password string) error {
	p, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return e
	}
	if !directPassword(password) || utf8.RuneCountInString(password) < int(p.Values.PasswordMinLength) {
		return ErrDirectInput
	}
	return nil
}
func passwordDelay(p AttemptPolicy, attempts int) int64 {
	threshold, base, ceiling := 5, int64(1), int64(60)
	if p == "relaxed" {
		threshold = 10
	}
	if p == "strict" {
		threshold, base, ceiling = 3, 10, 900
	}
	if attempts < threshold {
		return 0
	}
	return min(ceiling, base*(int64(1)<<min(attempts-threshold, 10)))
}
func pinDelay(p AttemptPolicy, attempts int) int64 {
	if p == "relaxed" {
		if attempts < 5 {
			return 0
		}
		if attempts < 10 {
			return 30
		}
		if attempts < 15 {
			return 300
		}
		if attempts < 20 {
			return 900
		}
		return 3600
	}
	if p == "strict" {
		if attempts < 3 {
			return 2
		}
		if attempts < 5 {
			return 300
		}
		if attempts < 10 {
			return 900
		}
		return 3600
	}
	if attempts < 3 {
		return 2
	}
	if attempts < 5 {
		return 30
	}
	if attempts < 10 {
		return 300
	}
	if attempts < 15 {
		return 900
	}
	return 3600
}
