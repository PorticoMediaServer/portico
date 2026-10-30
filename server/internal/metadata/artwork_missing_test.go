package metadata

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// missingArtworkResetFixture builds a service over a fresh catalogue with the
// requested number of movie entities. Only catalogue identity matters here; the
// repair step never touches files or providers.
func missingArtworkResetFixture(t *testing.T, entities int) (*Service, *catalogtest.Catalog, []catalogtest.Item) {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	items := make([]catalogtest.Item, entities)
	for i := range items {
		items[i] = c.Movie(lib, fmt.Sprintf("/films/film-%02d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
	}
	return New(c.DB, "test-token"), c, items
}

func insertMissingArtworkJob(t *testing.T, c *catalogtest.Catalog, id string, entity int64, role, subject, provider, status string) {
	t.Helper()
	c.Exec(`INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,provider,status,attempts,next_attempt,preview,created_at)
VALUES(?,?,?,?,?,?,?,3,'2026-01-02T00:00:00.000Z',0,'2026-01-01T00:00:00.000Z')`,
		id, "item", entity, role, subject, provider, status)
}

func insertMissingArtworkPreviewJob(t *testing.T, c *catalogtest.Catalog, id string, entity int64, role, subject, provider, status string) {
	t.Helper()
	c.Exec(`INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,provider,status,attempts,next_attempt,preview,created_at)
VALUES(?,?,?,?,?,?,?,3,'2026-01-02T00:00:00.000Z',1,'2026-01-01T00:00:00.000Z')`,
		id, "item", entity, role, subject, provider, status)
}

type missingArtworkJobState struct {
	Status      string
	Attempts    int64
	NextAttempt string
}

func readMissingArtworkJob(t *testing.T, c *catalogtest.Catalog, id string) missingArtworkJobState {
	t.Helper()
	var state missingArtworkJobState
	if err := c.DB.QueryRow(`SELECT status,attempts,next_attempt FROM artwork_jobs WHERE id=?`, id).Scan(&state.Status, &state.Attempts, &state.NextAttempt); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertMissingArtworkReset(t *testing.T, c *catalogtest.Catalog, id string) {
	t.Helper()
	if state := readMissingArtworkJob(t, c, id); state.Status != "pending" || state.Attempts != 0 || state.NextAttempt != "" {
		t.Fatalf("job %s was not requeued: %+v", id, state)
	}
}

func assertMissingArtworkUntouched(t *testing.T, c *catalogtest.Catalog, id string) {
	t.Helper()
	if state := readMissingArtworkJob(t, c, id); state.Status == "pending" || state.Attempts != 3 || state.NextAttempt != "2026-01-02T00:00:00.000Z" {
		t.Fatalf("job %s was changed by a hint that must not match it: %+v", id, state)
	}
}

func backgroundGateAcquisitions() uint64 {
	return dbwork.WriteGate().Stats().ByClass[dbwork.ClassBackgroundMedia.String()].Acquired
}

func TestRepairMissingArtworkStepBatchesOneGateTransaction(t *testing.T) {
	s, c, items := missingArtworkResetFixture(t, 32)
	for i, item := range items {
		insertMissingArtworkJob(t, c, fmt.Sprintf("job-%02d", i), item.ID, "poster", "", "tmdb", "complete")
	}
	// The old per-hint predicate never named preview, so a preview row for the
	// same identity was reset too. The batch must not narrow that.
	insertMissingArtworkPreviewJob(t, c, "job-preview", items[0].ID, "poster", "", "tmdb", "complete")
	for _, item := range items {
		s.queueMissingArtwork(RepairTarget{Kind: "item", ID: item.Public}, "poster", "")
	}

	gateBefore := backgroundGateAcquisitions()
	publicationsBefore := dbwork.Publications()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 1 {
		t.Fatalf("32 hints took %d writer-gate transactions, want 1", acquired)
	}
	if publications := dbwork.Publications() - publicationsBefore; publications != 1 {
		t.Fatalf("32 hints committed %d write transactions, want 1", publications)
	}
	for i := range items {
		assertMissingArtworkReset(t, c, fmt.Sprintf("job-%02d", i))
	}
	assertMissingArtworkReset(t, c, "job-preview")

	// The queue was drained, so a second pass is free: no gate acquisition and
	// no write transaction for nothing.
	gateBefore = backgroundGateAcquisitions()
	publicationsBefore = dbwork.Publications()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 0 {
		t.Fatalf("an empty hint queue took %d writer-gate transactions, want 0", acquired)
	}
	if publications := dbwork.Publications() - publicationsBefore; publications != 0 {
		t.Fatalf("an empty hint queue committed %d write transactions, want 0", publications)
	}
}

func TestRepairMissingArtworkStepKeepsPredicatesAndDropsMissingEntities(t *testing.T) {
	s, c, items := missingArtworkResetFixture(t, 6)
	insertMissingArtworkJob(t, c, "job-valid", items[0].ID, "poster", "", "tmdb", "complete")
	insertMissingArtworkJob(t, c, "job-role", items[1].ID, "backdrop", "", "tmdb", "complete")
	insertMissingArtworkJob(t, c, "job-subject", items[2].ID, "poster", "season-1", "tmdb", "complete")
	insertMissingArtworkJob(t, c, "job-generated", items[3].ID, "poster", "", "generated", "complete")
	insertMissingArtworkJob(t, c, "job-not-complete", items[4].ID, "poster", "", "tmdb", "failed")

	// A hint whose entity no longer exists must be dropped before the UPDATE,
	// even though the exact matching job row still exists. Delete first: the
	// catalogue's own delete trigger retires jobs for a live deletion, so the
	// orphan shape is created by inserting afterwards.
	c.Delete(items[5].ID)
	insertMissingArtworkJob(t, c, "job-deleted-entity", items[5].ID, "poster", "", "tmdb", "complete")
	deleted := RepairTarget{Kind: "item", ID: items[5].Public}

	s.queueMissingArtwork(RepairTarget{Kind: "item", ID: items[0].Public}, "poster", "")
	s.queueMissingArtwork(RepairTarget{Kind: "item", ID: items[1].Public}, "poster", "")
	s.queueMissingArtwork(RepairTarget{Kind: "item", ID: items[2].Public}, "poster", "season-2")
	s.queueMissingArtwork(RepairTarget{Kind: "item", ID: items[3].Public}, "poster", "")
	s.queueMissingArtwork(RepairTarget{Kind: "item", ID: items[4].Public}, "poster", "")
	s.queueMissingArtwork(deleted, "poster", "")

	gateBefore := backgroundGateAcquisitions()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 1 {
		t.Fatalf("mixed hints took %d writer-gate transactions, want 1", acquired)
	}
	assertMissingArtworkReset(t, c, "job-valid")
	assertMissingArtworkUntouched(t, c, "job-role")
	assertMissingArtworkUntouched(t, c, "job-subject")
	assertMissingArtworkUntouched(t, c, "job-generated")
	assertMissingArtworkUntouched(t, c, "job-not-complete")
	assertMissingArtworkUntouched(t, c, "job-deleted-entity")

	// The deleted hint was consumed, not left to be retried forever.
	gateBefore = backgroundGateAcquisitions()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 0 {
		t.Fatalf("deleted/mismatched hints left work behind: %d extra writer-gate transactions", acquired)
	}
}

func TestRepairMissingArtworkStepFailureRequeuesWholeBatch(t *testing.T) {
	s, c, items := missingArtworkResetFixture(t, 32)
	for i, item := range items {
		insertMissingArtworkJob(t, c, fmt.Sprintf("job-%02d", i), item.ID, "poster", "", "tmdb", "complete")
	}
	// One row rejects the UPDATE, which rolls back the whole statement.
	c.Exec(fmt.Sprintf(`CREATE TRIGGER artwork_missing_injected BEFORE UPDATE ON artwork_jobs
WHEN NEW.entity_id=%d AND NEW.role='poster' BEGIN SELECT RAISE(ABORT,'injected failure'); END`, items[17].ID))
	for _, item := range items {
		s.queueMissingArtwork(RepairTarget{Kind: "item", ID: item.Public}, "poster", "")
	}

	if err := s.RepairMissingArtworkStep(context.Background()); err == nil {
		t.Fatal("the injected update failure was not reported")
	}
	// A failed transaction reports no partial success: every row is as it was.
	for i := range items {
		id := fmt.Sprintf("job-%02d", i)
		if state := readMissingArtworkJob(t, c, id); state.Status != "complete" || state.Attempts != 3 || state.NextAttempt != "2026-01-02T00:00:00.000Z" {
			t.Fatalf("job %s changed despite the rolled-back batch: %+v", id, state)
		}
	}

	// The work is still pending: with the failure gone, the next pass completes
	// all 32 hints in one transaction.
	c.Exec(`DROP TRIGGER artwork_missing_injected`)
	gateBefore := backgroundGateAcquisitions()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 1 {
		t.Fatalf("requeued batch took %d writer-gate transactions, want 1", acquired)
	}
	for i := range items {
		assertMissingArtworkReset(t, c, fmt.Sprintf("job-%02d", i))
	}
}

func TestRepairMissingArtworkStepCancellationRequeuesWholeBatch(t *testing.T) {
	s, c, items := missingArtworkResetFixture(t, 32)
	for i, item := range items {
		insertMissingArtworkJob(t, c, fmt.Sprintf("job-%02d", i), item.ID, "poster", "", "tmdb", "complete")
	}
	for _, item := range items {
		s.queueMissingArtwork(RepairTarget{Kind: "item", ID: item.Public}, "poster", "")
	}

	gateBefore := backgroundGateAcquisitions()
	publicationsBefore := dbwork.Publications()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.RepairMissingArtworkStep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled step returned %v, want context.Canceled", err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 0 {
		t.Fatalf("cancelled step took %d writer-gate transactions, want 0", acquired)
	}
	if publications := dbwork.Publications() - publicationsBefore; publications != 0 {
		t.Fatalf("cancelled step committed %d write transactions, want 0", publications)
	}
	for i := range items {
		id := fmt.Sprintf("job-%02d", i)
		if state := readMissingArtworkJob(t, c, id); state.Status != "complete" || state.Attempts != 3 {
			t.Fatalf("job %s changed under a cancelled context: %+v", id, state)
		}
	}

	// Cancellation must not consume the hints: a live pass still completes 32.
	gateBefore = backgroundGateAcquisitions()
	if err := s.RepairMissingArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired := backgroundGateAcquisitions() - gateBefore; acquired != 1 {
		t.Fatalf("requeued batch took %d writer-gate transactions, want 1", acquired)
	}
	for i := range items {
		assertMissingArtworkReset(t, c, fmt.Sprintf("job-%02d", i))
	}
}
