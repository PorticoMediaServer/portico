package writer

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"strings"
	"testing"
	"time"
)

// Compile the exact US-13 narrow seam without defining or depending on its
// separately-owned interface/test constructor in this prerequisite commit.
var _ interface {
	Issuer() string
	ManagesTier(string) bool
	Recheck(context.Context, *commandTurn) error
} = (*usInvitationAuthorization)(nil)

func usInvitationAuthorityFixture(t *testing.T, role string) (*executor, *usOwnerAuthorityProof) {
	t.Helper()
	w, _, p := usOwnerAuthorityFixture(t)
	wc008NativeExec(t, w, `UPDATE direct_memberships SET role=?`, role)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET role=?`, role)
	return w, p
}
func usInvitationAuthorityTurn(t *testing.T, w *executor, kind CommandID) (*commandTurn, usInvitationAuthorityBudget) {
	t.Helper()
	turn := wc008NativeTurn(t, w, kind, usInvitationAuthorityDefinitions())
	return turn, usInvitationAuthorityBudget{memory: 4 << 20, fixture: &usOwnerAuthorityFixturePermit{frame: turn.transaction}}
}
func usInvitationAuthorityMint(t *testing.T, w *executor, p *usOwnerAuthorityProof, kind CommandID) (*commandTurn, *usInvitationAuthorization) {
	t.Helper()
	turn, b := usInvitationAuthorityTurn(t, w, kind)
	a, err := authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b)
	if err != nil || a == nil {
		t.Fatal("valid manager", a, err)
	}
	return turn, a
}
func usInvitationAuthorityDrift(t *testing.T, turn *commandTurn, sqls ...string) {
	t.Helper()
	for _, q := range sqls {
		if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
			t.Fatal("native source control", err)
		}
	}
}

func TestUSInvitationAuthorityRolesPurposesAndIssuer(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		for _, kind := range []CommandID{550, 551, 552} {
			t.Run(fmt.Sprint(role, "/", kind), func(t *testing.T) {
				w, p := usInvitationAuthorityFixture(t, role)
				turn, a := usInvitationAuthorityMint(t, w, p, kind)
				if a.Issuer() != p.witness.issuer.account {
					t.Fatal("issuer binding")
				}
				for _, target := range []string{"member", "admin", "owner", "account", "", "bogus"} {
					want := target == "member" || role == "owner" && target == "admin"
					if a.ManagesTier(target) != want {
						t.Fatalf("%s manages %s != %v", role, target, want)
					}
				}
				if err := a.Recheck(t.Context(), turn); err != nil {
					t.Fatal(err)
				}
				// This same admin fixture must still fail US-01's unmodified owner arm.
				_, _ = turn.transaction.rollback(nil)
				if role == "admin" {
					registry, err := stmttest.New(t, usAuthorityDefinitions())
					if err != nil {
						t.Fatal(err)
					}
					old, b := usOwnerAuthorityTurn(t, w, registry)
					if out, err := authorizeUSOwnerPolicy(t.Context(), old, p, t.Context(), b); out != nil || !errors.Is(err, ErrDisclosureDenied) {
						t.Fatal("US-01 widened", out, err)
					}
				}
			})
		}
	}
}

func TestUSInvitationAuthorityCurrentSourceDenials(t *testing.T) {
	controls := []struct {
		name string
		sql  []string
	}{
		{"token_retired", []string{`UPDATE authorization_family_tokens SET retired=1`}},
		{"token_expiry", []string{`UPDATE authorization_family_tokens SET expires_at='2099-01-01T00:00:00Z'`}},
		{"family_generation", []string{`UPDATE authorization_session_families SET current_generation=2`}},
		{"family_revoked", []string{`UPDATE authorization_session_families SET revoked=1`}},
		{"family_epoch", []string{`UPDATE authorization_session_families SET epoch=2`}},
		{"family_account", []string{`UPDATE authorization_session_families SET account_id='foreign'`}},
		{"family_profile", []string{`UPDATE authorization_session_families SET profile_id='foreign'`}},
		{"family_server", []string{`UPDATE authorization_session_families SET server_id='foreign'`}},
		{"family_authority_generation", []string{`UPDATE authorization_session_families SET authority_generation=2`}},
		{"family_peer_role", []string{`UPDATE authorization_session_families SET role=CASE WHEN role='owner' THEN 'admin' ELSE 'owner' END`}},
		{"family_remember", []string{`UPDATE authorization_session_families SET remember_until='2099-01-01T00:00:00Z'`}},
		{"family_manage", []string{`UPDATE authorization_session_families SET manage_until='2099-01-01T00:00:00Z'`}},
		{"family_security_kind", []string{`UPDATE authorization_session_families SET security_kind=1`}},
		{"family_account_scope", []string{`UPDATE authorization_session_families SET role='account'`}},
		{"family_member", []string{`UPDATE authorization_session_families SET role='member'`}},
		{"family_horizon", []string{`UPDATE authorization_session_families SET authorization_horizon='2099-01-01T00:00:00Z'`}},
		{"family_member_lifetime", []string{`UPDATE authorization_session_families SET command_member_incarnation='ffffffffffffffffffffffffffffffff'`}},
		{"family_profile_lifetime", []string{`UPDATE authorization_session_families SET command_profile_lifetime='ffffffffffffffffffffffffffffffff'`}},
		{"family_device", []string{`UPDATE authorization_session_families SET device_id=NULL`}},
		{"family_lineage", []string{`UPDATE authorization_session_families SET pin_lineage_id=NULL`}},
		{"member_disabled", []string{`UPDATE direct_memberships SET disabled=1`}},
		{"member_tier", []string{`UPDATE direct_memberships SET role='member'`}},
		{"member_incarnation", []string{`UPDATE direct_memberships SET incarnation='ffffffffffffffffffffffffffffffff'`}},
		{"account_epoch", []string{`UPDATE accounts SET epoch=2`}},
		{"must_change", []string{`UPDATE accounts SET must_change=1`}},
		{"secondary", []string{`UPDATE direct_profiles SET is_primary=0`}},
		{"primary_pointer", []string{`UPDATE accounts SET profile_id='foreign-profile'`}},
		{"profile_deleted", []string{`UPDATE direct_profiles SET deleted=1`}},
		{"profile_lifetime", []string{`UPDATE direct_profiles SET lifetime='ffffffffffffffffffffffffffffffff'`}},
		{"device_retired", []string{`UPDATE identity_devices SET retired_ms=1`}},
		{"device_foreign_account", []string{`UPDATE identity_devices SET account_id='foreign'`}},
		{"device_installation", []string{`UPDATE identity_devices SET installation_id='different-installation-synthetic-00001'`}},
		{"device_api", []string{`UPDATE identity_devices SET platform='api'`}},
		{"installation_retired", []string{`UPDATE identity_installations SET retired=1`}},
		{"installation_incarnation", []string{`UPDATE identity_installations SET incarnation='ffffffffffffffffffffffffffffffff'`}},
		{"cast_grant", []string{`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`}},
		{"owner_grant", []string{`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','owner_claim_completion')`}},
		{"credential_marker", []string{`INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('c','key','issuer','issuer-profile','n','[]','h','h','issuer-family','issuer-device',1)`}},
		{"reset_owner", []string{`INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`}},
		{"reset_password", []string{`INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','password')`}},
		{"waiting", []string{`INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES('issuer','m','waiting',1)`}},
		{"device_fence", []string{`UPDATE identity_security_fences SET generation=2`}},
		{"denied_fence", []string{`UPDATE identity_security_fences SET denied=1`}},
		{"missing_witness", []string{`DELETE FROM authorization_family_security_fences`}},
		{"authority_generation", []string{`UPDATE identity_state SET authority_generation=2`}},
		{"portico", []string{`UPDATE identity_state SET sign_in_kind='portico'`}},
		{"transition", []string{`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('m','issuer',1,'local','portico',0,'moving_to_portico',1,0,0,0,'server',0)`, `UPDATE identity_state SET transition_id='m'`}},
		{"server", []string{`UPDATE configuration SET value='foreign' WHERE key='id'`}},
	}
	for _, role := range []string{"owner", "admin"} {
		for _, c := range controls {
			for _, afterMint := range []bool{false, true} {
				t.Run(fmt.Sprint(role, "/", c.name, "/", afterMint), func(t *testing.T) {
					w, p := usInvitationAuthorityFixture(t, role)
					turn, b := usInvitationAuthorityTurn(t, w, 550)
					var a *usInvitationAuthorization
					var err error
					if afterMint {
						a, err = authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b)
						if err != nil {
							t.Fatal("live control", err)
						}
					}
					// A real nonempty prefix makes ignored refusal/COMMIT meaningful.
					usInvitationAuthorityDrift(t, turn, `UPDATE search_revision SET revision=2`)
					usInvitationAuthorityDrift(t, turn, c.sql...)
					if afterMint {
						err = a.Recheck(t.Context(), turn)
					} else {
						a, err = authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b)
					}
					// No retained owner/header/payload definition is in this registry: the
					// denial must come from native sources, never a missing receipt step.
					if !errors.Is(err, ErrDisclosureDenied) || !afterMint && a != nil {
						t.Fatal("current source admitted", a, err)
					}
					if afterMint && (a.Issuer() != "" || a.ManagesTier("member")) {
						t.Fatal("refused proof still usable")
					}
					if _, err = turn.transaction.commit(); err == nil {
						t.Fatal("ignored authority error committed")
					}
					var revision int
					if err = w.db.QueryRowContext(t.Context(), `SELECT revision FROM search_revision`).Scan(&revision); err != nil || revision != 1 {
						t.Fatal("prefix persisted", revision, err)
					}
				})
			}
		}
	}
}

