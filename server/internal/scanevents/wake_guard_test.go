package scanevents

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The publisher is woken only by writes that name its tables
// (dbwork.WakeOnTableWrites). A write to them that no statement names — a
// trigger's body, or a foreign-key action such as ON DELETE CASCADE — would
// never wake it, and a scan's start or end would go unannounced. There are
// none today; if one is ever added, extend the publisher's wake to the table
// that fires it.
func TestNoHiddenWritesToTheScanTables(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	write := regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|UPDATE\s+(?:OR\s+\w+\s+)?|DELETE\s+FROM)\s*["\x60\[]?(inventory_source_active|inventory_runs)\b`)
	rows, err := db.Query(`SELECT name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE type='trigger'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, table, sql string
		if err = rows.Scan(&name, &table, &sql); err != nil {
			t.Fatal(err)
		}
		if m := write.FindStringSubmatch(sql); m != nil {
			t.Errorf("trigger %s on %s writes %s: add %s to the scan-events publisher's WakeOnTableWrites", name, table, m[1], table)
		}
	}
	for _, table := range []string{"inventory_source_active", "inventory_runs"} {
		var sql string
		if err = db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		upper := strings.ToUpper(sql)
		for _, action := range []string{"ON DELETE CASCADE", "ON DELETE SET", "ON UPDATE CASCADE", "ON UPDATE SET"} {
			if strings.Contains(upper, action) {
				t.Errorf("%s has %s: a parent's write changes it without naming it; add the parent table to the scan-events publisher's WakeOnTableWrites", table, action)
			}
		}
	}
}
