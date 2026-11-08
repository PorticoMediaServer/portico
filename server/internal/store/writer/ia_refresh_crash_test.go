package writer

import (
	"context"
	"database/sql/driver"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The child exits immediately AFTER the real native statement succeeds, with
// the actual W transaction still open. No production hook or alternate SQL
// model substitutes for the journal/canonical/receipt body being exercised.
type iaRefreshCrashConn struct {
	writerDriverConn
	needle string
}

func (c *iaRefreshCrashConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	r, err := c.writerDriverConn.ExecContext(ctx, q, a)
	if err == nil && strings.HasPrefix(q, c.needle) {
		os.Exit(91)
	}
	return r, err
}
func TestIARefreshCrashChild(t *testing.T) {
	point := os.Getenv("PORTICO_IA_REFRESH_CRASH_POINT")
	if point == "" {
		t.Skip("crash subprocess only")
	}
	needles := map[string]string{"capture": "INSERT INTO catalog_info_undo_asset_stream_facts(", "mutation": "UPDATE asset_stream_facts SET ", "receipt": "INSERT INTO catalog_info_refresh_receipts("}
	needle, ok := needles[point]
	if !ok {
		t.Fatal("unknown crash point")
	}
	w, r, p, q := iaRefreshFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	begin := iaRefreshApply(t, w, r, p, q)
	path := iaConfigureLibraryPath(t, w)
	if err := os.WriteFile(os.Getenv("PORTICO_IA_REFRESH_CRASH_PATH"), []byte(path), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	w.openPool(base, func(c driver.Conn) driver.Conn { return &iaRefreshCrashConn{c.(writerDriverConn), needle} })
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{1, "refresh-asset", 0}, iaRefreshFact(11))
	turn, b := iaRefreshTurn(t, w, r, q)
	_, err = runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	t.Fatal("crash point not reached", err)
}
func TestIARefreshCrashReopensWithoutPartialCustody(t *testing.T) {
	for _, point := range []string{"capture", "mutation", "receipt"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			manifest := filepath.Join(root, "path")
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestIARefreshCrashChild$", "-test.count=1", "-test.timeout=60s")
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "TMPDIR=") && !strings.HasPrefix(e, "PORTICO_IA_REFRESH_CRASH_") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, "TMPDIR="+root, "PORTICO_IA_REFRESH_CRASH_POINT="+point, "PORTICO_IA_REFRESH_CRASH_PATH="+manifest)
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 91 {
				t.Fatalf("native crash missing: %v %s", err, output)
			}
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path := string(raw)
			if !strings.HasPrefix(path, root+string(filepath.Separator)) {
				t.Fatal("child path escaped own scratch")
			}
			cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
			db, _, err := sqliteconn.Open(t.Context(), path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, check := range []struct {
				sql  string
				want int64
			}{
				{`SELECT count(*) FROM catalog_info_undo_asset_stream_facts`, 0},
				{`SELECT revision FROM asset_stream_facts WHERE asset_id='refresh-asset'`, 10},
				{`SELECT count(*) FROM catalog_info_refresh_receipts`, 1},
				{`SELECT info_publication_pending FROM catalog_assets WHERE id=41`, 1},
				{`SELECT playback_origin_revision FROM catalog_assets WHERE id=41`, 1},
				{`SELECT used_bytes=(SELECT retained_bytes FROM catalog_info_refresh_undo) FROM catalog_info_input_budget`, 1},
			} {
				var got int64
				if err = db.QueryRowContext(t.Context(), check.sql).Scan(&got); err != nil || got != check.want {
					t.Fatal(point, check.sql, got, err)
				}
			}
		})
	}
}
