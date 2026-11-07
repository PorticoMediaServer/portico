package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Synthetic native composition only: the stamp below is TEST-ONLY input, not a
// production authority constructor or governor/hold qualification. Production
// dispatch must issue independently current authority and resource reservations.
func usPolicyReceiptFixture(t *testing.T) (*executor, *stmt.Registry, *usLibraryPolicyCommand) {
	t.Helper()
	w := usSchemaDraftFixture(t)
	virtualNow := time.Now().Add(time.Hour)
	w.now = func() time.Time { return virtualNow }
	identity, request := usPolicyFixture()
	command, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, sqlText := range []string{
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('target','target',X'','target-profile')`,
		`INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES('target','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member','["old"]')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sqlText); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`, request.ownerID, identity.Incarnation[:], request.issuer.server, request.issuer.account, request.issuer.memberIncarnation, request.issuer.profile, request.issuer.profileLifetime, request.issuer.installation, request.issuer.family); err != nil {
		t.Fatal(err)
	}
	defs := append(usPolicyDefinitions(), usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	r := nativeInventoryFixtureRegistry(t, w, defs, "us_policy_receipts_test.go")
	return w, r, command
}

func usPolicyReceiptTurn(t *testing.T, w *executor, r *stmt.Registry, c *usLibraryPolicyCommand) (*commandTurn, *usPolicyAuthorization, context.CancelFunc) {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), c.kind, recipe, &testCustody{})
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
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	a := &usPolicyAuthorization{turn: turn, stamp: &usPolicyAuthorityStamp{frame: tx, recipe: usDirectOwnerRecipe}, issuer: c.request.issuer, lifetime: lifetime,
		authorizedUntil: w.now().Add(time.Minute), memoryBytes: 4 << 20, receiptBytes: 1 << 20, nativeFixture: &usPolicyNativePermit{frame: tx}}
	return turn, a, cancel
}

func usPolicyReceiptRequestFor(c *usLibraryPolicyCommand) usPolicyReceiptRequest {
	return usPolicyReceiptRequest{ownerID: c.request.ownerID, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}
}

func usPolicyFixtureCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	physical, err := turn.transaction.commit()
	if err != nil || physical.state != committed || turn.transaction.settings.Synchronous != 2 {
		t.Fatal("FULL physical commit failed", physical, err)
	}
}

func TestUSPolicyReceiptCompositionReplayAndFloor(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	q := usPolicyReceiptRequestFor(input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	fresh, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, q, input)
	if err != nil || fresh.decision != receiptFresh || !fresh.result.changed || fresh.result.revision != 2 || fresh.chargedBytes <= 0 {
		t.Fatal(fresh, err)
	}
	usPolicyFixtureCommit(t, turn)
	// A later native source edit must not change historical replay or trigger
	// the original expected-revision CAS. Receipt/accounting is unchanged.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries='[]',revision=revision+1,disabled=1 WHERE account_id='target'`); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	turn, a, _ = usPolicyReceiptTurn(t, w, r, input)
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	replayed, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, q, nil)
	if err != nil || replayed.decision != receiptReplay || !reflect.DeepEqual(replayed.result, fresh.result) || replayed.digest != fresh.digest || replayed.chargedBytes != 0 || replayed.changedAccount != "" {
		t.Fatal(replayed, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("replay made new effects", before, after, err)
	}
	usPolicyFixtureCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, input)
	ack, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q)
	if err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usPolicyFixtureCommit(t, turn)
	// Physically drained cohort after ACK still resolves retired, not Applied
	// or an incomplete-result error. The floor precedes all cohort decoding.
	if _, err := w.db.ExecContext(t.Context(), "DELETE FROM security_result_libraries"); err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usPolicyReceiptTurn(t, w, r, input)
	retired, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, q, nil)
	if err != nil || retired.decision != receiptRetired || retired.result.ownerID != "" {
		t.Fatal(retired, err)
	}
}

func TestUSPolicyReceiptRefusalRollsBackWholePrefix(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	q := usPolicyReceiptRequestFor(input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	// Deliberately omit the final owner-advance step, after source/header/cohort
	// writes would have run. The SAME frame must roll back their entire prefix.
	delete(turn.steps, usPolicyOwnerAdvance)
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, q, input)
	if err == nil || !reflect.DeepEqual(out, usPolicyComposition{}) {
		t.Fatal("partial command escaped", out, err)
	}
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	var revision, receipts int64
	if err := w.db.QueryRowContext(t.Context(), "SELECT revision FROM direct_memberships WHERE account_id='target'").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM security_command_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || receipts != 0 {
		t.Fatal("source/receipt prefix survived rollback", revision, receipts)
	}
}

func TestUSPolicyReceiptRevocationAndMemoryPrecedePayload(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	turn, a, cancel := usPolicyReceiptTurn(t, w, r, input)
	cancel()
	// Omit the native owner step: a valid refusal must happen before touching it.
	delete(turn.steps, usSecurityOwner)
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptRequestFor(input), input)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPolicyComposition{}) {
		t.Fatal(out, err)
	}
	_, _ = turn.transaction.rollback(err)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, input)
	a.memoryBytes = 1
	out, err = composeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptRequestFor(input), input)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPolicyComposition{}) {
		t.Fatal(out, err)
	}
}

func TestUSPolicyReceiptRejectsChangedCachedProjection(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	q := usPolicyReceiptRequestFor(input)
	input.policy.String = `["different"]`
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, q, input)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPolicyComposition{}) {
		t.Fatal(out, err)
	}
}

func TestUSPolicyReceiptNoOpSavesExactResult(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='target'`, " \n"+input.policy.String+" \t"); err != nil {
		t.Fatal(err)
	}
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	// A semantic no-op must not need its source-effect statement at all.
	delete(turn.steps, usMemberPolicyWrite)
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptRequestFor(input), input)
	if err != nil || out.result.changed || out.result.revision != 1 || out.changedAccount != "" || out.chargedBytes <= 0 {
		t.Fatal(out, err)
	}
	usPolicyFixtureCommit(t, turn)
	var receipts, revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM direct_memberships WHERE account_id='target'`).Scan(&revision); err != nil || receipts != 1 || revision != 1 {
		t.Fatal(receipts, revision, err)
	}
}

func TestUSPolicyReceiptRetiredOwnerRefusesBeforeTarget(t *testing.T) {
	w, r, input := usPolicyReceiptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE security_command_owners SET retired_ms=1`); err != nil {
		t.Fatal(err)
	}
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	delete(turn.steps, usMemberPolicyOctets)
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptRequestFor(input), input)
	if !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPolicyComposition{}) {
		t.Fatal(out, err)
	}
}
