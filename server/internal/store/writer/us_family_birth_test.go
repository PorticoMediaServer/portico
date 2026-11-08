package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Exact relevant subset of CHANGE-015 #271@14d075f7, candidate SHA256
// 44411dfefac6bf6f285040302676807cb4e9d14dc28a0cb6a7468cdca98d7a97.
// Test clone only; no canonical DDL, installed HN receipt or runtime enable.
const usFamilyBirthCandidateDDL = `ALTER TABLE identity_devices ADD COLUMN retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0);

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

func usFamilyBirthFixture(t *testing.T) (*executor, usFamilyBirthInput) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, usFamilyBirthCandidateDDL)
	now := w.now()
	in := usFamilyBirthInput{family: "new-family", server: "server", account: "account", profile: "profile", member: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32), device: "device", installationID: "installation", installationIncarnation: strings.Repeat("c", 32), lineage: "lineage", horizon: now.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano), epoch: 1, authorityGeneration: 1, incarnation: [16]byte{1}}
	h := sha256.Sum256([]byte(in.installationID))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO configuration(key,value) VALUES('id','server')`, nil},
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'','profile')`, nil},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('account',?,'member')`, []any{in.member}},
		{`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES('profile','account',?,'Profile',1)`, []any{in.lifetime}},
		{`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`, nil},
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{hex.EncodeToString(h[:]), in.installationIncarnation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen,retired_ms) VALUES('device','account','installation','Device','test','test','1','','',0)`, nil},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('lineage','account','device','')`, nil},
	} {
		wc008NativeExec(t, w, q.sql, q.args...)
	}
	for n, scope := range []string{"device", "profile_pin", "profile_remember", "profile_lineage"} {
		profile, lifetime, device, lineage := in.profile, in.lifetime, "", ""
		if scope == "device" {
			profile, lifetime, device = "", "", in.device
		}
		if scope == "profile_lineage" {
			lineage = in.lineage
		}
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id,updated_ms) VALUES(?,?,'account',?,?,?,?,?,0)`, n+1, scope, in.member, profile, lifetime, device, lineage)
	}
	return w, in
}
func usFamilyBirthTurn(t *testing.T, w *executor, credential bool) *commandTurn {
	t.Helper()
	turn := wc008NativeTurn(t, w, usAPICredentialIssue, usFamilyBirthDefinitions())
	// Synthetic unqualified turn only. Describe(510) is intentionally NOT installed.
	if !credential {
		turn.transaction.descriptor.ID = 510
	}
	return turn
}
func TestUSFamilyBirthKindsAndCurrentFences(t *testing.T) {
	for _, credential := range []bool{false, true} {
		t.Run(fmt.Sprint(credential), func(t *testing.T) {
			w, in := usFamilyBirthFixture(t)
			p, err := prepareUSFamilyBirth(in, credential)
			if err != nil {
				t.Fatal(err)
			}
			turn := usFamilyBirthTurn(t, w, credential)
			out, err := applyUSFamilyBirth(t.Context(), turn, p, credential)
			if err != nil || out.controlBytes <= 0 {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var kind, count, gate int
			if err = w.db.QueryRowContext(t.Context(), `SELECT security_kind,pin_gate FROM authorization_session_families WHERE id='new-family'`).Scan(&kind, &gate); err != nil {
				t.Fatal(err)
			}
			want := 0
			if credential {
				want = 1
			}
			if kind != want || gate != 0 {
				t.Fatal(kind, gate)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='new-family'`).Scan(&count); err != nil || count != 4 {
				t.Fatal(count, err)
			}
			// Provenance is stored in the family, independent of any credential marker.
			if credential {
				wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('credential','key','account','profile','Key','[]',?,'hint','new-family','device',0)`, usCredentialFixtureHash("birth-key"))
				wc008NativeExec(t, w, `DELETE FROM api_credentials`)
				if err = w.db.QueryRowContext(t.Context(), `SELECT security_kind FROM authorization_session_families WHERE id='new-family'`).Scan(&kind); err != nil || kind != 1 {
					t.Fatal(kind, err)
				}
			}
		})
	}
}
func TestUSFamilyBirthCurrentSourceDenials(t *testing.T) {
	for name, q := range map[string]string{
		"device_retired":       `UPDATE identity_devices SET retired_ms=1`,
		"installation_retired": `UPDATE identity_installations SET retired=1`,
		"member_disabled":      `UPDATE direct_memberships SET disabled=1`,
		"member_incarnation":   `UPDATE direct_memberships SET incarnation='dddddddddddddddddddddddddddddddd'`,
		"profile_lifetime":     `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"epoch":                `UPDATE accounts SET epoch=2`,
		"authority":            `UPDATE identity_state SET authority_generation=2`,
		"server":               `UPDATE configuration SET value='other' WHERE key='id'`,
		"fence_denied":         `UPDATE identity_security_fences SET denied=1 WHERE scope_kind='device'`,
		"missing_fence":        `DELETE FROM identity_security_fences WHERE scope_kind='profile_pin'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, in := usFamilyBirthFixture(t)
			wc008NativeExec(t, w, q)
			p, e := prepareUSOrdinaryFamilyBirth(in)
			if e != nil {
				t.Fatal(e)
			}
			turn := usFamilyBirthTurn(t, w, false)
			out, e := applyUSOrdinaryFamilyBirth(t.Context(), turn, p)
			if out != (usFamilyBirthEffect{}) || !errors.Is(e, ErrDisclosureDenied) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSFamilyBirthRollbackAndPINGate(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
	p, err := prepareUSOrdinaryFamilyBirth(in)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, false)
	// Missing final witness step fails after the native family INSERT.
	delete(turn.steps, usFamilyBirthWitness)
	if out, e := applyUSOrdinaryFamilyBirth(t.Context(), turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	turn = usFamilyBirthTurn(t, w, false)
	out, err := applyUSOrdinaryFamilyBirth(t.Context(), turn, p)
	if err != nil || out.pinGate != 1 {
		t.Fatal(out, err)
	}
}
func TestUSFamilyBirthClosedPurposeAndCancellation(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	p, err := prepareUSOrdinaryFamilyBirth(in)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, false)
	turn.transaction.descriptor.ID = USMemberLibraries
	if out, e := applyUSOrdinaryFamilyBirth(t.Context(), turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
	turn.transaction.descriptor.ID = 510
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, e := applyUSOrdinaryFamilyBirth(canceled, turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
	if out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUSFamilyBirthNativePrepareAndRequiredKind(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	wc008NativePrepare(t, w, usFamilyBirthDefinitions())
	for _, kind := range []string{"omitted", "NULL", "2"} {
		q := `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation`
		values := `) VALUES('bad','server','account','profile','local','member',1,'horizon',1,?,?,?)`
		if kind != "omitted" {
			q += `,security_kind`
			values = `) VALUES('bad','server','account','profile','local','member',1,'horizon',1,?,?,?,` + kind + `)`
		}
		if _, e := w.db.ExecContext(t.Context(), q+values, in.member, in.lifetime, in.incarnation[:]); e == nil {
			t.Fatal("accepted", kind)
		}
	}
}

func TestUSFamilyBirthFullFrameGuards(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	p, err := prepareUSCredentialFamilyBirth(in)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, true)
	tx := turn.transaction
	for _, which := range []string{"not_begun", "done", "policy", "durability", "synchronous"} {
		begun, done, verified, durability, synchronous := tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous
		switch which {
		case "not_begun":
			tx.begun = false
		case "done":
			tx.done = true
		case "policy":
			tx.policyVerified = false
		case "durability":
			tx.descriptor.Durability = Normal
		case "synchronous":
			tx.settings.Synchronous = 1
		}
		out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p)
		tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous = begun, done, verified, durability, synchronous
		if out != (usFamilyBirthEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(which, out, e)
		}
	}
}
