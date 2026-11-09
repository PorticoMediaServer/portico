package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUSOwnerAuthorityCurrentSources(t *testing.T) {
	for name, q := range map[string]string{
		"disabled":                 `UPDATE direct_memberships SET disabled=1`,
		"role":                     `UPDATE direct_memberships SET role='admin'`,
		"member_lifetime":          `UPDATE direct_memberships SET incarnation='cccccccccccccccccccccccccccccccc'`,
		"epoch":                    `UPDATE accounts SET epoch=epoch+1`,
		"must_change":              `UPDATE accounts SET must_change=1`,
		"profile_lifetime":         `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"profile_deleted":          `UPDATE direct_profiles SET deleted=1`,
		"main_profile":             `UPDATE direct_profiles SET is_primary=0`,
		"authority":                `UPDATE identity_state SET authority_generation=authority_generation+1`,
		"server":                   `UPDATE configuration SET value='other' WHERE key='id'`,
		"device":                   `UPDATE identity_devices SET retired_ms=1`,
		"device_binding":           `UPDATE identity_devices SET installation_id='other'`,
		"installation":             `UPDATE identity_installations SET retired=1`,
		"family":                   `UPDATE authorization_session_families SET revoked=1`,
		"credential_birth":         `UPDATE authorization_session_families SET security_kind=1`,
		"token":                    `UPDATE authorization_family_tokens SET retired=1`,
		"missing_fence":            `DELETE FROM authorization_family_security_fences`,
		"generation":               `UPDATE identity_security_fences SET generation=generation+1`,
		"denied":                   `UPDATE identity_security_fences SET denied=1`,
		"cast_grant":               `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		"owner_claim_grant":        `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','owner_claim_completion')`,
		"waiting_move":             `INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES('issuer','move','waiting',0)`,
		"sign_in_kind":             `UPDATE identity_state SET sign_in_kind='portico'`,
		"active_transition":        `INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('transition','issuer',1,'local','portico',0,'moving_to_portico',1,0,0,0,'server',0); UPDATE identity_state SET transition_id='transition'`,
		"family_role":              `UPDATE authorization_session_families SET role='member'`,
		"installation_incarnation": `UPDATE identity_installations SET incarnation='dddddddddddddddddddddddddddddddd'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := usOwnerAuthorityFixture(t)
			wc008NativeExec(t, w, q)
			turn, b := usOwnerAuthorityTurn(t, w, r)
			// No owner/header/payload statement exists in this registry: current denial
			// must happen independently of every retained-result lookup.
			a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b)
			if a != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(a, err)
			}
		})
	}
}

func TestUSOwnerAuthorityRecheckAndLifetime(t *testing.T) {
	w, r, p := usOwnerAuthorityFixture(t)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, principal, b)
	if err != nil || a == nil || turn.transaction.settings.Synchronous != 2 {
		t.Fatal(a, err)
	}
	if err = a.recheck(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=generation+1`); err != nil {
		t.Fatal(err)
	}
	if err = a.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if err = a.recheck(t.Context(), &commandTurn{transaction: turn.transaction}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	cancel()
	if err = a.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
}

func TestUSOwnerAuthorityCredentialMarkerDrain(t *testing.T) {
	for _, kind := range []int{0, 1} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			w, r, p := usOwnerAuthorityFixture(t)
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=?`, kind)
			wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('credential','key','issuer','issuer-profile','Key','[]',?,'hint','issuer-family','issuer-device',0)`, usCredentialFixtureHash("synthetic-credential"))
			turn, b := usOwnerAuthorityTurn(t, w, r)
			if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(a, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, `DELETE FROM api_credentials`)
			turn, b = usOwnerAuthorityTurn(t, w, r)
			a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b)
			if kind == 1 {
				if a != nil || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal(a, err)
				}
			} else if err != nil || a == nil {
				t.Fatal(a, err)
			}
		})
	}
}

