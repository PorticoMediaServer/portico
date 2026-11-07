package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func usPINReceiptFixture(t *testing.T) (*executor, *stmt.Registry, *usPINCommand, *usOwnerAuthorityProof) {
	t.Helper()
	w, r, policy, proof := usOwnerAuthorityFixture(t)
	i := policy.request.issuer
	input, err := prepareUSPINCommand(usPINSet, policy.identity, policy.request.ownerID, i, i.memberIncarnation, usPINRequest{account: i.account, profile: i.profile, profileLifetime: i.profileLifetime, expectedRevision: 1, expectedPINRevision: 1, hash: []byte(usPINFixtureHash)})
	if err != nil {
		t.Fatal(err)
	}
	var defs []stmt.Definition
	for _, entry := range r.Manifest() {
		defs = append(defs, entry.Definition)
	}
	defs = append(defs, usPINEffectDefinitions()...)
	defs = append(defs, usPINFenceDefinitions()...)
	defs = append(defs, usPINReceiptDefinitions()...)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_pin_receipts_test.go")
	return w, registry, input, proof
}
func usPINReceiptTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID) (*commandTurn, *usPolicyAuthorization) {
	t.Helper()
	turn := usPINFenceTurnKind(t, w, r, kind)
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: &usPolicyNativePermit{frame: turn.transaction}})
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}
func usPINReceiptRequestFor(input *usPINCommand) usPolicyReceiptRequest {
	return usPolicyReceiptRequest{ownerID: input.request.ownerID, kind: input.request.prepared.kind, reference: ReceiptReference{Identity: input.identity, RequestDigest: input.digest}}
}
func TestUSPINReceiptCompositionAndImmutableReplay(t *testing.T) {
	w, r, input, proof := usPINReceiptFixture(t)
	turn, a := usPINReceiptTurn(t, w, r, proof, usPINSet)
	q := usPINReceiptRequestFor(input)
	fresh, err := composeUSPINAfterAuthority(t.Context(), turn, a, q, input)
	if err != nil || fresh.decision != receiptFresh || fresh.result.revision != 2 || fresh.result.pinRevision != 2 || !fresh.result.pinRequired || fresh.chargedBytes <= 0 {
		t.Fatal(fresh, err)
	}
	usPolicyFixtureCommit(t, turn)
	// A later actual PIN clear does not rewrite or reconstruct the saved set
	// result. Current issuer authority is proved again before the replay lookup.
	if _, err = w.db.ExecContext(t.Context(), `UPDATE direct_profiles SET pin_hash=NULL,revision=revision+1,pin_revision=pin_revision+1 WHERE id='issuer-profile'`); err != nil {
		t.Fatal(err)
	}
	turn, a = usPINReceiptTurn(t, w, r, proof, usPINSet)
	var before, after int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := composeUSPINAfterAuthority(t.Context(), turn, a, q, nil)
	if err != nil || replay.decision != receiptReplay || replay.result != fresh.result || replay.digest != fresh.digest || replay.chargedBytes != 0 || replay.changedAccount != "" {
		t.Fatal(replay, err)
	}
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("replay made effects", before, after, err)
	}
}
func TestUSPINReceiptFailureRollsBackPINAndFence(t *testing.T) {
	w, r, input, proof := usPINReceiptFixture(t)
	turn, a := usPINReceiptTurn(t, w, r, proof, usPINSet)
	delete(turn.steps, usPINReceiptInsert)
	out, err := composeUSPINAfterAuthority(t.Context(), turn, a, usPINReceiptRequestFor(input), input)
	if err == nil || out != (usPINComposition{}) {
		t.Fatal("partial PIN prefix escaped", out, err)
	}
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	var revision, pinRevision, required, fences, receipts int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT revision,pin_revision,pin_hash IS NOT NULL FROM direct_profiles WHERE id='issuer-profile'`).Scan(&revision, &pinRevision, &required); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`).Scan(&fences); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || pinRevision != 1 || required != 0 || fences != 0 || receipts != 0 {
		t.Fatal("partial native state survived", revision, pinRevision, required, fences, receipts)
	}
}

func TestUSPINClearReceiptHasClosedFalseResult(t *testing.T) {
	w, r, set, proof := usPINReceiptFixture(t)
	issuer := set.request.issuer
	clear, err := prepareUSPINCommand(usPINClear, set.identity, set.request.ownerID, issuer, set.request.memberIncarnation, usPINRequest{account: issuer.account, profile: issuer.profile, profileLifetime: issuer.profileLifetime, expectedRevision: 1, expectedPINRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE direct_profiles SET pin_hash=? WHERE id='issuer-profile'`, []byte(usPINFixtureHash)); err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,updated_ms) VALUES(2,'profile_pin',?,?,?, ?,0)`, issuer.account, issuer.memberIncarnation, issuer.profile, issuer.profileLifetime); err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES(?,'profile_pin',2,1)`, issuer.family); err != nil {
		t.Fatal(err)
	}
	turn, a := usPINReceiptTurn(t, w, r, proof, usPINClear)
	out, err := composeUSPINAfterAuthority(t.Context(), turn, a, usPINReceiptRequestFor(clear), clear)
	if err != nil || out.decision != receiptFresh || out.result.pinRequired || out.result.kind != usPINClear || out.result.revision != 2 || out.result.pinRevision != 2 {
		t.Fatal(out, err)
	}
	var required int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT result_pin_required FROM security_command_receipts`).Scan(&required); err != nil || required != 0 {
		t.Fatal(required, err)
	}
}
