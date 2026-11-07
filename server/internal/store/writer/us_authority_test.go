package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usOwnerAuthorityFixture(t *testing.T) (*executor, *stmt.Registry, *usLibraryPolicyCommand, *usOwnerAuthorityProof) {
	t.Helper()
	w, _, input := usPolicyReceiptFixture(t)
	now := w.now()
	witness := usOwnerAuthorityWitness{issuer: input.request.issuer, device: "issuer-device", lineage: "issuer-lineage", installationID: "issuer-installation-synthetic-000001", tokenExpiry: now.Add(10 * time.Minute).UTC().Format(time.RFC3339), horizon: now.Add(time.Hour).UTC().Format(time.RFC3339)}
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO configuration(key,value) VALUES('id','server') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nil},
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('issuer','issuer',X'','issuer-profile')`, nil},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('issuer',?,'owner')`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO direct_profiles(id,account_id,name,is_primary,lifetime) VALUES('issuer-profile','issuer','Issuer',1,?)`, []any{witness.issuer.profileLifetime}},
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{proof.installationHash, witness.issuer.installation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('issuer-device','issuer',?,'Issuer','test','test','1','','')`, []any{witness.installationID}},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('issuer-lineage','issuer','issuer-device','')`, nil},
		{`INSERT INTO identity_state(singleton,sign_in_kind,authority_generation) VALUES(1,'local',1) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind='local',authority_generation=1`, nil},
		{`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES('issuer-family','server','issuer','issuer-profile','local','owner',1,?,1,'issuer-device','issuer-lineage',?,?,?)`, []any{witness.horizon, witness.issuer.memberIncarnation, witness.issuer.profileLifetime, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family',1,?)`, []any{witness.issuer.tokenDigest, witness.tokenExpiry}},
		{`INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(1,'device','issuer',?,'issuer-device',0)`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family','device',1,1)`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	defs := append(usPolicyDefinitions(), usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usAuthorityDefinitions()...)
	r := nativeInventoryFixtureRegistry(t, w, defs, "us_authority_test.go")
	return w, r, input, proof
}

func TestUSOwnerAuthorityPrecedesReceiptAndRechecks(t *testing.T) {
	w, r, input, proof := usOwnerAuthorityFixture(t)
	// A cleared current PIN preserves sign-ins even if a retained family still
	// carries an old PIN gate. Clearing must not rewrite every family.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET pin_gate=1`); err != nil {
		t.Fatal(err)
	}
	turn, synthetic, _ := usPolicyReceiptTurn(t, w, r, input)
	budget := usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: synthetic.nativeFixture}
	auth, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, context.Background(), budget)
	if err != nil || auth == nil {
		t.Fatal(auth, err)
	}
	fresh, err := composeUSPolicyAfterAuthority(t.Context(), turn, auth, usPolicyReceiptRequestFor(input), input)
	if err != nil || fresh.decision != receiptFresh {
		t.Fatal(fresh, err)
	}
	usPolicyFixtureCommit(t, turn)
	// Retained receipts cannot survive a CURRENT deny as disclosure authority.
	if _, err = w.db.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=generation+1`); err != nil {
		t.Fatal(err)
	}
	turn, synthetic, _ = usPolicyReceiptTurn(t, w, r, input)
	budget.fixture = synthetic.nativeFixture
	delete(turn.steps, usSecurityOwner)
	auth, err = authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), budget)
	if auth != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("old generation authorized", auth, err)
	}
}

func TestUSOwnerAuthorityRejectsNativeScopeChanges(t *testing.T) {
	for _, test := range []struct{ name, sql string }{
		{"current_role", `UPDATE direct_memberships SET role='member' WHERE account_id='issuer'`},
		{"global_generation", `UPDATE identity_state SET authority_generation=authority_generation+1`},
		{"device_installation", `UPDATE identity_devices SET installation_id='other-installation' WHERE id='issuer-device'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, r, input, proof := usOwnerAuthorityFixture(t)
			if _, err := w.db.ExecContext(t.Context(), test.sql); err != nil {
				t.Fatal(err)
			}
			turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
			delete(turn.steps, usSecurityOwner)
			out, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: a.nativeFixture})
			if out != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(out, err)
			}
		})
	}
}

func TestUSOwnerAuthorityFinalDisclosureRechecksNativeFence(t *testing.T) {
	w, r, input, proof := usOwnerAuthorityFixture(t)
	turn, synthetic, _ := usPolicyReceiptTurn(t, w, r, input)
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: synthetic.nativeFixture})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another composed prefix advancing the fence in the SAME frame.
	// A previously issued stamp must not publish a result after that transition.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=generation+1`); err != nil {
		t.Fatal(err)
	}
	out, err := composeUSPolicyAfterAuthority(t.Context(), turn, a, usPolicyReceiptRequestFor(input), input)
	if !errors.Is(err, ErrDisclosureDenied) || out.result.ownerID != "" || out.chargedBytes != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("stale stamp disclosed a result", out, err)
	}
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	var revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM direct_memberships WHERE account_id='target'`).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("denied whole prefix survived", revision, err)
	}
}

// Consume the persisted token hash as the comparison witness, with the same
// encoding identity writes in families.go. This remains a native fixture.
func TestUSOwnerAuthorityStoredIdentityDigest(t *testing.T) {
	w, r, input, seeded := usOwnerAuthorityFixture(t)
	witness := seeded.witness
	if err := w.db.QueryRowContext(t.Context(), `SELECT token_hash FROM authorization_family_tokens WHERE family_id=? AND generation=?`, witness.issuer.family, witness.issuer.familyGeneration).Scan(&witness.issuer.tokenDigest); err != nil {
		t.Fatal(err)
	}
	if witness.issuer.tokenDigest != usCredentialFixtureHash("us-issuer-token") {
		t.Fatal("stored identity digest differs")
	}
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	turn, synthetic, _ := usPolicyReceiptTurn(t, w, r, input)
	delete(turn.steps, usSecurityOwner)
	budget := usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: synthetic.nativeFixture}
	out, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), budget)
	if err != nil || out == nil {
		t.Fatal("stored identity digest refused", err)
	}
	witness.issuer.tokenDigest = usCredentialFixtureHash("us-other-issuer-token")
	wrong, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	out, err = authorizeUSOwnerPolicy(t.Context(), turn, wrong, t.Context(), budget)
	if out != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("unmatched digest authorized", err)
	}
}

func TestUSOwnerAuthorityCurrentServerBeforeSavedBinding(t *testing.T) {
	w, r, input, proof := usOwnerAuthorityFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE configuration SET value='another-server' WHERE key='id'`); err != nil {
		t.Fatal(err)
	}
	turn, synthetic, _ := usPolicyReceiptTurn(t, w, r, input)
	delete(turn.steps, usSecurityOwner)
	budget := usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: synthetic.nativeFixture}
	auth, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), budget)
	if auth != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("old server scope reached saved owner lookup", err)
	}
}
