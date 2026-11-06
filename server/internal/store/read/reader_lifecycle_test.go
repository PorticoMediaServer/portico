package read

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Preserve the actual helper connection's native/context interfaces. The only
// injected behavior is a one-shot Validator refusal after a completed window,
// so database/sql must physically close and replace its reader.
type browseLifecycleNative interface {
	driver.Conn
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.SessionResetter
	driver.Validator
	ReadBudgetSupported() bool
	SQLiteDQS() (int, int, error)
	SQLiteLimit(int, int) (int, error)
}

type browseLifecycleConnector struct {
	driver.Connector
	authority                 *governor.Authority
	opens, closes, unreserved atomic.Int64
	setAttempts               atomic.Int64
	retire                    atomic.Bool
}

type browseLifecycleConn struct {
	browseLifecycleNative
	owner *browseLifecycleConnector
}

func (c *browseLifecycleConnector) checkCustody() {
	if c.authority.Usage()[governor.Foreground][governor.MemoryBytes] < 12<<20 {
		c.unreserved.Add(1)
	}
}

func (c *browseLifecycleConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.checkCustody()
	raw, err := c.Connector.Connect(ctx)
	c.checkCustody()
	if err != nil {
		return nil, err
	}
	native, ok := raw.(browseLifecycleNative)
	if !ok {
		return nil, errors.Join(errors.New("reader lacks required native interfaces"), raw.Close())
	}
	c.opens.Add(1)
	return &browseLifecycleConn{native, c}, nil
}

func (c *browseLifecycleConn) IsValid() bool {
	return !c.owner.retire.Swap(false) && c.browseLifecycleNative.IsValid()
}

func (c *browseLifecycleConn) SQLiteLimit(id, value int) (int, error) {
	if value != -1 {
		c.owner.setAttempts.Add(1)
		return 0, errors.New("reader limits must remain fixed after Connect")
	}
	return c.browseLifecycleNative.SQLiteLimit(id, -1)
}

func (c *browseLifecycleConn) Close() error {
	c.owner.closes.Add(1)
	return c.browseLifecycleNative.Close()
}

// This establishes fixed-reader lifecycle behavior on the actual canonical
// fixture, not Capture receipts, native plan approval or physical RSS budgets.
func TestBrowseReader128Lifecycle(t *testing.T) {
	var observed *browseLifecycleConnector
	f := newBrowseFixtureWithConnector(t, func(connector driver.Connector, authority *governor.Authority) driver.Connector {
		observed = &browseLifecycleConnector{Connector: connector, authority: authority}
		return observed
	})
	wantLimits := sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 128}
	check := func(phase string, opens, closes int64) {
		t.Helper()
		page, result, err := f.window(t, t.Context(), f.input)
		if err != nil || result.Refusal != nil || len(page.Items) != MaxPage {
			t.Fatalf("%s: browse failed: %v refusal=%v items=%d", phase, err, result.Refusal, len(page.Items))
		}
		if result.Settings.ReadLimits != wantLimits || result.Settings.QueryOnly != 1 || result.NativeReservedBytes != 8<<20 || result.GoReservedBytes != 4<<20 {
			t.Fatalf("%s: fixed reader/admission evidence: %+v", phase, result)
		}
		if observed.opens.Load() != opens || observed.closes.Load() != closes || observed.unreserved.Load() != 0 || observed.setAttempts.Load() != 0 || observed.authority.Usage() != [4]governor.Amounts{} {
			t.Fatalf("%s: opens=%d closes=%d unreserved=%d setAttempts=%d usage=%v", phase, observed.opens.Load(), observed.closes.Load(), observed.unreserved.Load(), observed.setAttempts.Load(), observed.authority.Usage())
		}
		t.Logf("%s: Reader C128/L65536; native=8388608 Go=4194304; opens=%d closes=%d", phase, opens, closes)
	}
	check("fresh open", 1, 0)
	// Explicit fixture maintenance on the separate writer, outside snapshots.
	f.exec(t, "ANALYZE")
	check("same physical reader after ANALYZE/reparse", 1, 0)
	observed.retire.Store(true)
	check("physical retirement after completed window", 1, 1)
	check("replacement reader on analyzed schema", 2, 1)
	if err := f.snapshots.Close(); err != nil || observed.closes.Load() != 2 || observed.authority.Usage() != [4]governor.Amounts{} {
		t.Fatal("replacement reader cleanup", err, observed.closes.Load(), observed.authority.Usage())
	}
}
