package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Instrument native Rows, never replace them: the INSERT/UPDATE still runs in
// SQLite and its unmodified persisted value is inspected before rollback.
type mgLCDebtFaultConnector struct {
	driver.Connector
	mode *string
	hit  *bool
}

func (c mgLCDebtFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	n, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &mgLCDebtFaultConn{n.(writerDriverConn), c.mode, c.hit}, nil
}

type mgLCDebtFaultConn struct {
	writerDriverConn
	mode *string
	hit  *bool
}

func (c *mgLCDebtFaultConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	r, e := c.writerDriverConn.QueryContext(ctx, q, a)
	if e != nil {
		return r, e
	}
	match := *c.mode == "bytes" && strings.HasPrefix(q, "SELECT variable_bytes FROM lc_generation_command_receipts")
	match = match || *c.mode == "returning" && strings.HasPrefix(q, "UPDATE lc_generations SET receipt_rows=")
	if match {
		return &mgLCDebtFaultRows{r, c.hit}, nil
	}
	return r, nil
}

func (c *mgLCDebtFaultConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	s, e := c.writerDriverConn.PrepareContext(ctx, q)
	if e != nil {
		return nil, e
	}
	return &mgLCDebtFaultStmt{s, q, c}, nil
}

type mgLCDebtFaultStmt struct {
	driver.Stmt
	q string
	c *mgLCDebtFaultConn
}

func (s *mgLCDebtFaultStmt) ExecContext(ctx context.Context, a []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, a)
}
func (s *mgLCDebtFaultStmt) QueryContext(ctx context.Context, a []driver.NamedValue) (driver.Rows, error) {
	r, e := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, a)
	if e != nil {
		return r, e
	}
	match := *s.c.mode == "bytes" && strings.HasPrefix(s.q, "SELECT variable_bytes FROM lc_generation_command_receipts")
	match = match || *s.c.mode == "returning" && strings.HasPrefix(s.q, "UPDATE lc_generations SET receipt_rows=")
	if match {
		return &mgLCDebtFaultRows{r, s.c.hit}, nil
	}
	return r, nil
}

type mgLCDebtFaultRows struct {
	driver.Rows
	hit *bool
}

func (r *mgLCDebtFaultRows) Next(v []driver.Value) error {
	if e := r.Rows.Next(v); e != nil {
		return e
	}
	v[0] = v[0].(int64) + 1
	*r.hit = true
	return nil
}
func mgLCDebtInstrument(t *testing.T, w *executor, mode *string, hit *bool) *executor {
	t.Helper()
	var seq int
	var name, path string
	if e := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	if e := w.close(); e != nil {
		t.Fatal(e)
	}
	c, e := sqliteconn.Connector(path, w.config)
	if e != nil {
		t.Fatal(e)
	}
	out := &executor{config: w.config, now: time.Now}
	out.openPool(mgLCDebtFaultConnector{c, mode, hit}, nil)
	t.Cleanup(func() {
		if e := out.close(); e != nil {
			t.Error(e)
		}
	})
	var warm int
	if e := out.db.QueryRowContext(t.Context(), "SELECT count(*) FROM lc_generations").Scan(&warm); e != nil {
		t.Fatal(e)
	}
	return out
}
func mgLCDebtTotals(t *testing.T, w *executor, b mgLCBuild, want int64) {
	t.Helper()
	var rows, bytes, count, sum int64
	e := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes,(SELECT count(*) FROM lc_generation_command_receipts WHERE generation_id=g.id AND command_incarnation=g.command_incarnation),(SELECT coalesce(sum(variable_bytes),0) FROM lc_generation_command_receipts WHERE generation_id=g.id AND command_incarnation=g.command_incarnation) FROM lc_generations g WHERE id=?`, b.generation).Scan(&rows, &bytes, &count, &sum)
	if e != nil || rows != want || count != want || bytes != sum || bytes <= 0 {
		t.Fatal("native debt disagrees", rows, bytes, count, sum, e)
	}
}
func mgLCDebtRollbackBirth(t *testing.T, w *executor, turn *commandTurn, b mgLCBuild) {
	t.Helper()
	if e := turn.closeRows(); e != nil {
		t.Fatal(e)
	}
	if _, e := turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
	var owners, children int64
	e := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM lc_generations WHERE id=?),(SELECT count(*) FROM lc_generation_command_receipts WHERE generation_id=?)`, b.generation, b.generation).Scan(&owners, &children)
	if e != nil || owners != 0 || children != 0 {
		t.Fatal("failed frame escaped rollback", owners, children, e)
	}
}

