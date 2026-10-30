package identity

// P2-62 installation lifetime and registration CAS tests. They call the service
// directly: registration is an internal API, not a route.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type installationRow struct {
	incarnation string
	revision    int64
	nonce       string
	root        string
	retired     int
}

func registrationFixture(t *testing.T) (*Service, *sql.DB, DirectSignIn, string) {
	t.Helper()
	s, db, _ := directFixture(t)
	installation := Token()
	ctx, err := WithIssuingDevice(context.Background(), registration(installation), "")
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.DirectLoginFrom(ctx, "owner", "Testing1!", true)
	if err != nil {
		t.Fatal(err)
	}
	if login.Session == nil {
		t.Fatal("the registering device has no session")
	}
	return s, db, login, installation
}

func mutationScopeFor(t *testing.T, db *sql.DB, login DirectSignIn, installation string) MutationScope {
	t.Helper()
	var account, profile string
	if err := db.QueryRow(`SELECT account_id,profile_id FROM authorization_session_families WHERE id=?`, login.Session.SessionFamilyID).Scan(&account, &profile); err != nil {
		t.Fatal(err)
	}
	return MutationScope{Authority: "local", AccountID: account, ProfileID: profile, FamilyID: login.Session.SessionFamilyID, InstallationID: installation}
}

func registerMutationRoot(t *testing.T, s *Service, db *sql.DB, scope MutationScope, q MutationRegistration) (MutationRoot, bool, error) {
	t.Helper()
	var root MutationRoot
	var replayed bool
	err := dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassSecurityFence, func(ctx context.Context, tx *sql.Tx) error {
		var e error
		root, replayed, e = s.RegisterMutationRootTx(ctx, tx, scope, q)
		return e
	})
	return root, replayed, err
}

func installationRowFor(t *testing.T, db *sql.DB, installation string) installationRow {
	t.Helper()
	var row installationRow
	if err := db.QueryRow(`SELECT incarnation,registration_revision,registration_nonce,registration_root,retired FROM identity_installations WHERE installation_hash=?`, mutationInstallationHash(installation)).Scan(&row.incarnation, &row.revision, &row.nonce, &row.root, &row.retired); err != nil {
		t.Fatal(err)
	}
	return row
}

