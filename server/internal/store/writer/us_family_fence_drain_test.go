package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Exact relevant CHANGE-015 #271@14d075f7 subset, candidate SHA256
// 44411dfefac6bf6f285040302676807cb4e9d14dc28a0cb6a7468cdca98d7a97.
// Only disposable test clones use this DDL; stmttest is unqualified.
const usFenceDrainCandidateDDL = `ALTER TABLE identity_devices ADD COLUMN retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0);

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

func usFenceDrainFixture(t *testing.T, n int) (*executor, usFamilyFenceDrainScope) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, usFenceDrainCandidateDDL)
	s := usFamilyFenceDrainScope{family: "target", server: "server", account: "account", profile: "profile", member: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32), device: "device", lineage: "lineage", incarnation: [16]byte{1}, generation: 1}
	wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'','profile')`)
	wc008NativeExec(t, w, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device','account','installation','Device','test','test','1','','')`)
	wc008NativeExec(t, w, `INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('lineage','account','device','')`)
	for _, family := range []string{"target", "other"} {
		wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked,revoked_reason,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,'server','account','profile','local','member',1,'horizon',1,1,'explicit_signout','device','lineage',?,?,?,0)`, family, s.member, s.lifetime, s.incarnation[:])
	}
	for k, scope := range []string{"device", "profile_pin", "profile_remember", "profile_lineage"} {
		profile, lifetime, device, lineage := s.profile, s.lifetime, "", ""
		if scope == "device" {
			profile, lifetime, device = "", "", s.device
		}
		if scope == "profile_lineage" {
			lineage = s.lineage
		}
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id,updated_ms) VALUES(?,?,'account',?,?,?,?,?,0)`, k+1, scope, s.member, profile, lifetime, device, lineage)
		wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('other',?,?,1)`, scope, k+1)
		if k < n {
			wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('target',?,?,1)`, scope, k+1)
		}
	}
	return w, s
}
func usFenceDrainTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	turn := wc008NativeTurn(t, w, USMemberLibraries, usFamilyFenceDrainDefinitions())
	turn.transaction.descriptor.ID = 582
	return turn
}
func TestUSFamilyFenceDrainChildrenBeforeParent(t *testing.T) {
	for _, n := range []int{0, 1, 4} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			w, s := usFenceDrainFixture(t, n)
			if n > 0 {
				if _, e := w.db.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`); e == nil {
					t.Fatal("parent deleted before drain")
				}
			}
			// Already advanced/denied retained fences do not turn lawful cleanup into
			// fresh issuer authorization; remove only the old family's own witnesses.
			wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=2`)
			wc008NativeExec(t, w, `UPDATE identity_security_fences SET denied=1 WHERE scope_kind='device'`)
			turn := usFenceDrainTurn(t, w)
			got, err := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
			if err != nil || got.removed != int64(n) {
				t.Fatal(got, err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var other int
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='other'`).Scan(&other); err != nil || other != 4 {
				t.Fatal("foreign witnesses changed", other, err)
			}
		})
	}
}
func TestUSFamilyFenceDrainPrefixRollback(t *testing.T) {
	w, s := usFenceDrainFixture(t, 4)
	turn := usFenceDrainTurn(t, w)
	delete(turn.steps, usFenceDrainRemaining) // fail after all four child DELETEs
	out, err := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
	if err == nil || out != (usFamilyFenceDrainEffect{}) {
		t.Fatal(out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
		t.Fatal(e)
	}
	var count int
	if e := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='target'`).Scan(&count); e != nil || count != 4 {
		t.Fatal(count, e)
	}
	if _, e := w.db.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`); e == nil {
		t.Fatal("parent no longer guarded after rollback")
	}
}
func TestUSFamilyFenceDrainStaleSourceAndCAS(t *testing.T) {
	for _, mode := range []string{"incarnation", "ack", "generation", "wrong_fence_scope"} {
		t.Run(mode, func(t *testing.T) {
			w, s := usFenceDrainFixture(t, 4)
			switch mode {
			case "incarnation":
				s.incarnation[0]++
			case "ack":
				s.acknowledged++
			case "generation":
				s.generation++
			case "wrong_fence_scope":
				wc008NativeExec(t, w, `UPDATE identity_security_fences SET member_incarnation='dddddddddddddddddddddddddddddddd' WHERE id=1`)
			}
			turn := usFenceDrainTurn(t, w)
			if out, e := drainUSFamilyFenceWitnesses(t.Context(), turn, s); e == nil || out != (usFamilyFenceDrainEffect{}) {
				t.Fatal(out, e)
			}
		})
	}
	w, _ := usFenceDrainFixture(t, 4)
	turn := usFenceDrainTurn(t, w)
	row, err := wc008DraftQuery(t.Context(), turn, usFenceDrainDelete, "target", "device", int64(1), int64(2))
	if err != nil {
		t.Fatal(err)
	}
	var yes int64
	if err = row.Scan(&yes); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='target'`).Scan(&yes); err != nil || yes != 4 {
		t.Fatal(yes, err)
	}
}
func TestUSFamilyFenceDrainFullFrameAndLifetime(t *testing.T) {
	w, s := usFenceDrainFixture(t, 1)
	turn := usFenceDrainTurn(t, w)
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
		out, e := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
		tx.descriptor.ID, tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous = id, begun, done, policy, durability, synchronous
		if out != (usFamilyFenceDrainEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(mode, out, e)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, e := drainUSFamilyFenceWitnesses(canceled, turn, s); e == nil || out != (usFamilyFenceDrainEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUSFamilyFenceDrainNativePrepare(t *testing.T) {
	w, _ := usFenceDrainFixture(t, 1)
	wc008NativePrepare(t, w, usFamilyFenceDrainDefinitions())
}
