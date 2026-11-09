package writer

import (
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
	"strings"
	"testing"
)

// Native source-drift and protected-PIN controls exercise every retained read
// and final release boundary. Each refused frame must leave custody unchanged.
type uoReadingAuditDrift struct {
	name string
	sql  []string
}

func uoReadingAuditDrifts() []uoReadingAuditDrift {
	s := func(n string, q ...string) uoReadingAuditDrift { return uoReadingAuditDrift{n, q} }
	ff := strings.Repeat("f", 32)
	return []uoReadingAuditDrift{
		s("token_retired", `UPDATE authorization_family_tokens SET retired=1`),
		s("family_generation_bump", `UPDATE authorization_session_families SET current_generation=2`),
		s("token_expiry_changed", `UPDATE authorization_family_tokens SET expires_at='2099-01-01T00:00:00Z'`),
		s("family_revoked", `UPDATE authorization_session_families SET revoked=1`),
		s("family_epoch", `UPDATE authorization_session_families SET epoch=2`),
		s("family_authority_generation", `UPDATE authorization_session_families SET authority_generation=2`),
		s("family_horizon", `UPDATE authorization_session_families SET authorization_horizon='2099-01-01T00:00:00Z'`),
		s("family_remember", `UPDATE authorization_session_families SET remember_until='2099-01-01T00:00:00Z'`),
		s("family_manage", `UPDATE authorization_session_families SET manage_until='2099-01-01T00:00:00Z'`),
		s("family_device_null", `UPDATE authorization_session_families SET device_id=NULL`),
		s("family_lineage_null", `UPDATE authorization_session_families SET pin_lineage_id=NULL`),
		s("family_member_incarnation", `UPDATE authorization_session_families SET command_member_incarnation='`+ff+`'`),
		s("family_profile_lifetime", `UPDATE authorization_session_families SET command_profile_lifetime='`+ff+`'`),
		s("family_role_vs_member", `UPDATE authorization_session_families SET role='admin'`),
		s("family_unknown_role", `UPDATE authorization_session_families SET role='guest'`),
		s("family_server", `UPDATE authorization_session_families SET server_id='other'`),
		s("security_kind", `UPDATE authorization_session_families SET security_kind=1`),
		s("account_epoch", `UPDATE accounts SET epoch=2`),
		s("must_change", `UPDATE accounts SET must_change=1`),
		s("member_disabled", `UPDATE direct_memberships SET disabled=1`),
		s("member_incarnation", `UPDATE direct_memberships SET incarnation='`+ff+`'`),
		s("profile_deleted", `UPDATE direct_profiles SET deleted=1`),
		s("profile_lifetime", `UPDATE direct_profiles SET lifetime='`+ff+`'`),
		s("pin_set_without_fence", `UPDATE direct_profiles SET pin_hash=X'01'`),
		s("device_retired", `UPDATE identity_devices SET retired_ms=1`),
		s("device_installation_mismatch", `UPDATE identity_devices SET installation_id='other-installation-synthetic-00001'`),
		s("device_api", `UPDATE identity_devices SET platform='api'`),
		s("installation_retired", `UPDATE identity_installations SET retired=1`),
		s("installation_incarnation", `UPDATE identity_installations SET incarnation='`+ff+`'`),
		s("grant", `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`),
		s("credential", `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('c','key','issuer','issuer-profile','n','[]','h','h','issuer-family','issuer-device',1)`),
		s("reset_marker", `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','password')`),
		s("waiting_move", `INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES('issuer','m','waiting',1)`),
		s("device_fence_denied", `UPDATE identity_security_fences SET denied=1`),
		s("device_fence_generation", `UPDATE identity_security_fences SET generation=2`),
		s("family_fence_gone", `DELETE FROM authorization_family_security_fences`),
		s("state_generation", `UPDATE identity_state SET authority_generation=2`),
		s("state_transition", `INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('pending-move','issuer',1,'local','portico',0,'preview',1,1,1,1,'Fixture',0)`, `UPDATE identity_state SET transition_id='pending-move'`),
		s("state_portico", `UPDATE identity_state SET sign_in_kind='portico'`),
		s("server_id", `UPDATE configuration SET value='other' WHERE key='id'`),
	}
}

