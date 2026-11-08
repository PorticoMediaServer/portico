package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Exact relevant CHANGE-015 #271@14d075f7 subset, candidate SHA256
// 44411dfefac6bf6f285040302676807cb4e9d14dc28a0cb6a7468cdca98d7a97.
// Disposable candidate clone only; no installed HN receipt or parent activation.
const usSecretDrainCandidateDDL = `ALTER TABLE authorization_session_families ADD COLUMN security_kind INTEGER NOT NULL CHECK(security_kind IN(0,1));


CREATE TABLE security_credential_secrets(
 credential_id TEXT NOT NULL PRIMARY KEY REFERENCES api_credentials(id),
 kind TEXT NOT NULL CHECK(kind IN('key','app_password')),
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 family_id TEXT NOT NULL,
 family_incarnation BLOB NOT NULL CHECK(length(family_incarnation)=16),
 secret_hash TEXT NOT NULL CHECK(length(secret_hash)=43 AND secret_hash NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(secret_hash,43,1) IN('A','E','I','M','Q','U','Y','c','g','k','o','s','w','0','4','8')),
 key_id TEXT NOT NULL CHECK(length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
 nonce BLOB NOT NULL CHECK(length(nonce)=12),
 body BLOB NOT NULL,
 aad_digest BLOB NOT NULL CHECK(length(aad_digest)=32),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(credential_id AS BLOB))+length(CAST(kind AS BLOB))+length(CAST(account_id AS BLOB))
  +length(CAST(member_incarnation AS BLOB))+length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))
  +length(CAST(family_id AS BLOB))+length(family_incarnation)+length(CAST(secret_hash AS BLOB))
  +length(CAST(key_id AS BLOB))+length(nonce)+length(body)+length(aad_digest)) VIRTUAL,
 CHECK(length(body)=CASE kind WHEN 'key' THEN 62 WHEN 'app_password' THEN 64 END)
) STRICT;


`

