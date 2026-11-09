package writer

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Synthetic native adapter fixture only. US-01 still mints the authority stamp
// after its own current-source checks; nothing here constructs a production
// issuer, registers an accepting command or qualifies a hold.
func usPolicyReceiptFixture(t *testing.T, kind CommandID, libraries []string) (*executor, *stmt.Registry, *usOwnerAuthorityProof, *usLibraryPolicyCommand) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixture(t)
	identity, request := usPolicyFixture()
	request.libraries = libraries
	if kind == USProfileLibraries {
		request.profile, request.profileLifetime = "profile", strings.Repeat("f", 32)
	}
	command, err := prepareUSLibraryPolicy(kind, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('target','target',X'','target-profile')`,
		`INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES('target','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member','["movies"]')`,
		`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,allowed_libraries) VALUES('profile','target','ffffffffffffffffffffffffffffffff','Target',1,'[]')`,
	} {
		wc008NativeExec(t, w, q)
	}
	i := proof.witness.issuer
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		request.ownerID, identity.Incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usPolicyDefinitions()...)
	defs = append(defs, usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, r, proof, command
}

func usPolicyReceiptTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID) (*commandTurn, *usOwnerAuthorization, context.CancelFunc) {
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
		steps = append(steps, statementStep{d.ID, Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	budget := usOwnerAuthorityBudget{memory: 4 << 20, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, lifetime, budget)
	if err != nil {
		t.Fatal(err)
	}
	return turn, a, cancel
}

func usPolicyReceiptFor(c *usLibraryPolicyCommand) usPolicyReceiptRequest {
	return usPolicyReceiptRequest{ownerID: c.request.ownerID, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}
}

func usPolicySavedFixture(t *testing.T, c *usLibraryPolicyCommand) (usPolicySavedResult, ResultDigest) {
	t.Helper()
	v := usPolicySavedResult{kind: c.kind, ownerID: c.request.ownerID, account: c.request.account,
		memberIncarnation: c.request.memberIncarnation, revision: 2, appliedMS: 1000, changed: true,
		libraries: slices.Clone(c.request.libraries)}
	if c.kind == USProfileLibraries {
		v.profile, v.profileLifetime = c.request.profile, c.request.profileLifetime
		if c.request.libraries == nil {
			v.inherits, v.changed = true, false
		}
	}
	digest, err := usPolicyResultDigest(c.identity, c.digest, v)
	if err != nil {
		t.Fatal(err)
	}
	return v, digest
}

// A second, fully current local-owner installation: same account/profile, its
// own device, lineage, installation, family and token. It proves that a valid
// owner session cannot reach retained rows born under another issuer scope.
func usPolicyOtherIssuer(t *testing.T, w *executor) *usOwnerAuthorityProof {
	t.Helper()
	now := w.now()
	witness := usOwnerAuthorityWitness{
		issuer: usPolicyIssuer{server: "server", account: "issuer", profile: "issuer-profile", family: "issuer-family-2",
			memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), installation: strings.Repeat("9", 32),
			tokenDigest: usCredentialFixtureHash("other-issuer-token"), epoch: 1, familyGeneration: 1, authorityGeneration: 1},
		device: "issuer-device-2", lineage: "issuer-lineage-2", installationID: "issuer-installation-synthetic-000002",
		tokenExpiry: now.Add(10 * time.Minute).Format(time.RFC3339), horizon: now.Add(time.Hour).Format(time.RFC3339),
	}
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	i := witness.issuer
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{proof.installationHash, i.installation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('issuer-device-2','issuer',?,'Issuer two','test','test','1','','')`, []any{witness.installationID}},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('issuer-lineage-2','issuer','issuer-device-2','')`, nil},
		{`INSERT INTO authorization_session_families(security_kind,id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(0,'issuer-family-2','server','issuer','issuer-profile','local','owner',1,?,1,'issuer-device-2','issuer-lineage-2',?,?,?)`, []any{witness.horizon, i.memberIncarnation, i.profileLifetime, []byte{8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family-2',1,?)`, []any{i.tokenDigest, witness.tokenExpiry}},
		{`INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(2,'device','issuer',?,'issuer-device-2',0)`, []any{i.memberIncarnation}},
		{`INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family-2','device',2,1)`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return proof
}

