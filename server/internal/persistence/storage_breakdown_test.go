package persistence

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// TestStorageBreakdown is an opt-in measurement for ARCH-SRV-01: bytes per
// table (with its indexes) and per item in an existing database, read-only.
//
//	PORTICO_STORAGE_DB=/path/catalog.sqlite go test ./internal/persistence -run TestStorageBreakdown -v
func TestStorageBreakdown(t *testing.T) {
	path := os.Getenv("PORTICO_STORAGE_DB")
	if path == "" {
		t.Skip("set PORTICO_STORAGE_DB")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var items float64
	if err = db.QueryRow(`SELECT count(*) FROM catalog_entities`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	var total float64
	if err = db.QueryRow(`SELECT sum(pgsize) FROM dbstat`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	t.Logf("items %.0f, %.1f MB, %.0f bytes/item", items, total/1e6, total/items)
	rows, err := db.Query(`SELECT COALESCE(m.tbl_name,s.name) owner,sum(s.pgsize) bytes,
	 sum(CASE WHEN m.type='index' THEN s.pgsize ELSE 0 END) idx
	 FROM dbstat s LEFT JOIN sqlite_master m ON m.name=s.name GROUP BY owner ORDER BY bytes DESC LIMIT 40`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner string
		var bytes, idx float64
		if err = rows.Scan(&owner, &bytes, &idx); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-40s %7.0f B/item (indexes %5.0f)", owner, bytes/items, idx/items)
	}
}
