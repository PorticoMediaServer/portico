package dbwork

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh path is created private; an existing database is opened by SQLite
// alone, never by a descriptor of ours whose close would drop a live handle's
// locks. A second handle beside a live one leaves the first working.
func TestOpenHandleCreatesPrivateAndLeavesAnExistingFileToSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	first, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("fresh database mode %v %v", info.Mode(), err)
	}
	if _, err = first.Exec(`CREATE TABLE t(x INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	second, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.Exec(`INSERT INTO t VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	second.Close()
	var n int
	if err = first.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("first handle after a second came and went: %d %v", n, err)
	}
}
