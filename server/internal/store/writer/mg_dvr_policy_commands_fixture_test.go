package writer

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The accepting fixture uses the REAL embedded A/B/G+013 schema: no draft
// overlay and no synthetic policy copy. It adds the reviewed US owner session,
// the shared lifetime root/owner and the two singleton policy rows. The existing
// leaf fixture keeps its held recording marker; this one is about acceptance.
type mgDVRPolicyCommandValues struct {
	defaults mgDVRDefaultsSnapshot
	storage  mgDVRStorageSnapshot
	root     string
	proof    *usOwnerAuthorityProof
}

func mgDVRPolicyCommandFixture(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string, mgDVRPolicyCommandValues) {
	t.Helper()
	w, path := testCanonicalExecutor(t, decorate...)
	now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	proof := usOwnerAuthorityPopulation(t, w, now)
	i := proof.witness.issuer
	v := mgDVRPolicyCommandValues{
		defaults: mgDVRDefaultsSnapshot{7, 60, 180, 0, 0, "keep-watching", "{series}/{title}", "{title} ({year})"},
		storage:  mgDVRStorageSnapshot{11, mgDVRStorageDesired{}},
		root:     "abcdefghijklmnopqrstuv",
		proof:    proof,
	}
	incarnation := mgPersonalIncarnation()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`, nil},
		{`INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)`,
			[]any{v.root, i.installation, i.account, i.profile, i.memberIncarnation, i.profileLifetime, i.family, int64(1), "active"}},
		{`INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, []any{v.root, incarnation[:]}},
		{`INSERT INTO dvr_defaults(singleton,revision,before_seconds,after_seconds,retention_days,episode_limit,live_tuner_conflict,show_folder_template,movie_folder_template) VALUES(1,?,?,?,?,?,?,?,?)`, v.defaults.values()},
		{`INSERT INTO dvr_storage_policy(singleton,revision,retention_days,episode_limit,floor_bytes,cap_bytes) VALUES(1,?,?,?,?,?)`, v.storage.values()},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return w, path, v
}

// NORMAL accepting frame over the personal root adapter definitions plus this
// command's own policy definitions. It is a private composition frame, not a new
// descriptor, authority or qualified accepting profile.
func mgDVRPolicyCommandTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgDVRPolicyDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond},
		setup:    Work{8, 100, 8, 4096, time.Millisecond},
		terminal: Work{8, 100, 8, 4096, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func mgDVRPolicyCommandCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func mgDVRPolicyFake(t *testing.T, v mgDVRPolicyCommandValues) *mgPersonalFakeRoots {
	t.Helper()
	return &mgPersonalFakeRoots{root: v.root, identity: mgPersonalFakeIdentity(v.proof), witness: v.proof}
}

func mgDVRPolicyDefaultsReference(t *testing.T, id CommandIdentity, r mgDVRDefaultsPolicyRequest) ReceiptReference {
	t.Helper()
	d, err := r.digest(id)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: d}
}
func mgDVRPolicyStorageReference(t *testing.T, id CommandIdentity, r mgDVRStoragePolicyRequest) ReceiptReference {
	t.Helper()
	d, err := r.digest(id)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: d}
}

func ackMGDVRPolicy(t *testing.T, w *executor, root string, sequence int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=? WHERE root_id=? AND last_command_sequence=?`, sequence, root, sequence); err != nil {
		t.Fatal(err)
	}
}
