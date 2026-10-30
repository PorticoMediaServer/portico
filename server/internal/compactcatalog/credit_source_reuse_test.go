package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// creditSourceCounts is the physical footprint a publication leaves behind.
type creditSourceCounts struct {
	publications, rows, members int
}

func creditSourceTestCounts(t *testing.T, db *sql.DB, entity int64) creditSourceCounts {
	t.Helper()
	return creditSourceCounts{
		publications: cc1Scalar(t, db, `SELECT count(*) FROM credit_source_publications WHERE entity_id=?`, entity),
		rows:         cc1Scalar(t, db, `SELECT count(*) FROM credit_source_rows WHERE entity_id=?`, entity),
		members:      cc1Scalar(t, db, `SELECT count(*) FROM credit_source_publication_members WHERE entity_id=?`, entity),
	}
}

// publishCreditSourceTest runs one publication in its own write transaction and
// returns the error instead of failing the test, so stale refusals are testable.
func publishCreditSourceTest(t *testing.T, db *sql.DB, entity int64, provider, recordReference, observedAt string, expectedRevision int64, tuples []AcceptedCreditTuple) (CreditSourcePublication, error) {
	t.Helper()
	var publication CreditSourcePublication
	err := dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassMaintenance, func(ctx context.Context, tx *sql.Tx) error {
		var publishErr error
		publication, publishErr = PublishCreditSourceTx(ctx, tx, entity, provider, recordReference, observedAt, expectedRevision, tuples)
		return publishErr
	})
	return publication, err
}

func creditSourceTestTuple(reference, name string, ordinal int) AcceptedCreditTuple {
	return AcceptedCreditTuple{
		RecordReference:  reference,
		CreditID:         name,
		ProviderPersonID: "person-" + name,
		TargetKind:       "movie",
		TargetID:         "movie-1",
		CreditedName:     name,
		Role:             "Actor",
		Department:       "Cast",
		SourceOrdinal:    ordinal,
	}
}

func creditSourceTestEntity(t *testing.T, db *sql.DB, key string) int64 {
	t.Helper()
	library := cc1Library(t, db, "films", "Films", "movie", "/films")
	return cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: key, Title: key}, nil)
}

