package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func usPINFenceFixture(t *testing.T, n int) (*executor, usPINFenceScope) {
	t.Helper()
	w := wc008NativeExecutor(t)
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
