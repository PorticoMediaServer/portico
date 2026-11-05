package sqliteconn_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestEnforcementRejectsWeakeningWithoutRepair(t *testing.T) {
	ctx := context.Background()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(ctx, filepath.Join(t.TempDir(), "enforcement.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, test := range []struct {
		pragma string
		bad    int
	}{
		{"foreign_keys", 0}, {"ignore_check_constraints", 1},
		{"defer_foreign_keys", 1}, {"writable_schema", 1}, {"read_uncommitted", 1},
	} {
		t.Run(test.pragma, func(t *testing.T) {
			setting := "=ON"
			reset := "=OFF"
			if test.bad == 0 {
				setting, reset = reset, setting
			}
			if _, err := conn.ExecContext(ctx, "PRAGMA "+test.pragma+setting); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.ExecContext(ctx, "PRAGMA "+test.pragma+reset) })
			_, err := sqliteconn.AssertEnforcement(ctx, conn)
			if !errors.Is(err, sqliteconn.ErrPolicy) {
				t.Fatalf("weakening accepted: %v", err)
			}
			if test.pragma == "foreign_keys" && !errors.Is(err, sqliteconn.ErrForeignKeysDisabled) {
				t.Fatalf("missing FK-specific error: %v", err)
			}
			if _, err := sqliteconn.Assert(ctx, conn, cfg); !errors.Is(err, sqliteconn.ErrPolicy) {
				t.Fatalf("full policy skipped enforcement: %v", err)
			}
			var after int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+test.pragma).Scan(&after); err != nil || after != test.bad {
				t.Fatalf("setting was repaired: %d, %v", after, err)
			}
		})
	}
	if _, err := sqliteconn.AssertEnforcement(ctx, conn); err != nil {
		t.Fatalf("positive enforcement: %v", err)
	}
}

func TestEnforcementOnReplacementPhysicalConnection(t *testing.T) {
	ctx := context.Background()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(ctx, filepath.Join(t.TempDir(), "replacement.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF; PRAGMA ignore_check_constraints=ON; PRAGMA defer_foreign_keys=ON; PRAGMA writable_schema=ON; PRAGMA read_uncommitted=ON"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("physical retirement = %v", err)
	}
	conn.Close()
	replacement, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	got, err := sqliteconn.AssertEnforcement(ctx, replacement)
	if err != nil || got != (sqliteconn.Enforcement{ForeignKeys: 1}) {
		t.Fatalf("replacement enforcement = %+v, %v", got, err)
	}
}
