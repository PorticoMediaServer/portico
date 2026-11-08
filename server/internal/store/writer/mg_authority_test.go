package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgAuthorityFixture(t *testing.T, purpose CommandID, kind mgActorKind) (*commandTurn, *mgActorProof, mgAuthorityBudget) {
	t.Helper()
	e, _, p := usOwnerAuthorityFixture(t)
	if _, err := e.db.ExecContext(t.Context(), mgSourceFixtureSQL); err != nil {
		t.Fatal(err)
	}
	if kind == mgActorViewer {
		if _, err := e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET role='member'; UPDATE live_source_identities SET settings_viewer_access='server-members'`); err != nil {
			t.Fatal(err)
		}
	}
	begin := purpose
	if purpose == 814 {
		begin = MGConfigureTuner
	} // no 814 runtime recipe opens here.
	defs := mgAuthorityDefinitions()
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 8 << 20, 25 * time.Millisecond}
	tx, err := e.begin(t.Context(), begin, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	steps := []statementStep{}
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	if purpose == 814 {
		turn.transaction.descriptor.ID = purpose
	}
	return turn, &mgActorProof{native: *p, kind: kind}, mgAuthorityBudget{memory: 4 << 20, fixture: &mgAuthorityFixturePermit{frame: turn.transaction}}
}
func openMGAuthorityFixture(t *testing.T, turn *commandTurn, p *mgActorProof, b mgAuthorityBudget) *mgSourceFrame {
	t.Helper()
	f, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b)
	if err != nil || f == nil {
		t.Fatal(f, err)
	}
	return f
}
func TestMGAuthorityCurrentActorBeforeDisclosure(t *testing.T) {
	for name, q := range map[string]string{
		"revoked":              `UPDATE authorization_session_families SET revoked=1`,
		"retired-token":        `UPDATE authorization_family_tokens SET retired=1`,
		"retired-device":       `UPDATE identity_devices SET retired_ms=1`,
		"retired-installation": `UPDATE identity_installations SET retired=1`,
		"disabled-member":      `UPDATE direct_memberships SET disabled=1`,
		"role":                 `UPDATE direct_memberships SET role='member'`,
		"profile":              `UPDATE direct_profiles SET deleted=1`,
		"member-lifetime":      `UPDATE direct_memberships SET incarnation='cccccccccccccccccccccccccccccccc'`,
		"profile-lifetime":     `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"fence":                `UPDATE identity_security_fences SET generation=generation+1`,
		"credential-birth":     `UPDATE authorization_session_families SET security_kind=1`,
		"must-change":          `UPDATE accounts SET must_change=1`,
		"server":               `UPDATE configuration SET value='other' WHERE key='id'`,
	} {
		t.Run(name, func(t *testing.T) {
			turn, p, b := mgAuthorityFixture(t, MGSetSourceViewerAccess, mgActorOwner)
			f := openMGAuthorityFixture(t, turn, p, b)
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			if err := f.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestMGAuthorityAcceptingPolicyAndLifetime(t *testing.T) {
	for _, purpose := range []CommandID{MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess} {
		t.Run(fmt.Sprint(purpose), func(t *testing.T) {
			turn, p, b := mgAuthorityFixture(t, purpose, mgActorOwner)
			f := openMGAuthorityFixture(t, turn, p, b)
			if err := f.requireFreshHead(t.Context(), mgSourceHeadWitness{"source", "old", "owner-only", 7}); err != nil {
				t.Fatal(err)
			}
			copy := *f
			if err := copy.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("copied frame", err)
			}
			if err := f.recheck(t.Context(), &commandTurn{transaction: turn.transaction}); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("foreign frame", err)
			}
			// A cached FULL stamp cannot move to a different FULL purpose either.
			old := turn.transaction.descriptor.ID
			turn.transaction.descriptor.ID = MGSetSourceEnabled
			if old == MGSetSourceEnabled {
				turn.transaction.descriptor.ID = MGRetireSource
			}
			if err := f.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("retargeted stamp", err)
			}
			turn.transaction.descriptor.ID = old
			if purpose == MGRetireSource || purpose == MGSetSourceViewerAccess {
				turn.transaction.settings.Synchronous = int(sqliteconn.Normal)
				if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrInvalid) {
					t.Fatal("NORMAL admitted security", got, err)
				}
				turn.transaction.settings.Synchronous = int(sqliteconn.Full)
			}
			low := b
			low.memory = 1
			if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", low); got != nil || !errors.Is(err, ErrRefused) {
				t.Fatal(got, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if got, err := openMGSourceFrame(ctx, turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(got, err)
			}
			if got, err := openMGSourceFrame(t.Context(), turn, p, ctx, "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(got, err)
			}
		})
	}
	turn, p, b := mgAuthorityFixture(t, MGConfigureTuner, mgActorViewer)
	if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("viewer configured source", got, err)
	}
}
func TestMGAuthorityAcquisitionRechecksSource(t *testing.T) {
	for name, q := range map[string]string{
		"viewer-access": `UPDATE live_source_identities SET settings_viewer_access='owner-only'`,
		"removal":       `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now'`,
		"disabled":      `UPDATE live_sources SET state='disabled'`,
		"head":          `UPDATE live_sources SET active_generation='new'`,
		"revision":      `UPDATE live_sources SET revision=revision+1`,
		"actor":         `UPDATE authorization_session_families SET revoked=1`,
	} {
		t.Run(name, func(t *testing.T) {
			turn, p, b := mgAuthorityFixture(t, 814, mgActorViewer)
			f := openMGAuthorityFixture(t, turn, p, b)
			witness := mgSourceHeadWitness{"source", "old", "server-members", 7}
			a, err := authorizeMGSourceAcquisition(t.Context(), f, witness)
			if err != nil || a == nil {
				t.Fatal(a, err)
			}
			for _, bad := range []mgSourceHeadWitness{{"source", "new", "server-members", 7}, {"source", "old", "owner-only", 7}, {"source", "old", "server-members", 8}, {"foreign", "old", "server-members", 7}} {
				if got, err := authorizeMGSourceAcquisition(t.Context(), f, bad); got != nil || err == nil {
					t.Fatal("forged comparison granted", got, err)
				}
			}
			copy := *a
			if err = copy.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			if err = a.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
		})
	}
	// Cleanup IDs cannot be retasked as new acquisition authority.
	turn, p, b := mgAuthorityFixture(t, 814, mgActorOwner)
	turn.transaction.descriptor.ID = 817
	if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
}
func TestMGAuthorityNativePlans(t *testing.T) {
	turn, _, _ := mgAuthorityFixture(t, MGConfigureTuner, mgActorOwner)
	if _, err := stmttest.New(t, mgAuthorityDefinitions()); err != nil {
		t.Fatal(err)
	}
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range mgAuthorityDefinitions() {
			if issues := plan.LintSQLOn(t.Context(), turn.transaction.conn, d.SQL); len(issues) > 0 {
				t.Fatal(d.ID, issues)
			}
			rows, err := turn.transaction.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			count := 0
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(d.ID, detail)
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || count == 0 {
				t.Fatal(d.ID, count, err)
			}
		}
	}
}

