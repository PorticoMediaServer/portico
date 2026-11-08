package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func usFenceDrainFixture(t *testing.T, n int) (*executor, usFamilyFenceDrainScope) {
	t.Helper()
	w := wc008NativeExecutor(t)
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
				_, e := w.db.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`)
				usFenceDrainRequireFK(t, e)
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
	_, e := w.db.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`)
	usFenceDrainRequireFK(t, e)
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

func usFenceDrainRequireFK(t *testing.T, err error) {
	t.Helper()
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 787 {
		t.Fatalf("want native FK787, got %v", err)
	}
}
func usFenceDrainRevokeHeader(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_last_sequence=1,command_acknowledged_sequence=0,revoke_command_sequence=1,revoke_operation_id=zeroblob(16),revoke_request_digest=zeroblob(32),revoke_command_kind='admin_revoke',revoke_result_version=1,revoke_issuer_account_id='issuer',revoke_issuer_profile_id='issuer-profile',revoke_issuer_member_incarnation='cccccccccccccccccccccccccccccccc',revoke_issuer_profile_lifetime='dddddddddddddddddddddddddddddddd',revoke_issuer_family_id='issuer-family',revoke_result_reason='admin_revoke',revoke_result_at='2026-11-08T00:00:00Z',revoke_applied_ms=1,revoke_result_count=1,revoke_result_digest=zeroblob(32),revoked_reason='admin_revoke' WHERE id='target'`)
}
func TestUSFamilyFenceDrainNullablePointers(t *testing.T) {
	for _, n := range []int{0, 2, 4} {
		for _, mode := range []string{"device", "lineage", "both"} {
			t.Run(fmt.Sprint(n)+mode, func(t *testing.T) {
				w, s := usFenceDrainFixture(t, n)
				if mode == "device" || mode == "both" {
					wc008NativeExec(t, w, `DELETE FROM identity_devices WHERE id='device'`)
					s.device = ""
				}
				if mode == "lineage" || mode == "both" {
					wc008NativeExec(t, w, `DELETE FROM identity_pin_lineages WHERE id='lineage'`)
					s.lineage = ""
				}
				turn := usFenceDrainTurn(t, w)
				out, err := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
				if err != nil || out.removed != int64(n) {
					t.Fatal(out, err)
				}
				if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='target'`); err != nil {
					t.Fatal(err)
				}
				if _, err = turn.transaction.commit(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestUSFamilyFenceDrainNullComparisonAndFence(t *testing.T) {
	for _, mode := range []string{"expected_null_device", "expected_null_lineage", "expected_live_device", "expected_live_lineage", "foreign_fence_with_null"} {
		t.Run(mode, func(t *testing.T) {
			w, s := usFenceDrainFixture(t, 4)
			switch mode {
			case "expected_null_device":
				s.device = ""
			case "expected_null_lineage":
				s.lineage = ""
			case "expected_live_device":
				wc008NativeExec(t, w, `DELETE FROM identity_devices WHERE id='device'`)
			case "expected_live_lineage":
				wc008NativeExec(t, w, `DELETE FROM identity_pin_lineages WHERE id='lineage'`)
			case "foreign_fence_with_null":
				wc008NativeExec(t, w, `DELETE FROM identity_devices WHERE id='device'; UPDATE identity_security_fences SET member_incarnation='dddddddddddddddddddddddddddddddd' WHERE id=1`)
				s.device = ""
			}
			turn := usFenceDrainTurn(t, w)
			if out, e := drainUSFamilyFenceWitnesses(t.Context(), turn, s); out != (usFamilyFenceDrainEffect{}) || !errors.Is(e, ErrConflict) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSFamilyFenceDrainLiveAndUnacknowledged(t *testing.T) {
	for _, mode := range []string{"live", "last", "ack"} {
		t.Run(mode, func(t *testing.T) {
			w, s := usFenceDrainFixture(t, 4)
			if mode == "live" {
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=0 WHERE id='target'`)
			} else {
				usFenceDrainRevokeHeader(t, w)
				if mode == "ack" {
					s.acknowledged = 1
				}
			}
			turn := usFenceDrainTurn(t, w)
			if out, e := drainUSFamilyFenceWitnesses(t.Context(), turn, s); out != (usFamilyFenceDrainEffect{}) || !errors.Is(e, ErrConflict) {
				t.Fatal(mode, out, e)
			}
		})
	}
}
func TestUSFamilyFenceDrainUnexpectedScope(t *testing.T) {
	w, s := usFenceDrainFixture(t, 4)
	// Test-only future child shape: widen the scope and retain family/fence PK
	// references. It models a new scope unknown to this four-case implementation;
	// canonical schema and constraint enforcement are untouched.
	wc008NativeExec(t, w, `ALTER TABLE authorization_family_security_fences RENAME TO old_fence_children;
 CREATE TABLE authorization_family_security_fences(family_id TEXT NOT NULL REFERENCES authorization_session_families(id),scope_kind TEXT NOT NULL CHECK(scope_kind IN('device','profile_pin','profile_remember','profile_lineage','future')),fence_id INTEGER NOT NULL REFERENCES identity_security_fences(id),confirmed_generation INTEGER NOT NULL CHECK(confirmed_generation>0),PRIMARY KEY(family_id,scope_kind)) WITHOUT ROWID,STRICT;
 INSERT INTO authorization_family_security_fences SELECT * FROM old_fence_children;
 DROP TABLE old_fence_children;
 INSERT INTO authorization_family_security_fences VALUES('target','future',1,1)`)
	turn := usFenceDrainTurn(t, w)
	out, err := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
	if out != (usFamilyFenceDrainEffect{}) || !errors.Is(err, ErrConflict) {
		t.Fatal(out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
		t.Fatal(e)
	}
	var count int
	if e := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='target'`).Scan(&count); e != nil || count != 5 {
		t.Fatal(count, e)
	}
}
func TestUSFamilyFenceDrainMalformedAndRedacted(t *testing.T) {
	w, original := usFenceDrainFixture(t, 1)
	turn := usFenceDrainTurn(t, w)
	for _, mode := range []string{"family", "server", "account", "profile", "member", "lifetime", "incarnation", "generation", "ack", "device", "lineage"} {
		s := original
		switch mode {
		case "family":
			s.family = ""
		case "server":
			s.server = ""
		case "account":
			s.account = ""
		case "profile":
			s.profile = ""
		case "member":
			s.member = "short"
		case "lifetime":
			s.lifetime = "bad"
		case "incarnation":
			s.incarnation = [16]byte{}
		case "generation":
			s.generation = 0
		case "ack":
			s.acknowledged = -1
		case "device":
			s.device = strings.Repeat("x", 129)
		case "lineage":
			s.lineage = strings.Repeat("x", 129)
		}
		if out, e := drainUSFamilyFenceWitnesses(t.Context(), turn, s); out != (usFamilyFenceDrainEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(mode, out, e)
		}
	}
	for _, f := range []string{"%v", "%+v", "%#v"} {
		got := fmt.Sprintf(f, original)
		if !strings.Contains(got, "redacted") || strings.Contains(got, original.member) {
			t.Fatal("scope leaked", got)
		}
	}
}
func TestUSFamilyFenceDrainCancelAfterPrefix(t *testing.T) {
	w, s := usFenceDrainFixture(t, 4)
	turn := usFenceDrainTurn(t, w)
	tx := turn.transaction
	sawPrefix := false
	tx.custody.(*testCustody).checkHook = func() {
		var count int
		if e := tx.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='target'`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count < 4 {
			sawPrefix = true
			tx.cancel()
		}
	}
	out, err := drainUSFamilyFenceWitnesses(t.Context(), turn, s)
	if !sawPrefix || out != (usFamilyFenceDrainEffect{}) || !errors.Is(err, context.Canceled) {
		t.Fatal(sawPrefix, out, err)
	}
	tx.custody.(*testCustody).checkHook = nil
	if _, e := tx.rollback(err); !errors.Is(e, err) {
		t.Fatal(e)
	}
	var count int
	if e := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='target'`).Scan(&count); e != nil || count != 4 {
		t.Fatal(count, e)
	}
}