func TestUSOwnerAuthorityPINFences(t *testing.T) {
	for _, mode := range []string{"cleared", "protected", "missing_pin", "forgotten", "expired_manage"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := usOwnerAuthorityFixture(t)
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
			if mode != "cleared" {
				wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=0`)
				for n, scope := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
					lineage := ""
					if scope == "profile_lineage" {
						lineage = "issuer-lineage"
					}
					wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms) VALUES(?,?,'issuer',?,'issuer-profile',?,?,0)`, n+2, scope, p.witness.issuer.memberIncarnation, p.witness.issuer.profileLifetime, lineage)
					wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family',?,?,1)`, scope, n+2)
				}
				witness := p.witness
				witness.remember = w.now().Add(time.Hour).Format(time.RFC3339)
				witness.manage = w.now().Add(time.Minute).Format(time.RFC3339)
				if mode == "expired_manage" {
					witness.manage = w.now().Add(-time.Second).Format(time.RFC3339)
				}
				var err error
				p, err = prepareUSOwnerAuthority(witness)
				if err != nil {
					t.Fatal(err)
				}
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET remember_until=?,manage_until=?`, witness.remember, witness.manage)
				if mode == "missing_pin" {
					wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_pin'`)
				}
				if mode == "forgotten" {
					wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=generation+1 WHERE scope_kind='profile_lineage'`)
				}
			}
			turn, b := usOwnerAuthorityTurn(t, w, r)
			a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b)
			if mode == "cleared" || mode == "protected" {
				if err != nil || a == nil {
					t.Fatal(a, err)
				}
			} else if a != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(a, err)
			}
		})
	}
}

func TestUSOwnerAuthorityExpiryBoundsRedaction(t *testing.T) {
	w, r, p := usOwnerAuthorityFixture(t)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	low := b
	low.memory = 1
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), low); a != nil || err == nil {
		t.Fatal(a, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if a, err := authorizeUSOwnerPolicy(canceled, turn, p, t.Context(), b); a != nil || err == nil {
		t.Fatal(a, err)
	}
	now := w.now()
	w.now = func() time.Time { return now.Add(10 * time.Minute) }
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(a, err)
	}
	for _, v := range []any{p, *p, p.witness, b, &usOwnerAuthorization{proof: *p}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			s := fmt.Sprintf(format, v)
			if !strings.Contains(s, "redacted") || strings.Contains(s, p.witness.issuer.tokenDigest) || strings.Contains(s, p.witness.installationID) {
				t.Fatal("sensitive formatting")
			}
		}
	}
}

// Native EXPLAIN on the exact proposed B subset. This is a hot-plan control,
// not an HN Capture receipt; the latter is blocked until schema adoption.
func TestUSOwnerAuthorityNativePlans(t *testing.T) {
	w, _, _ := usOwnerAuthorityFixture(t)
	for _, d := range usAuthorityDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(detail)
				}
				count++
			}
			if err = rows.Err(); err != nil || count == 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestUSOwnerAuthorityResetSessionDeniedBeforeDisclosure(t *testing.T) {
	for _, kind := range []string{"owner", "password"} {
		t.Run(kind, func(t *testing.T) {
			w, r, p := usOwnerAuthorityFixture(t)
			turn, b := usOwnerAuthorityTurn(t, w, r)
			a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b)
			if err != nil || a == nil {
				t.Fatal(a, err)
			}
			// Keep the owner/main profile, ordinary birth, must_change=0 and no grant
			// row intact. The independent reset-session marker alone must deny, even
			// after an earlier stamp in this same frame and before any receipt lookup.
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family',?)`, kind); err != nil {
				t.Fatal(err)
			}
			if got, e := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); got != nil || !errors.Is(e, ErrDisclosureDenied) {
				t.Fatal(got, e)
			}
			if e := a.recheck(t.Context(), turn); !errors.Is(e, ErrDisclosureDenied) {
				t.Fatal("reset stamp survived", e)
			}
		})
	}
}

func TestUSOwnerAuthorityClosedPurposeScope(t *testing.T) {
	w, r, p := usOwnerAuthorityFixture(t)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	for _, id := range []CommandID{USMemberLibraries, USProfileLibraries, usLocalPasswordReplace, usAPICredentialIssue, usAPICredentialRevoke, usPINSet, usPINClear, USProfileRestrictions} {
		turn.transaction.descriptor.ID = id
		if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); err != nil || a == nil {
			t.Fatal(id, a, err)
		}
	}
	// Keep every native step available: without the closed purpose gate an
	// unsupported ID would mint a stamp, so the control must detect that mint.
	for id := CommandID(500); id <= 599; id++ {
		if id == USMemberLibraries || id == USProfileLibraries || id == usLocalPasswordReplace || id == usAPICredentialIssue || id == usAPICredentialRevoke || id == usPINSet || id == usPINClear || id == USProfileRestrictions {
			continue
		}
		turn.transaction.descriptor.ID = id
		if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); a != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(id, a, err)
		}
	}
}

func TestUSOwnerAuthorityRequiresLineage(t *testing.T) {
	_, _, p := usOwnerAuthorityFixture(t)
	witness := p.witness
	witness.lineage = ""
	if got, err := prepareUSOwnerAuthority(witness); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
}

func TestUSOwnerAuthorityFixturePermitGate(t *testing.T) {
	w, r, p := usOwnerAuthorityFixture(t)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); a == nil || err != nil {
		t.Fatal(a, err)
	}
	for _, tc := range []struct {
		name   string
		permit *usOwnerAuthorityFixturePermit
	}{
		{name: "nil"},
		{name: "foreign_frame", permit: &usOwnerAuthorityFixturePermit{frame: &transaction{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalidBudget := b
			invalidBudget.fixture = tc.permit
			a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), invalidBudget)
			var refusal *Refusal
			if a != nil || !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
				t.Fatal(a, err)
			}
		})
	}
}

func TestUSOwnerAuthorityRejectsNonlocalFamily(t *testing.T) {
	w, r, p := usOwnerAuthorityFixture(t)
	// The fresh schema's CHECK also permits only local. Seed a deliberately
	// invalid historical/corrupt row to independently exercise the recipe's
	// f.authority='local' predicate; restore CHECK enforcement before the turn.
	wc008NativeExec(t, w, `PRAGMA ignore_check_constraints=ON; UPDATE authorization_session_families SET authority='portico' WHERE id='issuer-family'; PRAGMA ignore_check_constraints=OFF`)
	var ignored int
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA ignore_check_constraints`).Scan(&ignored); err != nil || ignored != 0 {
		t.Fatal(ignored, err)
	}
	var authority string
	if err := w.db.QueryRowContext(t.Context(), `SELECT authority FROM authorization_session_families WHERE id='issuer-family'`).Scan(&authority); err != nil || authority != "portico" {
		t.Fatal(authority, err)
	}
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, p, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(a, err)
	}
}