func TestMGAuthorityRefusesForgedPreparedFacts(t *testing.T) {
	for _, field := range []string{"deadline", "hash", "owner", "lifetime"} {
		t.Run(field, func(t *testing.T) {
			turn, p, b := mgAuthorityFixture(t, MGConfigureTuner, mgActorOwner)
			switch field {
			case "deadline":
				expired := turn.transaction.writer.now().Add(-time.Hour).UTC().Format(time.RFC3339)
				p.native.witness.tokenExpiry = expired
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET expires_at=?`, expired); err != nil {
					t.Fatal(err)
				}
			case "hash":
				p.native.installationHash = strings.Repeat("f", 64)
			case "owner":
				p.native.witness.issuer.account = "foreign"
			case "lifetime":
				p.native.witness.issuer.profileLifetime = strings.Repeat("e", 32)
			}
			if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMGAuthorityProtectedViewerAndFalseOwner(t *testing.T) {
	turn, p, b := mgAuthorityFixture(t, 814, mgActorViewer)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE direct_profiles SET pin_hash=X'01'`); err != nil {
		t.Fatal(err)
	}
	if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("missing PIN confirmation", got, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,updated_ms) VALUES(2,'profile_pin','issuer',?,'issuer-profile',?,0); INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family','profile_pin',2,1)`, p.native.witness.issuer.memberIncarnation, p.native.witness.issuer.profileLifetime); err != nil {
		t.Fatal(err)
	}
	f := openMGAuthorityFixture(t, turn, p, b)
	if _, err := authorizeMGSourceAcquisition(t.Context(), f, mgSourceHeadWitness{"source", "old", "server-members", 7}); err != nil {
		t.Fatal(err)
	}
	p.kind = mgActorOwner
	if got, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("claimed owner granted", got, err)
	}
	p.kind = mgActorViewer
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=generation+1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if err := f.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
}
