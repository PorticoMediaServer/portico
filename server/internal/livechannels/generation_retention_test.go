package livechannels

import (
	"context"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Opt-in: point PORTICO_RETENTION_DB at a COPY of a real state database. The
// pruner must leave exactly the active generations, with every foreign key and
// the file itself intact.
func TestPruneSupersededGenerationsOnARealCopy(t *testing.T) {
	path := os.Getenv("PORTICO_RETENTION_DB")
	if path == "" {
		t.Skip("set PORTICO_RETENTION_DB to a copy of a state database")
	}
	db, err := dbwork.OpenHandle(path, dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{db: db}
	countViolations := func() int {
		rows, err := db.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
		}
		return n
	}
	// A long-lived state can carry violations of its own; pruning must add none.
	violationsBefore := countViolations()
	var before, active int
	_ = db.QueryRow(`SELECT count(*) FROM live_generations`).Scan(&before)
	_ = db.QueryRow(`SELECT count(DISTINCT active_generation) FROM live_sources WHERE active_generation<>''`).Scan(&active)
	for i := 0; i < before+1; i++ {
		found, err := s.PruneSupersededGeneration(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
	}
	var after, orphans int
	_ = db.QueryRow(`SELECT count(*) FROM live_generations`).Scan(&after)
	_ = db.QueryRow(`SELECT count(*) FROM live_programmes p WHERE NOT EXISTS(SELECT 1 FROM live_generations g WHERE g.id=p.generation_id)`).Scan(&orphans)
	violations := countViolations() - violationsBefore
	var integrity string
	_ = db.QueryRow(`PRAGMA quick_check`).Scan(&integrity)
	t.Logf("generations %d -> %d (active %d), orphaned programmes %d, new fk violations %d, quick_check %s", before, after, active, orphans, violations, integrity)
	if orphans != 0 || violations > 0 || integrity != "ok" {
		t.Fatal("pruning damaged the database")
	}
	var missing int
	_ = db.QueryRow(`SELECT count(*) FROM live_sources s WHERE s.active_generation<>'' AND NOT EXISTS(SELECT 1 FROM live_generations g WHERE g.id=s.active_generation)`).Scan(&missing)
	if missing != 0 {
		t.Fatal("an active generation was removed")
	}
}