// Exact additional immutable receipt cohort from the same candidate.
const usSecretDrainReceiptDDL = `CREATE TABLE security_command_owners(
 id TEXT NOT NULL PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 ingress TEXT NOT NULL CHECK(ingress IN('authenticated','capability','system')),
 server_id TEXT NOT NULL CHECK(length(CAST(server_id AS BLOB)) BETWEEN 1 AND 128),
 issuer_account_id TEXT,
 issuer_member_incarnation TEXT,
 issuer_profile_id TEXT,
 issuer_profile_lifetime TEXT,
 issuer_installation_incarnation TEXT,
 original_family_id TEXT,
 capability_kind TEXT,
 capability_id TEXT,
 capability_digest BLOB,
 last_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_sequence>=0),
 acknowledged_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_sequence>=0 AND acknowledged_sequence<=last_sequence),
 reclaimed_sequence INTEGER NOT NULL DEFAULT 0 CHECK(reclaimed_sequence>=0 AND reclaimed_sequence<=acknowledged_sequence),
 retained_count INTEGER NOT NULL DEFAULT 0 CHECK(retained_count>=0),
 retained_variable_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retained_variable_bytes>=0),
 created_ms INTEGER NOT NULL CHECK(created_ms>=0),
 retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0),
 CHECK(last_sequence-acknowledged_sequence<=1),
 CHECK((retained_count=0)=(retained_variable_bytes=0)),
 CHECK((ingress='authenticated' AND issuer_account_id IS NOT NULL AND length(CAST(issuer_account_id AS BLOB)) BETWEEN 1 AND 128
  AND issuer_member_incarnation IS NOT NULL AND length(issuer_member_incarnation)=32 AND issuer_profile_id IS NOT NULL AND length(CAST(issuer_profile_id AS BLOB)) BETWEEN 1 AND 128
  AND issuer_profile_lifetime IS NOT NULL AND length(issuer_profile_lifetime)=32
  AND issuer_installation_incarnation IS NOT NULL AND length(issuer_installation_incarnation)=32
  AND original_family_id IS NOT NULL AND length(CAST(original_family_id AS BLOB)) BETWEEN 1 AND 128
  AND capability_kind IS NULL AND capability_id IS NULL AND capability_digest IS NULL)
 OR (ingress='capability' AND issuer_account_id IS NULL AND issuer_member_incarnation IS NULL AND issuer_profile_id IS NULL
  AND issuer_profile_lifetime IS NULL AND issuer_installation_incarnation IS NULL AND original_family_id IS NULL
  AND capability_kind IS NOT NULL AND capability_kind IN('invitation','setup','quick_connect','factor','reset','hosted_challenge')
  AND capability_id IS NOT NULL AND length(CAST(capability_id AS BLOB)) BETWEEN 1 AND 128 AND capability_digest IS NOT NULL AND length(capability_digest)=32)
 OR (ingress='system' AND issuer_account_id IS NULL AND issuer_member_incarnation IS NULL AND issuer_profile_id IS NULL
  AND issuer_profile_lifetime IS NULL AND issuer_installation_incarnation IS NULL AND original_family_id IS NULL
  AND capability_kind IS NULL AND capability_id IS NULL AND capability_digest IS NULL)),
 UNIQUE(id,command_incarnation)
) STRICT;
CREATE INDEX security_command_owner_retirement ON security_command_owners(retired_ms,id);

-- Immutable accepted result, not a current permission/account/session source.
-- Only the twelve concrete purposes below are admitted, not the reserved US
-- namespace. Policy/PIN native post-scalars and invitation DTO facts are typed;
-- the sole protected body is creation's exact invitation Code (not a generic
-- result/permission document). Prepare its AES-GCM image outside the writer,
-- then verify the actual code_hash/native manifest before accepting it.
-- result_digest binds original command plus complete scalars/cohorts/Code image.
-- Other planned US purposes are NOT native receipt schemas until explicitly
-- documented and added to this same draft packet before adoption.
CREATE TABLE security_command_receipts(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 command_kind INTEGER NOT NULL CHECK(command_kind IN(511,512,520,521,522,532,533,540,541,542,550,551,552,553)),
 result_version INTEGER NOT NULL CHECK(result_version=1),
 result_codec TEXT NOT NULL CHECK(result_codec IN('session_renewal_v1','session_refresh_v1','password_replace_v1','credential_issue_v1','credential_revoke_v1','pin_set_v1','pin_clear_v1','member_libraries_v1','profile_libraries_v1','profile_restrictions_v1','invitation_create_v1','invitation_edit_v1','invitation_revoke_v1','invitation_short_issue_v1')),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 target_account_id TEXT,
 target_member_incarnation TEXT,
 target_profile_id TEXT,
 target_profile_lifetime TEXT,
 result_revision INTEGER,
 policy_inherits INTEGER,
 result_changed INTEGER,
 result_pin_revision INTEGER,
 result_pin_required INTEGER,
 result_count INTEGER NOT NULL CHECK(result_count BETWEEN 0 AND 1000),
 result_key_id TEXT,
 result_nonce BLOB,
 result_body BLOB,
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(operation_id)+length(request_digest)+length(result_digest)+length(CAST(result_codec AS BLOB))
  +coalesce(length(CAST(target_account_id AS BLOB)),0)+coalesce(length(CAST(target_member_incarnation AS BLOB)),0)
  +coalesce(length(CAST(target_profile_id AS BLOB)),0)+coalesce(length(CAST(target_profile_lifetime AS BLOB)),0)
  +coalesce(length(CAST(result_key_id AS BLOB)),0)+coalesce(length(result_nonce),0)+coalesce(length(result_body),0)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 UNIQUE(owner_id,command_incarnation,operation_id),
 UNIQUE(owner_id,command_incarnation,command_sequence,command_kind),
 FOREIGN KEY(owner_id,command_incarnation) REFERENCES security_command_owners(id,command_incarnation),
 CHECK((command_kind=511 AND result_codec='session_renewal_v1')
  OR (command_kind=512 AND result_codec='session_refresh_v1')
  OR (command_kind=520 AND result_codec='password_replace_v1')
  OR (command_kind=521 AND result_codec='credential_issue_v1')
  OR (command_kind=522 AND result_codec='credential_revoke_v1')
  OR (command_kind=532 AND result_codec='pin_set_v1')
  OR (command_kind=533 AND result_codec='pin_clear_v1')
  OR (command_kind=540 AND result_codec='member_libraries_v1')
  OR (command_kind=541 AND result_codec='profile_libraries_v1')
  OR (command_kind=542 AND result_codec='profile_restrictions_v1')
  OR (command_kind=550 AND result_codec='invitation_create_v1')
  OR (command_kind=551 AND result_codec='invitation_edit_v1')
  OR (command_kind=552 AND result_codec='invitation_revoke_v1')
  OR (command_kind=553 AND result_codec='invitation_short_issue_v1')),
 CHECK((command_kind IN(540,541) AND target_account_id IS NOT NULL AND length(CAST(target_account_id AS BLOB)) BETWEEN 1 AND 128
  AND target_member_incarnation IS NOT NULL AND length(target_member_incarnation)=32
  AND result_revision IS NOT NULL AND result_revision BETWEEN 1 AND 9007199254740991
  AND policy_inherits IS NOT NULL AND policy_inherits IN(0,1)
  AND result_changed IS NOT NULL AND result_changed IN(0,1)
  AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL
  AND ((command_kind=540 AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND policy_inherits=0)
   OR (command_kind=541 AND target_profile_id IS NOT NULL AND length(CAST(target_profile_id AS BLOB)) BETWEEN 1 AND 128
    AND target_profile_lifetime IS NOT NULL AND length(target_profile_lifetime)=32))
  AND (policy_inherits=0 OR result_count=0))
 OR (command_kind IN(532,533) AND target_account_id IS NOT NULL AND length(CAST(target_account_id AS BLOB)) BETWEEN 1 AND 128
  AND target_member_incarnation IS NOT NULL AND length(target_member_incarnation)=32
  AND target_profile_id IS NOT NULL AND length(CAST(target_profile_id AS BLOB)) BETWEEN 1 AND 128
  AND target_profile_lifetime IS NOT NULL AND length(target_profile_lifetime)=32
  AND result_revision IS NOT NULL AND result_revision BETWEEN 1 AND 9007199254740991
  AND result_pin_revision IS NOT NULL AND result_pin_revision>0
  AND result_pin_required IS NOT NULL AND result_pin_required IN(0,1)
  AND ((command_kind=532 AND result_pin_required=1) OR (command_kind=533 AND result_pin_required=0))
  AND result_changed IS NOT NULL AND result_changed=1 AND policy_inherits IS NULL AND result_count=0
  AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind IN(511,512,520,521,522,553) AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count=0 AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind=542 AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count<=64 AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind IN(550,551,552) AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count<=256
  AND ((command_kind=550 AND result_key_id IS NOT NULL AND length(CAST(result_key_id AS BLOB)) BETWEEN 1 AND 128
    AND result_nonce IS NOT NULL AND length(result_nonce)=12 AND result_body IS NOT NULL AND length(result_body)=59)
   OR (command_kind IN(551,552) AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL))))
) WITHOUT ROWID, STRICT;

CREATE TABLE security_credential_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL CHECK(command_kind IN(521,522)),
 credential_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN('key','token','app_password')),
 name TEXT NOT NULL,
 scope_count INTEGER NOT NULL CHECK(scope_count BETWEEN 1 AND 4),
 hint TEXT NOT NULL,
 created_ms INTEGER NOT NULL CHECK(created_ms>=0),
 expires_ms INTEGER NOT NULL CHECK(expires_ms>=0),
 last_used_ms INTEGER NOT NULL CHECK(last_used_ms>=0),
 revoked_ms INTEGER NOT NULL CHECK(revoked_ms>=0),
 state TEXT NOT NULL CHECK(state IN('active','expired','revoked','ended')),
 ended_reason TEXT NOT NULL CHECK(ended_reason IN('','member_disabled','member_deleted','profile_deleted','permission_removed','unavailable')),
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 username TEXT NOT NULL,
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 profile_name TEXT NOT NULL,
 family_id TEXT NOT NULL,
 family_incarnation BLOB NOT NULL CHECK(length(family_incarnation)=16),
 device_id TEXT NOT NULL,
 secret_hash TEXT NOT NULL CHECK(length(secret_hash)=43 AND secret_hash NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(secret_hash,43,1) IN('A','E','I','M','Q','U','Y','c','g','k','o','s','w','0','4','8')),
 secret_key_id TEXT,
 secret_nonce BLOB,
 secret_body BLOB,
 secret_aad_digest BLOB,
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(credential_id AS BLOB))+length(CAST(kind AS BLOB))
  +length(CAST(name AS BLOB))+length(CAST(hint AS BLOB))+length(CAST(state AS BLOB))+length(CAST(ended_reason AS BLOB))
  +length(CAST(account_id AS BLOB))+length(CAST(member_incarnation AS BLOB))+length(CAST(username AS BLOB))
  +length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))+length(CAST(profile_name AS BLOB))
  +length(CAST(family_id AS BLOB))+length(family_incarnation)+length(CAST(device_id AS BLOB))+length(CAST(secret_hash AS BLOB))
  +coalesce(length(CAST(secret_key_id AS BLOB)),0)+coalesce(length(secret_nonce),0)+coalesce(length(secret_body),0)+coalesce(length(secret_aad_digest),0)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind)
  REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind),
 CHECK((state='ended')=(ended_reason<>'')),
 CHECK((kind='app_password' AND hint='') OR (kind IN('key','token') AND length(CAST(hint AS BLOB))=6)),
 CHECK((command_kind=521 AND state='active' AND revoked_ms=0 AND last_used_ms=0
  AND secret_key_id IS NOT NULL AND length(CAST(secret_key_id AS BLOB)) BETWEEN 1 AND 128
  AND secret_nonce IS NOT NULL AND length(secret_nonce)=12
  AND secret_body IS NOT NULL AND length(secret_body)=CASE kind WHEN 'key' THEN 62 WHEN 'token' THEN 63 WHEN 'app_password' THEN 64 END
  AND secret_aad_digest IS NOT NULL AND length(secret_aad_digest)=32)
 OR (command_kind=522 AND secret_key_id IS NULL AND secret_nonce IS NULL AND secret_body IS NULL AND secret_aad_digest IS NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE security_credential_result_scopes(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 3),
 scope TEXT NOT NULL CHECK(scope IN('read','playback','library','admin')),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(scope AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence,ordinal),
 UNIQUE(owner_id,command_incarnation,command_sequence,scope),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence)
  REFERENCES security_credential_results(owner_id,command_incarnation,command_sequence)
) WITHOUT ROWID, STRICT;

`