func uoReadingAuditExec(t *testing.T, turn *commandTurn, q []string) {
	t.Helper()
	for _, x := range q {
		if _, err := turn.transaction.conn.ExecContext(t.Context(), x); err != nil {
			t.Fatalf("drift sql: %s: %v", x, err)
		}
	}
}

func uoReadingAuditHeader() mutationreceipt.Result {
	return mutationreceipt.Result{Status: "applied", TargetID: "t", Revision: 19}
}

func uoReadingAuditCommitOriginal(t *testing.T, w *executor, p *usOwnerAuthorityProof, q uoPublicReadingRequest) uoPublicReadingResult {
	t.Helper()
	turn, a := uoPublicReadingTurn(t, w, p, q.kind)
	c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	v := uoPublicReadingValue(q.kind)
	if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, uoReadingAuditHeader(), v); err != nil {
		t.Fatal(err)
	}
	uoPublicReadingCommit(t, turn)
	return v
}

func uoReadingAuditDump(t *testing.T, w *executor) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"user_mutation_global", "user_mutation_accounts", "user_mutation_receipts", "user_mutation_reading_results"} {
		rows, err := w.db.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err := rows.Scan(p...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", table, v)
		}
		rows.Close()
	}
	return b.String()
}

func TestUOPublicReadingAuthorityDriftMatrix(t *testing.T) {
	for _, d := range uoReadingAuditDrifts() {
		for _, phase := range []string{"check", "save", "final", "replay", "replay_final"} {
			t.Run(d.name+"/"+phase, func(t *testing.T) {
				w, p, q := uoPublicReadingFixture(t, false)
				if strings.HasPrefix(phase, "replay") {
					uoReadingAuditCommitOriginal(t, w, p, q)
				}
				before := uoReadingAuditDump(t, w)
				turn, a := uoPublicReadingTurn(t, w, p, q.kind)
				switch phase {
				case "check", "replay":
					uoReadingAuditExec(t, turn, d.sql)
					out, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
					if err == nil || out != (uoPublicReadingCheck{}) {
						t.Fatalf("drift admitted: %v %v", out, err)
					}
				case "save":
					c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
					if err != nil {
						t.Fatal(err)
					}
					uoReadingAuditExec(t, turn, d.sql)
					if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, uoReadingAuditHeader(), uoPublicReadingValue(q.kind)); err == nil {
						t.Fatal("drift saved")
					}
				case "final":
					c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
					if err != nil {
						t.Fatal(err)
					}
					if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, uoReadingAuditHeader(), uoPublicReadingValue(q.kind)); err != nil {
						t.Fatal(err)
					}
					uoReadingAuditExec(t, turn, d.sql)
				case "replay_final":
					c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
					if err != nil || c.original == nil {
						t.Fatal(c, err)
					}
					uoReadingAuditExec(t, turn, d.sql)
				}
				if _, err := turn.transaction.commit(); err == nil {
					t.Fatal("frame committed after drift")
				}
				if after := uoReadingAuditDump(t, w); after != before {
					t.Fatalf("durable receipt state changed\n%s\n%s", before, after)
				}
			})
		}
	}
}

func uoReadingAuditProtect(t *testing.T, w *executor, p *usOwnerAuthorityProof, kinds ...string) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
	i := p.witness.issuer
	for n, kind := range kinds {
		lineage := ""
		if kind == "profile_lineage" {
			lineage = p.witness.lineage
		}
		id := 10 + n
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms) VALUES(?,?,?,?,?,?,?,0)`, id, kind, i.account, i.memberIncarnation, i.profile, i.profileLifetime, lineage)
		wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family',?,?,1)`, kind, id)
	}
}

