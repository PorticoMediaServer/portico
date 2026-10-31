package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
	"golang.org/x/crypto/bcrypt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SetupOptions struct {
	RequestID   string `json:"requestId,omitempty"`
	SetupToken  string `json:"setupToken"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	Name        string `json:"name"`
	AuthMode    string `json:"authMode,omitempty"`
	Interactive bool   `json:"interactive,omitempty"`
}
type OnboardingState struct {
	AuthMode       string `json:"authMode"`
	Phase          string `json:"phase"`
	Ready          bool   `json:"ready"`
	ClaimInstalled bool   `json:"claimInstalled"`
	Revision       int64  `json:"revision,string"`
	SessionLabel   string `json:"sessionLabel"`
}

func setupOperationID() string {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		panic(e)
	}
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}

// SetupWithOptions creates either a local owner or an account-mode pending
// initialization. It never substitutes a local credential for a Portico Account.
func (s *Service) SetupWithOptions(ctx context.Context, q SetupOptions, private bool) (SetupResult, error) {
	defer authorityfiles.Process.Admit()()
	if q.AuthMode == "" {
		q.AuthMode = "hosted"
	}
	if q.RequestID == "" {
		q.RequestID = setupOperationID()
	}
	q.Username = strings.ToLower(strings.TrimSpace(q.Username))
	q.Name = strings.TrimSpace(q.Name)
	if !setupUUID.MatchString(q.RequestID) || (q.AuthMode != "local" && q.AuthMode != "hosted") || !setupLabel(q.Name, 100) {
		return SetupResult{}, ErrDirectInput
	}
	if q.AuthMode == "hosted" && (q.Username != "" || q.Password != "") {
		return SetupResult{}, ErrDirectInput
	}
	if q.AuthMode == "local" && (len(q.Username) < 3 || len(q.Username) > 64 || strings.ContainsAny(q.Username, " \t\r\n@") || !directPassword(q.Password)) {
		return SetupResult{}, ErrDirectInput
	}
	raw, _ := json.Marshal(q)
	defer clear(raw)
	requestHash := s.setupHash("initialize-request", string(raw))
	var existing, mode, secretHash string
	var setupRevision int64
	e := s.db.QueryRowContext(ctx, `SELECT operation_id,auth_mode,setup_hash,revision FROM onboarding_state_v1 WHERE singleton=1`).Scan(&existing, &mode, &secretHash, &setupRevision)
	switch {
	case e == nil:
		if existing != q.RequestID {
			return SetupResult{}, ErrConflict
		}
		if mode == q.AuthMode {
			return s.resumeSetup(ctx, q.RequestID, q.SetupToken, requestHash, private)
		}
		// An owner who declines the web claim may select local sign-in using the
		// same protected setup secret; no public bootstrap can replace that secret.
		if mode != "hosted" || q.AuthMode != "local" || subtle.ConstantTimeCompare([]byte(secretHash), []byte(s.setupHash("initialize-secret", q.SetupToken))) != 1 {
			return SetupResult{}, ErrUnauthorized
		}
	case errors.Is(e, sql.ErrNoRows):
		token, e := os.ReadFile(s.setupPath)
		if e != nil || subtle.ConstantTimeCompare(token, []byte(q.SetupToken)) != 1 {
			return SetupResult{}, ErrUnauthorized
		}
	default:
		return SetupResult{}, e
	}
	var hash []byte
	if q.AuthMode == "local" {
		if e = s.takeHashSlot(ctx); e != nil {
			return SetupResult{}, e
		}
		hash, e = bcrypt.GenerateFromPassword([]byte(q.Password), PasswordCost)
		<-s.hashSlots
		if e != nil {
			return SetupResult{}, e
		}
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return SetupResult{}, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); e != nil {
		return SetupResult{}, e
	}
	if count != 0 {
		return SetupResult{}, ErrConflict
	}
	if existing != "" {
		var active sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT active_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SetupResult{}, err
		}
		if active.Valid {
			return SetupResult{}, ErrConflict
		}
		// Fence both the continuation secret and the pending revision against a
		// concurrent claim completion or another method choice.
		result, e := tx.ExecContext(ctx, `DELETE FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND auth_mode='hosted' AND setup_hash=? AND revision=?`, q.RequestID, secretHash, setupRevision)
		if e != nil {
			return SetupResult{}, e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return SetupResult{}, ErrConflict
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, q.Name); e != nil {
		return SetupResult{}, e
	}
	kind := SignInPortico
	if q.AuthMode == "local" {
		kind = SignInLocal
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind=excluded.sign_in_kind,revision=identity_state.revision+1`, kind); e != nil {
		return SetupResult{}, e
	}
	out := SetupResult{}
	var receipt []byte
	if q.AuthMode == "local" {
		if e = ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
			return SetupResult{}, e
		}
		aid, pid := Token(), Token()
		if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`, aid, q.Username, hash, pid); e != nil {
			return SetupResult{}, e
		}
		out.Envelope, e = s.IssueTx(ctx, tx, aid, pid, "local", "owner", 1, time.Time{})
		if e != nil {
			return SetupResult{}, e
		}
		receipt, e = s.sealSetup(out.Envelope, "initialize:"+q.RequestID)
		if e != nil {
			return SetupResult{}, e
		}
	} else {
		out.Pending = &PendingAccountSetup{ServerID: s.serverID, RequestID: q.RequestID, AuthMode: "hosted", ClaimRequired: true, Revision: 1}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO onboarding_state_v1(singleton,auth_mode,operation_id,request_hash,setup_hash,receipt,receipt_until,ready) VALUES(1,?,?,?,?,?,?,?)`, q.AuthMode, q.RequestID, requestHash, s.setupHash("initialize-secret", q.SetupToken), receipt, time.Now().Unix()+setupReceiptSeconds, q.AuthMode == "local"); e != nil {
		return SetupResult{}, e
	}
	if e = gated.Commit(); e != nil {
		return SetupResult{}, e
	}
	_ = os.Remove(s.setupPath)
	return out, nil
}

