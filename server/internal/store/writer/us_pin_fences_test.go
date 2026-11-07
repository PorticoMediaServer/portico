package writer

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usPINFenceFixture(t *testing.T) (*executor, *stmt.Registry, usPolicyIssuer) {
	t.Helper()
	w, _, input, _ := usOwnerAuthorityFixture(t)
	for n := range 8 {
		id := fmt.Sprintf("sibling-family-%d", n)
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation,pin_gate) SELECT ?1,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation,1 FROM authorization_session_families WHERE id='issuer-family'`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET pin_gate=1 WHERE id='issuer-family'`); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usPINFenceDefinitions(), "us_pin_fences_test.go")
	return w, r, input.request.issuer
}
func usPINFenceTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	return usPINFenceTurnKind(t, w, r, usPINSet)
}
func usPINFenceTurnKind(t *testing.T, w *executor, r *stmt.Registry, kind CommandID) *commandTurn {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{id: d.ID, work: Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func TestUSProfilePINFenceBoundedAndOneKeep(t *testing.T) {
	w, r, issuer := usPINFenceFixture(t)
	turn := usPINFenceTurn(t, w, r)
	scope := usPINFenceScope{issuer.account, issuer.memberIncarnation, issuer.profile, issuer.profileLifetime}
	changes := func() int64 {
		t.Helper()
		var n int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	first, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, nil)
	if err != nil || first.generation != 1 || first.revision != 1 || changes()-before != 1 {
		t.Fatal(first, changes()-before, err)
	}
	keep := &usPINKeepProof{frame: turn.transaction, issuer: issuer}
	for generation := int64(2); generation <= 3; generation++ {
		before = changes()
		next, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000+generation, keep)
		if err != nil || next.id != first.id || next.generation != generation || next.revision != generation || changes()-before != 3 {
			t.Fatal(next, changes()-before, err)
		}
	}
	var siblings, acting, confirmed int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families WHERE id<>'issuer-family' AND pin_gate=1`).Scan(&siblings); err != nil || siblings != 8 {
		t.Fatal("sibling families changed", siblings, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT pin_gate FROM authorization_session_families WHERE id='issuer-family'`).Scan(&acting); err != nil || acting != 0 {
		t.Fatal(acting, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT confirmed_generation FROM authorization_family_security_fences WHERE family_id='issuer-family' AND scope_kind='profile_pin'`).Scan(&confirmed); err != nil || confirmed != 3 {
		t.Fatal(confirmed, err)
	}
}
func TestUSProfilePINFenceKeepFailureRollsBack(t *testing.T) {
	w, r, issuer := usPINFenceFixture(t)
	turn := usPINFenceTurn(t, w, r)
	delete(turn.steps, usPINKeepWitness)
	scope := usPINFenceScope{issuer.account, issuer.memberIncarnation, issuer.profile, issuer.profileLifetime}
	out, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, &usPINKeepProof{frame: turn.transaction, issuer: issuer})
	if err == nil || out != (usPINFenceResult{}) {
		t.Fatal("partial fence escaped", out, err)
	}
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	var fences, gate int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`).Scan(&fences); err != nil || fences != 0 {
		t.Fatal(fences, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT pin_gate FROM authorization_session_families WHERE id='issuer-family'`).Scan(&gate); err != nil || gate != 1 {
		t.Fatal("keep-family prefix survived", gate, err)
	}
}
