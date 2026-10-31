package setupflow_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

const (
	serverID = "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY"
	request  = "11111111-2222-4333-8444-555555555555"
	op       = "op_setup"
)

// fixture is a fresh server with no owner, as first start leaves it.
type fixture struct {
	t     *testing.T
	db    *sql.DB
	s     *identity.Service
	token string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f','setupflow-test-key','2026-10-31T00:00:00Z');
 INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',0);
 INSERT INTO configuration(key,value) VALUES('id',?)`, serverID, serverID, serverID); err != nil {
		t.Fatal(err)
	}
	s, err := identity.New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'portico') ON CONFLICT(singleton) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, db: db, s: s}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// hosted starts a Portico Account setup through the real setup path.
func (f *fixture) hosted() {
	f.t.Helper()
	ctx := context.Background()
	token, err := f.s.BrowserSetupToken(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.token = token
	if _, err = f.s.SetupWithOptions(ctx, identity.SetupOptions{RequestID: request, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		f.t.Fatal(err)
	}
}

// claim gives the setup an active claim operation at stage, as the claim coordinator writes it.
func (f *fixture) claim(stage, account string, approval, acknowledged int) {
	f.t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	generation := ""
	if stage == "retrieving" || stage == "installed" {
		generation = "1"
	}
	f.exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,setup_operation_id,created_at,updated_at,purpose,approval_revision,claim_generation,credential_generation,installation_acknowledged)
 VALUES(?,?,?,X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',0,1,?,'','',1,?,?,?,'setup',?,?,?,?)`,
		op, serverID, account, stage, request, now, now, approval, generation, generation, acknowledged)
	f.exec(`UPDATE networking_claim_identity SET active_operation_id=? WHERE singleton=1`, op)
	if stage == "installed" {
		f.exec(`UPDATE networking_claim_identity SET installed_operation_id=? WHERE singleton=1`, op)
	}
}

// completion records the setup's signed start for the active claim.
func (f *fixture) completion(phase, attempt string, expires time.Time) {
	f.t.Helper()
	created := expires.Add(-claimweb.SetupLifetime()).UnixMilli()
	f.exec(`INSERT INTO identity_setup_completions(operation_id,setup_request_id,setup_revision,setup_proof,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms,attempt_id)
 VALUES(?,?,1,'proof',?,?,X'7b7d',zeroblob(64),X'7b7d',zeroblob(64),'state',?,'installation','challenge',?,?,?)`,
		op, request, phase, repeat43("h"), repeat43("c"), created, expires.UnixMilli(), attempt)
}

// age moves the start of setup back by d, as a person walking away does.
func (f *fixture) age(d time.Duration) {
	f.exec(`UPDATE onboarding_state_v1 SET receipt_until=receipt_until-?`, int64(d/time.Second))
}

func (f *fixture) derive(lookup setupflow.DeliveryLookup) setupflow.State {
	f.t.Helper()
	state, err := setupflow.Derive(context.Background(), f.db, time.Now(), lookup)
	if err != nil {
		f.t.Fatalf("derive: %v", err)
	}
	return state
}

func repeat43(c string) string {
	out := ""
	for len(out) < 43 {
		out += c
	}
	return out
}

// startOutcome is what today's browser start does with these rows.
func (f *fixture) startOutcome() error {
	_, err := f.s.BrowserSetupToken(context.Background())
	return err
}