func usInvitationAuthorityProtect(t *testing.T, w *executor, p *usOwnerAuthorityProof, remember bool) *usOwnerAuthorityProof {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
	i := p.witness.issuer
	for n, kind := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
		lineage := ""
		if kind == "profile_lineage" {
			lineage = p.witness.lineage
		}
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms) VALUES(?,?,?,?,?,?,?,0)`, n+2, kind, i.account, i.memberIncarnation, i.profile, i.profileLifetime, lineage)
		wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family',?,?,1)`, kind, n+2)
	}
	if !remember {
		return p
	}
	witness := p.witness
	witness.remember = w.now().Add(20 * time.Minute).Format(time.RFC3339)
	witness.manage = w.now().Add(5 * time.Minute).Format(time.RFC3339)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET remember_until=?,manage_until=?`, witness.remember, witness.manage)
	out, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestUSInvitationAuthorityPINFences(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		for _, mode := range []string{"protected", "remembered", "gate", "pin_missing", "pin_generation", "remember_missing", "lineage_missing", "lineage_generation", "manage_expired", "remember_expired", "remember_expired_lineage", "clear"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				w, p := usInvitationAuthorityFixture(t, role)
				p = usInvitationAuthorityProtect(t, w, p, mode != "protected")
				switch mode {
				case "gate":
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
				case "pin_missing":
					wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_pin'`)
				case "pin_generation":
					wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=2 WHERE scope_kind='profile_pin'`)
				case "remember_missing":
					wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_remember'`)
				case "lineage_missing":
					wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_lineage'`)
				case "lineage_generation":
					wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=2 WHERE scope_kind='profile_lineage'`)
				case "manage_expired":
					witness := p.witness
					witness.manage = w.now().Add(-time.Second).Format(time.RFC3339)
					var err error
					p, err = prepareUSOwnerAuthority(witness)
					if err != nil {
						t.Fatal(err)
					}
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET manage_until=?`, witness.manage)
				case "remember_expired", "remember_expired_lineage":
					witness := p.witness
					witness.remember = w.now().Add(-time.Minute).Format(time.RFC3339)
					witness.manage = ""
					var err error
					p, err = prepareUSOwnerAuthority(witness)
					if err != nil {
						t.Fatal(err)
					}
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET remember_until=?,manage_until=?`, witness.remember, witness.manage)
					if mode == "remember_expired_lineage" {
						wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_lineage'`)
					}
				case "clear":
					wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=NULL`)
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
				}
				turn, b := usInvitationAuthorityTurn(t, w, 551)
				a, err := authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b)
				allowed := mode == "protected" || mode == "remembered" || mode == "remember_expired" || mode == "clear"
				if allowed && (err != nil || a == nil) {
					t.Fatal(a, err)
				}
				if !allowed && (a != nil || !errors.Is(err, ErrDisclosureDenied)) {
					t.Fatal(a, err)
				}
				if allowed && a.Recheck(t.Context(), turn) != nil {
					t.Fatal("positive recheck")
				}
			})
		}
	}
}

func TestUSInvitationAuthorityRoleDriftAndScope(t *testing.T) {
	for _, initial := range []string{"owner", "admin"} {
		t.Run(initial, func(t *testing.T) {
			w, p := usInvitationAuthorityFixture(t, initial)
			turn, a := usInvitationAuthorityMint(t, w, p, 552)
			next := "admin"
			if initial == "admin" {
				next = "owner"
			}
			usInvitationAuthorityDrift(t, turn, `UPDATE direct_memberships SET role='`+next+`'`, `UPDATE authorization_session_families SET role='`+next+`'`)
			if err := a.Recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("cached tier refreshed", err)
			}
			if a.ManagesTier("member") || a.Issuer() != "" {
				t.Fatal("refused proof remains live")
			}
		})
	}
	var zero *usInvitationAuthorization
	if zero.Issuer() != "" || zero.ManagesTier("member") || (&usInvitationAuthorization{}).ManagesTier("member") {
		t.Fatal("zero proof")
	}
	w, p := usInvitationAuthorityFixture(t, "owner")
	turn, a := usInvitationAuthorityMint(t, w, p, 550)
	copied := *a
	if copied.ManagesTier("member") || copied.Issuer() != "" {
		t.Fatal("copied stamp")
	}
	if err := a.Recheck(t.Context(), &commandTurn{transaction: turn.transaction}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("foreign turn", err)
	}
	_, _ = turn.transaction.rollback(nil)
	fresh, _ := usInvitationAuthorityTurn(t, w, 550)
	if err := a.Recheck(t.Context(), fresh); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("prior frame", err)
	}
}

func TestUSInvitationAuthorityPurposeBudgetAndLifetime(t *testing.T) {
	for _, mode := range []string{"purpose", "descriptor", "normal", "policy", "budget", "nil-permit", "foreign-permit", "lifetime", "finished", "rolledback", "expired", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			w, p := usInvitationAuthorityFixture(t, "owner")
			kind := CommandID(550)
			if mode == "purpose" {
				kind = USMemberLibraries
			}
			turn, b := usInvitationAuthorityTurn(t, w, kind)
			life, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "descriptor":
				turn.transaction.descriptor.Name = "forged"
			case "normal":
				turn.transaction.settings.Synchronous = 1
			case "policy":
				turn.transaction.policyVerified = false
			case "budget":
				b.memory = 64 << 10
			case "nil-permit":
				b.fixture = nil
			case "foreign-permit":
				b.fixture = &usOwnerAuthorityFixturePermit{frame: &transaction{}}
			case "lifetime":
				cancel()
			case "finished":
				if _, err := turn.transaction.commit(); err != nil {
					t.Fatal(err)
				}
			case "rolledback":
				_, _ = turn.transaction.rollback(nil)
			case "expired", "deadline":
				witness := p.witness
				end := w.now()
				if mode == "deadline" {
					end = end.Add(time.Second)
				}
				witness.tokenExpiry = end.Format(time.RFC3339)
				var err error
				p, err = prepareUSOwnerAuthority(witness)
				if err != nil {
					t.Fatal(err)
				}
				wc := turn.transaction
				_, err = wc.conn.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET expires_at=?`, witness.tokenExpiry)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "deadline" {
					wc.ctx = authorityDeadlineContext{Context: wc.ctx, deadline: end.Add(time.Second)}
				}
			}
			a, err := authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, life, b)
			if err == nil || a != nil {
				t.Fatal("invalid proof minted", a, err)
			}
		})
	}
}