func usPolicyForeignOwner(t *testing.T, w *executor, ownerID string, scope usPolicyIssuer, incarnation []byte) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		ownerID, incarnation, scope.server, scope.account, scope.memberIncarnation, scope.profile, scope.profileLifetime, scope.installation, scope.family)
}

func usPolicyReceiptCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	physical, err := turn.transaction.commit()
	if err != nil || physical.state != committed || turn.transaction.settings.Synchronous != 2 {
		t.Fatal("FULL physical commit failed", physical, err)
	}
}

func TestUSPolicyReceiptReplayAfterSourceDrift(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if fresh, err := lookupUSPolicy(t.Context(), turn, a, q); err != nil || fresh.decision != receiptFresh || fresh.owner.count != 0 {
		t.Fatal(fresh, err)
	}
	charged, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest)
	if err != nil || charged <= 0 {
		t.Fatal(charged, err)
	}
	usPolicyReceiptCommit(t, turn)
	// Real current-source drift after the original save: different value,
	// revision and disabled state. Replay must not read or reapply it.
	wc008NativeExec(t, w, `UPDATE direct_memberships SET allowed_libraries='[]',revision=revision+1,disabled=1 WHERE account_id='target'`)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	// No retained-result path may consult the live target statements.
	delete(turn.steps, usMemberPolicyOctets)
	delete(turn.steps, usMemberPolicyFacts)
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != digest || !reflect.DeepEqual(replay.result, v) {
		t.Fatal(replay, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("replay made new effects", before, after, err)
	}
}

func TestUSPolicyReceiptChangedDigestConflicts(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	_, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	changed := q
	changed.reference.RequestDigest = RequestDigest{9}
	if out, err := lookupUSPolicy(t.Context(), turn, a, changed); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPolicyReceiptLookup{}) {
		t.Fatal(out, err)
	}
}

func TestUSPolicyReceiptDeniedBeforeOwnerBytes(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	turn, a, cancel := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	cancel()
	// Current authority denial must precede every retained owner/header lookup.
	delete(turn.steps, usSecurityOwner)
	delete(turn.steps, usPolicyReceiptHeader)
	out, err := lookupUSPolicy(t.Context(), turn, a, usPolicyReceiptFor(input))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPolicyReceiptLookup{}) {
		t.Fatal(out, err)
	}
	// The entry recheck must deny BEFORE any owner/header statement runs. If it
	// is removed, the deleted owner step yields us_policy_statement instead.
	if strings.Contains(err.Error(), "us_policy_statement") {
		t.Fatal("owner statement ran before the authority denial", err)
	}
}

func TestUSPolicyReceiptFloorOnlyACK(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	_, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest)
	if err != nil || charged <= 0 {
		t.Fatal(charged, err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	ack, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q)
	if err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usPolicyReceiptCommit(t, turn)
	var acked, count, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_sequence,retained_count,retained_variable_bytes FROM security_command_owners`).Scan(&acked, &count, &bytes); err != nil || acked != 1 || count != 1 || bytes != charged {
		t.Fatal("ACK refunded or deleted retained bytes", acked, count, bytes, charged, err)
	}
	var headers, libraries int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_result_libraries`).Scan(&libraries); err != nil || headers != 1 || libraries != 2 {
		t.Fatal("ACK physically removed cohort", headers, libraries, err)
	}
	// The floor precedes all cohort decoding: even a drained child cohort must
	// classify retired, never Applied or an incomplete-result error.
	wc008NativeExec(t, w, `DELETE FROM security_result_libraries`)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	retired, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil || retired.decision != receiptRetired || retired.result.ownerID != "" {
		t.Fatal(retired, err)
	}
	if again, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q); err != nil || again != 1 {
		t.Fatal("floor ACK was not idempotent", again, err)
	}
}

func TestUSPolicyReceiptPartialCohortIsResolveRequired(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	_, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	wc008NativeExec(t, w, `DELETE FROM security_result_libraries WHERE ordinal=1`)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if out, err := lookupUSPolicy(t.Context(), turn, a, q); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usPolicyReceiptLookup{}) {
		t.Fatal("partial cohort escaped as Applied", out, err)
	}
}

