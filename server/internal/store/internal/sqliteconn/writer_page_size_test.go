package sqliteconn

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestWriterPageSizeIsExplicitAndWriterOnly(t *testing.T) {
	base := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Normal}
	for _, size := range []int32{0, 512, 1024, 4096, 65536} {
		cfg := base
		cfg.WriterPageSize = size
		if err := cfg.validate(); err != nil {
			t.Fatal(size, err)
		}
	}
	for _, size := range []int32{-1, 1, 511, 1000, 65537} {
		cfg := base
		cfg.WriterPageSize = size
		if err := cfg.validate(); !errors.Is(err, ErrPolicy) {
			t.Fatal("invalid page assertion", size, err)
		}
	}
	for _, role := range []Role{Reader, Checkpointer} {
		cfg := base
		cfg.Role, cfg.WriterPageSize = role, 4096
		if err := cfg.validate(); !errors.Is(err, ErrPolicy) {
			t.Fatal("non-writer page assertion", role, err)
		}
	}
	for _, value := range []any{Config{WriterPageSize: 4096}, Settings{WriterPageSize: 4096}} {
		b, err := json.Marshal(value)
		if err != nil || !bytes.Contains(b, []byte(`"WriterPageSize":4096`)) {
			t.Fatal("missing explicit evidence", string(b), err)
		}
	}
}

func TestWriterPageSizeAssertionNeverSetsOrRepairs(t *testing.T) {
	_, db, cfg := fixture(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var actual int32
	if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	got, err := Assert(t.Context(), conn, cfg)
	if err != nil || got.WriterPageSize != 0 {
		t.Fatal("zero policy fabricated a geometry sample", got, err)
	}
	cfg.WriterPageSize = actual
	got, err = Assert(t.Context(), conn, cfg)
	if err != nil || got.WriterPageSize != actual {
		t.Fatal("actual page assertion", got, err)
	}
	wrong := actual / 2
	if actual == 512 {
		wrong = 1024
	}
	cfg.WriterPageSize = wrong
	got, err = Assert(t.Context(), conn, cfg)
	if !errors.Is(err, ErrPolicy) || got.WriterPageSize != actual {
		t.Fatal("mismatched geometry accepted or hidden", got, err)
	}
	var after int32
	if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&after); err != nil || after != actual {
		t.Fatal("assertion changed physical page size", actual, after, err)
	}
}

func TestWriterPageSizeNativeGeometry(t *testing.T) {
	for _, size := range []int32{512, 65536} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "geometry.sqlite")
			cfg := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Normal}
			seed, _, err := Open(t.Context(), path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer seed.Close()
			// SQLite cannot change page size in WAL mode. Establish the empty
			// file's native geometry before loading any schema or records.
			if _, err := seed.ExecContext(t.Context(), fmt.Sprintf("PRAGMA journal_mode=DELETE; PRAGMA page_size=%d; VACUUM; PRAGMA journal_mode=WAL", size)); err != nil {
				t.Fatal(err)
			}
			if _, err := seed.ExecContext(t.Context(), "CREATE TABLE geometry(value INTEGER); INSERT INTO geometry VALUES(1473)"); err != nil {
				t.Fatal(err)
			}
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}

			cfg.WriterPageSize = size
			base, err := Connector(path, cfg, ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			counted := &countedConnector{Connector: base}
			db := sql.OpenDB(counted)
			defer db.Close()
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			for generation := range 2 {
				conn, err := db.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var actual int32
				if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&actual); err != nil || actual != size {
					t.Fatal("native geometry", generation, actual, size, err)
				}
				if got, err := Assert(t.Context(), conn, cfg); err != nil || got.WriterPageSize != size {
					t.Fatal("matching same-connection evidence", generation, got, err)
				}
				wrong := cfg
				wrong.WriterPageSize = 4096
				if got, err := Assert(t.Context(), conn, wrong); !errors.Is(err, ErrPolicy) || got.WriterPageSize != size {
					t.Fatal("mismatch accepted or actual geometry hidden", generation, got, err)
				}
				if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&actual); err != nil || actual != size {
					t.Fatal("assertion changed geometry", generation, actual, err)
				}
				var value int
				if err := conn.QueryRowContext(t.Context(), "SELECT value FROM geometry").Scan(&value); err != nil || value != 1473 {
					t.Fatal("loaded content changed", generation, value, err)
				}
				if generation == 0 {
					if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
						t.Fatal(err)
					}
				}
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if counted.opened.Load() != 2 {
				t.Fatal("physical replacement not exercised", counted.opened.Load())
			}
		})
	}
}
