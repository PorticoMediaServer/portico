package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"testing"
	"time"
)

// Exact B subset, proposed only. Native fixture captures do not install a
// runtime recipe or alter canonical/embedded DDL. Source: US-DDL-DRAFT.sql
// cfc631526c1f02ff2d2669e9d540500268d4bc3447f32bd63b28863b733fc23b.
const usAuthorityDraftDDL = `ALTER TABLE identity_devices ADD COLUMN retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0);

-- Explicit immutable birth provenance: 0 ordinary interactive family,
-- 1 API/key/token/app-password/integration credential family. No default:
-- every fresh producer must choose its concrete type. A credential family
-- never becomes ordinary when its credential marker is physically drained.
-- Fresh EMPTY pre-release schema only; this changes the phase-1a parent layout.
ALTER TABLE authorization_session_families ADD COLUMN security_kind INTEGER NOT NULL CHECK(security_kind IN(0,1));


CREATE TABLE identity_security_fences(
 id INTEGER PRIMARY KEY,
 scope_kind TEXT NOT NULL CHECK(scope_kind IN('device','profile_pin','profile_remember','profile_lineage')),
 account_id TEXT NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL DEFAULT '',
 profile_lifetime TEXT NOT NULL DEFAULT '',
 device_id TEXT NOT NULL DEFAULT '',
 lineage_id TEXT NOT NULL DEFAULT '',
 generation INTEGER NOT NULL DEFAULT 1 CHECK(generation>0),
 denied INTEGER NOT NULL DEFAULT 0 CHECK(denied IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_ms INTEGER NOT NULL CHECK(updated_ms>=0),
 CHECK((scope_kind='device' AND length(CAST(device_id AS BLOB)) BETWEEN 1 AND 128 AND profile_id='' AND profile_lifetime='' AND lineage_id='')
 OR (scope_kind IN('profile_pin','profile_remember') AND length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 128 AND length(profile_lifetime)=32 AND device_id='' AND lineage_id='' AND denied=0)
 OR (scope_kind='profile_lineage' AND length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 128 AND length(profile_lifetime)=32 AND device_id='' AND length(CAST(lineage_id AS BLOB)) BETWEEN 1 AND 128 AND denied=0)),
 UNIQUE(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id),
 UNIQUE(id,scope_kind)
) STRICT;

-- At most four closed witnesses per family, written at verified issuance or
-- selection/confirmation. A keep-family updates at most its own witness. A
-- generation mismatch denies or narrows to PIN reentry under current source
-- policy; a missing witness is never a grant. FK/indexes bound cleanup probes.
CREATE TABLE authorization_family_security_fences(
 family_id TEXT NOT NULL REFERENCES authorization_session_families(id),
 scope_kind TEXT NOT NULL CHECK(scope_kind IN('device','profile_pin','profile_remember','profile_lineage')),
 fence_id INTEGER NOT NULL,
 confirmed_generation INTEGER NOT NULL CHECK(confirmed_generation>0),
 PRIMARY KEY(family_id,scope_kind),
 FOREIGN KEY(fence_id,scope_kind) REFERENCES identity_security_fences(id,scope_kind)
) WITHOUT ROWID, STRICT;
CREATE INDEX authorization_family_security_fence_parent ON authorization_family_security_fences(fence_id,scope_kind,family_id);

`

func usOwnerAuthorityFixture(t *testing.T) (*executor, *stmt.Registry, *usOwnerAuthorityProof) {
	t.Helper()
	w, _, _ := testExecutor(t)
	// Frozen whole-second current clock, no real-clock assertions.
	now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	if _, err := w.db.ExecContext(t.Context(), usAuthorityDraftDDL); err != nil {
		t.Fatal(err)
	}
	_, request := usPolicyFixture()
	witness := usOwnerAuthorityWitness{issuer: request.issuer, device: "issuer-device", lineage: "issuer-lineage", installationID: "issuer-installation-synthetic-000001", tokenExpiry: now.Add(10 * time.Minute).Format(time.RFC3339), horizon: now.Add(time.Hour).Format(time.RFC3339)}
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
		{`INSERT INTO authorization_session_families(security_kind,id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(0,'issuer-family','server','issuer','issuer-profile','local','owner',1,?,1,'issuer-device','issuer-lineage',?,?,?)`, []any{witness.horizon, witness.issuer.memberIncarnation, witness.issuer.profileLifetime, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family',1,?)`, []any{witness.issuer.tokenDigest, witness.tokenExpiry}},
		{`INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(1,'device','issuer',?,'issuer-device',0)`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family','device',1,1)`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}

	// The current Capture correctly refuses an unadopted B layout against the
	// locked native schema. Keep this unit registry explicitly unqualified;
	// installed Capture/EXPLAIN receipts wait for B promotion, never bypass it.
	r, err := stmttest.New(t, usAuthorityDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, r, proof
}

func usOwnerAuthorityTurn(t *testing.T, w *executor, r *stmt.Registry) (*commandTurn, usOwnerAuthorityBudget) {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), USMemberLibraries, recipe, &testCustody{})
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
	return turn, usOwnerAuthorityBudget{memory: 4 << 20, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
}