// A reload or closed tab resumes setup within this window; after it, the owner
// signs in with the password they just chose.
const setupReceiptSeconds = int64(setupflow.ReceiptWindow / time.Second)

func (s *Service) ResumeSetup(ctx context.Context, id, secret string, private bool) (SetupResult, error) {
	return s.resumeSetup(ctx, id, secret, "", private)
}
func (s *Service) resumeSetup(ctx context.Context, id, secret, requestHash string, private bool) (SetupResult, error) {
	if !setupUUID.MatchString(id) || len(secret) != 43 {
		return SetupResult{}, ErrUnauthorized
	}
	gated2, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return SetupResult{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var storedSecret, storedRequest string
	var cipher []byte
	var until, revision int64
	var mode string
	e = tx.QueryRowContext(ctx, `SELECT setup_hash,request_hash,receipt,receipt_until,auth_mode,revision FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=?`, id).Scan(&storedSecret, &storedRequest, &cipher, &until, &mode, &revision)
	if errors.Is(e, sql.ErrNoRows) {
		return SetupResult{}, e
	}
	if e != nil {
		return SetupResult{}, e
	}
	if subtle.ConstantTimeCompare([]byte(storedSecret), []byte(s.setupHash("initialize-secret", secret))) != 1 || requestHash != "" && subtle.ConstantTimeCompare([]byte(storedRequest), []byte(requestHash)) != 1 {
		return SetupResult{}, ErrUnauthorized
	}
	if mode == "hosted" && len(cipher) == 0 {
		return SetupResult{Pending: &PendingAccountSetup{ServerID: s.serverID, RequestID: id, AuthMode: "hosted", ClaimRequired: true, Revision: revision}}, gated2.Commit()
	}
	if until <= time.Now().Unix() || len(cipher) == 0 {
		return SetupResult{}, ErrUnauthorized
	}
	var out Envelope
	if e = s.openSetup(cipher, "initialize:"+id, &out); e != nil {
		return SetupResult{}, e
	}
	record, e := s.familyTokenTx(ctx, tx, Digest(out.AccessToken))
	if e != nil {
		return SetupResult{}, e
	}
	if _, e = s.SessionFamilyTx(ctx, tx, record.principal); e != nil {
		return SetupResult{}, e
	}
	return SetupResult{Envelope: out}, gated2.Commit()
}

func (s *Service) Onboarding(ctx context.Context, p Principal) (OnboardingState, error) {
	if p.Authority != "local" || p.Role != "owner" {
		return OnboardingState{}, ErrForbidden
	}
	gated4, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return OnboardingState{}, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if _, e = s.SessionFamilyTx(ctx, tx, p); e != nil {
		return OnboardingState{}, e
	}
	out, e := s.onboardingTx(ctx, tx, p)
	if e != nil {
		return out, e
	}
	return out, gated4.Commit()
}
func (s *Service) onboardingTx(ctx context.Context, tx *sql.Tx, p Principal) (OnboardingState, error) {
	out := OnboardingState{AuthMode: "local", Ready: true, Phase: "ready", SessionLabel: "This Server"}
	e := tx.QueryRowContext(ctx, `SELECT auth_mode,ready,revision FROM onboarding_state_v1 WHERE singleton=1`).Scan(&out.AuthMode, &out.Ready, &out.Revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if s.SetupClaimReadyTx != nil {
		out.ClaimInstalled, e = s.SetupClaimReadyTx(ctx, tx)
		if e != nil {
			return out, e
		}
	}
	if out.AuthMode == "hosted" && !out.Ready {
		out.Phase = "waiting_for_web"
		if out.ClaimInstalled {
			out.Ready = true
			out.Phase = "ready"
		}
	}
	return out, nil
}
func (s *Service) FinishSetup(ctx context.Context, p Principal, expected int64) (OnboardingState, error) {
	gated5, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return OnboardingState{}, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	if _, e = s.SessionFamilyTx(ctx, tx, p); e != nil {
		return OnboardingState{}, e
	}
	out, e := s.onboardingTx(ctx, tx, p)
	if e != nil {
		return out, e
	}
	if p.Authority != "local" || p.Role != "owner" || out.Revision != expected {
		return out, ErrConflict
	}
	if out.AuthMode == "hosted" && !out.ClaimInstalled {
		return out, errors.New("Complete the current account claim before finishing setup")
	}
	if !out.Ready {
		if _, e = tx.ExecContext(ctx, `UPDATE onboarding_state_v1 SET ready=1,revision=revision+1,receipt_until=MIN(receipt_until,?) WHERE singleton=1 AND revision=?`, time.Now().Unix()+300, expected); e != nil {
			return out, e
		}
		out.Ready = true
		out.Phase = "ready"
		out.Revision++
	}
	return out, gated5.Commit()
}

// BrowserSetupToken is only exposed by the origin-checked first-run route.
// The normal setup transaction still consumes the token atomically; no login or
// initialized-server route can retrieve it.
func (s *Service) BrowserSetupToken(ctx context.Context) (string, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM accounts").Scan(&count); err != nil {
		return "", err
	}
	if count != 0 {
		return "", ErrConflict
	}
	var pending bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM onboarding_state_v1)`).Scan(&pending); err != nil {
		return "", err
	}
	if pending {
		// An abandoned Portico Account attempt must never strand the server: once its
		// window has passed without approval it gives way to this new start.
		token, err := s.supersedeStaleSetup(ctx)
		if err != nil {
			return "", err
		}
		if token != "" {
			return token, nil
		}
	}
	token, err := os.ReadFile(s.setupPath)
	if err != nil {
		return "", err
	}
	return string(token), nil
}

// writeSetupSecret replaces the setup secret file atomically: a reader sees the old secret
// or the new one, never a partly written file.
func writeSetupSecret(path, token string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".setup-token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(token); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0600)
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// SetupInProgressError refuses a new setup start while another browser's Portico
// Account attempt is still inside its window. Until says when it may be started over.
type SetupInProgressError struct{ Until time.Time }

func (e *SetupInProgressError) Error() string { return "setup_in_progress" }

// supersedeStaleSetup retires the pending Portico Account setup when it can no longer
// finish: never approved, never installed, and past its window (claimweb.SetupLifetime
// after its newest start, or after it began when it never reached Hosted). A live attempt
// refuses with SetupInProgressError; a local, approved or installed one keeps ErrConflict.
//
// It returns the new setup secret, written (atomically, inside the same fence) only when
// it retired the old attempt; "" when a concurrent start already did, so the caller reads
// the secret that start wrote.
func (s *Service) supersedeStaleSetup(ctx context.Context) (string, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return "", err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	var accounts int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
		return "", err
	}
	var operation, mode string
	var ready, receiptUntil int64
	err = tx.QueryRowContext(ctx, `SELECT operation_id,auth_mode,ready,receipt_until FROM onboarding_state_v1 WHERE singleton=1`).Scan(&operation, &mode, &ready, &receiptUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return "", gated.Commit()
	}
	if err != nil {
		return "", err
	}
	if accounts != 0 || mode != "hosted" || ready != 0 {
		return "", ErrConflict
	}
	var active, installed sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT active_operation_id,installed_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active, &installed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if installed.Valid {
		return "", ErrConflict
	}
	if active.Valid {
		// Only this setup's own claim, still exactly as prepared, may be retired.
		var setupOperation, stage, account string
		var approval int64
		if err = tx.QueryRowContext(ctx, `SELECT setup_operation_id,stage,account_id,approval_revision FROM networking_claim_intents WHERE operation_id=?`, active.String).Scan(&setupOperation, &stage, &account, &approval); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", ErrConflict
			}
			return "", err
		}
		if setupOperation != operation {
			return "", ErrConflict
		}
		// Approved and still finishing: honestly in progress (it can still complete), never
		// "already set up", and never superseded.
		if stage != "prepared" || account != "account-unbound" || approval != 0 {
			return "", &SetupInProgressError{}
		}
	}
	var settled int
	var newest sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE phase IN('installed','consumed','completed')),max(expires_ms) FROM identity_setup_completions WHERE setup_request_id=?`, operation).Scan(&settled, &newest); err != nil {
		return "", err
	}
	if settled != 0 {
		return "", ErrConflict
	}
	now := time.Now()
	// receipt_until is the start plus setupReceiptSeconds; a start never prepared at
	// Hosted keeps the same window from that moment.
	until := time.Unix(receiptUntil-setupReceiptSeconds, 0).Add(claimweb.SetupLifetime())
	if newest.Valid {
		until = time.UnixMilli(newest.Int64)
	}
	if now.Before(until) {
		return "", &SetupInProgressError{Until: until}
	}
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`UPDATE identity_setup_completions SET phase='superseded' WHERE setup_request_id=? AND phase IN('prepared','expired')`, []any{operation}},
		{`UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=? AND stage='prepared' AND account_id='account-unbound' AND approval_revision=0`, []any{active.String}},
		{`UPDATE networking_claim_identity SET active_operation_id=NULL,pending_operation_id=CASE WHEN pending_operation_id=? THEN NULL ELSE pending_operation_id END WHERE singleton=1 AND active_operation_id=? AND installed_operation_id IS NULL`, []any{active.String, active.String}},
		{`DELETE FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND ready=0`, []any{operation}},
	} {
		if _, err = tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return "", err
		}
	}
	token := Token()
	if err = writeSetupSecret(s.setupPath, token); err != nil {
		return "", err
	}
	return token, gated.Commit()
}

