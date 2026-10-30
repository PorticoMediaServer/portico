package mediaanalysis

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// WJ2: the retire sweep examines one bounded page of live results per pass,
// retires the stale ones with their heads, and starts over after a short page.
func TestRetireSweepExaminesOnePageAtATime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	total := 2*retirePageSize + 44
	for n := 0; n < total; n++ {
		id := fmt.Sprintf("r%04d", n)
		if _, err = db.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms,retired_ms) VALUES(?,?,'a','r','i',1,1,'probe','v','e','t',1000,'{}',0,0)`, id, id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO analysis_heads VALUES(?,'r','probe',?)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{db: db}
	count := func(query string) int {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for pass, want := range []struct {
		retired int
		cursor  string
	}{{retirePageSize, fmt.Sprintf("r%04d", retirePageSize-1)}, {2 * retirePageSize, fmt.Sprintf("r%04d", 2*retirePageSize-1)}, {total, ""}} {
		if err = s.retirePage(context.Background(), time.UnixMilli(5)); err != nil {
			t.Fatal(err)
		}
		if got := count(`SELECT count(*) FROM analysis_results WHERE retired_ms>0`); got != want.retired || s.retireCursor != want.cursor {
			t.Fatalf("pass %d: retired %d cursor %q, want %d %q", pass, got, s.retireCursor, want.retired, want.cursor)
		}
		if heads := count(`SELECT count(*) FROM analysis_heads`); heads != total-want.retired {
			t.Fatalf("pass %d: %d heads left", pass, heads)
		}
	}
	// With nothing live left a pass examines nothing and keeps the cursor at the start.
	if err = s.retirePage(context.Background(), time.UnixMilli(6)); err != nil || s.retireCursor != "" {
		t.Fatal(err, s.retireCursor)
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id FROM analysis_results INDEXED BY analysis_results_retention WHERE retired_ms=0 AND id>? ORDER BY id LIMIT ?`, "", retirePageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if text := strings.Join(plan, "; "); strings.Contains(text, "TEMP B-TREE") || !strings.Contains(text, "analysis_results_retention") {
		t.Fatal(text)
	}
}