func lifetimeRoots(t *testing.T, db *sql.DB, state string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT count(*) FROM user_mutation_roots WHERE state=?`, state).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRegistrationCommitsOneRootAndReplaysWithoutAdvancing(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	incarnation, nonce := installationRowFor(t, db, installation).incarnation, newMutationID()
	first, replayed, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	if replayed {
		t.Fatal("the first registration reported a replay")
	}
	if first.State != "active" || first.RegistrationRevision != 1 || first.InstallationIncarnation != incarnation {
		t.Fatalf("first root %+v", first)
	}
	if n := lifetimeRoots(t, db, "active"); n != 1 {
		t.Fatalf("active roots=%d", n)
	}
	// The exact expected revision and nonce replay the same result.
	again, replayed, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: nonce})
	if err != nil || !replayed || again.ID != first.ID || again.RegistrationRevision != 1 {
		t.Fatalf("lost-response replay %+v %v %v", again, replayed, err)
	}
	// A repeated nonce at the current revision is the same result and does not
	// advance the installation to another revision.
	current, replayed, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 1, Nonce: nonce})
	if err != nil || !replayed || current.ID != first.ID || current.RegistrationRevision != 1 {
		t.Fatalf("repeated nonce at the current revision advanced %+v %v %v", current, replayed, err)
	}
	if n := lifetimeRoots(t, db, "active"); n != 1 {
		t.Fatalf("repeated nonce created a root: active=%d", n)
	}
	// The same revision under a different nonce was already committed by someone
	// else: conflict, and nothing advanced.
	if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrRegistrationNonce) {
		t.Fatalf("different nonce at a committed revision: %v", err)
	}
	// A lifetime that already granted a root cannot change its incarnation.
	if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: newMutationIncarnation(), ExpectedRevision: 1, Nonce: newMutationID()}); !errors.Is(err, ErrInstallationIncarnation) {
		t.Fatalf("a granted lifetime changed incarnation: %v", err)
	}
	// An expectation that is not the current revision is stale.
	if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 5, Nonce: newMutationID()}); !errors.Is(err, ErrRegistrationStale) {
		t.Fatalf("stale expectation: %v", err)
	}
	watermark := installationRowFor(t, db, installation)
	if watermark.revision != 1 || watermark.nonce != nonce || watermark.root != first.ID || watermark.incarnation != incarnation || watermark.retired != 0 {
		t.Fatalf("watermark %+v", watermark)
	}
	var read MutationLifetimes
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		var e error
		read, e = s.MutationLifetimesTx(ctx, dbwork.Snapshot(ctx), installation)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if read.RegistrationRevision != 1 || read.RootID != first.ID || read.Retired {
		t.Fatalf("read lifetimes %+v", read)
	}
}

func TestRegistrationSwitchDrainsOneRootAndKeepsOneSlot(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	incarnation := installationRowFor(t, db, installation).incarnation
	first, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: newMutationID()})
	if err != nil {
		t.Fatal(err)
	}
	secondProfile := Token()
	if _, err := db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,?,'Second')`, secondProfile, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE authorization_session_families SET profile_id=?,role='member' WHERE id=?`, secondProfile, scope.FamilyID); err != nil {
		t.Fatal(err)
	}
	moved := scope
	moved.ProfileID = secondProfile
	second, _, err := registerMutationRoot(t, s, db, moved, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 1, Nonce: newMutationID()})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("a scope switch reused the drained root")
	}
	if n := lifetimeRoots(t, db, "active"); n != 1 {
		t.Fatalf("active roots=%d", n)
	}
	if n := lifetimeRoots(t, db, "draining"); n != 1 {
		t.Fatalf("draining roots=%d", n)
	}
	// A drained root that retains no receipt is removed by the next switch, which
	// still holds exactly one active and one draining root.
	if _, err := db.Exec(`UPDATE authorization_session_families SET profile_id=?,role='member' WHERE id=?`, scope.ProfileID, scope.FamilyID); err != nil {
		t.Fatal(err)
	}
	third, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 2, Nonce: newMutationID()})
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.ID == second.ID {
		t.Fatal("a drained root was reused")
	}
	if n := lifetimeRoots(t, db, "active"); n != 1 {
		t.Fatalf("active roots=%d", n)
	}
	if n := lifetimeRoots(t, db, "draining"); n != 1 {
		t.Fatalf("draining roots=%d", n)
	}
}

func TestRegistrationRefusesAThirdSlotWhileTheDrainedRootRetainsReceipts(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	incarnation := installationRowFor(t, db, installation).incarnation
	first, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: newMutationID()})
	if err != nil {
		t.Fatal(err)
	}
	secondProfile := Token()
	if _, err := db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,?,'Second')`, secondProfile, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE authorization_session_families SET profile_id=?,role='member' WHERE id=?`, secondProfile, scope.FamilyID); err != nil {
		t.Fatal(err)
	}
	moved := scope
	moved.ProfileID = secondProfile
	if _, _, err = registerMutationRoot(t, s, db, moved, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 1, Nonce: newMutationID()}); err != nil {
		t.Fatal(err)
	}
	// The drained root now retains a result that has not been acknowledged.
	var scopeID int64
	if _, err = db.Exec(`INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES('local',?,?,0)`, scope.AccountID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM user_mutation_accounts WHERE authority='local' AND account_id=?`, scope.AccountID).Scan(&scopeID); err != nil {
		t.Fatal(err)
	}
	receipt := fmt.Sprintf("p251_%013d_%032x", 1793196000000, 1)
	if _, err = db.Exec(`INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,operation,digest,is_stop,issued_ms,status) VALUES(?,?,?,?,?,?,?,?,?,0,?,'applied')`, receipt, scopeID, scope.ProfileID, scope.FamilyID, profileLifetimeValue(t, db, scope.AccountID, scope.ProfileID), incarnation, first.ID, "delete_personal_item", strings.Repeat("a", 64), 1793196000000); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE authorization_session_families SET profile_id=?,role='member' WHERE id=?`, scope.ProfileID, scope.FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 2, Nonce: newMutationID()}); !errors.Is(err, ErrMutationRootSlot) {
		t.Fatalf("a third root stole the retained slot: %v", err)
	}
	if n := lifetimeRoots(t, db, "active"); n != 1 {
		t.Fatalf("active roots=%d", n)
	}
	if n := lifetimeRoots(t, db, "draining"); n != 1 {
		t.Fatalf("draining roots=%d", n)
	}
}

func profileLifetimeValue(t *testing.T, db *sql.DB, account, profile string) string {
	t.Helper()
	var lifetime string
	if err := db.QueryRow(`SELECT lifetime FROM direct_profiles WHERE account_id=? AND id=?`, account, profile).Scan(&lifetime); err != nil {
		t.Fatal(err)
	}
	return lifetime
}

func TestRegistrationReplayAfterRootDeletionIsGoneAndNeverReallocates(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	incarnation, nonce := installationRowFor(t, db, installation).incarnation, newMutationID()
	first, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM user_mutation_roots WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: nonce}); !errors.Is(err, ErrMutationRootGone) {
		t.Fatalf("a deleted root was replayed: %v", err)
	}
	var roots int64
	if err = db.QueryRow(`SELECT count(*) FROM user_mutation_roots`).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if roots != 0 {
		t.Fatal("a registration attempt allocated a replacement root")
	}
	if watermark := installationRowFor(t, db, installation); watermark.root != first.ID || watermark.revision != 1 {
		t.Fatalf("watermark %+v", watermark)
	}
}

func TestRegistrationNeverCreatesAnUnknownInstallation(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	unknown := Token()
	probe := scope
	probe.InstallationID = unknown
	if _, _, err := registerMutationRoot(t, s, db, probe, MutationRegistration{Incarnation: newMutationIncarnation(), ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrInstallationUnknown) {
		t.Fatalf("unknown installation: %v", err)
	}
	var n int64
	if err := db.QueryRow(`SELECT count(*) FROM identity_installations WHERE installation_hash=?`, mutationInstallationHash(unknown)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a registration attempt created an installation lifetime")
	}
	before := installationRowFor(t, db, installation)
	// Re-registering the same device keeps one row and the same lifetime.
	if _, err := s.RegisterDevice(context.Background(), login.AccountToken, registration(installation), ""); err != nil {
		t.Fatal(err)
	}
	if after := installationRowFor(t, db, installation); after != before {
		t.Fatalf("device re-registration rotated the lifetime: %+v -> %+v", before, after)
	}
}

func TestRegistrationRequiresTheExactLiveScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	t.Run("another device", func(t *testing.T) {
		s, db, login, installation := registrationFixture(t)
		scope := mutationScopeFor(t, db, login, installation)
		otherInstallation := Token()
		ctx, err := WithIssuingDevice(context.Background(), registration(otherInstallation), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DirectLoginFrom(ctx, "owner", "Testing1!", true); err != nil {
			t.Fatal(err)
		}
		scope.InstallationID = otherInstallation
		if _, _, err = registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: installationRowFor(t, db, scope.InstallationID).incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrMutationRootScope) {
			t.Fatalf("another device's installation registered this family: %v", err)
		}
	})
	t.Run("revoked family", func(t *testing.T) {
		s, db, login, installation := registrationFixture(t)
		scope := mutationScopeFor(t, db, login, installation)
		if _, err := db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, scope.FamilyID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: installationRowFor(t, db, scope.InstallationID).incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrMutationRootScope) {
			t.Fatalf("a revoked family registered a root: %v", err)
		}
	})
	t.Run("erased profile", func(t *testing.T) {
		s, db, login, installation := registrationFixture(t)
		scope := mutationScopeFor(t, db, login, installation)
		if _, err := db.Exec(`UPDATE direct_profiles SET deleted=1 WHERE account_id=? AND id=?`, scope.AccountID, scope.ProfileID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: installationRowFor(t, db, scope.InstallationID).incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrMutationRootScope) {
			t.Fatalf("an erased profile registered a root: %v", err)
		}
	})
	t.Run("disabled membership", func(t *testing.T) {
		s, db, login, installation := registrationFixture(t)
		scope := mutationScopeFor(t, db, login, installation)
		if _, err := db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id=?`, scope.AccountID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: installationRowFor(t, db, scope.InstallationID).incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrMutationRootScope) {
			t.Fatalf("a disabled membership registered a root: %v", err)
		}
	})
	t.Run("retired installation", func(t *testing.T) {
		s, db, login, installation := registrationFixture(t)
		scope := mutationScopeFor(t, db, login, installation)
		if _, err := db.Exec(`UPDATE identity_installations SET retired=1 WHERE installation_hash=?`, mutationInstallationHash(installation)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: installationRowFor(t, db, scope.InstallationID).incarnation, ExpectedRevision: 0, Nonce: newMutationID()}); !errors.Is(err, ErrInstallationRetired) {
			t.Fatalf("a retired lifetime registered a root: %v", err)
		}
	})
}

