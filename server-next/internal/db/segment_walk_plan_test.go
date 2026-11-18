package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// A walk through a library by version id reads versions from the cursor in id order and
// stops at the page's end. Read through the library's titles instead, every page read and
// sorted all of them: at 400,000 items a background pass over the episodes took a processor
// for half an hour and slowed every page people opened meanwhile.
func TestALibraryWalkReadsOnlyItsPage(t *testing.T) {
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	where := ` AND i.kind=? AND l.public_id=? AND m.id>? AND m.video_codec IS NOT NULL ORDER BY m.id LIMIT 25`
	args := []any{KindMovie, KindSeason, KindMovie, KindEpisode, KindEpisode, "lib", 0}
	rows, err := d.read.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+segmentSQL(segmentWalkJoins, where), args...)
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
	got := strings.Join(plan, "; ")
	if !strings.Contains(got, "SEARCH m USING INTEGER PRIMARY KEY (rowid>?)") || strings.Contains(got, "TEMP B-TREE") {
		t.Fatalf("a library walk does not read versions from its cursor: %s", got)
	}
}