func creditSourceTestHead(t *testing.T, db *sql.DB, entity int64, provider string) CreditSourcePublication {
	t.Helper()
	head, err := ReadCreditSourceHead(context.Background(), db, entity, provider)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// TestPublishCreditSourceReusesAnIdenticalHead: a refresh that accepted exactly
// the head's record and ordered tuples publishes nothing and keeps the head's
// identity, revision and rows. Any difference in record, tuple, order or length
// still seals a new generation.
func TestPublishCreditSourceReusesAnIdenticalHead(t *testing.T) {
	const provider = "screen"
	db := cc1OpenDB(t, "credit-reuse.sqlite")
	entity := creditSourceTestEntity(t, db, "file:reuse.mkv")
	base := []AcceptedCreditTuple{
		creditSourceTestTuple("record-a", "Ann", 0),
		creditSourceTestTuple("record-a", "Bob", 1),
	}

	first, err := publishCreditSourceTest(t, db, entity, provider, "record-a", "2026-10-01T00:00:00Z", 0, base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.ID == "" || len(first.Members) != len(base) {
		t.Fatalf("first publication = %+v", first)
	}
	before := creditSourceTestCounts(t, db, entity)
	headBefore := creditSourceTestHead(t, db, entity, provider)

	reused, err := publishCreditSourceTest(t, db, entity, provider, "record-a", "2026-10-02T00:00:00Z", 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != first.ID || reused.Revision != 1 {
		t.Fatalf("identical republish = id %q revision %d; want id %q revision 1", reused.ID, reused.Revision, first.ID)
	}
	if got := creditSourceTestCounts(t, db, entity); got != before {
		t.Fatalf("identical republish wrote rows: %+v -> %+v", before, got)
	}
	headAfter := creditSourceTestHead(t, db, entity, provider)
	if headAfter.ID != headBefore.ID || headAfter.Revision != 1 || headAfter.ObservedAt != headBefore.ObservedAt {
		t.Fatalf("head changed on reuse: %+v -> %+v", headBefore, headAfter)
	}
	if len(headAfter.Members) != len(headBefore.Members) {
		t.Fatalf("head membership changed on reuse: %d -> %d", len(headBefore.Members), len(headAfter.Members))
	}
	for i := range headAfter.Members {
		if headAfter.Members[i] != headBefore.Members[i] {
			t.Fatalf("member %d changed on reuse: %+v -> %+v", i, headBefore.Members[i], headAfter.Members[i])
		}
	}

	// A stale expectation must still be refused before any reuse is considered.
	if _, err = publishCreditSourceTest(t, db, entity, provider, "record-a", "2026-10-03T00:00:00Z", 0, base); !errors.Is(err, ErrCreditSourceStale) {
		t.Fatalf("expectedRevision 0 after head: %v; want ErrCreditSourceStale", err)
	}
	if got := creditSourceTestCounts(t, db, entity); got != before {
		t.Fatalf("stale refusal wrote rows: %+v -> %+v", before, got)
	}

	cases := []struct {
		name   string
		record string
		tuples []AcceptedCreditTuple
	}{
		{
			name:   "different record reference",
			record: "record-b",
			tuples: []AcceptedCreditTuple{
				creditSourceTestTuple("record-b", "Ann", 0),
				creditSourceTestTuple("record-b", "Bob", 1),
			},
		},
		{
			name:   "one tuple changed",
			record: "record-a",
			tuples: []AcceptedCreditTuple{
				creditSourceTestTuple("record-a", "Ann", 0),
				creditSourceTestTuple("record-a", "Cid", 1),
			},
		},
		{
			name:   "two tuples swapped",
			record: "record-a",
			tuples: []AcceptedCreditTuple{
				creditSourceTestTuple("record-a", "Bob", 1),
				creditSourceTestTuple("record-a", "Ann", 0),
			},
		},
		{
			name:   "one tuple removed",
			record: "record-a",
			tuples: []AcceptedCreditTuple{
				creditSourceTestTuple("record-a", "Ann", 0),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := cc1OpenDB(t, "credit-reuse-variant.sqlite")
			entity := creditSourceTestEntity(t, db, "file:reuse.mkv")
			if _, err := publishCreditSourceTest(t, db, entity, provider, "record-a", "2026-10-01T00:00:00Z", 0, base); err != nil {
				t.Fatal(err)
			}
			before := creditSourceTestCounts(t, db, entity)
			previous := creditSourceTestHead(t, db, entity, provider)
			next, err := publishCreditSourceTest(t, db, entity, provider, tc.record, "2026-10-02T00:00:00Z", 1, tc.tuples)
			if err != nil {
				t.Fatal(err)
			}
			if next.Revision != 2 || next.ID == "" || next.ID == previous.ID {
				t.Fatalf("variant publication = id %q revision %d", next.ID, next.Revision)
			}
			if got := creditSourceTestCounts(t, db, entity); got.publications != before.publications+1 {
				t.Fatalf("variant publications %d; want %d", got.publications, before.publications+1)
			}
			head := creditSourceTestHead(t, db, entity, provider)
			if head.ID != next.ID || head.Revision != 2 || head.RecordReference != tc.record {
				t.Fatalf("variant head = %+v", head)
			}
			if len(head.Members) != len(tc.tuples) {
				t.Fatalf("variant members %d; want %d", len(head.Members), len(tc.tuples))
			}
			for i, member := range head.Members {
				if member.Position != i || member.Original != tc.tuples[i] {
					t.Fatalf("variant member %d = %+v; want %+v", i, member.Original, tc.tuples[i])
				}
			}
		})
	}

	t.Run("empty tuple list", func(t *testing.T) {
		db := cc1OpenDB(t, "credit-reuse-empty.sqlite")
		entity := creditSourceTestEntity(t, db, "file:empty.mkv")
		first, err := publishCreditSourceTest(t, db, entity, provider, "record-empty", "2026-10-01T00:00:00Z", 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if first.Revision != 1 || len(first.Members) != 0 {
			t.Fatalf("empty publication = %+v", first)
		}
		before := creditSourceTestCounts(t, db, entity)
		reused, err := publishCreditSourceTest(t, db, entity, provider, "record-empty", "2026-10-02T00:00:00Z", 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if reused.ID != first.ID || reused.Revision != 1 {
			t.Fatalf("identical empty republish = id %q revision %d", reused.ID, reused.Revision)
		}
		if got := creditSourceTestCounts(t, db, entity); got != before {
			t.Fatalf("identical empty republish wrote rows: %+v -> %+v", before, got)
		}
		head := creditSourceTestHead(t, db, entity, provider)
		if head.ID != first.ID || head.Revision != 1 || len(head.Members) != 0 {
			t.Fatalf("empty head = %+v", head)
		}
	})
}

// TestPublishCreditSourceReuseKeepsDuplicateTuplesStable: duplicate accepted
// tuples are ambiguous occurrences with distinct rows. Reusing the head must
// leave exactly those member rows in place.
func TestPublishCreditSourceReuseKeepsDuplicateTuplesStable(t *testing.T) {
	const provider = "screen"
	db := cc1OpenDB(t, "credit-reuse-duplicates.sqlite")
	entity := creditSourceTestEntity(t, db, "file:duplicates.mkv")
	tuple := creditSourceTestTuple("record-dup", "Ann", 0)
	base := []AcceptedCreditTuple{tuple, tuple}

	first, err := publishCreditSourceTest(t, db, entity, provider, "record-dup", "2026-10-01T00:00:00Z", 0, base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || len(first.Members) != 2 {
		t.Fatalf("first publication = %+v", first)
	}
	for i, member := range first.Members {
		if member.Status != CreditSourceAmbiguous {
			t.Fatalf("duplicate member %d status = %q; want ambiguous", i, member.Status)
		}
	}
	if first.Members[0].SourceRowID == first.Members[1].SourceRowID {
		t.Fatal("duplicate occurrences share one source row")
	}
	before := creditSourceTestCounts(t, db, entity)
	headBefore := creditSourceTestHead(t, db, entity, provider)

	reused, err := publishCreditSourceTest(t, db, entity, provider, "record-dup", "2026-10-02T00:00:00Z", 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != first.ID || reused.Revision != 1 {
		t.Fatalf("identical duplicate republish = id %q revision %d", reused.ID, reused.Revision)
	}
	if got := creditSourceTestCounts(t, db, entity); got != before {
		t.Fatalf("identical duplicate republish wrote rows: %+v -> %+v", before, got)
	}
	headAfter := creditSourceTestHead(t, db, entity, provider)
	if len(headAfter.Members) != len(headBefore.Members) {
		t.Fatalf("duplicate membership changed: %d -> %d", len(headBefore.Members), len(headAfter.Members))
	}
	for i := range headAfter.Members {
		if headAfter.Members[i] != headBefore.Members[i] {
			t.Fatalf("duplicate member %d changed: %+v -> %+v", i, headBefore.Members[i], headAfter.Members[i])
		}
	}
	if headAfter.Members[0].SourceRowID != first.Members[0].SourceRowID || headAfter.Members[1].SourceRowID != first.Members[1].SourceRowID {
		t.Fatal("duplicate member rows were rebound on reuse")
	}
}