// Test-only frame context keeps the real cancellation source and changes only
// the deadline witness, so the native current checks are otherwise valid.
type authorityDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c authorityDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestUSInvitationAuthorityPreparedTimesAndHash(t *testing.T) {
	for _, mode := range []string{"expiry", "installation"} {
		t.Run(mode, func(t *testing.T) {
			w, p := usInvitationAuthorityFixture(t, "admin")
			if mode == "expiry" {
				p.witness.tokenExpiry = w.now().Add(-time.Second).Format(time.RFC3339)
				wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET expires_at=?`, p.witness.tokenExpiry)
			} else {
				p.witness.installationID = "another-installation-synthetic-000001"
				wc008NativeExec(t, w, `UPDATE identity_devices SET installation_id=?`, p.witness.installationID)
			}
			turn, b := usInvitationAuthorityTurn(t, w, 550)
			if a, err := authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b); err == nil || a != nil {
				t.Fatal("stale parsed preparation minted", a, err)
			}
		})
	}
}
func TestUSInvitationAuthorityRedaction(t *testing.T) {
	w, p := usInvitationAuthorityFixture(t, "owner")
	_, a := usInvitationAuthorityMint(t, w, p, 550)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		text := fmt.Sprintf(format, a)
		for _, secret := range []string{p.witness.issuer.tokenDigest, p.witness.installationID, p.witness.issuer.account, p.witness.lineage} {
			if strings.Contains(text, secret) {
				t.Fatal("unredacted authority")
			}
		}
	}
}

func TestUSInvitationAuthorityProtectedFinalRelease(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		for _, scope := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
			t.Run(role+"/"+scope, func(t *testing.T) {
				w, p := usInvitationAuthorityFixture(t, role)
				p = usInvitationAuthorityProtect(t, w, p, true)
				turn, a := usInvitationAuthorityMint(t, w, p, 552)
				usInvitationAuthorityDrift(t, turn, `UPDATE search_revision SET revision=2`)
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=2 WHERE scope_kind=?`, scope); err != nil {
					t.Fatal(err)
				}
				if err := a.Recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("stale PIN authority released", err)
				}
				if _, err := turn.transaction.commit(); err == nil {
					t.Fatal("ignored final PIN denial committed")
				}
				var rev int
				if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM search_revision`).Scan(&rev); err != nil || rev != 1 {
					t.Fatal("prefix survived", rev, err)
				}
			})
		}
	}
}
func TestUSInvitationAuthorityCanceledReleaseAndCompletedProof(t *testing.T) {
	for _, mode := range []string{"mint", "recheck", "finished", "rolledback"} {
		t.Run(mode, func(t *testing.T) {
			w, p := usInvitationAuthorityFixture(t, "admin")
			turn, b := usInvitationAuthorityTurn(t, w, 550)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "mint" {
				cancel()
				a, err := authorizeUSInvitationManager(ctx, turn, p, p.witness.issuer, t.Context(), b)
				if a != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled mint released", a, err)
				}
				return
			}
			a, err := authorizeUSInvitationManager(t.Context(), turn, p, p.witness.issuer, t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "finished" {
				if _, err = turn.transaction.commit(); err != nil {
					t.Fatal(err)
				}
			} else if mode == "rolledback" {
				_, _ = turn.transaction.rollback(nil)
			} else {
				cancel()
			}
			err = a.Recheck(ctx, turn)
			if err == nil || a.Issuer() != "" || a.ManagesTier("member") {
				t.Fatal("invalid lifetime retained authority", err)
			}
		})
	}
}

func TestUSInvitationAuthorityFenceSchemaGuards(t *testing.T) {
	w, p := usInvitationAuthorityFixture(t, "owner")
	_ = usInvitationAuthorityProtect(t, w, p, true)
	_, err := w.db.ExecContext(t.Context(), `UPDATE identity_devices SET authority='hosted'`)
	var native interface{ Code() int }
	if !errors.As(err, &native) || native.Code() != 275 {
		t.Fatal("nonlocal device escaped canonical CHECK", err)
	}
	for _, kind := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
		_, err := w.db.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1 WHERE scope_kind=?`, kind)
		var native interface{ Code() int }
		if !errors.As(err, &native) || native.Code() != 275 {
			t.Fatal("non-device denied bit escaped canonical CHECK", kind, err)
		}
	}
}

