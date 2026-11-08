package writer

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// UO-01's security consumer is a FULL parent purpose. It must consume US-01's
// current owner stamp for this SAME turn; ordinary NORMAL roots cannot carry one.
// Followers copy these controls, not a new authority recipe or exported issuer.
func uoRootSecurityFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, string) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixture(t)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`)
	root := "abcdefghijklmnopqrstuv"
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)`, root, proof.witness.issuer.installation, "issuer", "issuer-profile", "issuer-birth", proof.witness.issuer.profileLifetime, proof.witness.issuer.family, int64(1), "active")
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, root, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	return w, proof, root
}

// This mirrors the merged US owner-authority fixture turn and adds only the
// root statement steps. It returns the mint error so current-source denials stay
// visible to the test instead of becoming fixture failures.
func uoRootSecurityAttempt(t *testing.T, w *executor, proof *usOwnerAuthorityProof) (*commandTurn, *usOwnerAuthorization, error) {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), USMemberLibraries, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	defs := append(usAuthorityDefinitions(), uoRootDefinitions()...)
	for i := range defs {
		if len(defs[i].Cohorts) == 0 {
			var args []any
			for _, p := range defs[i].Params {
				switch p.Type {
				case stmt.Text:
					args = append(args, "unit")
				case stmt.Blob:
					args = append(args, []byte{1})
				default:
					args = append(args, int64(1))
				}
			}
			defs[i].Cohorts = []stmt.Cohort{{Name: "unqualified-native-control", Bindings: args}}
		}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	budget := usOwnerAuthorityBudget{memory: 4 << 20, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	stamp, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), budget)
	return turn, stamp, err
}

func uoRootSecurityTurn(t *testing.T, w *executor, proof *usOwnerAuthorityProof) (*commandTurn, *uoRootBinding) {
	t.Helper()
	turn, stamp, err := uoRootSecurityAttempt(t, w, proof)
	if err != nil || stamp == nil {
		t.Fatal("current US owner stamp", err)
	}
	binding, err := bindUORoot(turn, stamp)
	if err != nil {
		t.Fatal(err)
	}
	return turn, &binding
}

func TestUORootSecurityConsumerBindsUSOwnerStamp(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootReference{root: root, kind: USMemberLibraries, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	want := uoOperationResult{status: 2, revision: 5, cursor: 6, done: 7, failed: 8, appliedMS: 100, target: "security-operation"}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, want); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}

	// Original replay still works after its current mutable source drifts.
	wc008NativeExec(t, w, `UPDATE direct_profiles SET name='drifted' WHERE id='issuer-profile'`)
	turn, binding = uoRootSecurityTurn(t, w, proof)
	replay, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || replay.decision != receiptReplay {
		t.Fatal(replay.decision, err)
	}
	got, err := restoreUORootOperation(t.Context(), turn, replay)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A revoked current session denies before the payload statement is reached.
	turn, binding = uoRootSecurityTurn(t, w, proof)
	replay, err = lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate inside this SAME open writer turn: the pool has one writer
	// connection, so a direct w.db call here would wait on our own frame.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreUORootOperation(t.Context(), turn, replay); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, sql.ErrNoRows) {
		t.Fatal("current deny did not precede payload access", err)
	}
}

func TestUORootStaleInstallationRegistrationDeniesBeforeCustody(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	wc008NativeExec(t, w, `UPDATE identity_installations SET incarnation='dddddddddddddddddddddddddddddddd'`)
	turn, stamp, err := uoRootSecurityAttempt(t, w, proof)
	if stamp != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stale installation minted a stamp", stamp, err)
	}
	if _, err := bindUORoot(turn, nil); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("FULL consumer without a current stamp accepted", err)
	}
	q := uoRootReference{root: root, kind: USMemberLibraries, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	// With no current stamp there is no binding, so retained custody is
	// unreachable even though the owner/receipt rows still exist.
	if _, err := lookupUORootCommand(t.Context(), turn, nil, q); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil binding reached retained custody", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUORootBindingRefusesNilAndForeignStamps(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	ordinaryTurn := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	ordinary, err := bindUORoot(ordinaryTurn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ordinaryTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	firstTurn, firstBinding := uoRootSecurityTurn(t, w, proof)
	q := uoRootReference{root: root, kind: USMemberLibraries, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	if _, err := lookupUORootCommand(t.Context(), firstTurn, &ordinary, q); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("ordinary binding reached a FULL turn's custody", err)
	}
	if _, err := bindUORoot(firstTurn, nil); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("FULL consumer without US stamp accepted", err)
	}
	if _, err := firstTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A stamp for a closed turn cannot be replayed on a later turn.
	secondTurn, _ := uoRootSecurityTurn(t, w, proof)
	if _, err := bindUORoot(secondTurn, firstBinding.security); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stamp for another turn accepted", err)
	}
	if err := firstBinding.check(t.Context(), secondTurn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("foreign binding checked another turn", err)
	}
	if _, err := secondTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := bindUORoot(wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions()), firstBinding.security); !errors.Is(err, ErrInvalid) {
		t.Fatal("NORMAL purpose borrowed a US stamp", err)
	}
}
