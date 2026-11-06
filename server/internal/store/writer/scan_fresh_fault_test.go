package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type inventoryFreshDriverFault struct {
	ids    map[string]stmt.ID
	seen   []stmt.ID
	at     stmt.ID
	err    error
	cancel func()
	before func(writerDriverConn)
	hit    bool
}

type inventoryFreshFaultConn struct {
	writerDriverConn
	fault *inventoryFreshDriverFault
}

func (c *inventoryFreshFaultConn) hit(q string) bool {
	id, ok := c.fault.ids[q]
	if !ok {
		return false
	}
	c.fault.seen = append(c.fault.seen, id)
	if id != c.fault.at {
		return false
	}
	c.fault.hit = true
	if c.fault.before != nil {
		c.fault.before(c.writerDriverConn)
	}
	return true
}

func (c *inventoryFreshFaultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	hit := c.hit(q)
	if hit && c.fault.err != nil {
		return nil, c.fault.err
	}
	result, err := c.writerDriverConn.ExecContext(ctx, q, args)
	if hit && err == nil && c.fault.cancel != nil {
		c.fault.cancel()
	}
	return result, err
}

func (c *inventoryFreshFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	hit := c.hit(q)
	if hit && c.fault.err != nil {
		return nil, c.fault.err
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

func inventoryFreshInstallDriver(t *testing.T, e *executor, path string, fault *inventoryFreshDriverFault) {
	t.Helper()
	fault.ids = make(map[string]stmt.ID)
	for _, d := range scanInventoryFreshDefinitions() {
		fault.ids[strings.TrimSpace(d.SQL)] = d.ID
	}
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, e.config)
	if err != nil {
		t.Fatal(err)
	}
	e.openPool(connector, func(c driver.Conn) driver.Conn {
		return &inventoryFreshFaultConn{writerDriverConn: c.(writerDriverConn), fault: fault}
	})
	primeEffectiveFixturePool(t, e)
}

func TestInventoryFreshEveryNativePrefixRollsBack(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	fault := &inventoryFreshDriverFault{}
	inventoryFreshInstallDriver(t, e, path, fault)
	turn, _ := inventoryFreshTestTurn(t, e, path, registry)
	if _, err := applyInventoryAccept(t.Context(), turn, c); err != nil {
		t.Fatal(err)
	}
	observed := append([]stmt.ID(nil), fault.seen...)
	if len(observed) < 35 {
		t.Fatal("did not observe complete native effects", len(observed))
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range observed {
		t.Run(string(id), func(t *testing.T) {
			fault.at, fault.err, fault.hit = id, errors.New("injected native boundary"), false
			turn, a := inventoryFreshTestTurn(t, e, path, registry)
			out, err := applyInventoryAccept(t.Context(), turn, c)
			if !fault.hit || !errors.Is(err, fault.err) || !reflect.DeepEqual(out, InventoryResult{}) {
				t.Fatal("native boundary leaked partial result", id, out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(fault.err); !errors.Is(err, fault.err) {
				t.Fatal(err)
			}
			fault.at, fault.err = "", nil
			inventoryFreshAssertAbsent(t, e)
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("failed boundary stranded grant/connection")
			}
		})
	}
}

func TestInventoryFreshCASDriftAndLastEffectCancellation(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	fault := &inventoryFreshDriverFault{}
	inventoryFreshInstallDriver(t, e, path, fault)
	for _, tt := range []struct {
		name string
		id   stmt.ID
		sql  string
	}{
		{"directory", scanFreshDirectoryCAS, `UPDATE inventory_directories SET revision='drift' WHERE job_id='fresh-run'`},
		{"run", scanFreshRunCAS, `UPDATE inventory_runs SET job_revision=43 WHERE job_id='fresh-run'`},
		{"source", scanFreshProgressCAS, `UPDATE library_sources SET generation=24 WHERE id='fresh-source'`},
		{"age_pending", scanFreshLibraryCAS, `UPDATE libraries SET age_pending_operations=1 WHERE id='fresh-library'`},
		{"age_publication", scanFreshLibraryCAS, `UPDATE libraries SET age_published_revision=82 WHERE id='fresh-library'`},
		{"membership", scanFreshMembershipCAS, `UPDATE catalog_membership_clock SET sequence=91`},
		{"age_count", scanFreshAgeCAS, `UPDATE catalog_age_item_counts SET total=99 WHERE block_unrated=0`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fault.at, fault.hit = tt.id, false
			fault.before = func(c writerDriverConn) {
				if _, err := c.ExecContext(t.Context(), tt.sql, nil); err != nil {
					t.Fatal(err)
				}
			}
			turn, _ := inventoryFreshTestTurn(t, e, path, registry)
			out, err := applyInventoryAccept(t.Context(), turn, c)
			if !fault.hit || !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, InventoryResult{}) {
				t.Fatal("drift did not refuse", out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			fault.at, fault.before = "", nil
			inventoryFreshAssertAbsent(t, e)
		})
	}
	fault.at = scanResultInsert
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault.cancel = cancel
	turn, _ := inventoryFreshTestTurn(t, e, path, registry)
	if out, err := applyInventoryAccept(ctx, turn, c); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("last-effect cancel disclosed data", out, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	fault.at, fault.cancel = "", nil
	inventoryFreshAssertAbsent(t, e)
}

func TestInventoryFreshIndependentNativeEpochReadback(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	fault := &inventoryFreshDriverFault{}
	inventoryFreshInstallDriver(t, e, path, fault)
	fault.at = scanFreshAssetRevision
	fault.before = func(c writerDriverConn) {
		// Deliberately distinct native counters inside a rollback-bound fault
		// fixture. No DDL/default/RETURNING fabrication or production path.
		for _, q := range []string{`UPDATE catalog_assets SET file_size_revision=113`,
			`UPDATE catalog_entities SET scalar_source_revision=127,aggregate_revision=131,admission_revision=137`} {
			if _, err := c.ExecContext(t.Context(), q, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	turn, _ := inventoryFreshTestTurn(t, e, path, registry)
	out, err := applyInventoryAccept(t.Context(), turn, c)
	if err != nil || !fault.hit {
		t.Fatal(out, err)
	}
	var exact bool
	err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
(SELECT contributor_revision=127 AND membership_sequence=90 FROM catalog_scalar_contributions WHERE parent_id=?)
AND (SELECT file_size_revision=113 AND file_size=42 FROM catalog_asset_contributions WHERE parent_id=?)
AND (SELECT scalar_source_revision=127 AND aggregate_revision=131 AND admission_revision=137 AND revision=1 FROM catalog_entities WHERE id=?)
AND (SELECT accepted_asset_revision=a.info_epoch AND a.file_size_revision=113 FROM scan_command_results r JOIN catalog_assets a ON a.id=r.asset_id WHERE run_id='fresh-run')`,
		out.Members[0].SubjectID, out.Members[0].SubjectID, out.Members[0].SubjectID).Scan(&exact)
	if err != nil || !exact || out.AcceptedRunRevision != 42 || out.Members[0].AcceptedAssetRevision != 1 {
		t.Fatal("independent native epochs collapsed", exact, out, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	fault.at, fault.before = "", nil
	inventoryFreshAssertAbsent(t, e)
}
