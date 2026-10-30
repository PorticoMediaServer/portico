package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

const supersedeServer = "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY"

// pendingHostedSetup leaves the server exactly as an abandoned "Continue to Portico
// Account" does: a hosted onboarding row, no account, and (optionally) its prepared claim.
func pendingHostedSetup(t *testing.T, claim string) (*Service, *sql.DB, string) {
	t.Helper()
	_, db, dir := familyFixture(t)
	if _, err := db.Exec(`DELETE FROM accounts`); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	token, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const operation = "11111111-2222-4333-8444-555555555555"
	if _, err = s.SetupWithOptions(ctx, SetupOptions{RequestID: operation, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	if claim != "" {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = db.Exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,setup_operation_id,created_at,updated_at,purpose,approval_revision)
 VALUES('op_setup',?, 'account-unbound',X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',0,1,?,'','',1,?,?,?,'setup',0)`, supersedeServer, claim, operation, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE networking_claim_identity SET active_operation_id='op_setup' WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
	}
	return s, db, token
}

// age moves the pending setup's start back past the setup window.
func age(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE onboarding_state_v1 SET receipt_until=receipt_until-3600`); err != nil {
		t.Fatal(err)
	}
}

func TestAnExpiredUnapprovedSetupGivesWayToANewBrowserStart(t *testing.T) {
	s, db, old := pendingHostedSetup(t, "prepared")
	ctx := context.Background()
	var progress *SetupInProgressError
	if _, err := s.BrowserSetupToken(ctx); !errors.As(err, &progress) || !progress.Until.After(time.Now()) {
		t.Fatalf("a live attempt from another browser was not refused honestly: %v", err)
	}
	age(t, db)
	token, err := s.BrowserSetupToken(ctx)
	if err != nil || token == "" || token == old {
		t.Fatalf("an expired, never-approved attempt still blocks a new start: %v", err)
	}
	var stage string
	var active sql.NullString
	if err = db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id='op_setup'`).Scan(&stage); err != nil || stage != "cancelled" {
		t.Fatal("the stale claim was not retired: ", stage, err)
	}
	if err = db.QueryRow(`SELECT active_operation_id FROM networking_claim_identity`).Scan(&active); err != nil || active.Valid {
		t.Fatal("the stale claim still holds the server: ", active, err)
	}
	// The new attempt starts like a fresh server; the old secret starts nothing.
	if _, err = s.SetupWithOptions(ctx, SetupOptions{RequestID: "99999999-2222-4333-8444-555555555555", SetupToken: old, Name: "Again", AuthMode: "hosted"}, true); err == nil {
		t.Fatal("the superseded secret still started setup")
	}
	if _, err = s.SetupWithOptions(ctx, SetupOptions{RequestID: "99999999-2222-4333-8444-555555555555", SetupToken: token, Name: "Again", AuthMode: "hosted"}, true); err != nil {
		t.Fatal("the new attempt could not start: ", err)
	}
}

func TestAPendingSetupWithALiveStartIsNotSuperseded(t *testing.T) {
	s, db, _ := pendingHostedSetup(t, "prepared")
	age(t, db)
	until := time.Now().Add(10 * time.Minute).UnixMilli()
	h43, sig := strings.Repeat("h", 43), make([]byte, 64)
	if _, err := db.Exec(`INSERT INTO identity_setup_completions(operation_id,setup_request_id,setup_revision,setup_proof,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms)
 VALUES('op_setup','11111111-2222-4333-8444-555555555555',1,'p','prepared',?,X'00',?,X'00',?,'s',?,'i','c',?,?)`, h43, sig, sig, h43, until-60000, until); err != nil {
		t.Fatal(err)
	}
	var progress *SetupInProgressError
	if _, err := s.BrowserSetupToken(context.Background()); !errors.As(err, &progress) {
		t.Fatalf("a start still inside its window was superseded: %v", err)
	}
}

// Superseding can never take over a claim that was approved or installed: those keep
// O-163's renewal path, and a new browser start is refused as before.
func TestAnApprovedOrInstalledSetupIsNeverSuperseded(t *testing.T) {
	for _, stage := range []string{"approved", "finalizing"} {
		t.Run(stage, func(t *testing.T) {
			s, db, _ := pendingHostedSetup(t, stage)
			age(t, db)
			// Approved and finishing reads as in progress (it can still complete), with no
			// moment to start over at, and is never superseded.
			var progress *SetupInProgressError
			if _, err := s.BrowserSetupToken(context.Background()); !errors.As(err, &progress) || !progress.Until.IsZero() {
				t.Fatalf("a %s claim was superseded or misreported: %v", stage, err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM onboarding_state_v1`).Scan(&count); err != nil || count != 1 {
				t.Fatal("the pending setup was removed: ", count, err)
			}
		})
	}
	s, db, _ := pendingHostedSetup(t, "prepared")
	age(t, db)
	if _, err := db.Exec(`UPDATE networking_claim_identity SET installed_operation_id='op_setup'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BrowserSetupToken(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatalf("an installed claim was superseded: %v", err)
	}
	if _, err := db.Exec(`UPDATE networking_claim_identity SET installed_operation_id=NULL; UPDATE networking_claim_intents SET account_id='acct_owner'`); err != nil {
		t.Fatal(err)
	}
	var progress *SetupInProgressError
	if _, err := s.BrowserSetupToken(context.Background()); !errors.As(err, &progress) {
		t.Fatalf("a claim bound to an account was superseded: %v", err)
	}
}

// Two browsers starting together after expiry: one retires the old attempt and writes the
// new secret; the other reads that same secret, never a second one that silently replaces it.
func TestConcurrentStartsAfterExpiryShareOneSecret(t *testing.T) {
	s, db, _ := pendingHostedSetup(t, "prepared")
	age(t, db)
	ctx := context.Background()
	first, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BrowserSetupToken(ctx)
	if err != nil || second != first {
		t.Fatalf("a second start replaced the secret: %v", err)
	}
}