// SetupClaimAuthority verifies the continuation secret without inventing a
// member, profile, session or owner. It is consumed only by claim bootstrap.
func (s *Service) SetupClaimAuthority(ctx context.Context, secret string) (PendingAccountSetup, string, error) {
	if len(secret) != 43 {
		return PendingAccountSetup{}, "", ErrUnauthorized
	}
	read, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return PendingAccountSetup{}, "", e
	}
	defer read.Rollback()
	var out PendingAccountSetup
	var hash string
	e = read.Tx().QueryRowContext(ctx, `SELECT operation_id,revision,setup_hash FROM onboarding_state_v1 WHERE singleton=1 AND auth_mode='hosted' AND ready=0 AND NOT EXISTS(SELECT 1 FROM accounts)`).Scan(&out.RequestID, &out.Revision, &hash)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			e = ErrUnauthorized
		}
		return out, "", e
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(s.setupHash("initialize-secret", secret))) != 1 {
		return out, "", ErrUnauthorized
	}
	out.ServerID = s.serverID
	out.AuthMode = "hosted"
	out.ClaimRequired = true
	return out, hash, read.Commit()
}
func (s *Service) CheckSetupClaimTx(ctx context.Context, tx *sql.Tx, requestID string, revision int64, proofHash string) error {
	var hash string
	e := tx.QueryRowContext(ctx, `SELECT setup_hash FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND revision=? AND auth_mode='hosted' AND ready=0 AND NOT EXISTS(SELECT 1 FROM accounts)`, requestID, revision).Scan(&hash)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		return e
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(proofHash)) != 1 {
		return ErrUnauthorized
	}
	return ctx.Err()
}