func uoReadingAuditRemember(t *testing.T, w *executor, p *usOwnerAuthorityProof) *usOwnerAuthorityProof {
	t.Helper()
	wn := p.witness
	wn.remember = "2099-01-01T00:00:00Z"
	np, err := prepareUSOwnerAuthority(wn)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET remember_until=?`, wn.remember)
	return np
}

func TestUOPublicReadingAuthorityProtectedBranch(t *testing.T) {
	mint := func(t *testing.T, w *executor, p *usOwnerAuthorityProof) error {
		turn := wc008NativeTurn(t, w, 770, uoPublicReadingFixtureDefinitions())
		defer turn.transaction.rollback(nil)
		_, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
		return err
	}
	t.Run("pin_fence_present_allows", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p, "profile_pin")
		if err := mint(t, w, p); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pin_fence_absent_refuses", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p)
		if err := mint(t, w, p); err == nil {
			t.Fatal("admitted")
		}
	})
	t.Run("pin_gate_refuses", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p, "profile_pin")
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
		if err := mint(t, w, p); err == nil {
			t.Fatal("admitted")
		}
	})
	t.Run("pin_fence_generation_refuses", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p, "profile_pin")
		wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=2 WHERE scope_kind='profile_pin'`)
		if err := mint(t, w, p); err == nil {
			t.Fatal("admitted")
		}
	})
	t.Run("remember_all_fences_allows", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p, "profile_pin", "profile_remember", "profile_lineage")
		np := uoReadingAuditRemember(t, w, p)
		if err := mint(t, w, np); err != nil {
			t.Fatal(err)
		}
	})
	for _, missing := range []string{"profile_remember", "profile_lineage"} {
		t.Run("remember_missing_"+missing, func(t *testing.T) {
			w, p, _ := uoPublicReadingFixture(t, false)
			kinds := []string{"profile_pin", "profile_remember", "profile_lineage"}
			var have []string
			for _, k := range kinds {
				if k != missing {
					have = append(have, k)
				}
			}
			uoReadingAuditProtect(t, w, p, have...)
			np := uoReadingAuditRemember(t, w, p)
			if err := mint(t, w, np); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	t.Run("cleared_pin_with_gate_allows", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
		if err := mint(t, w, p); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("expired_remember_still_requires_fences", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		uoReadingAuditProtect(t, w, p, "profile_pin")
		wn := p.witness
		wn.remember = "2001-01-01T00:00:00Z"
		np, err := prepareUSOwnerAuthority(wn)
		if err != nil {
			t.Fatal(err)
		}
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET remember_until=?`, wn.remember)
		if err := mint(t, w, np); err == nil {
			t.Fatal("admitted")
		}
	})
}

func TestUOPublicReadingCurrentEffectiveRole(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		for _, primary := range []int{0, 1} {
			for _, mismatch := range []bool{false, true} {
				t.Run(fmt.Sprint(role, "/", primary, "/", mismatch), func(t *testing.T) {
					w, p, _ := uoPublicReadingFixture(t, false)
					effective := role
					if primary == 0 {
						effective = "member"
					}
					family := effective
					if mismatch {
						family = "owner"
						if family == effective {
							family = "member"
						}
					}
					wc008NativeExec(t, w, `UPDATE direct_memberships SET role=?`, role)
					wc008NativeExec(t, w, `UPDATE direct_profiles SET is_primary=?`, primary)
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET role=?`, family)
					turn := wc008NativeTurn(t, w, 770, uoPublicReadingFixtureDefinitions())
					a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
					if mismatch && (err == nil || a != nil) {
						t.Fatal("mismatched effective family role minted", a, err)
					}
					if !mismatch && (err != nil || a == nil) {
						t.Fatal("valid effective family role refused", a, err)
					}
				})
			}
		}
	}
}

func TestUOPublicReadingRootReadErrorIsNotGone(t *testing.T) {
	w, p, q := uoPublicReadingFixture(t, true)
	defs := uoPublicReadingFixtureDefinitions()
	for n := range defs {
		if defs[n].ID == uoPublicReadingRoot {
			defs[n].SQL = strings.Replace(defs[n].SQL, "SELECT state FROM", "SELECT CASE WHEN abs(-9223372036854775807-1) THEN state END FROM", 1)
		}
	}
	turn := wc008NativeTurn(t, w, q.kind, defs)
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err == nil || errors.Is(err, mutationreceipt.ErrRootGone) {
		t.Fatal("read failure misclassified as gone", err)
	}
}

func TestUOPublicReadingPrimaryPointerMustMatch(t *testing.T) {
	w, p, _ := uoPublicReadingFixture(t, false)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET is_primary=1`)
	wc008NativeExec(t, w, `UPDATE accounts SET profile_id='other-profile'`)
	turn := wc008NativeTurn(t, w, 770, uoPublicReadingFixtureDefinitions())
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err == nil || a != nil {
		t.Fatal("inconsistent primary admitted", a, err)
	}
}