func TestUSPolicyReceiptKeepsExactNilAndEmpty(t *testing.T) {
	for _, mode := range []string{"inherit", "deny"} {
		t.Run(mode, func(t *testing.T) {
			libraries := []string(nil)
			if mode == "deny" {
				libraries = []string{}
			}
			w, r, proof, input := usPolicyReceiptFixture(t, USProfileLibraries, libraries)
			q := usPolicyReceiptFor(input)
			v, digest := usPolicySavedFixture(t, input)
			turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
			_, err := lookupUSPolicy(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
				t.Fatal(err)
			}
			usPolicyReceiptCommit(t, turn)
			turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
			replay, err := lookupUSPolicy(t.Context(), turn, a, q)
			if err != nil || replay.decision != receiptReplay || replay.digest != digest || !reflect.DeepEqual(replay.result, v) {
				t.Fatal(replay, err)
			}
			if (mode == "inherit") != (replay.result.libraries == nil) || (mode == "deny") != (replay.result.libraries != nil) {
				t.Fatal("nil/empty distinction lost", mode, replay.result.libraries)
			}
		})
	}
}

func TestUSPolicyReceiptPrefixRollsBack(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	_, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	// Omit only the final owner-advance step: the header and both ordered
	// children are already a nonempty prefix in this SAME frame.
	delete(turn.steps, usPolicyOwnerAdvance)
	if charged, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err == nil || charged != 0 {
		t.Fatal("partial receipt escaped", charged, err)
	} else if _, rbErr := turn.transaction.rollback(err); !errors.Is(rbErr, err) {
		t.Fatal(rbErr)
	}
	var headers, libraries, count, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_result_libraries`).Scan(&libraries); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT retained_count,retained_variable_bytes FROM security_command_owners`).Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	if headers != 0 || libraries != 0 || count != 0 || bytes != 0 {
		t.Fatal("receipt prefix or owner debt survived rollback", headers, libraries, count, bytes)
	}
}

