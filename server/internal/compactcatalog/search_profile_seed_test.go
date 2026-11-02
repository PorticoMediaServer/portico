package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Measurement only: extend the existing focused film fixture with the real
// search document/prefix producer, one document per unchanged atomic-unit plan.
// No other catalogue projection or whole-server readiness is claimed.
func TestSearchProfileSeed(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_PROFILE_ITEMS"))
	if n == 0 {
		t.Skip("hosted search fixture")
	}
	root := os.Getenv("PORTICO_SCALE_SEEDS")
	if root == "" {
		t.Fatal("explicit seed directory required")
	}
	prefix := "search-films"
	realistic := os.Getenv("PORTICO_SEARCH_REALISTIC") == "1"
	if realistic {
		prefix = "search-realistic-films"
	}
	path := filepath.Join(root, fmt.Sprintf("%s-%d.sqlite", prefix, n))
	if _, err := os.Stat(path); err == nil {
		return
	}
	in, err := os.Open(filepath.Join(root, fmt.Sprintf("new-server-films-%d.sqlite", n)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path + ".building")
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path + ".building")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	schedule, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				schedule.Tick(ctx)
			}
		}
	}()
	defer func() { cancel(); <-done }()
	start := time.Now()
	var corpus realisticCorpus
	if realistic {
		corpus = seedRealisticSearchTitles(t, db, n)
	}
	dbwork.ResetPlans()
	var after int64
	count := 0
	for {
		var id int64
		err = db.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE kind=1 AND id>? ORDER BY id LIMIT 1`, after).Scan(&id)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		err = dbwork.WithWriteTxContext(dbwork.WithPlan(ctx, derivationUnitPlan), db, dbwork.ClassMaintenance, func(writeCtx context.Context, tx *sql.Tx) error {
			return deriveSearch(writeCtx, tx, fmt.Sprintf("[%d]", id))
		})
		if err != nil {
			t.Fatalf("document %d: %v", id, err)
		}
		after = id
		count++
		if count%1000 == 0 {
			t.Logf("search documents %d/%d in %s", count, n, time.Since(start))
		}
	}
	if count != n {
		t.Fatalf("documents %d, want %d", count, n)
	}
	plans := dbwork.Plans()
	if plans.Overruns != 0 {
		t.Fatalf("write-plan overruns: %+v", plans)
	}
	var documents int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_search_documents WHERE entity_kind=1`).Scan(&documents); err != nil || documents != n {
		t.Fatalf("documents=%d: %v", documents, err)
	}
	if realistic {
		validateRealisticSearchCorpus(t, db, &corpus)
		raw, err := json.MarshalIndent(corpus, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path+".corpus.json", raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "realistic-corpus.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Stop the normal checkpointer before the final checkpoint/close/publication.
	cancel()
	<-done
	if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".building", path); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(map[string]any{"items": n, "documents": documents, "seconds": time.Since(start).Seconds(), "plans": plans, "boundary": "existing focused films plus production deriveSearch, one document under existing unit plan"}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "search-seed.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
