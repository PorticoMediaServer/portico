package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"strings"
	"sync"
	"testing"
)

type receiptCloseConn struct {
	writerDriverConn
	afterReceiptClose func()
}

func (c *receiptCloseConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.Contains(q, "FROM enrichment_command_receipts") {
		return &receiptCloseRows{Rows: rows, hook: c.afterReceiptClose}, nil
	}
	return rows, err
}

type receiptCloseRows struct {
	driver.Rows
	hook func()
	once sync.Once
}

func (r *receiptCloseRows) Close() error {
	err := r.Rows.Close()
	r.once.Do(r.hook)
	return err
}

func TestEffectiveFacadeCancelAndRevokeAfterReceipt(t *testing.T) {
	for _, entry := range []string{"stage_replay", "resolve", "ack"} {
		for _, order := range []string{"cancel_then_revoke", "revoke_then_cancel"} {
			t.Run(entry+"/"+order, func(t *testing.T) {
				w, cap, command, executor, authority := effectiveFacadeFixture(t)
				seedEffectiveReceipt(t, executor, command)
				if err := executor.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(w.effective.path, executor.config)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				executor.openPool(connector, func(conn driver.Conn) driver.Conn {
					return &receiptCloseConn{writerDriverConn: conn.(writerDriverConn), afterReceiptClose: func() {
						if order == "cancel_then_revoke" {
							cancel()
							cap.scope.revoked.Store(true)
						} else {
							cap.scope.revoked.Store(true)
							cancel()
						}
					}}
				})
				primeEffectiveFixturePool(t, executor)
				request := effectiveReference(command)
				var result EffectiveStageResult
				var ack AckResult
				switch entry {
				case "stage_replay":
					result, err = w.StageEffective(ctx, cap, command)
				case "resolve":
					result, err = w.ResolveEffective(ctx, cap, request)
				case "ack":
					ack, err = w.AckEffective(ctx, cap, request)
				}
				if !errors.Is(err, ErrDisclosureDenied) || result.Value != (EffectiveStageBeginResult{}) || result.Outcome.State != 0 || result.Outcome.Replayed || result.Outcome.Reference != (ReceiptReference{}) || ack.AcknowledgedThrough != 0 {
					t.Fatal("cancelled/revoked caller received saved result", result, ack, err)
				}
				if authority.Usage()[governor.Background] != (governor.Amounts{}) || executor.db.Stats().InUse != 0 {
					t.Fatal("revocation stranded custody")
				}
			})
		}
	}
}
