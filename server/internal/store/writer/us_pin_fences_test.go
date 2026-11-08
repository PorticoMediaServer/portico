package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Exact proposed B subset only, not canonical/embedded DDL. Registry receipts
// here are unqualified test-only stmttest, never an installed Capture bypass.
const usPINFenceDraftDDL = `ALTER TABLE authorization_session_families ADD COLUMN security_kind INTEGER NOT NULL CHECK(security_kind IN(0,1));


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

func usPINFenceFixture(t *testing.T, n int) (*executor, usPINFenceScope) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, usPINFenceDraftDDL)
	scope := usPINFenceScope{"target", strings.Repeat("a", 32), "profile", strings.Repeat("b", 32)}
	for k := 0; k < n; k++ {
		wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,pin_gate,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,'server','target','profile','local','owner',1,'horizon',1,1,?,?,?,0)`, fmt.Sprint("family-", k), scope.member, scope.lifetime, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}
	return w, scope
}

func TestUSPINFenceBoundedGeneration(t *testing.T) {
	for _, n := range []int{1, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			w, scope := usPINFenceFixture(t, n)
			turn := wc008NativeTurn(t, w, usPINSet, usPINFenceDefinitions())
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, nil)
			if err != nil || got.generation != 1 || got.revision != 1 {
				t.Fatal(got, err)
			}
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after-before != 1 {
				t.Fatal("family fanout", before, after, err)
			}
			again, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1001, nil)
			if err != nil || again.id != got.id || again.generation != 2 || again.revision != 2 {
				t.Fatal(again, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families WHERE pin_gate=1`).Scan(&count); err != nil || count != n {
				t.Fatal("source families rewritten", count, err)
			}
		})
	}
}

func TestUSPINFenceOnlyOneVerifiedKeep(t *testing.T) {
	w, scope := usPINFenceFixture(t, 3)
	turn := wc008NativeTurn(t, w, usPINSet, usPINFenceDefinitions())
	keep := &usPINKeepProof{frame: turn.transaction, issuer: usPolicyIssuer{server: "server", account: scope.account, profile: scope.profile, memberIncarnation: scope.member, profileLifetime: scope.lifetime, family: "family-0", familyGeneration: 1, epoch: 1, authorityGeneration: 1}}
	got, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, keep)
	if err != nil {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families WHERE pin_gate=0`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var generation int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT confirmed_generation FROM authorization_family_security_fences WHERE family_id='family-0' AND scope_kind='profile_pin'`).Scan(&generation); err != nil || generation != got.generation {
		t.Fatal(generation, err)
	}
}

func TestUSPINFenceKeepFailureRollsBackPrefix(t *testing.T) {
	for _, mode := range []string{"wrong_frame", "wrong_lifetime", "stale_family", "stale_epoch", "credential", "missing_step"} {
		t.Run(mode, func(t *testing.T) {
			w, scope := usPINFenceFixture(t, 1)
			if mode == "credential" {
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=1`)
			}
			turn := wc008NativeTurn(t, w, usPINSet, usPINFenceDefinitions())
			keep := &usPINKeepProof{frame: turn.transaction, issuer: usPolicyIssuer{server: "server", account: scope.account, profile: scope.profile, memberIncarnation: scope.member, profileLifetime: scope.lifetime, family: "family-0", familyGeneration: 1, epoch: 1, authorityGeneration: 1}}
			switch mode {
			case "wrong_frame":
				keep.frame = &transaction{}
			case "wrong_lifetime":
				keep.issuer.profileLifetime = strings.Repeat("c", 32)
			case "stale_family":
				keep.issuer.familyGeneration++
			case "stale_epoch":
				keep.issuer.epoch++
			case "missing_step":
				delete(turn.steps, usPINKeepWitness)
			}
			got, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, keep)
			if err == nil || got != (usPINFenceResult{}) {
				t.Fatal(got, err)
			}
			if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
				t.Fatal(e)
			}
			var count int
			if e := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_security_fences`).Scan(&count); e != nil || count != 0 {
				t.Fatal("prefix survived", count, e)
			}
		})
	}
}

func TestUSPINFenceClearOverflowAndFinish(t *testing.T) {
	w, scope := usPINFenceFixture(t, 1)
	turn := wc008NativeTurn(t, w, usPINClear, usPINFenceDefinitions())
	got, err := advanceUSProfilePINFence(t.Context(), turn, scope, 1000, nil)
	if err != nil || got.generation != 1 {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// A cleared PIN is interpreted by US current-source authority; this bounded
	// transition does not issue a sign-in or rewrite retained family PIN gates.
	wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=9007199254740991`)
	turn = wc008NativeTurn(t, w, usPINClear, usPINFenceDefinitions())
	if out, e := advanceUSProfilePINFence(t.Context(), turn, scope, 1001, nil); e == nil || out != (usPINFenceResult{}) {
		t.Fatal(out, e)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, usPINClear, usPINFenceDefinitions())
	turn.transaction.cancel()
	if out, e := advanceUSProfilePINFence(t.Context(), turn, scope, 1001, nil); e == nil || out != (usPINFenceResult{}) {
		t.Fatal(out, e)
	}
}

func TestUSPINFenceNativePrepare(t *testing.T) {
	w, _ := usPINFenceFixture(t, 1)
	wc008NativePrepare(t, w, usPINFenceDefinitions())
}
