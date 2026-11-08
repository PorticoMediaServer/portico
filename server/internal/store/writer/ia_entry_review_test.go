package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Native fault injection only, using W's existing private decorator seam. The
// mutation occurs after native rows close, with an uncanceled caller lifetime:
// finishCommand's cancellation redaction cannot substitute for authority.
type iaEntryQueryRevocation struct {
	fired bool
	err   error
}
type iaEntryQueryRevocationConn struct {
	writerDriverConn
	query string
	state *iaEntryQueryRevocation
}

func (c *iaEntryQueryRevocationConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err != nil || !strings.Contains(query, c.query) {
		return rows, err
	}
	return &receiptCloseRows{Rows: rows, hook: func() {
		if c.state.fired {
			return
		}
		c.state.fired = true
		_, c.state.err = c.writerDriverConn.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`, nil)
	}}, nil
}
func installIAEntryQueryRevocation(t *testing.T, w *executor, query string) *iaEntryQueryRevocation {
	t.Helper()
	var sequence int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	state := &iaEntryQueryRevocation{}
	w.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &iaEntryQueryRevocationConn{writerDriverConn: conn.(writerDriverConn), query: query, state: state}
	})
	return state
}
func iaEntrySourceFixture(t *testing.T, w *executor, q *iaEntryRequest) {
	t.Helper()
	q.purpose, q.sourceID = IAConfigureSource, "source"
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root) VALUES('ia-library','Library','movie','/media')`)
	wc008NativeExec(t, w, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity) VALUES('source','ia-library','/media','/media','incarnation','native')`)
}
func TestIAEntryFinalRecheckAfterReceiptClose(t *testing.T) {
	for _, mode := range []string{"fresh", "replay", "retired"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			if mode != "fresh" {
				seedIAEntryHistory(t, w, q, mode == "retired")
			}
			state := installIAEntryQueryRevocation(t, w, "FROM catalog_configuration_receipts")
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
			if !state.fired || state.err != nil {
				t.Fatal("receipt-close mutation did not run", state.err)
			}
			if out != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("receipt released after same-turn revocation", out, err)
			}
		})
	}
}
func TestIAEntryFreshSourceRechecks(t *testing.T) {
	t.Run("before_source_query", func(t *testing.T) {
		w, r, p, q := iaEntryFixture(t)
		iaEntrySourceFixture(t, w, &q)
		turn, b := iaEntryTurn(t, w, r, q)
		entry, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`); err != nil {
			t.Fatal(err)
		}
		// The initial recheck must deny before even attempting the source lookup.
		// The missing step distinguishes removal of that recheck from a later one.
		delete(turn.steps, iaEntryFreshSource)
		if err = entry.checkFreshSource(t.Context(), turn, 1, "incarnation", "/media", "native"); !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("source lookup preceded current authority", err)
		}
	})
	t.Run("after_source_close", func(t *testing.T) {
		w, r, p, q := iaEntryFixture(t)
		iaEntrySourceFixture(t, w, &q)
		state := installIAEntryQueryRevocation(t, w, "FROM library_sources INDEXED BY")
		turn, b := iaEntryTurn(t, w, r, q)
		entry, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
		if err != nil {
			t.Fatal(err)
		}
		err = entry.checkFreshSource(t.Context(), turn, 1, "incarnation", "/media", "native")
		if !state.fired || state.err != nil {
			t.Fatal("source-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("source proof released after revocation", err)
		}
	})
}
func TestIAEntryRetainedOwnerConflictWithoutHeader(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	seedIAEntryHistory(t, w, q, true)
	wc008NativeExec(t, w, `DELETE FROM catalog_configuration_receipts`)
	q.reference.RequestDigest[0]++
	q.action = iaEntryResolve
	turn, b := iaEntryTurn(t, w, r, q)
	out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if out != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("reclaimed header masked original owner digest conflict", out, err)
	}
}
func TestIAEntryNamespaceIdentityConflict(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	// No retained owner exists: its matches guard cannot mask a namespace defect.
	q.libraryKey = "different-library"
	turn, b := iaEntryTurn(t, w, r, q)
	out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if out != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("namespace ID/key collision admitted", out, err)
	}
}
func TestIAEntryBudgetSynchronousAndMemoryOverclaim(t *testing.T) {
	for _, purpose := range []CommandID{IAConfigureLibrary, IAConfigureSource} {
		t.Run(map[CommandID]string{IAConfigureLibrary: "NORMAL", IAConfigureSource: "FULL"}[purpose], func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			q.purpose = purpose
			if purpose == IAConfigureSource {
				q.sourceID = "source"
			}
			turn, b := iaEntryTurn(t, w, r, q)
			original := turn.transaction.settings.Synchronous
			wrong := int(sqliteconn.Full)
			if original == wrong {
				wrong = int(sqliteconn.Normal)
			}
			turn.transaction.settings.Synchronous = wrong
			if out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b); out != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("wrong synchronous witness admitted", out, err)
			}
			turn.transaction.settings.Synchronous = original
			over := b
			over.memory++
			out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), over)
			var refusal *Refusal
			if out != nil || !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
				t.Fatal("authority over-claimed the actual grant", out, err)
			}
		})
	}
}
func TestIAEntryTokenAndHorizonExpiry(t *testing.T) {
	for _, field := range []string{"token", "horizon"} {
		t.Run(field, func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			now := w.now()
			witness := p.witness
			if field == "token" {
				witness.tokenExpiry = now.Format(time.RFC3339)
				wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET expires_at=?`, witness.tokenExpiry)
			} else {
				witness.horizon = now.Format(time.RFC3339)
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET authorization_horizon=?`, witness.horizon)
			}
			// Both parsed proof and exact native timestamp agree; only expiry denies.
			proof, err := prepareUSOwnerAuthority(witness)
			if err != nil {
				t.Fatal(err)
			}
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, proof, t.Context(), b)
			if out != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("expired source admitted", out, err)
			}
		})
	}
}
func TestIAEntryPINAuthority(t *testing.T) {
	for _, mode := range []string{"current", "pin_gate", "missing_pin", "forgotten_lineage", "expired_manage"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			witness := p.witness
			now := w.now()
			witness.remember = now.Add(time.Hour).Format(time.RFC3339)
			witness.manage = now.Add(time.Minute).Format(time.RFC3339)
			if mode == "expired_manage" {
				witness.manage = now.Format(time.RFC3339)
			}
			wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=?,remember_until=?,manage_until=?`, map[bool]int{false: 0, true: 1}[mode == "pin_gate"], witness.remember, witness.manage)
			for n, kind := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
				lineage := ""
				if kind == "profile_lineage" {
					lineage = witness.lineage
				}
				wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms) VALUES(?,?,'issuer',?,'issuer-profile',?,?,0)`, n+2, kind, witness.issuer.memberIncarnation, witness.issuer.profileLifetime, lineage)
				wc008NativeExec(t, w, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family',?,?,1)`, kind, n+2)
			}
			if mode == "missing_pin" {
				wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE scope_kind='profile_pin'`)
			}
			if mode == "forgotten_lineage" {
				wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=generation+1 WHERE scope_kind='profile_lineage'`)
			}
			proof, err := prepareUSOwnerAuthority(witness)
			if err != nil {
				t.Fatal(err)
			}
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, proof, t.Context(), b)
			if mode != "current" {
				if out != nil || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal(out, err)
				}
				return
			}
			if out == nil || err != nil {
				t.Fatal(out, err)
			}
			out.authority.until = now
			if err = out.authority.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("expired stamp reused despite its narrower bound", err)
			}
		})
	}
}
func TestIAEntryResolveUncommittedOwner(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	seedIAEntryHistory(t, w, q, false)
	wc008NativeExec(t, w, `DELETE FROM catalog_configuration_receipts; UPDATE catalog_configuration_operations SET last_command_sequence=0,acknowledged_command_sequence=0`)
	q.action = iaEntryResolve
	turn, b := iaEntryTurn(t, w, r, q)
	out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if out != nil || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("resolve returned a fresh admission as a result", out, err)
	}
}