func insertRestorePurgeJob(t *testing.T, db *sql.DB, id, account, profile, accountIncarnation, profileIncarnation, state string, phase int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO security_purge_jobs(job_id,authority,account_id,profile_id,account_incarnation,profile_incarnation,desired_revision,phase,cursor_key,process_epoch,attempt,next_attempt_ms,state) VALUES(?,'local',?,?,?,?,1,?,'cursor','epoch',2,0,?)`, id, account, profile, accountIncarnation, profileIncarnation, phase, state); err != nil {
		t.Fatal(err)
	}
}

func purgeJobState(t *testing.T, db *sql.DB, id string) (incarnation string, phase int, state string) {
	t.Helper()
	if err := db.QueryRow(`SELECT profile_incarnation,phase,state FROM security_purge_jobs WHERE job_id=?`, id).Scan(&incarnation, &phase, &state); err != nil {
		t.Fatal(err)
	}
	return
}

// TestRestoreKeepsOnlyTheExactPurgePromise is the deterministic regression for
// the restore scrub: a job naming the erased profile's exact old lifetime and
// membership incarnation is carried to the new lifetime, while a stale job for a
// lifetime that was already replaced - and any job naming a profile that is still
// live - is left stale and never rebound.
func TestRestoreKeepsOnlyTheExactPurgePromise(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	incarnation := installationRowFor(t, db, installation).incarnation
	root, _, err := registerMutationRoot(t, s, db, scope, MutationRegistration{Incarnation: incarnation, ExpectedRevision: 0, Nonce: newMutationID()})
	if err != nil {
		t.Fatal(err)
	}
	// One receipt so the scrub has a committed result to retire.
	var scopeID int64
	if _, err = db.Exec(`INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES('local',?,?,0)`, scope.AccountID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM user_mutation_accounts WHERE authority='local' AND account_id=?`, scope.AccountID).Scan(&scopeID); err != nil {
		t.Fatal(err)
	}
	receipt := fmt.Sprintf("p251_%013d_%032x", 1793196000000, 2)
	if _, err = db.Exec(`INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,operation,digest,is_stop,issued_ms,status) VALUES(?,?,?,?,?,?,?,?,?,0,?,'applied')`, receipt, scopeID, scope.ProfileID, scope.FamilyID, profileLifetimeValue(t, db, scope.AccountID, scope.ProfileID), incarnation, root.ID, "delete_personal_item", strings.Repeat("b", 64), 1793196000000); err != nil {
		t.Fatal(err)
	}
	var membership string
	if err = db.QueryRow(`SELECT incarnation FROM direct_memberships WHERE account_id=?`, scope.AccountID).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	erased, live := Token(), Token()
	if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name,deleted) VALUES(?,?,'Erased',1)`, erased, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,?,'Live')`, live, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	erasedOld := profileLifetimeValue(t, db, scope.AccountID, erased)
	liveOld := profileLifetimeValue(t, db, scope.AccountID, live)
	installationOld := installationRowFor(t, db, installation)
	insertRestorePurgeJob(t, db, fmt.Sprintf("J%021d", 1), scope.AccountID, erased, membership, erasedOld, "pending", 3)
	insertRestorePurgeJob(t, db, fmt.Sprintf("J%021d", 2), scope.AccountID, live, membership, liveOld, "blocked", 2)
	insertRestorePurgeJob(t, db, fmt.Sprintf("J%021d", 3), scope.AccountID, erased, membership, newMutationIncarnation(), "pending", 1)
	if _, err = s.SignEveryoneOutAfterRestore(context.Background(), "restore-lifetime-test"); err != nil {
		t.Fatal(err)
	}
	var receipts, roots int64
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM user_mutation_receipts),(SELECT count(*) FROM user_mutation_roots)`).Scan(&receipts, &roots); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || roots != 0 {
		t.Fatalf("restore kept %d receipts and %d roots", receipts, roots)
	}
	erasedNew := profileLifetimeValue(t, db, scope.AccountID, erased)
	if erasedNew == erasedOld {
		t.Fatal("the erased profile lifetime did not rotate")
	}
	if profileLifetimeValue(t, db, scope.AccountID, live) == liveOld {
		t.Fatal("the live profile lifetime did not rotate")
	}
	installationNow := installationRowFor(t, db, installation)
	if installationNow.incarnation == installationOld.incarnation || installationNow.revision != 0 || installationNow.nonce != "" || installationNow.root != "" {
		t.Fatalf("installation lifetime after restore %+v (was %+v)", installationNow, installationOld)
	}
	if got, phase, state := purgeJobState(t, db, fmt.Sprintf("J%021d", 1)); got != erasedNew || phase != 0 || state != "pending" {
		t.Fatalf("the exact promise was not carried: %q %d %q, want %q", got, phase, state, erasedNew)
	}
	if got, phase, state := purgeJobState(t, db, fmt.Sprintf("J%021d", 2)); got != liveOld || phase != 2 || state != "blocked" {
		t.Fatalf("a live profile's job was rebound: %q %d %q", got, phase, state)
	}
	if got, phase, state := purgeJobState(t, db, fmt.Sprintf("J%021d", 3)); phase != 1 || state != "pending" || got == erasedNew {
		t.Fatalf("a stale lifetime's job was rebound: %q %d %q", got, phase, state)
	}
}

func TestRegistrationNeverAdoptsAClientChosenOrRestoredIncarnation(t *testing.T) {
	s, db, login, installation := registrationFixture(t)
	scope := mutationScopeFor(t, db, login, installation)
	original := installationRowFor(t, db, installation)
	q := MutationRegistration{Incarnation: newMutationIncarnation(), Nonce: newMutationID()}
	if _, _, err := registerMutationRoot(t, s, db, scope, q); !errors.Is(err, ErrInstallationIncarnation) {
		t.Fatalf("client replaced server lifetime: %v", err)
	}
	if actual := installationRowFor(t, db, installation); actual != original {
		t.Fatalf("registration changed enrollment: %+v -> %+v", original, actual)
	}
	q.Incarnation = original.incarnation
	if _, _, err := registerMutationRoot(t, s, db, scope, q); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE identity_installations SET incarnation=lower(hex(randomblob(16))),registration_revision=0,registration_nonce='',registration_root='' WHERE installation_hash=?`, mutationInstallationHash(installation)); err != nil {
		t.Fatal(err)
	}
	q.ExpectedRevision = 0
	if _, _, err := registerMutationRoot(t, s, db, scope, q); !errors.Is(err, ErrInstallationIncarnation) {
		t.Fatalf("old registration restored an old lifetime: %v", err)
	}
}

func TestRegistrationRechecksFamilyEpochAndHorizon(t *testing.T) {
	for _, changed := range []string{"epoch", "horizon"} {
		t.Run(changed, func(t *testing.T) {
			s, db, login, installation := registrationFixture(t)
			scope := mutationScopeFor(t, db, login, installation)
			q := MutationRegistration{Incarnation: installationRowFor(t, db, installation).incarnation, Nonce: newMutationID()}
			var err error
			if changed == "epoch" {
				_, err = db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id=?`, scope.AccountID)
			} else {
				_, err = db.Exec(`UPDATE authorization_session_families SET authorization_horizon='2000-01-01T00:00:00Z' WHERE id=?`, scope.FamilyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = registerMutationRoot(t, s, db, scope, q); !errors.Is(err, ErrMutationRootScope) {
				t.Fatalf("retired family registered: %v", err)
			}
		})
	}
}
