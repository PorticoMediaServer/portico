package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// Trending reads its few dozen rows first and looks each title up by id, whatever the size
// of the library: read the other way, every Home read every TMDB id of the library.
func TestTrendingReadsItsRowsFirst(t *testing.T) {
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.read.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+trendingIDsSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if len(plan) == 0 || !strings.HasPrefix(plan[0], "SCAN trending") || !strings.Contains(strings.Join(plan, "; "), "(provider=? AND external_id=?)") {
		t.Fatalf("trending does not start from its own rows: %s", strings.Join(plan, "; "))
	}
}
