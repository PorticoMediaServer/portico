package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Test-binary memo for native plan/FK captures. A capture is a deterministic
// function of the schema rows (including rootpages), statistics tables, actual
// connection settings, engine, statement manifest, fixture metadata and index
// policy. All of those inputs are fingerprinted from the LIVE connection on
// every call, so a clone with any difference captures afresh. Only identical
// inputs reuse the earlier, equally native, result. Production never sees this.
var captureMemo = struct {
	sync.Mutex
	golden map[[32]byte]plan.Golden
	fk     map[[32]byte]plan.ForeignKeyAudit
}{golden: map[[32]byte]plan.Golden{}, fk: map[[32]byte]plan.ForeignKeyAudit{}}

func captureInputs(ctx context.Context, conn *sql.Conn, policy sqliteconn.Config, parts ...any) ([32]byte, error) {
	settings, err := sqliteconn.Assert(ctx, conn, policy)
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%#v\n%#v\n", policy, settings)
	for _, q := range []string{
		"SELECT sqlite_version(),sqlite_source_id()",
		"SELECT type,name,tbl_name,rootpage,quote(sql) FROM sqlite_schema ORDER BY type,name",
	} {
		if err := hashRows(ctx, conn, h, q); err != nil {
			return [32]byte{}, err
		}
	}
	for _, stat := range []string{"sqlite_stat1", "sqlite_stat4"} {
		var n int
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name=?", stat).Scan(&n); err != nil {
			return [32]byte{}, err
		}
		if n > 0 {
			if err := hashRows(ctx, conn, h, "SELECT * FROM "+stat+" ORDER BY 1,2"); err != nil {
				return [32]byte{}, err
			}
		}
	}
	for _, p := range parts {
		fmt.Fprintf(h, "%#v\n", p)
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key, nil
}

func hashRows(ctx context.Context, conn *sql.Conn, h interface{ Write([]byte) (int, error) }, q string) error {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		fmt.Fprintf(h, "%#v\n", vals)
	}
	return rows.Err()
}

func cachedCapture(ctx context.Context, conn *sql.Conn, registry *stmt.Registry, fixture plan.Fixture, policy plan.IndexPolicy) (plan.Golden, error) {
	// FTS native audits may read row data; those captures are never memoized.
	for _, entry := range registry.Manifest() {
		if entry.Plan.NativeRead != nil || entry.Plan.NativeInsert != "" || entry.Plan.NativeRetained != "" {
			return plan.Capture(ctx, conn, registry, fixture, policy)
		}
	}
	key, err := captureInputs(ctx, conn, fixture.ConnectionPolicy, registry.Manifest(), fixture, policy)
	if err != nil {
		return plan.Golden{}, err
	}
	captureMemo.Lock()
	g, ok := captureMemo.golden[key]
	captureMemo.Unlock()
	if ok {
		return g, nil
	}
	g, err = plan.Capture(ctx, conn, registry, fixture, policy)
	if err == nil {
		captureMemo.Lock()
		captureMemo.golden[key] = g
		captureMemo.Unlock()
	}
	return g, err
}

func cachedForeignKeys(ctx context.Context, conn *sql.Conn, policy sqliteconn.Config) (plan.ForeignKeyAudit, error) {
	key, err := captureInputs(ctx, conn, policy, "foreign-keys")
	if err != nil {
		return plan.ForeignKeyAudit{}, err
	}
	captureMemo.Lock()
	fk, ok := captureMemo.fk[key]
	captureMemo.Unlock()
	if ok {
		return fk, nil
	}
	fk, err = plan.ForeignKeys(ctx, conn, policy)
	if err == nil {
		captureMemo.Lock()
		captureMemo.fk[key] = fk
		captureMemo.Unlock()
	}
	return fk, err
}