// CompleteAccountSetup requires both the installed, validated claim's account
// and that same account's fresh Hosted identity assertion. Claim approval alone
// contains no username: no display name, email or guessed owner is substituted.
func (s *Service) CompleteAccountSetup(ctx context.Context, requestID, secret string, who PorticoIdentity) (DirectSignIn, error) {
	pending, proof, e := s.SetupClaimAuthority(ctx, secret)
	if e != nil || pending.RequestID != requestID {
		return DirectSignIn{}, ErrUnauthorized
	}
	if !validHostedAccountID(who.AccountID) || len(who.Username) < 3 || len(who.Username) > 64 || strings.ContainsAny(who.Username, " \t\r\n@") || !directName(who.Username) || who.Consent == "" || who.SignerKeyID == "" {
		return DirectSignIn{}, ErrUnauthorized
	}
	var out DirectSignIn
	e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var err error
		out, err = s.completeAccountSetupTx(ctx, tx, pending, proof, who)
		return err
	})
	if e != nil {
		return DirectSignIn{}, e
	}
	return out, nil
}

// completeAccountSetupTx is the single owner-admission boundary for both the
// existing completion API and the protected setup continuation. It uses the
// caller's writer so challenge spending and durable adoption commit together.
func (s *Service) completeAccountSetupTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof string, who PorticoIdentity) (DirectSignIn, error) {
	if !validHostedAccountID(who.AccountID) || len(who.Username) < 3 || len(who.Username) > 64 || strings.ContainsAny(who.Username, " \t\r\n@") || !directName(who.Username) || who.Consent == "" || who.SignerKeyID == "" {
		return DirectSignIn{}, ErrUnauthorized
	}
	requestID := pending.RequestID
	var out DirectSignIn
	var e error
	e = func() error {
		if who.Return != nil {
			if who.Return.Purpose != "setup" {
				return ErrUnauthorized
			}
			if e := CheckPorticoReturnTx(ctx, tx, who); e != nil {
				return e
			}
		}
		if e := s.CheckSetupClaimTx(ctx, tx, requestID, pending.Revision, proof); e != nil {
			return e
		}
		if s.SetupClaimReadyTx == nil {
			return ErrUnauthorized
		}
		installed, e := s.SetupClaimReadyTx(ctx, tx)
		if e != nil {
			return e
		}
		if !installed {
			return ErrUnauthorized
		}
		var account string
		e = tx.QueryRowContext(ctx, `SELECT i.account_id FROM networking_claim_identity c JOIN networking_claim_intents i ON i.operation_id=c.installed_operation_id AND i.operation_id=c.active_operation_id AND i.server_id=c.server_id AND i.public_key=c.public_key AND i.local_generation=c.reset_generation WHERE c.singleton=1 AND i.stage='installed' AND i.installation_acknowledged=1 AND i.setup_operation_id=?`, requestID).Scan(&account)
		if e != nil || account != who.AccountID {
			return ErrUnauthorized
		}
		if e = admitPorticoSignerTx(ctx, tx, who.SignerKeyID); e != nil {
			return e
		}
		aid, pid := Token(), Token()
		if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'',?)`, aid, who.Username, pid); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO account_portico_links(account_id,hosted_account_id,consent) VALUES(?,?,?)`, aid, who.AccountID, who.Consent); e != nil {
			return e
		}
		name := who.DisplayName
		if !directName(name) {
			name = who.Username
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=?`, name, pid); e != nil {
			return e
		}
		out, e = s.PorticoSignInTx(ctx, tx, who)
		if e != nil {
			return e
		}
		if out.Session == nil {
			return ErrUnauthorized
		}
		receipt, e := s.sealSetup(*out.Session, "initialize:"+requestID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE onboarding_state_v1 SET ready=1,revision=revision+1,receipt=?,receipt_until=? WHERE singleton=1`, receipt, time.Now().Unix()+setupReceiptSeconds)
		if e == nil && who.Return != nil {
			e = CheckPorticoReturnTx(ctx, tx, who)
		}
		return e
	}()
	if e != nil {
		return DirectSignIn{}, e
	}
	return out, nil
}
