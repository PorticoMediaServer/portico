package catalog

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// TestRestrictedListeningFirstPageAtScale is an opt-in measurement (item 3):
// a PG-13 viewer's first ordered page of a whole music library, with totals
// from the class, against the previous whole-library count.
//
//	PORTICO_HOME_ROWS_DB=/path/catalog.sqlite go test ./internal/catalog -run TestRestrictedListeningFirstPageAtScale -count=1 -v
func TestRestrictedListeningFirstPageAtScale(t *testing.T) {
	path := os.Getenv("PORTICO_HOME_ROWS_DB")
	if path == "" {
		t.Skip("set PORTICO_HOME_ROWS_DB to a scalebench fixture")
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	pg13 := 13
	r := identity.ContentRestrictions{MaximumAge: &pg13, BlockUnrated: true}
	built := time.Now()
	if err = s.RebuildVisibilityClass(context.Background(), "music", r); err != nil {
		t.Fatal(err)
	}
	t.Logf("pg13 music class (with availability) built in %s", time.Since(built).Round(time.Millisecond))
	viewer := Viewer{Profile: "viewer", Fence: "scale-pg13", Libraries: []string{"music"}, Restrictions: r}
	request := ContentRequest{Viewer: viewer, Library: "music", Limit: 40}
	target := ListeningTarget{"music", "library", "music"}
	times := []time.Duration{}
	var total, missing int
	for k := 0; k < 11; k++ {
		began := time.Now()
		page, err := s.ListeningSelection(request, target, "ordered", "fixture", false, "")
		if err != nil {
			t.Fatal(err)
		}
		times = append(times, time.Since(began))
		total, missing = page.TotalCount, page.UnavailableCount
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("first page with class totals: p50 %s p95 %s (total %d unavailable %d)", times[5].Round(100*time.Microsecond), times[10].Round(100*time.Microsecond), total, missing)
	// The opt-in comparison keeps the old source-table predicate independent of
	// listeningWhere, which now names compact integer entities.
	where := `i.kind=7`
	restriction, bound := ItemRestrictionSQL("i.id", r)
	began := time.Now()
	var walkTotal, walkMissing int
	query := `SELECT COALESCE(sum(EXISTS(SELECT 1 FROM catalog_item_availability v WHERE v.entity_id=i.id AND v.available=1 AND v.retired=0)),0),COALESCE(sum(NOT EXISTS(SELECT 1 FROM catalog_item_availability v WHERE v.entity_id=i.id AND v.available=1 AND v.retired=0)),0)
	 FROM catalog_browse_rows ar INDEXED BY catalog_browse_title
	 CROSS JOIN catalog_albums a ON a.entity_id=ar.entity_id AND a.retired=0
	 CROSS JOIN catalog_songs s INDEXED BY catalog_songs_listening_order ON s.album_id=a.entity_id
	 JOIN catalog_entities i ON i.id=s.entity_id
	 WHERE ar.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ar.kind=6 AND i.library_id=ar.library_id AND ` + where + ` AND ` + restriction
	if err = db.QueryRow(query, append([]any{"music"}, bound...)...).Scan(&walkTotal, &walkMissing); err != nil {
		t.Fatal(err)
	}
	t.Logf("previous whole-library count: %s (total %d unavailable %d)", time.Since(began).Round(time.Millisecond), walkTotal, walkMissing)
	if walkTotal != total || walkMissing != missing {
		t.Fatalf("class totals %d/%d differ from the walk %d/%d", total, missing, walkTotal, walkMissing)
	}
}
