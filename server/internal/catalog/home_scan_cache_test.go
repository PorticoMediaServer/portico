package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHomeScanCacheCoalescesAndBoundsStaleDelivery(t *testing.T) {
	var cache homeScanCache
	var builds atomic.Int32
	now := time.Unix(100, 0)
	first, reused, err := cache.load(context.Background(), "profile", now, func() ([]byte, error) {
		builds.Add(1)
		return []byte("old"), nil
	})
	if err != nil || reused || string(first) != "old" {
		t.Fatalf("initial composition: %s %v %v", first, reused, err)
	}
	never := func() ([]byte, error) { t.Error("duplicate composition"); return nil, errors.New("duplicate") }
	if data, reused, err := cache.load(context.Background(), "profile", now.Add(time.Second), never); err != nil || !reused || string(data) != "old" {
		t.Fatalf("fresh cache: %s %v %v", data, reused, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		data, reused, err := cache.load(context.Background(), "profile", now.Add(4*time.Second), func() ([]byte, error) {
			builds.Add(1)
			close(entered)
			<-release
			return []byte("new"), nil
		})
		if err == nil && (reused || string(data) != "new") {
			err = fmt.Errorf("refresher: %s reused=%v", data, reused)
		}
		done <- err
	}()
	<-entered
	data, reused, err := cache.load(context.Background(), "profile", now.Add(4*time.Second), never)
	if err != nil || !reused || string(data) != "old" {
		t.Errorf("peer should serve bounded stale value during refresh: %s %v %v", data, reused, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cache.load(cancelled, "profile", now.Add(7*time.Second), never); !errors.Is(err, context.Canceled) {
		t.Errorf("expired stale value must wait cancellably: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if data, reused, err := cache.load(context.Background(), "profile", now.Add(5*time.Second), never); err != nil || !reused || string(data) != "new" || builds.Load() != 2 {
		t.Fatalf("coalesced publication: %s %v %v builds=%d", data, reused, err, builds.Load())
	}
}

func TestHomeScanCacheBoundsMemoryAndDoesNotRetainFailures(t *testing.T) {
	var cache homeScanCache
	now := time.Now()
	value := make([]byte, 512<<10)
	for i := 0; i < 70; i++ {
		if _, _, err := cache.load(context.Background(), fmt.Sprint(i), now, func() ([]byte, error) { return append([]byte(nil), value...), nil }); err != nil {
			t.Fatal(err)
		}
	}
	if cache.bytes > homeScanCacheBytes || cache.entries["0"] != nil {
		t.Fatalf("unbounded retention or missing LRU eviction: bytes=%d", cache.bytes)
	}
	if _, _, err := cache.load(context.Background(), "large", now, func() ([]byte, error) { return make([]byte, homeScanValueBytes+1), nil }); err != nil || cache.entries["large"] != nil {
		t.Fatalf("oversized value retained: %v", err)
	}
	failed := errors.New("not ready")
	if _, _, err := cache.load(context.Background(), "failure", now, func() ([]byte, error) { return nil, failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if _, reused, err := cache.load(context.Background(), "failure", now, func() ([]byte, error) { return []byte("ready"), nil }); err != nil || reused {
		t.Fatalf("failed build blocked retry: %v %v", reused, err)
	}
}

func TestHomeScanCompositionRefreshesPolicyWithoutWaitingForScanTTL(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", t.TempDir())
	item := c.Movie(library, "/cache-film.mkv", "Cache film", 2026)
	c.Attributes(item.ID, "contentRating", "CacheAge")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('cacheage',7)`)
	c.Drain()
	s := New(c.DB)
	now := time.Now()
	s.Clock = func() time.Time { return now }
	r := homeRequestFixture("movies")
	ceiling := 12
	r.Viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &ceiling}
	if err := s.RebuildVisibilityClass(context.Background(), "movies", r.Viewer.Restrictions); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('home-cache-scan','movies','running','now')`)
	read := func() HomeRow {
		t.Helper()
		row, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 12})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first := read()
	if len(first.Entries) != 1 || first.Entries[0].ID != item.Public {
		t.Fatalf("positive control: %+v", first)
	}
	manifest, err := s.HomeManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	first.Entries[0].Title = "caller mutation"
	c.Exec(`UPDATE library_revisions SET revision=revision+1,order_revision=order_revision+1 WHERE library_id='movies'`)
	stale := read()
	if stale.Revision != first.Revision || stale.Entries[0].Title != "Cache film" {
		t.Fatalf("ordinary scan revision defeated reuse or caller mutated shared value: %+v", stale)
	}
	if cached, err := s.HomeManifest(r); err != nil || cached.Revision != manifest.Revision {
		t.Fatalf("manifest ignored scan freshness: %+v %v", cached, err)
	}
	now = now.Add(homeScanFreshness)
	if refreshed := read(); refreshed.Revision == first.Revision {
		t.Fatal("scan TTL did not publish the newer revision")
	}
	c.Exec(`UPDATE content_rating_ages SET minimum_age=18 WHERE value_key='cacheage'`)
	if hidden := read(); len(hidden.Entries) != 0 {
		t.Fatalf("classification change waited for TTL: %+v", hidden)
	}
	c.Exec(`UPDATE content_rating_ages SET minimum_age=7 WHERE value_key='cacheage'`)
	if restored := read(); len(restored.Entries) != 1 || restored.Entries[0].ID != item.Public {
		t.Fatalf("7+ classification did not rebuild immediately: %+v", restored)
	}
	c.Exec(`UPDATE jobs SET status='complete' WHERE id='home-cache-scan'`)
	before := read()
	c.Exec(`UPDATE library_revisions SET revision=revision+1,order_revision=order_revision+1 WHERE library_id='movies'`)
	if after := read(); before.Revision == after.Revision {
		t.Fatal("Home reused scan freshness after the scan finished")
	}
}

func TestCachedReadingHomeReauthorizesEveryDelivery(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	viewer.Profile = actorKey(actor)
	name := "Reading cache"
	definition := SavedViewDefinition{LibraryID: "reading", Pivot: "books", Sort: titleAscending(), Presentation: "grid"}
	made, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "cache-reading-view", Kind: "view", Name: &name, Definition: &definition}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('reading-cache-scan','reading','running','now')`)
	rowID := homeViewPrefix + made.ResourceID
	request := HomeRequest{Viewer: viewer, ServerID: "server", RowOrder: []string{rowID}, ReadingPersonalAuthorize: func(*sql.Tx, string) error { return nil }}
	row, err := s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2})
	if err != nil || len(row.Entries) != 2 {
		t.Fatalf("Reading positive control: %+v %v", row, err)
	}
	denied := errors.New("personal grant revoked")
	request.ReadingPersonalAuthorize = func(*sql.Tx, string) error { return denied }
	if _, err = s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, denied) {
		t.Fatalf("cached personal projection bypassed current authorization: %v", err)
	}
}
