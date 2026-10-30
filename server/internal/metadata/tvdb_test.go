package metadata

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type tvdbFixture struct {
	search func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error)
	pages  func(context.Context, int64, metadataprovider.EpisodeOrder, int) (metadataprovider.EpisodePage, error)
}

func (f tvdbFixture) SearchSeries(c context.Context, t string, y int) ([]metadataprovider.SeriesCandidate, error) {
	return f.search(c, t, y)
}
func (f tvdbFixture) Episodes(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
	return f.pages(c, id, o, p)
}
func tvdbDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	lib := c.Library("lib", "TV", "tv", "/media")
	show := c.Show(lib, "Fixture Show", 2020)
	season := c.Season(show, 1)
	for _, fixture := range []struct {
		name, title, identity string
		number                int
	}{
		{"one", "Episode 1", "parsed", 1},
		{"two", "Episode 2", "parsed", 2},
		{"manual", "My manual title", "manual", 3},
	} {
		item := c.Episode(show, season, fixture.number, "/media/"+fixture.name+".mkv")
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetFactsTx(ctx, tx, item.ID, map[string]any{
				"title": fixture.title, "local_identity_status": fixture.identity, "ordering_basis": "unspecified",
			})
		})
	}
	c.Drain()
	return db, path
}

func tvdbNames(t *testing.T, db *sql.DB) catalogtest.Names {
	t.Helper()
	names := catalogtest.Names{}
	for _, fixture := range []struct {
		name  string
		kind  int
		title string
	}{
		{"show", 2, "Fixture Show"}, {"season", 3, "Season 1"},
		{"one", 4, "Episode 1"}, {"two", 4, "Episode 2"}, {"manual", 4, "My manual title"},
	} {
		var item catalogtest.Item
		if err := db.QueryRow(`SELECT id,pid(public_id) FROM catalog_entities WHERE kind=? AND title=? ORDER BY id LIMIT 1`, fixture.kind, fixture.title).Scan(&item.ID, &item.Public); err != nil {
			t.Fatalf("fixture %q: %v", fixture.name, err)
		}
		names[fixture.name] = item
	}
	return names
}
func tvdbExact(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
	return []metadataprovider.SeriesCandidate{{ID: "42", Name: "Fixture Show", Year: "2020", Type: "series", Overview: "Observed series overview"}}, nil
}
func ep(id int64, n int) metadataprovider.Episode {
	season := 1
	return metadataprovider.Episode{ID: id, SeriesID: 42, Name: "Observed episode", Overview: "Observed episode overview", SeasonNumber: &season, Number: &n}
}
func tvdbSelect(t *testing.T, s *Service, show string) {
	t.Helper()
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.TVDBState(show)
	if e != nil || state.Status != "needs_order" || state.ProviderID != 42 || state.Order != "" {
		t.Fatal(state, e)
	}
	if e = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "official"}, nil); e != nil {
		t.Fatal(e)
	}
}
func TestTVDBDurablePageStagingRestartAndManualPreservation(t *testing.T) {
	db, path := tvdbDB(t)
	defer func() { db.Close() }()
	names := tvdbNames(t, db)
	show := names["show"].Public
	s := New(db, "")
	next := 1
	fixture := tvdbFixture{search: tvdbExact, pages: func(ctx context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		page := metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p}
		if p == 0 {
			page.Episodes = []metadataprovider.Episode{ep(100, 1)}
			page.NextPage = &next
		} else {
			page.Episodes = []metadataprovider.Episode{ep(101, 2), ep(102, 3)}
		}
		return page, nil
	}}
	s.tvdb = fixture
	tvdbSelect(t, s, show)
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	var title string
	db.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, names["one"].ID).Scan(&title)
	if title != "Episode 1" {
		t.Fatal("partial page prematurely applied")
	}
	db.Close()
	var e error
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s = New(db, "")
	names = tvdbNames(t, db)
	s.tvdb = fixture
	state, e := s.TVDBState(show)
	if e != nil || state.Page != 1 || state.Status != "pending_episodes" {
		t.Fatal(state, e)
	}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.TVDBState(show)
	if e != nil || state.Status != "complete" {
		t.Fatal(state, e)
	}
	catalogtest.New(t, db).Drain()
	detail, e := catalog.New(db).Detail(catalog.Viewer{Profile: "profile", Fence: "f", Libraries: []string{"lib"}}, "server", names["one"].Public, false)
	if e != nil || detail.Item.Title != "Observed episode" || detail.Item.Overview != "Observed episode overview" || detail.Metadata.Status != "available" || len(detail.Metadata.Ratings) != 0 || detail.Item.Episode.OrderingBasis != "official" || detail.Item.Episode.ProviderMatchStatus != "matched" {
		t.Fatal(detail, e)
	}
	manual, e := catalog.New(db).Get("profile", names["manual"].Public)
	if e != nil || manual.Title != "My manual title" || manual.Episode.LocalIdentityStatus != "manual" || manual.Episode.ProviderMatchStatus != "manual_preserved" {
		t.Fatal(manual, e)
	}
	var source string
	db.QueryRow(`SELECT source_url FROM metadata_details WHERE item_id=? AND provider='tvdb'`, names["one"].ID).Scan(&source)
	if source != "https://thetvdb.com/dereferrer/episode/100" {
		t.Fatal(source)
	}
}
func TestTVDBAmbiguousSeriesAndCrossPageNumberingNeverGuessed(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"].Public
	s := New(db, "")
	s.tvdb = tvdbFixture{search: func(c context.Context, t string, y int) ([]metadataprovider.SeriesCandidate, error) {
		rows, _ := tvdbExact(c, t, y)
		other := rows[0]
		other.ID = "43"
		return append(rows, other), nil
	}}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.TVDBState(show)
	if e != nil || state.Status != "needs_selection" || state.ProviderID != 0 || len(state.Candidates) != 2 {
		t.Fatal(state, e)
	}
	if e = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 999, Order: "official"}, nil); e == nil {
		t.Fatal("unobserved match selected")
	}
	if e = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "official"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 43, Order: "dvd"}, nil); !errors.Is(e, ErrTVDBConflict) {
		t.Fatal("stale selection overwrote choice", e)
	}
	next := 1
	s.tvdb = tvdbFixture{pages: func(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		out := metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p, Episodes: []metadataprovider.Episode{ep(int64(100+p), 1)}}
		if p == 0 {
			out.NextPage = &next
		}
		return out, nil
	}}
	for i := 0; i < 3; i++ {
		if e = s.TVDBStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	var status, title string
	db.QueryRow(`SELECT p.status,e.title FROM tvdb_episode_links p JOIN catalog_entities e ON e.id=p.item_id WHERE p.item_id=?`, names["one"].ID).Scan(&status, &title)
	if status != "ambiguous" || title != "Episode 1" {
		t.Fatal(status, title)
	}
}
func TestTVDB429BackoffCancellationAndLeaseRecovery(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"].Public
	s := New(db, "")
	calls := 0
	s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
		calls++
		return nil, &metadataprovider.Error{Provider: "tvdb", Status: 429, Code: "request_rejected", RetryAfter: time.Hour}
	}}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.TVDBState(show)
	if e != nil || state.Attempts != 1 || state.Status != "pending_search" {
		t.Fatal(state, e)
	}
	next, e := time.Parse(time.RFC3339, state.NextAttempt)
	if e != nil || time.Until(next) < 59*time.Minute {
		t.Fatal(state.NextAttempt, e)
	}
	if e = s.TVDBStep(context.Background()); e != nil || calls != 1 {
		t.Fatal("ignored durable backoff", e, calls)
	}
	if _, e = db.Exec(`UPDATE tvdb_jobs SET next_attempt='',attempts=0;DELETE FROM metadata_provider_cooldowns`); e != nil {
		t.Fatal(e)
	}
	s.tvdb = tvdbFixture{search: func(ctx context.Context, _ string, _ int) ([]metadataprovider.SeriesCandidate, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if e = s.TVDBStep(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	state, e = s.TVDBState(show)
	if e != nil || state.Attempts != 0 || state.Status != "pending_search" {
		t.Fatal("cancel consumed attempt", state, e)
	}
	if _, e = db.Exec(`UPDATE tvdb_jobs SET lease='abandoned',lease_until='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	s.tvdb = tvdbFixture{search: tvdbExact}
	if e = s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.TVDBState(show)
	if e != nil || state.Status != "needs_order" {
		t.Fatal("abandoned lease not recovered", state, e)
	}
}
func TestTVDBNoYearUnavailableAndSanitizedErrors(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"].Public
	if _, e := db.Exec(`UPDATE catalog_entities SET year=0 WHERE id=?`, names["show"].ID); e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	s.tvdb = tvdbFixture{search: tvdbExact}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.TVDBState(show)
	if e != nil || state.Status != "needs_selection" {
		t.Fatal(state, e)
	}
	if e = s.RetryTVDB(show, state.Revision, nil); e != nil {
		t.Fatal(e)
	}
	s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
		return nil, errors.New("network with secret-key-or-url")
	}}
	for i := 0; i < 4; i++ {
		db.Exec(`UPDATE tvdb_jobs SET next_attempt=''`)
		if e = s.TVDBStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	state, e = s.TVDBState(show)
	if e != nil || state.Status != "unavailable" || state.Attempts != 4 || strings.Contains(state.Error, "secret") {
		t.Fatal(state, e)
	}
}
func TestTVDBExplicitAbsolutePreservesUnassignedHierarchy(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"]
	c := catalogtest.New(t, db)
	if _, e := db.Exec(`UPDATE libraries SET kind='anime' WHERE id='lib'`); e != nil {
		t.Fatal(e)
	}
	names["absolute"] = c.Episode(show, names["season"], 8, "/media/absolute.mkv")
	tvdbUpdateEpisodeFacts(t, db, names["absolute"], map[string]any{"season_id": nil, "numbering": "absolute", "local_identity_status": "parsed"})
	// Match the supervised projector before asking the provider to acquire a
	// candidate for these changed source facts; keep its readiness fence intact.
	settleMetadataCatalogue(t, db)
	s := New(db, "")
	number := 8
	episode := ep(108, 8)
	episode.AbsoluteNumber = &number
	s.tvdb = tvdbFixture{search: tvdbExact, pages: func(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		return metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p, Episodes: []metadataprovider.Episode{episode}}, nil
	}}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, _ := s.TVDBState(show.Public)
	if e := s.SelectTVDB(show.Public, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "absolute"}, nil); e != nil {
		t.Fatal(e)
	}
	var e error
	for i := 0; i < 2; i++ {
		if e = s.TVDBStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	c.Drain()
	item, e := catalog.New(db).Get("p", names["absolute"].Public)
	if e != nil || item.Episode.SeasonID != "" || item.Episode.Numbering != "absolute" || item.Episode.Number != 8 || item.Episode.ProviderMatchStatus != "matched" {
		t.Fatal(item, e)
	}
	other, e := catalog.New(db).Get("p", names["one"].Public)
	if e != nil || other.Episode.ProviderMatchStatus != "order_mismatch" || other.Title != "Episode 1" {
		t.Fatal(other, e)
	}
}
func TestTVDBInFlightPageCannotOverwriteOwnerReselection(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"].Public
	s := New(db, "")
	entered, release := make(chan struct{}), make(chan struct{})
	s.tvdb = tvdbFixture{search: tvdbExact, pages: func(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		close(entered)
		<-release
		return metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p, Episodes: []metadataprovider.Episode{ep(100, 1)}}, nil
	}}
	tvdbSelect(t, s, show)
	done := make(chan error, 1)
	go func() { done <- s.TVDBStep(context.Background()) }()
	<-entered
	state, e := s.TVDBState(show)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "dvd"}, nil); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	state, e = s.TVDBState(show)
	if e != nil || state.Order != "dvd" || state.Status != "pending_episodes" {
		t.Fatal(state, e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM tvdb_episode_evidence`).Scan(&n)
	if n != 0 {
		t.Fatal("stale page evidence committed", n)
	}
}
func TestTVDBDuplicateAcrossPagesStopsWithoutPartialProjection(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show := names["show"].Public
	s := New(db, "")
	next := 1
	s.tvdb = tvdbFixture{search: tvdbExact, pages: func(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		out := metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p, Episodes: []metadataprovider.Episode{ep(100, 1)}}
		if p == 0 {
			out.NextPage = &next
		}
		return out, nil
	}}
	tvdbSelect(t, s, show)
	for i := 0; i < 2; i++ {
		if e := s.TVDBStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	state, e := s.TVDBState(show)
	if e != nil || state.Status != "unresolved" || state.Error != "invalid_episodes" {
		t.Fatal(state, e)
	}
	var title string
	db.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, names["one"].ID).Scan(&title)
	if title != "Episode 1" {
		t.Fatal("partial duplicate evidence projected", title)
	}
}
func TestTVDBProviderCooldownBlocksOtherShowsAfterRestart(t *testing.T) {
	db, path := tvdbDB(t)
	defer func() { db.Close() }()
	c := catalogtest.New(t, db)
	s := New(db, "")
	calls := 0
	s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
		calls++
		return nil, &metadataprovider.Error{Provider: "tvdb", Status: 503, Code: "request_rejected", RetryAfter: time.Hour}
	}}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	c.Show(c.Handle("lib"), "Another", 2020)
	c.Drain()
	db.Close()
	var e error
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s = New(db, "")
	s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
		t.Fatal("durable provider cooldown bypassed")
		return nil, nil
	}}
	if e = s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestTVDBLateEpisodeGetsExistingCompletedEvidence(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	show, season := names["show"], names["season"]
	s := New(db, "")
	s.tvdb = tvdbFixture{search: tvdbExact, pages: func(c context.Context, id int64, o metadataprovider.EpisodeOrder, p int) (metadataprovider.EpisodePage, error) {
		return metadataprovider.EpisodePage{SeriesID: id, Order: o, Page: p, Episodes: []metadataprovider.Episode{ep(104, 4)}}, nil
	}}
	tvdbSelect(t, s, show.Public)
	for i := 0; i < 2; i++ {
		if e := s.TVDBStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	c := catalogtest.New(t, db)
	names["late"] = c.Entity(compactcatalog.Entity{Library: c.Handle("lib"), Kind: compactcatalog.Episode, Parent: season.ID, Key: "tvdb-fixture:late", Title: "Episode 4"}, map[string]any{
		"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": 4,
		"local_identity_status": "parsed", "ordering_basis": "unspecified",
	})
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	item, e := catalog.New(db).Get("p", names["late"].Public)
	if e != nil || item.Episode.ProviderMatchStatus != "matched" || item.Title != "Observed episode" {
		t.Fatal(item, e)
	}
}
