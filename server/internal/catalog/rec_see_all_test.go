package catalog

import (
	"fmt"
	"testing"
	"time"
)

// See all for a personal row ("More recommendations": genre, person and
// dataset tag rows) lists the titles the row's own page shows, in its order.
// Found by the B2 walk: every such See all failed with "that home row does
// not exist for this viewer".
func TestPersonalRowSeeAllListsTheRowsTitles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	w := newRecWorld(t)
	datasetVocabulary(w, [3]string{"subgenre:cyberpunk", "subgenre", "Cyberpunk"})
	films := w.c.Handle("m")
	for i := 0; i < 30; i++ {
		f := w.c.Movie(films, fmt.Sprintf("/m/g%02d.mkv", i), fmt.Sprintf("Grit %02d", i), 1970+i)
		tagWorks(w, 3, "subgenre:cyberpunk", f.ID)
	}
	w.c.Drain()
	for _, f := range w.scifi[:3] {
		tagWorks(w, 3, "subgenre:cyberpunk", f.ID)
		w.watch("p", f)
	}
	w.c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now(), RowOrder: []string{"recommended", "for_you"}}
	personal := homePersonalRows(t, w.s, r)
	checked := map[string]bool{}
	for id := range personal {
		page, err := w.s.HomeSingleRow(r, id, HomeRowPage{Limit: 50})
		if err != nil {
			t.Fatal(id, err)
		}
		members, err := w.s.ResolveHomeRowMembership(r, id)
		if err != nil {
			t.Fatalf("%s: See all failed: %v", id, err)
		}
		rows, err := w.s.read().Query(`SELECT pid(e.public_id) FROM (`+members.query+`) m CROSS JOIN catalog_entities e ON e.id=m.id ORDER BY m.pos`, members.args...)
		if err != nil {
			t.Fatal(err)
		}
		var listed []string
		for rows.Next() {
			var v string
			rows.Scan(&v)
			listed = append(listed, v)
		}
		rows.Close()
		if len(listed) == 0 || len(listed) < len(page.Entries) {
			t.Fatalf("%s: See all lists %d titles, the row shows %d", id, len(listed), len(page.Entries))
		}
		for i, entry := range page.Entries {
			if listed[i] != entry.ID {
				t.Fatalf("%s: See all's title %d is %s, the row's is %s", id, i, listed[i], entry.ID)
			}
		}
		checked[id[:len("for_you:")+3]] = true
	}
	for _, kind := range []string{"for_you:gen", "for_you:per", "for_you:tag"} {
		if !checked[kind] {
			t.Fatalf("no %s… row was checked: %v", kind, checked)
		}
	}
}