func TestMGLCReceiptDebtNativeAccounting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		b.lease = "bail-é-雪"
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, mgLCProgress{})
		p, e := beginMGLC(t.Context(), turn, b, c)
		if e != nil {
			t.Fatal(e)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCBeginKind, p)
		mgLCCommit(t, turn)
		mgLCDebtTotals(t, w, b, 1)
		// Replay is read-only before ACK; repeated ACK must issue no new writes.
		for _, acked := range []bool{false, true} {
			turn = mgLCTurn(t, w)
			var before, after int64
			if e := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); e != nil {
				t.Fatal(e)
			}
			if acked {
				if n, e := acknowledgeMGLC(t.Context(), turn, b, c, s.kind, s.digest); e != nil || n != 1 {
					t.Fatal(n, e)
				}
			} else {
				d, r, e := lookupMGLCReceipt(t.Context(), turn, b, c, s.kind)
				if e != nil || d != receiptReplay || r != s {
					t.Fatal(d, r, e)
				}
			}
			if e := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); e != nil || after != before {
				t.Fatal("repeat had effects", before, after, e)
			}
			mgLCCommit(t, turn)
			if !acked {
				p = mgLCTestAck(t, w, b, s, p)
			}
			mgLCDebtTotals(t, w, b, 1)
		}
		turn = mgLCTurn(t, w)
		c = mgLCNext(b, p)
		next, e := finishMGLCCandidates(t.Context(), turn, b, c, p)
		if e != nil {
			t.Fatal(e)
		}
		s = mgLCTestSave(t, turn, b, c, mgLCCandidatesKind, next)
		mgLCCommit(t, turn)
		mgLCDebtTotals(t, w, b, 2)
		mgLCTestAck(t, w, b, s, next)
		mgLCDebtTotals(t, w, b, 2)
	})
}
func TestMGLCReceiptDebtGeneratedValueMismatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		mode := "bytes"
		hit := false
		w = mgLCDebtInstrument(t, w, &mode, &hit)
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, mgLCProgress{})
		p, e := beginMGLC(t.Context(), turn, b, c)
		if e != nil {
			t.Fatal(e)
		}
		out, e := saveMGLCReceipt(t.Context(), turn, b, c, mgLCBeginKind, p)
		if !errors.Is(e, ErrConflict) || out != (mgLCSaved{}) || !hit {
			t.Fatal("generated byte mismatch released", out, e, hit)
		}
		var children int
		if e := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM lc_generation_command_receipts WHERE generation_id=?", b.generation).Scan(&children); e != nil || children != 1 {
			t.Fatal("native insert not reached", children, e)
		}
		mgLCDebtRollbackBirth(t, w, turn, b)
	})
}
func TestMGLCReceiptDebtReturningValueMismatch(t *testing.T) {
	for _, present := range []bool{false, true} {
		name := "null"
		if present {
			name = "present"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, b := mgLCFixture(t)
				mode := ""
				hit := false
				w = mgLCDebtInstrument(t, w, &mode, &hit)
				var p mgLCProgress
				if present {
					p = mgLCBeginFixture(t, w, b)
				}
				turn := mgLCTurn(t, w)
				c := mgLCNext(b, p)
				var e error
				kind := mgLCBeginKind
				if present {
					kind = mgLCCandidatesKind
					p, e = finishMGLCCandidates(t.Context(), turn, b, c, p)
				} else {
					p, e = beginMGLC(t.Context(), turn, b, c)
				}
				if e != nil {
					t.Fatal(e)
				}
				mode = "returning"
				out, e := saveMGLCReceipt(t.Context(), turn, b, c, kind, p)
				if !errors.Is(e, ErrConflict) || out != (mgLCSaved{}) || !hit {
					t.Fatal("RETURNING mismatch released", out, e, hit)
				}
				var nativeRows int64
				if e := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT receipt_rows FROM lc_generations WHERE id=?", b.generation).Scan(&nativeRows); e != nil || nativeRows != c.identity.Sequence {
					t.Fatal("native update not reached", nativeRows, e)
				}
				if !present {
					mgLCDebtRollbackBirth(t, w, turn, b)
					return
				}
				if e := turn.closeRows(); e != nil {
					t.Fatal(e)
				}
				if _, e := turn.transaction.rollback(nil); e != nil {
					t.Fatal(e)
				}
				mgLCDebtTotals(t, w, b, 1)
				var last int64
				if e := w.db.QueryRowContext(t.Context(), "SELECT last_command_sequence FROM lc_generations WHERE id=?", b.generation).Scan(&last); e != nil || last != 1 {
					t.Fatal("command escaped rollback", last, e)
				}
			})
		})
	}
}
func TestMGLCReceiptDebtStaleNativeWitness(t *testing.T) {
	for _, field := range []string{"receipt_rows", "receipt_variable_bytes", "receipt_reclaim_after_sequence", "receipt_reclaim_due_ms"} {
		t.Run(field, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, b := mgLCFixture(t)
				p := mgLCBeginFixture(t, w, b)
				turn := mgLCTurn(t, w)
				before, e := readMGLCReceiptDebt(t.Context(), turn, b, p)
				if e != nil {
					t.Fatal(e)
				}
				// Mutate a valid native witness after the SELECT, within this same frame.
				value := before.rows + 1
				switch field {
				case "receipt_variable_bytes":
					value = before.bytes + 1
				case "receipt_reclaim_after_sequence":
					value = 1
				case "receipt_reclaim_due_ms":
					value = before.due.Int64 + 1
				}
				if _, e := turn.transaction.conn.ExecContext(t.Context(), "UPDATE lc_generations SET "+field+"=? WHERE id=?", value, b.generation); e != nil {
					t.Fatal(e)
				}
				after := before
				after.bytes++
				out, e := replaceMGLCReceiptDebt(t.Context(), turn, b, p, before, after)
				if !errors.Is(e, ErrConflict) || out != (mgLCReceiptDebt{}) {
					t.Fatal("stale witness accepted", out, e)
				}
				if e := turn.closeRows(); e != nil {
					t.Fatal(e)
				}
				if _, e := turn.transaction.rollback(nil); e != nil {
					t.Fatal(e)
				}
				mgLCDebtTotals(t, w, b, 1)
				check := mgLCTurn(t, w)
				restored, e := readMGLCReceiptDebt(t.Context(), check, b, p)
				if e != nil || restored != before {
					t.Fatal("witness mutation escaped", restored, before, e)
				}
			})
		})
	}
}
func TestMGLCReceiptDebtAckAfterSourceRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, mgLCProgress{})
		p, e := beginMGLC(t.Context(), turn, b, c)
		if e != nil {
			t.Fatal(e)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCBeginKind, p)
		mgLCCommit(t, turn)
		time.Sleep(2 * time.Hour)
		for _, q := range []string{`UPDATE libraries SET data_revision=data_revision+1 WHERE id='lc-lib'`, `UPDATE lc_channels SET revision=revision+1,enabled=0,removed=1,state='retired' WHERE id='lc-channel'`} {
			if _, e := w.db.ExecContext(t.Context(), q); e != nil {
				t.Fatal(e)
			}
		}
		mgLCTestAck(t, w, b, s, p)
		mgLCDebtTotals(t, w, b, 1)
		var due, ack int64
		if e := w.db.QueryRowContext(t.Context(), "SELECT receipt_reclaim_due_ms,acknowledged_command_sequence FROM lc_generations WHERE id=?", b.generation).Scan(&due, &ack); e != nil || due != time.Now().UnixMilli() || ack != 1 {
			t.Fatal("expired-source ACK failed to schedule", due, ack, e)
		}
	})
}
