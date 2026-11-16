package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// The query serving each guide row must seek within its channel and time range,
// rather than scanning all channels or sorting the guide before taking a page.
func TestGuidePageSeeksItsChannelAndTimeRange(t *testing.T) {
	g, err := openGuide(t.Context(), filepath.Join(t.TempDir(), "guide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.write.Close()
	defer g.read.Close()
	rows, err := g.read.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+guideProgrammesQuery, 0, "channel", 0, 100, 0, 200)
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
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(plan, "; ")
	if !strings.Contains(got, "SEARCH programmes USING PRIMARY KEY (source_id=? AND guide_key=? AND starts_at>?") || strings.Contains(got, "SCAN programmes") || strings.Contains(got, "TEMP B-TREE") {
		t.Fatalf("the guide page did not seek its channel/time index: %s", got)
	}
}
