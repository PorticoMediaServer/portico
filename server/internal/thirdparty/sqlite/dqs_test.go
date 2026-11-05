package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestDQSReadOnlyNativeFlags(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dqs.sqlite")+"?_dqs=0")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, want := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {0, 0}} {
		if err := c.Raw(func(raw any) error {
			native := raw.(*conn)
			for i, op := range []int32{sqlite3.SQLITE_DBCONFIG_DQS_DDL, sqlite3.SQLITE_DBCONFIG_DQS_DML} {
				if rc := native.dbConfigBool(op, want[i] != 0); rc != sqlite3.SQLITE_OK {
					return native.errstr(rc)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			ddl, dml, err := DQS(c)
			if err != nil || [2]int{ddl, dml} != want {
				t.Fatalf("flags %v: got %d/%d: %v", want, ddl, dml, err)
			}
		}
	}
	if _, _, err := DQS(nil); err == nil {
		t.Fatal("nil connection accepted")
	}
}

func TestDQSDefaultDriverUnchanged(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ddl, dml, err := DQS(c)
	if err != nil || ddl != 1 || dml != 1 {
		t.Fatalf("legacy flags: %d/%d %v", ddl, dml, err)
	}
}