func TestUSInvitationAuthorityExpectedIssuerBinding(t *testing.T) {
	for _, field := range []string{"account", "profile", "family", "server", "member", "lifetime", "installation", "token", "epoch", "family-generation", "authority-generation"} {
		t.Run(field, func(t *testing.T) {
			w, p := usInvitationAuthorityFixture(t, "owner")
			expected := p.witness.issuer
			switch field {
			case "account":
				expected.account = "foreign"
			case "profile":
				expected.profile = "foreign"
			case "family":
				expected.family = "foreign"
			case "server":
				expected.server = "foreign"
			case "member":
				expected.memberIncarnation = strings.Repeat("f", 32)
			case "lifetime":
				expected.profileLifetime = strings.Repeat("f", 32)
			case "installation":
				expected.installation = strings.Repeat("f", 32)
			case "token":
				expected.tokenDigest = "foreign"
			case "epoch":
				expected.epoch++
			case "family-generation":
				expected.familyGeneration++
			case "authority-generation":
				expected.authorityGeneration++
			}
			turn, b := usInvitationAuthorityTurn(t, w, 550)
			a, err := authorizeUSInvitationManager(t.Context(), turn, p, expected, t.Context(), b)
			if a != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("foreign request issuer bound", a, err)
			}
		})
	}
}
