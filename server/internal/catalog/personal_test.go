package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func phase34OpenPersonalFixture(t *testing.T, path string) (*sql.DB, *Service, catalogtest.Item) {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("a", "A", "movie", "/a")
	item := c.Movie(library, "/a/i.mkv", "Item", 2020)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, item.Asset, false)
	})
	c.Drain()
	return db, New(db), item
}

func phase34PersonalViewerForItem(t *testing.T, s *Service, profile, fence, item string) Viewer {
	t.Helper()
	library, err := s.LibraryForItem(item)
	if err != nil {
		t.Fatal(err)
	}
	return Viewer{Profile: profile, Fence: fence, Libraries: []string{library}}
}

func TestPersonalSetReceiptsIsolationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, s, item := phase34OpenPersonalFixture(t, path)
	yes, no := true, false
	m := PersonalMutation{OperationID: "watch", ExpectedRevision: 0, Watchlisted: &yes}
	first, e := s.SetPersonal("a", "p", item.Public, m, nil)
	if e != nil || first.Revision != 1 || !first.Watchlisted {
		t.Fatal(first, e)
	}
	favorite, e := s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "favorite", ExpectedRevision: 1, Favorite: &yes}, nil)
	if e != nil || favorite.Revision != 2 || !favorite.Favorite {
		t.Fatal(favorite, e)
	}
	retry, e := s.SetPersonal("a", "p", item.Public, m, nil)
	if e != nil || retry.Revision != 1 || retry.Favorite {
		t.Fatal("replay reapplied or changed original receipt", retry, e)
	}
	changed := m
	changed.Watchlisted = &no
	if _, e = s.SetPersonal("a", "p", item.Public, changed, nil); !errors.Is(e, ErrOperationConflict) {
		t.Fatal("operation collision", e)
	}
	if _, e = s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "stale", ExpectedRevision: 1, Favorite: &no}, nil); !errors.Is(e, ErrPersonalConflict) {
		t.Fatal("stale state overwritten", e)
	}
	rated, e := s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "rating", ExpectedRevision: 2, Rating: json.RawMessage(`9`)}, nil)
	if e != nil || rated.Revision != 3 || *rated.Rating != 9 {
		t.Fatal(rated, e)
	}
	if _, e = s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "bad", ExpectedRevision: 3, Rating: json.RawMessage(`4.2`)}, nil); e == nil {
		t.Fatal("invalid rating accepted")
	}
	if _, e = s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "denied", ExpectedRevision: 3, Favorite: &no}, func(*sql.Tx) error { return errors.New("denied") }); e == nil {
		t.Fatal("authorization callback ignored")
	}
	if _, e = s.SetPersonal("a", "p", item.Public, m, func(*sql.Tx) error { return errors.New("revoked") }); e == nil {
		t.Fatal("idempotent receipt bypassed current authorization")
	}
	other, e := readPersonal(db, "other", item.Public)
	if e != nil || other.Revision != 0 || other.Watchlisted || other.Rating != nil {
		t.Fatal("profile leaked", other, e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = New(db)
	persisted, e := readPersonal(db, "p", item.Public)
	if e != nil || persisted.Revision != 3 || *persisted.Rating != 9 {
		t.Fatal("state lost on restart", persisted, e)
	}
	retry, e = s.SetPersonal("a", "p", item.Public, m, nil)
	if e != nil || retry.Revision != 1 {
		t.Fatal("receipt lost on restart", retry, e)
	}
	cleared, e := s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "clear", ExpectedRevision: 3, Rating: json.RawMessage(`null`)}, nil)
	if e != nil || cleared.Rating != nil || cleared.Revision != 4 {
		t.Fatal(cleared, e)
	}
	detail, e := s.Detail(phase34PersonalViewerForItem(t, s, "p", "f", item.Public), "server", item.Public, false)
	if e != nil || detail.Metadata.Status != "unavailable" || len(detail.Metadata.Ratings) != 0 || len(detail.Metadata.Credits) != 0 || len(detail.Actions) != 4 {
		t.Fatal("fabricated sparse detail", detail, e)
	}
}

func TestConcurrentPersonalCASHasOneWinner(t *testing.T) {
	db, s, item := phase34OpenPersonalFixture(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	yes := true
	for _, op := range []string{"one", "two"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			_, e := s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: op, ExpectedRevision: 0, Favorite: &yes}, nil)
			results <- e
		}(op)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrPersonalConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}

func TestPersonalReceiptExpiryCASAndBoundedCleanup(t *testing.T) {
	db, s, item := phase34OpenPersonalFixture(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	yes := true
	m := PersonalMutation{OperationID: "old", ExpectedRevision: 0, Favorite: &yes}
	if _, e := s.SetPersonal("a", "p", item.Public, m, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE personal_receipts SET created_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetPersonal("a", "p", item.Public, m, nil); !errors.Is(e, ErrOperationExpired) {
		t.Fatal("expired mutation reapplied", e)
	}
	state, e := readPersonal(db, "p", item.Public)
	if e != nil || state.Revision != 1 || !state.Favorite {
		t.Fatal(state, e)
	}
	if e = s.CleanupPersonalReceipts(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1005) INSERT INTO personal_receipts SELECT 'a','p',?,'old-'||x,'h','{}','2000-01-01T00:00:00Z' FROM n`, item.ID); e != nil {
		t.Fatal(e)
	}
	dbwork.ResetPlans()
	if e = s.CleanupPersonalReceipts(); e != nil {
		t.Fatal(e)
	}
	// The whole expired backlog goes, in transactions that each stay inside a
	// maintenance write plan.
	var remaining int
	if e = db.QueryRow(`SELECT count(*) FROM personal_receipts`).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal("expired receipts retained", remaining, e)
	}
	if plans := dbwork.Plans(); plans.Overruns != 0 || plans.Largest.Rows > receiptCleanupBatch {
		t.Fatal("cleanup batch unbounded", plans.Largest)
	}
	if e = s.CleanupPersonalReceipts(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO personal_receipts SELECT 'a','p',?,'cap-'||x,'h','{}','2999-01-01T00:00:00Z' FROM n`, item.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "capacity", ExpectedRevision: 1, Watchlisted: &yes}, nil); e != nil {
		t.Fatal("per-item receipt history must not exhaust operation quota", e)
	}
}

func TestPersonalRatingIntegerScale(t *testing.T) {
	for _, value := range []string{"0", "1", "9", "10", "null"} {
		_, _, err := mutationField(&PersonalMutation{Rating: json.RawMessage(value)})
		if err != nil {
			t.Errorf("rating %s: %v", value, err)
		}
	}
	for _, value := range []string{"-1", "11", "4.5", "0.5", `"9"`} {
		_, _, err := mutationField(&PersonalMutation{Rating: json.RawMessage(value)})
		if err == nil {
			t.Errorf("accepted rating %s", value)
		}
	}
}