// B1: a 540 result saved with an explicit empty list must replay as `[]`, not
// nil. Live member responses are always a list, and DDL forces inherits=0.
func TestUSPolicyReceiptMemberEmptyListStaysExplicit(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	if v.inherits || v.libraries == nil {
		t.Fatal("member fixture must save an explicit empty list", v)
	}
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	_, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	replay, err := lookupUSPolicy(t.Context(), turn, a, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != digest {
		t.Fatal(replay, err)
	}
	if replay.result.libraries == nil || len(replay.result.libraries) != 0 || !reflect.DeepEqual(replay.result, v) {
		t.Fatal("explicit empty member result replayed as nil", replay.result)
	}
}

// B3: a different current issuer must be denied at the owner scope, before any
// receipt/payload statement, with exactly the missing-owner denial.
func TestUSPolicyReceiptForeignIssuerMatchesMissingOwner(t *testing.T) {
	w, r, _, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	other := usPolicyOtherIssuer(t, w)
	payload := []stmt.ID{usPolicyReceiptBinding, usPolicyReceiptHeader, usPolicyReceiptOctets, usPolicyReceiptLibraries, usPolicyReceiptLibraryOctets}
	foreignTurn, foreignAuth, _ := usPolicyReceiptTurn(t, w, r, other, input.kind)
	for _, id := range payload {
		delete(foreignTurn.steps, id)
	}
	foreignOut, foreignErr := lookupUSPolicy(t.Context(), foreignTurn, foreignAuth, q)
	if !errors.Is(foreignErr, ErrOwnerUnavailable) || !reflect.DeepEqual(foreignOut, usPolicyReceiptLookup{}) {
		t.Fatal(foreignOut, foreignErr)
	}
	_, _ = foreignTurn.transaction.rollback(foreignErr)
	missingTurn, missingAuth, _ := usPolicyReceiptTurn(t, w, r, other, input.kind)
	for _, id := range payload {
		delete(missingTurn.steps, id)
	}
	missing := q
	missing.ownerID = "no-such-owner"
	missingOut, missingErr := lookupUSPolicy(t.Context(), missingTurn, missingAuth, missing)
	if !errors.Is(missingErr, ErrOwnerUnavailable) || !reflect.DeepEqual(missingOut, usPolicyReceiptLookup{}) {
		t.Fatal(missingOut, missingErr)
	}
	if foreignErr.Error() != missingErr.Error() {
		t.Fatal("foreign issuer leaked a different denial", foreignErr, missingErr)
	}
	if strings.Contains(foreignErr.Error(), "us_policy_statement") {
		t.Fatal("payload statement ran for a foreign issuer", foreignErr)
	}
}

// F1: store must never trust caller-supplied owner facts. It re-reads the
// scoped owner under the current issuer and rejects mismatched bindings.
func TestUSPolicyReceiptStoreRefusesForeignOwnerScope(t *testing.T) {
	// Each case differs from the current issuer scope in exactly ONE owner-read
	// predicate. Dropping that predicate would let the store write into this
	// foreign owner, so every case must be refused with the owner unavailable.
	for _, tc := range []struct {
		name string
		drop func(*usPolicyIssuer)
	}{
		{"server", func(i *usPolicyIssuer) { i.server = "other-server" }},
		{"account", func(i *usPolicyIssuer) { i.account = "other-account" }},
		{"member", func(i *usPolicyIssuer) { i.memberIncarnation = strings.Repeat("7", 32) }},
		{"profile", func(i *usPolicyIssuer) { i.profile = "other-profile" }},
		{"lifetime", func(i *usPolicyIssuer) { i.profileLifetime = strings.Repeat("8", 32) }},
	} {
		t.Run("foreign_owner_row_"+tc.name, func(t *testing.T) {
			w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
			scope := proof.witness.issuer
			tc.drop(&scope)
			// A REAL incarnation: a zero one would hide a dropped predicate
			// behind the identity mismatch instead of the scope mismatch.
			usPolicyForeignOwner(t, w, "foreign-owner", scope, input.identity.Incarnation[:])
			q := usPolicyReceiptFor(input)
			q.ownerID = "foreign-owner"
			v, digest := usPolicySavedFixture(t, input)
			v.ownerID = "foreign-owner"
			digest, err := usPolicyResultDigest(input.identity, input.digest, v)
			if err != nil {
				t.Fatal(err)
			}
			turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
			if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); !errors.Is(err, ErrOwnerUnavailable) {
				t.Fatal("foreign issuer owner row was writable", err)
			}
			_, _ = turn.transaction.rollback(err)
			var receipts, last, count int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT last_sequence,retained_count FROM security_command_owners WHERE id='foreign-owner'`).Scan(&last, &count); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || last != 0 || count != 0 {
				t.Fatal("foreign owner was mutated or a receipt escaped", receipts, last, count)
			}
		})
	}
	t.Run("owner_binding_mismatch", func(t *testing.T) {
		w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
		q := usPolicyReceiptFor(input)
		v, _ := usPolicySavedFixture(t, input)
		v.ownerID = "foreign-owner"
		digest, err := usPolicyResultDigest(input.identity, input.digest, v)
		if err != nil {
			t.Fatal(err)
		}
		turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
		if _, err = storeUSPolicyResult(t.Context(), turn, a, q, v, digest); !errors.Is(err, ErrInvalid) {
			t.Fatal("mismatched result owner was accepted", err)
		}
	})
}

// F2: the same operation at the next retained sequence must be a typed
// conflict before the insert, never a raw UNIQUE violation or overwrite.
func TestUSPolicyReceiptSameOperationNextSequenceConflicts(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if _, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if ack, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q); err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usPolicyReceiptCommit(t, turn)
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSLibraryPolicy(input.kind, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	nextValue, nextDigest := usPolicySavedFixture(t, next)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if _, err = storeUSPolicyResult(t.Context(), turn, a, usPolicyReceiptFor(next), nextValue, nextDigest); !errors.Is(err, ErrConflict) {
		t.Fatal("reused operation was not a typed conflict", err)
	} else if strings.Contains(err.Error(), "UNIQUE") {
		t.Fatal("raw SQLite uniqueness error escaped", err)
	}
	_, _ = turn.transaction.rollback(err)
	var receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("second sequence overwrote or added a receipt", receipts, err)
	}
}

// F3: a current-source denial on ACK must be reported as denial, never joined
// to the benign sequence-conflict class.
func TestUSPolicyReceiptACKDenialIsNotConflict(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	turn, a, cancel := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	cancel()
	if _, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptFor(input)); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) {
		t.Fatal("ACK denial was reported as a conflict", err)
	}
}

// A virtual clock that expires the authority at a pinned call. Installing it
// only after the turn is minted keeps the entry checks on the original time.
type usPolicyFlipClock struct {
	base   time.Time
	flipAt int
	calls  int
}

func (c *usPolicyFlipClock) now() time.Time {
	c.calls++
	if c.flipAt > 0 && c.calls >= c.flipAt {
		return c.base.Add(2 * time.Hour)
	}
	return c.base
}

func usPolicyFlipClockOn(w *executor, flipAt int) *usPolicyFlipClock {
	c := &usPolicyFlipClock{base: w.now(), flipAt: flipAt}
	w.now = c.now
	return c
}

func usPolicyLateDenialLookup(t *testing.T) (*executor, func() error) {
	t.Helper()
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if _, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	q.reference.RequestDigest = RequestDigest{9}
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	return w, func() error {
		_, err := lookupUSPolicy(t.Context(), turn, a, q)
		return err
	}
}

func usPolicyLateDenialACK(t *testing.T) (*executor, func() error) {
	t.Helper()
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	return w, func() error {
		_, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q)
		return err
	}
}

func usPolicyLateDenialStore(t *testing.T) (*executor, func() error) {
	t.Helper()
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if _, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	return w, func() error {
		_, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest)
		return err
	}
}

// F3: a denial that arrives on the deferred recheck REPLACES a body conflict.
// It must never be joined into the conflict class, and the pinned call count
// fails the test if the deferred authority checks are removed.
func TestUSPolicyReceiptLateDenialWins(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls int
		setup func(*testing.T) (*executor, func() error)
	}{
		{"lookup", 44, usPolicyLateDenialLookup},
		{"ack", 58, usPolicyLateDenialACK},
		{"store", 58, usPolicyLateDenialStore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, call := tc.setup(t)
			clock := usPolicyFlipClockOn(w, 0)
			if err := call(); !errors.Is(err, ErrConflict) {
				t.Fatalf("setup does not reach a body conflict: %v", err)
			}
			if clock.calls != tc.calls {
				t.Fatalf("authority-check clock pin drifted: got %d want %d", clock.calls, tc.calls)
			}
			w2, call2 := tc.setup(t)
			usPolicyFlipClockOn(w2, tc.calls)
			if err := call2(); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) {
				t.Fatalf("late denial joined or missed: %v", err)
			}
		})
	}
}

// (b) A reclaimed sequence is never writable again: the floor is durable and
// physical reclaim (US-26) may have removed the original cohort.
func TestUSPolicyReceiptReclaimedSequenceRefused(t *testing.T) {
	w, r, proof, input := usPolicyReceiptFixture(t, USMemberLibraries, []string{"movies", "music"})
	q := usPolicyReceiptFor(input)
	v, digest := usPolicySavedFixture(t, input)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if _, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usPolicyReceiptCommit(t, turn)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if ack, err := acknowledgeUSPolicyAfterAuthority(t.Context(), turn, a, q); err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usPolicyReceiptCommit(t, turn)
	// Simulate US-26's physical reclaim after the durable floor.
	wc008NativeExec(t, w, `DELETE FROM security_result_libraries`)
	wc008NativeExec(t, w, `DELETE FROM security_command_receipts`)
	wc008NativeExec(t, w, `UPDATE security_command_owners SET reclaimed_sequence=1`)
	turn, a, _ = usPolicyReceiptTurn(t, w, r, proof, input.kind)
	if charged, err := storeUSPolicyResult(t.Context(), turn, a, q, v, digest); !errors.Is(err, ErrConflict) || charged != 0 {
		t.Fatal("reclaimed sequence was writable", charged, err)
	}
	_, _ = turn.transaction.rollback(nil)
	var receipts, libraries, last, count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_result_libraries`).Scan(&libraries); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_sequence,retained_count FROM security_command_owners`).Scan(&last, &count); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || libraries != 0 || last != 1 || count != 1 {
		t.Fatal("refused re-store disturbed the reclaimed owner", receipts, libraries, last, count)
	}
}

func TestUSPolicyReceiptStatementCapture(t *testing.T) {
	defs := usPolicyReceiptDefinitions()
	if len(defs) != 11 {
		t.Fatal("receipt statement inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal("receipt statement must stay hot without waivers", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_policy_receipt_statements.go", "us_policy_receipts.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete receipt inventory")
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fk, err := cachedForeignKeys(t.Context(), conn, w.config)
	if err != nil || plan.CheckSchemaForeignKeys(&fk) != nil {
		t.Fatal("security FK coverage changed", err)
	}
}
