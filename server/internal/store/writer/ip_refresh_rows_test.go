package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Faults wrap only a disposable fixture's existing W connection. They do not
// add a second writer, production callback or database entry point.
type ipRefreshFaultConn struct {
	writerDriverConn
	mode  string
	fault error
}

func (c *ipRefreshFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.HasPrefix(q, "SELECT entity_id,generation,revision FROM metadata_acquisition_jobs") {
		return &ipRefreshFaultRows{Rows: rows, mode: c.mode, fault: c.fault}, nil
	}
	return rows, err
}

type ipRefreshFaultRows struct {
	driver.Rows
	mode  string
	fault error
}

func (r *ipRefreshFaultRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if errors.Is(err, io.EOF) && r.mode == "err" {
		return r.fault
	}
	return err
}
func (r *ipRefreshFaultRows) Close() error {
	err := r.Rows.Close()
	if r.mode == "close" {
		return errors.Join(err, r.fault)
	}
	return err
}

func TestIPRefreshNativeRowsFailures(t *testing.T) {
	for _, mode := range []string{"err", "close"} {
		t.Run(mode, func(t *testing.T) {
			w := ipRefreshFixture(t, 1)
			var sequence int
			var name, path string
			if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected IP raw " + mode)
			w.openPool(connector, func(c driver.Conn) driver.Conn { return &ipRefreshFaultConn{c.(writerDriverConn), mode, fault} })
			turn := ipPolicyTestTurn(t, w, IPRefreshPage)
			out, err := ipRefreshBody(t.Context(), turn, ipRefreshTestRequest(ipRefreshPageKind), ipPolicyTestDecode())
			if !errors.Is(err, fault) || out != (ipRefreshResult{}) {
				t.Fatal(out, err)
			}
			_, _ = turn.transaction.rollback(err)
			if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_refresh_receipts`) != 0 || ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`) != 0 {
				t.Fatal("row failure leaked effects")
			}
		})
	}
}
