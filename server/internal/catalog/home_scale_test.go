package catalog

import (
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestHomeReadOnlyScale(t *testing.T) {
	path := os.Getenv("PORTICO_HOME_SCALE_DB")
	if path == "" {
		t.Skip("set isolated scale fixture database")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := New(db)
	scalePlan(t, db, `SELECT e.entity_id FROM catalog_browse_rows e INDEXED BY catalog_browse_feature JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id='large') AND e.kind=1 AND e.available=1 AND e.backdrop=1 ORDER BY e.entity_id LIMIT 1`)
	for n := 0; n < 11; n++ {
		start := time.Now()
		p, e := s.HomeRows(HomeRequest{Viewer: Viewer{Profile: "viewer", Fence: "home", Libraries: []string{"large", "other"}}, Profile: "viewer", ViewerFence: "home", Libraries: []string{"large", "other"}})
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("Home iteration%d %s rows=%d", n, time.Since(start), len(p.Rows))
	}
}
