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
	// O-169, asked beside each other in setup step 1. Empty takes the default:
	// Remote access On for a Portico Account server, Off for local sign-in;
	// Remote security Enhanced.
	RemoteAccess   string `json:"remoteAccess,omitempty"`
	RemoteSecurity string `json:"remoteSecurity,omitempty"`
}

// ErrSetupRemoteUnwired: this server has no writer for setup's remote choices.
var ErrSetupRemoteUnwired = errors.New("setup cannot record its remote access choices")

// setupRemoteDefaults fills the O-169 choices setup didn't make.
func setupRemoteDefaults(q *SetupOptions) {
	if q.RemoteAccess == "" {
		q.RemoteAccess = "on"
		if q.AuthMode == "local" {
			q.RemoteAccess = "off"
		}
	}
	if q.RemoteSecurity == "" {
		q.RemoteSecurity = "enhanced"
	}
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
	setupRemoteDefaults(&q)
	if !setupUUID.MatchString(q.RequestID) || (q.AuthMode != "local" && q.AuthMode != "hosted") || !setupLabel(q.Name, 100) ||
		(q.RemoteAccess != "on" && q.RemoteAccess != "private" && q.RemoteAccess != "off") || (q.RemoteSecurity != "enhanced" && q.RemoteSecurity != "standard") {
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
	var existing, mode string
	e := s.db.QueryRowContext(ctx, `SELECT operation_id,auth_mode FROM onboarding_state_v1 WHERE singleton=1`).Scan(&existing, &mode)
	switch {
	case e == nil:
		// A server being set up for a Portico Account never gets a local owner (Justin,
		// 31 Oct). Back (AbandonSetup) returns setup to its start first; local is then a
		// fresh choice with a fresh setup token.
		if mode == "hosted" && q.AuthMode == "local" {
			return SetupResult{}, ErrPorticoSetup
		}
		if existing != q.RequestID || mode != q.AuthMode {
			return SetupResult{}, ErrConflict
		}
		return s.resumeSetup(ctx, q.RequestID, q.SetupToken, requestHash, private)
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
	// The setup row was absent when read; it must still be (setupflow S4, S5, S8r).
	event := setupflow.EventInitializeHosted
	if q.AuthMode == "local" {
		event = setupflow.EventInitializeLocal
	}
	if state, _, e := setupflow.Require(ctx, tx, time.Now(), nil, event); e != nil || state.Phase != setupflow.Open {
		return SetupResult{}, setupRefusal(state, e)
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, q.Name); e != nil {
		return SetupResult{}, e
	}
	// Remote access and Remote security (O-169) commit with setup itself. A
	// server without the writer can't be set up (fail closed, as for
	// SetupClaimReadyTx): it would be set up without the person's choices.
	if s.SetupRemoteTx == nil {
		return SetupResult{}, ErrSetupRemoteUnwired
	}
	if e = s.SetupRemoteTx(ctx, tx, q.RemoteAccess, q.RemoteSecurity); e != nil {
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

// ErrPorticoSetup refuses a local owner while the server is being set up for a
// Portico Account (setup_portico_mode).
var ErrPorticoSetup = setupflow.ErrPorticoSetup

// setupRefusal turns a setupflow refusal into what setup callers already answer:
// in progress (with when it can start over), already set up, or the Portico refusal.
func setupRefusal(state setupflow.State, err error) error {
	switch {
	case err == nil:
		return ErrConflict
	case errors.Is(err, setupflow.ErrInProgress):
		live := state.Phase == setupflow.Started || state.Phase == setupflow.AwaitingApproval
		if live && !state.Cancelling {
			return &SetupInProgressError{Until: state.ExpiresAt}
		}
		return &SetupInProgressError{}
	case errors.Is(err, setupflow.ErrPorticoSetup):
		return ErrPorticoSetup
	case errors.Is(err, setupflow.ErrAlreadyComplete), errors.Is(err, setupflow.ErrInconsistent), errors.Is(err, setupflow.ErrStateChanged):
		return ErrConflict
	}
	return err
}

// supersedeStaleSetup retires the pending Portico Account setup when it can no longer
// finish: never approved, never installed, and past its window (setupflow S1). A setup
// still in progress refuses with SetupInProgressError (with a time while it is unapproved
// and live); a completed or inconsistent one with ErrConflict.
//
// It returns the new setup secret, written (atomically, inside the same fence) only when
// it retired the old attempt; "" when there was nothing to retire, so the caller reads
// the secret the file already holds.
func (s *Service) supersedeStaleSetup(ctx context.Context) (string, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return "", err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	state, _, err := setupflow.Require(ctx, tx, time.Now(), nil, setupflow.EventStart)
	if err != nil {
		return "", setupRefusal(state, err)
	}
	if state.Phase == setupflow.Open {
		return "", gated.Commit()
	}
	token, err := s.retireSetupTx(ctx, tx, state, "superseded")
	if err != nil {
		return "", err
	}
	return token, gated.Commit()
}

// AbandonSetup is Back from a Portico Account setup that was never approved
// (setupflow S8): the server returns to the start of setup and offers both kinds
// again. Its claim is retired here alone; nothing is sent to Portico Services, then
// or later, because the server may next be set up for local sign-in. The request on
// Portico Services expires by itself and an approval of it is never applied.
//
// A replay after success finds setup already open and answers it. stop is called
// with the retired claim operation, after commit, to end anything still waiting on it.
func (s *Service) AbandonSetup(ctx context.Context, requestID, secret string, revision int64, stop func(operationID string)) (setupflow.State, error) {
	if !setupUUID.MatchString(requestID) || len(secret) != 43 || revision < 1 {
		return setupflow.State{}, ErrUnauthorized
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return setupflow.State{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	state, _, err := setupflow.Require(ctx, tx, time.Now(), nil, setupflow.EventAbandon)
	if state.Phase == setupflow.Open {
		return state, gated.Commit()
	}
	if err != nil && !errors.Is(err, setupflow.ErrStateChanged) {
		return state, err
	}
	var hash string
	if e := tx.QueryRowContext(ctx, `SELECT setup_hash FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND auth_mode='hosted'`, requestID).Scan(&hash); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return setupflow.State{}, ErrUnauthorized
		}
		return setupflow.State{}, e
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(s.setupHash("initialize-secret", secret))) != 1 {
		return setupflow.State{}, ErrUnauthorized
	}
	if err != nil {
		// Approved or later: setup finishes; the owner can switch kinds afterwards.
		return state, err
	}
	if state.Revision != revision {
		return state, setupflow.ErrStateChanged
	}
	if _, err = s.retireSetupTx(ctx, tx, state, "cancelled"); err != nil {
		return setupflow.State{}, err
	}
	if err = gated.Commit(); err != nil {
		return setupflow.State{}, err
	}
	if state.Operation != nil && stop != nil {
		stop(state.Operation.ID)
	}
	return setupflow.State{Phase: setupflow.Open}, nil
}

// ErrSetupClaimLive refuses a start over while the setup's approved claim is still in
// force on this server: the caller withdraws it (networking RetireSetupClaim) first.
var ErrSetupClaimLive = errors.New("setup claim still live")

// StartOverSetup ends any unfinished setup and returns a fresh setup secret (finding #2:
// "switch or start over"). It needs no secret from the earlier attempt: whoever may start
// setup may start it over. The earlier signed starts end as superseded, so their return
// and owner exchange can never complete, and the onboarding row goes. An unapproved claim
// is retired here alone, as Abandon; an approved or installed one must already be
// withdrawing (its deny-only cancellation queued for Portico Services), else
// ErrSetupClaimLive. A set-up server refuses as a start does.
func (s *Service) StartOverSetup(ctx context.Context) (string, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return "", err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	state, _, err := setupflow.Require(ctx, tx, time.Now(), nil, setupflow.EventStartOver)
	if err != nil {
		return "", setupRefusal(state, err)
	}
	if state.Phase == setupflow.Open {
		if err = gated.Commit(); err != nil {
			return "", err
		}
		return s.BrowserSetupToken(ctx)
	}
	retire := state
	if op := state.Operation; op != nil {
		var account string
		var approval int64
		if err = tx.QueryRowContext(ctx, `SELECT account_id,approval_revision FROM networking_claim_intents WHERE operation_id=?`, op.ID).Scan(&account, &approval); err != nil {
			return "", err
		}
		if account != "account-unbound" || approval != 0 {
			if !state.Cancelling {
				return "", ErrSetupClaimLive
			}
			// An approved claim's queued withdrawal is Portico Services' only notice:
			// it stays, and ends the claim when delivered.
			retire.Operation = nil
		}
	}
	// Every signed start of this request ends, installed ones included: an installed
	// claim's withdrawal is already queued, so none of them may admit an owner. Their
	// identity challenges are spent with them.
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash IN(SELECT server_challenge_hash FROM identity_setup_completions WHERE setup_request_id=? AND phase IN('prepared','expired','installed','consumed'))`, state.RequestID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase='superseded' WHERE setup_request_id=? AND phase IN('prepared','expired','installed','consumed')`, state.RequestID); err != nil {
		return "", err
	}
	token, err := s.retireSetupTx(ctx, tx, retire, "superseded")
	if err != nil {
		return "", err
	}
	return token, gated.Commit()
}

// retireSetupTx retires an unapproved Portico Account setup on this server alone:
// its signed starts end as phase, its claim (still exactly as prepared, or being
// withdrawn) is cancelled with any queued withdrawal dropped, and the onboarding row
// goes. It writes and returns a fresh setup secret. Nothing here reaches Portico Services.
func (s *Service) retireSetupTx(ctx context.Context, tx *sql.Tx, state setupflow.State, phase string) (string, error) {
	queries := []struct {
		query string
		args  []any
	}{
		{`UPDATE identity_setup_completions SET phase=? WHERE setup_request_id=? AND phase IN('prepared','expired')`, []any{phase, state.RequestID}},
		{`DELETE FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND auth_mode='hosted' AND ready=0`, []any{state.RequestID}},
	}
	if op := state.Operation; op != nil {
		queries = append([]struct {
			query string
			args  []any
		}{
			{`DELETE FROM networking_claim_cancellations WHERE operation_id=? AND state='pending'`, []any{op.ID}},
			{`UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=? AND stage IN('prepared','cancel_pending','cancelled') AND account_id='account-unbound' AND approval_revision=0`, []any{op.ID}},
			{`UPDATE networking_claim_identity SET active_operation_id=NULL,pending_operation_id=CASE WHEN pending_operation_id=? THEN NULL ELSE pending_operation_id END WHERE singleton=1 AND active_operation_id=? AND installed_operation_id IS NULL`, []any{op.ID, op.ID}},
		}, queries...)
	}
	for _, q := range queries {
		if _, err := tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return "", err
		}
	}
	token := Token()
	if err := writeSetupSecret(s.setupPath, token); err != nil {
		return "", err
	}
	return token, nil
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
