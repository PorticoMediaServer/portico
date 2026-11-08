package writer

import (
	"context"
	"database/sql"
	"errors"
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

func usSecretDrainFixture(t *testing.T, kind string) (*executor, usCredentialSecretDrainScope, int64) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, usSecretDrainCandidateDDL)
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
	for _, mode := range []string{"family_incarnation", "generation", "ack", "secret_binding", "live_source", "live_family", "ordinary_family", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			w, s, _ := usSecretDrainFixture(t, "key")
			switch mode {
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
