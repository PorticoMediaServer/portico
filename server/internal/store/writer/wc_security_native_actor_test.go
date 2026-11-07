package writer

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Change only ACTUAL native membership state, leaving every cached actor field
// and token valid. This private fixture uses a registered exact-key statement,
// the same owned executor, real Recovery grant/FULL turn and physical terminal.
// It is not a product account-disable command or a public fixture capability.
func wcSecurityDisableNativeActor(t *testing.T, w *Writer, d *WCSecurityDriver) {
	t.Helper()
	r, e := w.effective, w.effective.executor
	def := stmt.Definition{ID: "wc.security.fixture.disable_actor", Owner: "WC native actor control", CallSites: []string{"wc_security_native_actor_test.go"}, Kind: stmt.Write,
		SQL:     `UPDATE direct_memberships INDEXED BY sqlite_autoindex_direct_memberships_1 SET disabled=1 WHERE account_id=? AND incarnation=? AND disabled=0 RETURNING disabled`,
		Params:  []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "birth", Type: stmt.Text}},
		Cohorts: []stmt.Cohort{{Name: "actual_actor", Bindings: []any{d.scope.accountID, d.scope.memberBirth}}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}}}}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, []stmt.Definition{def}, "wc_security_native_actor_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New([]stmt.Definition{def}, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.wcSecurityPlan
	expected := governor.Request{Key: governor.JobKey{Domain: uint16(SecurityAccept), ID: "wc-native-actor-control"}, Owner: r.owner, Class: governor.Recovery, Writer: true, Need: p.need, IO: p.io, MaxHold: p.recipe.limit.Hold, Deadline: e.now().Add(p.recipe.limit.Hold), Budget: governor.UnitBudget{RawRows: uint32(p.recipe.limit.Rows), VMOps: p.recipe.limit.VMOps, Pages: uint32(p.recipe.limit.Pages)}, Footprint: []governor.Scope{{Domain: uint16(SecurityAccept)}}}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(SecurityAccept, p.recipe.limit, r.governor, r.authority, r.wal, decision.Grant, expected)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.begin(t.Context(), SecurityAccept, p.recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	turn, err := newCommandTurn(tx, registry, []statementStep{{def.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}}})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := wc008DraftInteger(t.Context(), turn, def.ID, d.scope.accountID, d.scope.memberBirth)
	if err != nil || disabled != 1 {
		t.Fatal(disabled, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	physical, err := tx.commit()
	if err != nil || physical.state != committed {
		t.Fatal("native actor control did not physically commit", physical, err)
	}
}

func TestWCSecurityCompositionNativeActorDisableFencesOriginalResolveAck(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "fresh"
		if saved {
			name = "saved"
		}
		t.Run(name, func(t *testing.T) {
			w, d, family := wcSecurityCompositionFixture(t, "admin", "member")
			command, err := d.PrepareRevoke(t.Context(), family)
			if err != nil {
				t.Fatal(err)
			}
			q, err := SecurityReference(command)
			if err != nil {
				t.Fatal(err)
			}
			if saved {
				if out, e := w.AcceptSecurity(t.Context(), d.Disclosure(), command); e != nil || out.Outcome.State != Applied {
					t.Fatal(out, e)
				}
			}
			wcSecurityDisableNativeActor(t, w, d)
			if out, e := w.AcceptSecurity(t.Context(), d.Disclosure(), command); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, SecurityResult{}) {
				t.Fatal("disabled native actor executed original", out, e)
			}
			if out, e := w.ResolveSecurity(t.Context(), d.Disclosure(), q); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, SecurityResult{}) {
				t.Fatal("disabled native actor disclosed history", out, e)
			}
			if out, e := w.AckSecurity(t.Context(), d.Disclosure(), q); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, AckResult{}) {
				t.Fatal("disabled native actor advanced ACK", out, e)
			}
		})
	}
}
