package compactcatalog_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Retained file evidence survives a transient offline folder. Explicit root
// replacement and removal still follow the exact folder, including Windows roots.
func TestUninventoriedFilesFollowTheirOwnFolder(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('m2','m','/m2','/m2'),('m3','m','D:\Films\','D:\Films\')`)
	first := c.Movie(films, "/m/First.mkv", "First", 2020)
	second := c.Movie(films, "/m2/Second.mkv", "Second", 2020)
	windows := c.Movie(films, `D:\Films\Third.mkv`, "Third", 2020)
	// "/mx" is not inside "/m": this file is in no folder of the library.
	outside := c.Movie(films, "/mx/Outside.mkv", "Outside", 2020)
	c.Drain()
	type want struct {
		name string
		id   int64
		ok   bool
	}
	check := func(step string, cases ...want) {
		t.Helper()
		for _, w := range cases {
			var view, derived bool
			if err := c.DB.QueryRow(`SELECT available FROM inventory_item_availability WHERE item_id=?`, w.id).Scan(&view); err != nil {
				t.Fatal(err)
			}
			if err := c.DB.QueryRow(`SELECT available FROM catalog_item_availability WHERE entity_id=?`, w.id).Scan(&derived); err != nil {
				t.Fatal(err)
			}
			if view != w.ok || derived != w.ok {
				t.Errorf("%s: %s available (view %v, derived %v), want %v", step, w.name, view, derived, w.ok)
			}
		}
	}
	setHealth := func(id, health string) {
		t.Helper()
		c.Exec(`UPDATE library_sources SET health=? WHERE id=?`, health, id)
		c.Drain()
	}
	check("all folders online", want{"first", first.ID, true}, want{"second", second.ID, true}, want{"windows", windows.ID, true}, want{"outside", outside.ID, true})
	setHealth("m2", "offline")
	check("second folder offline retains evidence", want{"first", first.ID, true}, want{"second", second.ID, true}, want{"windows", windows.ID, true}, want{"outside", outside.ID, true})
	setHealth("m3", "root_changed")
	check("third folder's root changed", want{"windows", windows.ID, false}, want{"outside", outside.ID, true})
	setHealth("m", "offline")
	check("transient offline retains evidence", want{"first", first.ID, true}, want{"second", second.ID, true}, want{"windows", windows.ID, false}, want{"outside", outside.ID, true})
	setHealth("m2", "ready")
	check("second folder back", want{"first", first.ID, true}, want{"second", second.ID, true}, want{"windows", windows.ID, false}, want{"outside", outside.ID, true})
	c.Exec(`UPDATE library_sources SET enabled=0,health='removing' WHERE id='m2'`)
	c.Drain()
	check("second folder removed", want{"second", second.ID, false}, want{"outside", outside.ID, true})
}
