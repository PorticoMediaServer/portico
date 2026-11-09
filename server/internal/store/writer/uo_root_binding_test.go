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
// current owner stamp for this SAME turn; ordinary NORMAL roots cannot carry one
// and cannot borrow a stamp. Followers copy these controls, not a new authority
// recipe or exported issuer.
func uoRootSecurityFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, string) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixture(t)
	i := proof.witness.issuer
	wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`)
	root := "abcdefghijklmnopqrstuv"
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)`, root, i.installation, i.account, i.profile, i.memberIncarnation, i.profileLifetime, i.family, int64(1), "active")
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
	binding, err := bindUORoot(turn, stamp, nil)
	if err != nil {
		t.Fatal(err)
	}
	return turn, &binding
}

func TestUORootSecurityConsumerBindsUSOwnerStamp(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
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

	// Original replay still works after its current mutable source drifts. The
	// changed field is not part of the root identity.
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

// M1b: the pre-lookup authority check is separately observable. The owner row is
// removed and the session revoked inside the same turn before the lookup. With
// the pre-check, the error is Denied; if only that check were dropped, the owner
// miss would return ResolveRequired and the final check would never be reached.
func TestUORootCurrentAuthorityBeforeLookup(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM user_mutation_command_owners WHERE root_id=?`, root); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupUORootCommand(t.Context(), turn, binding, q); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) {
		t.Fatal("revoked session reached the owner lookup before authority", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUORootStaleInstallationRegistrationDeniesBeforeCustody(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	wc008NativeExec(t, w, `UPDATE identity_installations SET incarnation='dddddddddddddddddddddddddddddddd'`)
	turn, stamp, err := uoRootSecurityAttempt(t, w, proof)
	if stamp != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stale installation minted a stamp", stamp, err)
	}
	if _, err := bindUORoot(turn, nil, nil); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("FULL consumer without a current stamp accepted", err)
	}
	q := uoRootSecurityRef(root)
	// With no current stamp there is no binding, so retained custody is
	// unreachable even though the owner/receipt rows still exist.
	if _, err := lookupUORootCommand(t.Context(), turn, nil, q); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil binding reached retained custody", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// The NORMAL arm is a closed seam until SOL-USERS's us_current_fence mint lands.
// It refuses nil, zero, well-shaped, prior, finished and rolled-back proofs with
// the same typed error; a US stamp cannot substitute and vice versa.
func TestUORootNormalSeamRefusesWithoutMint(t *testing.T) {
	w := wc008NativeExecutor(t)
	normal := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	if _, err := bindUORoot(normal, nil, nil); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("nil NORMAL proof not the typed seam", err)
	}
	if _, err := bindUORoot(normal, nil, &uoRootCurrentProof{}); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("zero NORMAL proof not the typed seam", err)
	}
	valid := uoRootNormalProof(t, normal)
	// The shape is complete, but the authorized mint does not exist yet.
	if _, err := bindUORoot(normal, nil, valid); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("unminted NORMAL proof not the typed seam", err)
	}
	if _, err := bindUORoot(normal, &usOwnerAuthorization{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("NORMAL purpose borrowed a US stamp", err)
	}
	if _, err := normal.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := bindUORoot(normal, nil, valid); !errors.Is(err, ErrInvalid) {
		t.Fatal("rolled-back frame itself not refused", err)
	}

	finished := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	finishedProof := uoRootNormalProof(t, finished)
	if _, err := finished.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := bindUORoot(finished, nil, finishedProof); !errors.Is(err, ErrInvalid) {
		t.Fatal("finished frame itself not refused", err)
	}

	prior := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	priorProof := uoRootNormalProof(t, prior)
	if _, err := prior.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	current := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	if _, err := bindUORoot(current, nil, priorProof); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("prior-frame proof not refused", err)
	}
	if err := priorProof.current(current); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("prior-frame proof validated on another frame", err)
	}

	securityW, proof, _ := uoRootSecurityFixture(t)
	securityTurn, securityBinding := uoRootSecurityTurn(t, securityW, proof)
	if _, err := bindUORoot(securityTurn, securityBinding.security, valid); !errors.Is(err, ErrInvalid) {
		t.Fatal("FULL consumer accepted a NORMAL proof", err)
	}
	if _, err := securityTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func uoRootNormalProof(t *testing.T, turn *commandTurn) *uoRootCurrentProof {
	t.Helper()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok {
		t.Fatal("turn has no deadline")
	}
	return &uoRootCurrentProof{turn: turn, purpose: SetPersonalFlags, lifetime: t.Context(), deadline: deadline, identity: uoRootIdentity{account: "account", memberIncarnation: "member", profile: "profile", profileLifetime: "lifetime", installation: "installation", family: "family"}}
}

// B1: the root PK is read before the command-owner lookup and must match the
// validated stamp identity. Missing and every mismatch are one ErrUORootGone
// class on lookup, fresh persistence and original replay.
func TestUORootForeignRootIdentityIsGone(t *testing.T) {
	changes := map[string]string{
		"account":      `account_id='other-account'`,
		"membership":   `account_incarnation='other-membership'`,
		"profile":      `profile_id='other-profile'`,
		"lifetime":     `profile_incarnation='dddddddddddddddddddddddddddddddd'`,
		"family":       `family_id='other-family'`,
		"installation": `installation_incarnation='eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'`,
	}
	for name, set := range changes {
		t.Run(name, func(t *testing.T) {
			w, proof, root := uoRootSecurityFixture(t)
			q := uoRootSecurityRef(root)
			wc008NativeExec(t, w, `UPDATE user_mutation_roots SET `+set+` WHERE id=?`, root)
			turn, binding := uoRootSecurityTurn(t, w, proof)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, q); !errors.Is(err, ErrUORootGone) {
				t.Fatal("foreign root identity passed lookup", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// Fresh persistence rechecks the same identity after the lookup.
			w, proof, root = uoRootSecurityFixture(t)
			q = uoRootSecurityRef(root)
			turn, binding = uoRootSecurityTurn(t, w, proof)
			lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET `+set+` WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
			if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "foreign"}); !errors.Is(err, ErrUORootGone) {
				t.Fatal("foreign root identity passed persist", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// Original replay is refused under the same class.
			w, proof, root, q = uoRootPersisted(t)
			wc008NativeExec(t, w, `UPDATE user_mutation_roots SET `+set+` WHERE id=?`, root)
			turn, binding = uoRootSecurityTurn(t, w, proof)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, q); !errors.Is(err, ErrUORootGone) {
				t.Fatal("foreign root identity passed replay", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A missing root uses the identical class as a mismatched one.
	w, proof, _ := uoRootSecurityFixture(t)
	missing := uoRootReference{root: "zyxwvutsrqponmlkjihgfe", kind: USMemberLibraries, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn, binding := uoRootSecurityTurn(t, w, proof)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, missing); !errors.Is(err, ErrUORootGone) {
		t.Fatal("missing root is not RootGone", err)
	}
}

// B3: an active root admits Fresh and replay; a draining root admits only an
// authorized original replay, never Fresh, and persistence rechecks active. A
// draining root does not have to be the installation's newest root.
func TestUORootDrainingRootReplaysOnly(t *testing.T) {
	w, proof, root, q := uoRootPersisted(t)
	wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root)
	// A newer active root for the same installation does not invalidate the
	// draining root's authorized original replay.
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) SELECT 'zyxwvutsrqponmlkjihgfe',installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision+1,'active' FROM user_mutation_roots WHERE id=?`, root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	replay, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || replay.decision != receiptReplay {
		t.Fatal("draining root did not replay its original", replay.decision, err)
	}
	want := uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "operation"}
	if got, err := restoreUORootOperation(t.Context(), turn, replay); err != nil || got != want {
		t.Fatal(got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Fresh under a draining root is RootGone, even after the predecessor ACK.
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, root)
	next := q
	next.identity.Sequence = 2
	turn, binding = uoRootSecurityTurn(t, w, proof)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, next); !errors.Is(err, ErrUORootGone) {
		t.Fatal("draining root admitted Fresh", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Persistence rechecks active after a fresh lookup.
	w, proof, root = uoRootSecurityFixture(t)
	q = uoRootSecurityRef(root)
	turn, binding = uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "draining"}); !errors.Is(err, ErrUORootGone) {
		t.Fatal("persistence did not recheck active", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("draining persistence left a receipt", receipts, err)
	}
}