func usSecretDrainFixture(t *testing.T, kind string) (*executor, usCredentialSecretDrainScope, int64) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, usSecretDrainCandidateDDL)
	wc008NativeExec(t, w, usSecretDrainReceiptDDL)
	s := usCredentialSecretDrainScope{credential: "credential", kind: kind, account: "account", profile: "profile", member: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32), family: "family", server: "server", secretHash: usCredentialFixtureHash("secret-drain-key"), familyIncarnation: [16]byte{1}, familyGeneration: 1}
	wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'','profile')`)
	wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked,revoked_reason,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES('family','server','account','profile','local','member',1,'horizon',1,1,'explicit_signout',?,?,?,1)`, s.member, s.lifetime, s.familyIncarnation[:])
	for _, id := range []string{"credential", "other"} {
		family := s.family
		hash := s.secretHash
		if id == "other" {
			family = "unrelated-family"
			hash = usCredentialFixtureHash("other-drain-key")
		}
		wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,revoked_ms) VALUES(?,?,'account','profile','Key','[]',?,'hint',?,'device',0,1)`, id, kind, hash, family)
		length := 62
		if kind == "app_password" {
			length = 64
		}
		wc008NativeExec(t, w, `INSERT INTO security_credential_secrets(credential_id,kind,account_id,member_incarnation,profile_id,profile_lifetime,family_id,family_incarnation,secret_hash,key_id,nonce,body,aad_digest) VALUES(?,?,'account',?,'profile',?,?,?,?, 'key',zeroblob(12),zeroblob(?),zeroblob(32))`, id, kind, s.member, s.lifetime, family, s.familyIncarnation[:], hash, length)
	}

	ownerInc := [16]byte{9}
	operation := [16]byte{8}
	hint := "abcdef"
	length := 62
	if kind == "app_password" {
		hint = ""
		length = 64
	}
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,last_sequence,retained_count,retained_variable_bytes,created_ms) VALUES('receipt-owner',?,'system','server',1,1,1,1)`, ownerInc[:])
	wc008NativeExec(t, w, `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES('receipt-owner',?,1,?,zeroblob(32),521,1,'credential_issue_v1',1,zeroblob(32),0)`, ownerInc[:], operation[:])
	wc008NativeExec(t, w, `INSERT INTO security_credential_results(owner_id,command_incarnation,command_sequence,command_kind,credential_id,kind,name,scope_count,hint,created_ms,expires_ms,last_used_ms,revoked_ms,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,secret_key_id,secret_nonce,secret_body,secret_aad_digest) VALUES('receipt-owner',?,1,521,'credential',?,'Original name',1,?,1,0,0,0,'active','','account',?,'Original user','profile',?,'Original profile','family',?,'device',?,'original-key',zeroblob(12),?,zeroblob(32))`, ownerInc[:], kind, hint, s.member, s.lifetime, s.familyIncarnation[:], s.secretHash, bytes.Repeat([]byte{0x5a}, length))
	wc008NativeExec(t, w, `INSERT INTO security_credential_result_scopes(owner_id,command_incarnation,command_sequence,ordinal,scope) VALUES('receipt-owner',?,1,0,'read')`, ownerInc[:])
	var bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	return w, s, bytes
}
func usSecretDrainTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	turn := wc008NativeTurn(t, w, USMemberLibraries, usCredentialSecretDrainDefinitions())
	turn.transaction.descriptor.ID = 582
	return turn
}
func TestUSCredentialSecretDrainChildBeforeParent(t *testing.T) {
	for _, kind := range []string{"key", "app_password"} {
		t.Run(kind, func(t *testing.T) {
			w, s, bytes := usSecretDrainFixture(t, kind)
			if _, e := w.db.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); e == nil {
				t.Fatal("source deleted before child drain")
			}
			var originalBody []byte
			var originalName string
			var originalBytes int64
			if e := w.db.QueryRowContext(t.Context(), `SELECT name,secret_body,variable_bytes FROM security_credential_results WHERE credential_id='credential'`).Scan(&originalName, &originalBody, &originalBytes); e != nil {
				t.Fatal(e)
			}
			turn := usSecretDrainTurn(t, w)
			out, err := drainUSCredentialSecret(t.Context(), turn, s)
			if err != nil || !out.removed || out.variableBytes != bytes {
				t.Fatal(out, err)
			}
			again, err := drainUSCredentialSecret(t.Context(), turn, s)
			if err != nil || again != (usCredentialSecretDrainEffect{}) {
				t.Fatal("double refund", again, err)
			}
			// Parent source and family remain until the independently authorized parent
			// chooses retirement; a child effect is not a source-delete capability.
			var yes int
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM api_credentials WHERE id='credential'`).Scan(&yes); err != nil || yes != 1 {
				t.Fatal(yes, err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_credential_secrets WHERE credential_id='other'`).Scan(&yes); err != nil || yes != 1 {
				t.Fatal("foreign secret changed", yes, err)
			}
			var savedBody []byte
			var savedName string
			var savedBytes int64
			if e := w.db.QueryRowContext(t.Context(), `SELECT name,secret_body,variable_bytes FROM security_credential_results WHERE credential_id='credential'`).Scan(&savedName, &savedBody, &savedBytes); e != nil {
				t.Fatal(e)
			}
			if savedName != originalName || string(savedBody) != string(originalBody) || savedBytes != originalBytes {
				t.Fatal("immutable issued response changed after source/secret retirement")
			}
			var scope string
			if e := w.db.QueryRowContext(t.Context(), `SELECT scope FROM security_credential_result_scopes WHERE owner_id='receipt-owner'`).Scan(&scope); e != nil || scope != "read" {
				t.Fatal("immutable scope cohort changed", e)
			}

		})
	}
}
func TestUSCredentialSecretDrainPrefixRollback(t *testing.T) {
	w, s, bytes := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	delete(turn.steps, usSecretDrainAbsent) // fail after actual secret DELETE
	out, err := drainUSCredentialSecret(t.Context(), turn, s)
	if err == nil || out != (usCredentialSecretDrainEffect{}) {
		t.Fatal(out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
		t.Fatal(e)
	}
	var restored int64
	if e := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&restored); e != nil || restored != bytes {
		t.Fatal(restored, e)
	}
	if _, e := w.db.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); e == nil {
		t.Fatal("source guard lost after rollback")
	}
}
func TestUSCredentialSecretDrainScopeAndLeaf(t *testing.T) {
	for _, mode := range []string{"family_incarnation", "generation", "ack", "secret_binding", "live_source", "live_family", "ordinary_family", "descendant", "wrong_hash", "source_hash", "row_hash", "server", "member", "row_member"} {
		t.Run(mode, func(t *testing.T) {
			w, s, _ := usSecretDrainFixture(t, "key")
			switch mode {
			case "wrong_hash":
				s.secretHash = usCredentialFixtureHash("wrong-hash")
			case "source_hash":
				wc008NativeExec(t, w, `UPDATE api_credentials SET secret_hash=? WHERE id='credential'`, usCredentialFixtureHash("changed-source-hash"))
			case "row_hash":
				wc008NativeExec(t, w, `UPDATE security_credential_secrets SET secret_hash=? WHERE credential_id='credential'`, usCredentialFixtureHash("changed-row-hash"))
			case "server":
				s.server = "other-server"
			case "member":
				s.member = strings.Repeat("d", 32)
			case "row_member":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_member_incarnation='dddddddddddddddddddddddddddddddd' WHERE id='family'`)
			case "family_incarnation":
				s.familyIncarnation[0]++
			case "generation":
				s.familyGeneration++
			case "ack":
				s.acknowledged++
			case "secret_binding":
				wc008NativeExec(t, w, `UPDATE security_credential_secrets SET profile_lifetime='dddddddddddddddddddddddddddddddd' WHERE credential_id='credential'`)
			case "live_source":
				wc008NativeExec(t, w, `UPDATE api_credentials SET revoked_ms=0 WHERE id='credential'`)
			case "live_family":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=0,revoked_reason='' WHERE id='family'`)
			case "ordinary_family":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=0 WHERE id='family'`)
			case "descendant":
				wc008NativeExec(t, w, `UPDATE api_credentials SET parent_id='credential' WHERE id='other'`)
			}
			turn := usSecretDrainTurn(t, w)
			if out, e := drainUSCredentialSecret(t.Context(), turn, s); e == nil || out != (usCredentialSecretDrainEffect{}) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSCredentialSecretDrainFullFrameAndLifetime(t *testing.T) {
	w, s, _ := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	tx := turn.transaction
	for _, mode := range []string{"purpose", "begun", "done", "policy", "durability", "synchronous"} {
		id, begun, done, policy, durability, synchronous := tx.descriptor.ID, tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous
		switch mode {
		case "purpose":
			tx.descriptor.ID = USMemberLibraries
		case "begun":
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
		out, e := drainUSCredentialSecret(t.Context(), turn, s)
		tx.descriptor.ID, tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous = id, begun, done, policy, durability, synchronous
		if out != (usCredentialSecretDrainEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(mode, out, e)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, e := drainUSCredentialSecret(canceled, turn, s); e == nil || out != (usCredentialSecretDrainEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUSCredentialSecretDrainNativePrepare(t *testing.T) {
	w, _, _ := usSecretDrainFixture(t, "key")
	wc008NativePrepare(t, w, usCredentialSecretDrainDefinitions())
}

func TestUSCredentialSecretDrainExactDeleteCAS(t *testing.T) {
	w, s, bytes := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	row, err := wc008DraftQuery(t.Context(), turn, usSecretDrainDelete, s.credential, s.kind, s.account, s.member, s.profile, s.lifetime, s.family, s.familyIncarnation[:], s.secretHash, bytes+1)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err = row.Scan(&got); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(got, err)
	}
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&got); err != nil || got != bytes {
		t.Fatal(got, err)
	}
}

func TestUSCredentialSecretDrainUnacknowledgedFamily(t *testing.T) {
	for _, expected := range []int64{0, 1} {
		t.Run(fmt.Sprint(expected), func(t *testing.T) {
			w, s, _ := usSecretDrainFixture(t, "key")
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_last_sequence=1,command_acknowledged_sequence=0,revoke_command_sequence=1,revoke_operation_id=zeroblob(16),revoke_request_digest=zeroblob(32),revoke_command_kind='admin_revoke',revoke_result_version=1,revoke_issuer_account_id='issuer',revoke_issuer_profile_id='issuer-profile',revoke_issuer_member_incarnation='cccccccccccccccccccccccccccccccc',revoke_issuer_profile_lifetime='dddddddddddddddddddddddddddddddd',revoke_issuer_family_id='issuer-family',revoke_result_reason='admin_revoke',revoke_result_at='2026-11-08T00:00:00Z',revoke_applied_ms=1,revoke_result_count=1,revoke_result_digest=zeroblob(32),revoked_reason='admin_revoke' WHERE id='family'`)
			s.acknowledged = expected
			turn := usSecretDrainTurn(t, w)
			if out, e := drainUSCredentialSecret(t.Context(), turn, s); out != (usCredentialSecretDrainEffect{}) || !errors.Is(e, ErrConflict) {
				t.Fatal(out, e)
			}
		})
	}
}