func TestAFreshServerIsOpenAndStarts(t *testing.T) {
	f := newFixture(t)
	if got := f.derive(nil); got.Phase != setupflow.Open || got.Phase.Coarse() != setupflow.CoarseReady {
		t.Fatalf("got %+v", got)
	}
	if err := f.startOutcome(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func TestAStartedSetupHoldsItsWindowExactlyAsAStartJudgesIt(t *testing.T) {
	f := newFixture(t)
	f.hosted()
	got := f.derive(nil)
	if got.Phase != setupflow.Started || got.RequestID != request || got.AuthMode != "hosted" || got.Revision != 1 || got.Operation != nil {
		t.Fatalf("got %+v", got)
	}
	var progress *identity.SetupInProgressError
	if err := f.startOutcome(); !errors.As(err, &progress) || !progress.Until.Equal(got.ExpiresAt) {
		t.Fatalf("start says %v; derive says window ends %v", err, got.ExpiresAt)
	}
	if left := time.Until(got.ExpiresAt); left <= claimweb.SetupLifetime()-time.Minute || left > claimweb.SetupLifetime() {
		t.Fatalf("window %v from now", left)
	}
}

func TestAnAgedUnapprovedSetupIsExpiredAndTheNextStartSupersedesIt(t *testing.T) {
	for _, withClaim := range []bool{false, true} {
		f := newFixture(t)
		f.hosted()
		if withClaim {
			f.claim("prepared", "account-unbound", 0, 0)
			if got := f.derive(nil); got.Phase != setupflow.AwaitingApproval || got.Operation == nil || got.Operation.ID != op {
				t.Fatalf("claim=%v: got %+v", withClaim, got)
			}
		}
		f.age(time.Hour)
		if got := f.derive(nil); got.Phase != setupflow.Expired || got.Phase.Coarse() != setupflow.CoarseReady {
			t.Fatalf("claim=%v: got %+v", withClaim, got)
		}
		if err := f.startOutcome(); err != nil {
			t.Fatalf("claim=%v: start: %v", withClaim, err)
		}
		if got := f.derive(nil); got.Phase != setupflow.Open {
			t.Fatalf("claim=%v: after supersede %+v", withClaim, got)
		}
	}
}

func TestTheNewestSignedStartSetsTheApprovalWindow(t *testing.T) {
	f := newFixture(t)
	f.hosted()
	f.claim("prepared", "account-unbound", 0, 0)
	expires := time.Now().Add(10 * time.Minute).Truncate(time.Millisecond)
	f.completion("prepared", "", expires)
	f.age(time.Hour) // the onboarding row's own window no longer counts
	got := f.derive(nil)
	if got.Phase != setupflow.AwaitingApproval || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("got %+v, want awaiting until %v", got, expires)
	}
	f.exec(`UPDATE identity_setup_completions SET expires_ms=?,created_ms=?`, time.Now().Add(-time.Second).UnixMilli(), time.Now().Add(-time.Minute).UnixMilli())
	if got = f.derive(nil); got.Phase != setupflow.Expired {
		t.Fatalf("lapsed start: %+v", got)
	}
}

func TestApprovedAndFinishingSetupsAreInProgressAndExhaustedWhenNothingDelivers(t *testing.T) {
	cases := []struct {
		stage    string
		account  string
		approval int
		ack      int
		want     setupflow.Phase
	}{
		{"prepared", "account-unbound", 3, 0, setupflow.Approved},
		{"prepared", "acct_owner", 0, 0, setupflow.Approved},
		{"approved", "acct_owner", 1, 0, setupflow.Approved},
		{"finalizing", "acct_owner", 1, 0, setupflow.Finishing},
		{"retrieving", "acct_owner", 1, 0, setupflow.Finishing},
		{"installed", "acct_owner", 1, 0, setupflow.Finishing},
	}
	for _, c := range cases {
		f := newFixture(t)
		f.hosted()
		f.claim(c.stage, c.account, c.approval, c.ack)
		if got := f.derive(nil); got.Phase != c.want || got.Delivery != nil {
			t.Errorf("%s: untracked got %+v, want %s", c.stage, got, c.want)
		}
		since := time.Now().Add(-3 * time.Minute)
		running := func(string) (setupflow.Delivery, bool) {
			return setupflow.Delivery{Running: true, Since: since, Attempts: 4}, true
		}
		if got := f.derive(running); got.Phase != c.want || got.Delivery == nil || !got.Delivery.Slow(time.Now()) {
			t.Errorf("%s: running got %+v", c.stage, got)
		}
		stopped := func(string) (setupflow.Delivery, bool) { return setupflow.Delivery{Since: since}, true }
		if got := f.derive(stopped); got.Phase != setupflow.Exhausted || got.Delivery == nil {
			t.Errorf("%s: stopped got %+v", c.stage, got)
		}
		gone := func(string) (setupflow.Delivery, bool) { return setupflow.Delivery{}, false }
		if got := f.derive(gone); got.Phase != setupflow.Exhausted || got.Delivery != nil {
			t.Errorf("%s: untracked by a live lookup got %+v", c.stage, got)
		}
		// A start says in progress, with no moment to start over at.
		var progress *identity.SetupInProgressError
		if err := f.startOutcome(); !errors.As(err, &progress) || !progress.Until.IsZero() {
			t.Errorf("%s: start %v", c.stage, err)
		}
	}
}

func TestAnInstalledSetupReadsItsOwnerAdmissionWindow(t *testing.T) {
	future, past := time.Now().Add(10*time.Minute).Truncate(time.Millisecond), time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	cases := []struct {
		name      string
		phase     string // "" means no signed start
		attempt   string
		expires   time.Time
		want      setupflow.Phase
		renewable bool
	}{
		{"no signed start", "", "", time.Time{}, setupflow.Installed, false},
		{"prepared, live", "prepared", "", future, setupflow.Installed, false},
		{"installed, live", "installed", "", future, setupflow.Installed, false},
		{"installed, lapsed", "installed", "", past, setupflow.InstalledLapsed, true},
		{"prepared, lapsed", "prepared", "", past, setupflow.InstalledLapsed, true},
		{"consumed, live", "consumed", "attempt", future, setupflow.Installed, false},
		// Characterized, not endorsed: today nothing renews these (setup_start_web.go:58).
		{"consumed, lapsed", "consumed", "attempt", past, setupflow.InstalledLapsed, false},
		{"cancelled", "cancelled", "attempt", future, setupflow.InstalledLapsed, false},
		{"expired", "expired", "", past, setupflow.InstalledLapsed, false},
	}
	for _, c := range cases {
		f := newFixture(t)
		f.hosted()
		f.claim("installed", "acct_owner", 1, 1)
		if c.phase != "" {
			f.completion(c.phase, c.attempt, c.expires)
		}
		got := f.derive(nil)
		if got.Phase != c.want || got.Renewable != c.renewable || !got.ExpiresAt.Equal(c.expires) {
			t.Errorf("%s: got %+v, want %s renewable=%v until %v", c.name, got, c.want, c.renewable, c.expires)
		}
		// S3: an installed setup is in progress, never "already set up", and has no time
		// at which another device could start over.
		var progress *identity.SetupInProgressError
		if err := f.startOutcome(); !errors.As(err, &progress) || !progress.Until.IsZero() {
			t.Errorf("%s: start %v", c.name, err)
		}
	}
}

func TestAWithdrawnClaimIsStartedAndCancelling(t *testing.T) {
	for _, stage := range []string{"cancel_pending", "cancelled"} {
		f := newFixture(t)
		f.hosted()
		f.claim(stage, "account-unbound", 0, 0)
		f.age(time.Hour)
		got := f.derive(nil)
		if got.Phase != setupflow.Started || !got.Cancelling {
			t.Errorf("%s: got %+v", stage, got)
		}
		var progress *identity.SetupInProgressError
		if err := f.startOutcome(); !errors.As(err, &progress) {
			t.Errorf("%s: start %v", stage, err)
		}
	}
}

func TestAServerWithAnOwnerIsCompleted(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.SetupWithOptions(context.Background(), identity.SetupOptions{SetupToken: f.mustToken(), Name: "Family server", AuthMode: "local", Username: "owner", Password: "correct horse battery staple 42"}, true); err != nil {
		t.Fatal(err)
	}
	got := f.derive(nil)
	if got.Phase != setupflow.Completed || got.AuthMode != "local" || got.Phase.Coarse() != setupflow.CoarseComplete {
		t.Fatalf("got %+v", got)
	}
}

func (f *fixture) mustToken() string {
	f.t.Helper()
	token, err := f.s.BrowserSetupToken(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return token
}

func TestRowsThatDescribeNoPhaseAreInconsistent(t *testing.T) {
	cases := map[string]func(f *fixture){
		"claim of another request": func(f *fixture) {
			f.hosted()
			f.claim("prepared", "account-unbound", 0, 0)
			f.exec(`UPDATE networking_claim_intents SET setup_operation_id='99999999-2222-4333-8444-555555555555'`)
		},
		"installed pointer on a prepared claim": func(f *fixture) {
			f.hosted()
			f.claim("prepared", "account-unbound", 0, 0)
			f.age(time.Hour)
			f.exec(`UPDATE networking_claim_identity SET installed_operation_id=?`, op)
		},
		"settled start without a claim": func(f *fixture) {
			f.hosted()
			f.claim("prepared", "account-unbound", 0, 0)
			f.completion("installed", "", time.Now().Add(time.Minute))
			f.exec(`UPDATE networking_claim_identity SET active_operation_id=NULL`)
		},
	}
	for name, setup := range cases {
		f := newFixture(t)
		setup(f)
		if _, err := setupflow.Derive(context.Background(), f.db, time.Now(), nil); !errors.Is(err, setupflow.ErrInconsistent) {
			t.Errorf("%s: derive %v", name, err)
		}
		if err := f.startOutcome(); !errors.Is(err, identity.ErrConflict) {
			t.Errorf("%s: start %v", name, err)
		}
	}
}
