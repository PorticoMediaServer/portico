package metadata

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// A work's similar titles and external ids follow its accepted identity:
// written with it, replaced whole on refresh, gone when it is cleared or an
// owner moves the work to another title.

func screenLinkRows(t *testing.T, db *sql.DB, entity int64) (similar, external string) {
	t.Helper()
	read := func(query string) string {
		rows, err := db.Query(query, entity)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, " ")
	}
	return read(`SELECT source||'#'||rank||'='||provider||':'||provider_kind||':'||provider_id FROM catalog_similar WHERE entity_id=? ORDER BY source,rank`),
		read(`SELECT provider||':'||provider_kind||':'||provider_id FROM catalog_external_ids WHERE entity_id=? ORDER BY provider,provider_kind,provider_id`)
}

func screenRequeue(t *testing.T, db *sql.DB, kind string, entity int64) {
	t.Helper()
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind=? AND target_id=?;
DELETE FROM metadata_document_freshness`, kind, entity)
}

func TestScreenLinksFollowTheAcceptedMovieIdentity(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	p.record.Crosswalk = []metadataprovider.ScreenID{{Provider: "tvdb", Type: "movie", ID: "71"}, {Provider: "imdb", Type: "title", ID: "tt0113277"}}
	p.record.Similar = []metadataprovider.ScreenID{{Provider: "tmdb", Type: "movie", ID: "949"}, {Provider: "tmdb", Type: "show", ID: "7"}}
	screenConfirm(t, s)
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	similar, external := screenLinkRows(t, db, item.ID)
	if similar != "tmdb#1=tmdb:movie:949 tmdb#2=tmdb:tv:7" || external != "imdb:title:tt0113277 tmdb:movie:42 tvdb:movie:71" {
		t.Fatalf("published links: %q / %q", similar, external)
	}

	// A refresh of the same identity replaces both sets whole: a dropped
	// crosswalk and a reordered, shortened list leave nothing behind.
	p.record.Crosswalk = []metadataprovider.ScreenID{{Provider: "imdb", Type: "title", ID: "tt0113277"}}
	p.record.Similar = []metadataprovider.ScreenID{{Provider: "tmdb", Type: "show", ID: "7"}}
	screenRequeue(t, db, "item", item.ID)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	similar, external = screenLinkRows(t, db, item.ID)
	if similar != "tmdb#1=tmdb:tv:7" || external != "imdb:title:tt0113277 tmdb:movie:42" {
		t.Fatalf("refreshed links: %q / %q", similar, external)
	}
	// Other sources' lists are not the screen publication's to replace.
	publicationExec(t, db, `INSERT INTO catalog_similar VALUES(?,'portico-dataset',1,'tmdb','movie','5')`, item.ID)

	// An explicit unmatch retires the identity and its links with it.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = restoreScreenIdentity(ctx, tx, RepairTarget{Kind: "item", ID: item.Public}, &RepairIdentity{}, MBActor{Authority: "owner", AccountID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	similar, external = screenLinkRows(t, db, item.ID)
	if similar != "portico-dataset#1=tmdb:movie:5" || external != "" {
		t.Fatalf("unmatched links: %q / %q", similar, external)
	}
}

func TestScreenLinksNeverComeFromAnUnacceptedCandidate(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	// A title nobody would accept automatically: candidates are stored for the
	// owner, and nothing about them reaches the links.
	p.record.Title = "Something Else Entirely"
	p.record.Similar = []metadataprovider.ScreenID{{Provider: "tmdb", Type: "movie", ID: "949"}}
	p.record.Crosswalk = []metadataprovider.ScreenID{{Provider: "imdb", Type: "title", ID: "tt0113277"}}
	screenConfirm(t, s)
	if e := s.ScreenStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID); got != "" {
		t.Fatalf("fixture was accepted: %q", got)
	}
	if similar, external := screenLinkRows(t, db, item.ID); similar != "" || external != "" {
		t.Fatalf("unaccepted candidate wrote links: %q / %q", similar, external)
	}
}

func TestScreenLinksMoveWithAShowReidentification(t *testing.T) {
	s, db, show, tmdb, anilist := screenAnimeShowFixture(t)
	tmdb.record.Crosswalk = []metadataprovider.ScreenID{{Provider: "tvdb", Type: "show", ID: "81189"}, {Provider: "imdb", Type: "title", ID: "tt0903747"}}
	tmdb.record.Similar = []metadataprovider.ScreenID{{Provider: "tmdb", Type: "show", ID: "1396"}}
	anilist.record.Similar = []metadataprovider.ScreenID{{Provider: "anilist", Type: "anime", ID: "20"}, {Provider: "anilist", Type: "anime", ID: "21"}}
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	similar, external := screenLinkRows(t, db, show.ID)
	if similar != "tmdb#1=tmdb:tv:1396" || external != "imdb:title:tt0903747 tmdb:tv:7 tvdb:series:81189" {
		t.Fatalf("show links: %q / %q", similar, external)
	}
	if n := publicationScalar(t, db, `SELECT (SELECT count(*) FROM catalog_similar WHERE entity_id<>?)+(SELECT count(*) FROM catalog_external_ids WHERE entity_id<>?)`, show.ID, show.ID); n != "0" {
		t.Fatalf("links written for something other than the work: %s", n)
	}

	// A library has one provider: the owner switches it, then matches the show again.
	discoveryConfirmLibrary(t, s, "anime", []string{"anilist"}, "US")
	state, e := s.ScreenState(ctx, "show", show.Public)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SearchScreen(ctx, "show", show.Public, ScreenSearch{ExpectedRevision: state.Revision, Provider: "anilist", Query: "Series"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	// A review search publishes candidates only: the accepted links stand.
	if similar, _ = screenLinkRows(t, db, show.ID); similar != "tmdb#1=tmdb:tv:1396" {
		t.Fatalf("a review search disturbed accepted links: %q", similar)
	}
	state, e = s.ScreenState(ctx, "show", show.Public)
	if e != nil {
		t.Fatal(e)
	}
	key := ""
	for _, c := range state.Candidates {
		if c.Provider == "anilist" {
			key = c.Key
		}
	}
	if key == "" {
		t.Fatal("no anilist candidate published")
	}
	if e = s.SelectScreen(ctx, "show", show.Public, ScreenSelection{ExpectedRevision: state.Revision, CandidateKey: key, Order: "seasonal"}, nil); e != nil {
		t.Fatal(e)
	}
	// The owner rejected the old title: its links go with the decision, before
	// the new identity is fetched.
	if similar, external = screenLinkRows(t, db, show.ID); similar != "" || external != "" {
		t.Fatalf("rejected identity's links survived the selection: %q / %q", similar, external)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	similar, external = screenLinkRows(t, db, show.ID)
	if similar != "anilist#1=anilist:anime:20 anilist#2=anilist:anime:21" || external != "anilist:anime:9" {
		t.Fatalf("reidentified links: %q / %q", similar, external)
	}
}

func TestScreenLinksRetireWhenTheOwnerPicksAnotherTVDBShow(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	show := tvdbNames(t, db)["show"]
	s := New(db, "")
	s.tvdb = tvdbFixture{search: func(c context.Context, title string, year int) ([]metadataprovider.SeriesCandidate, error) {
		rows, _ := tvdbExact(c, title, year)
		other := rows[0]
		other.ID = "43"
		return append(rows, other), nil
	}}
	if e := s.TVDBStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET provider='tvdb',provider_type='show',provider_id='42' WHERE target_kind='show' AND target_id=?;
INSERT INTO catalog_similar VALUES(?,'tmdb',1,'tmdb','tv','1396');
INSERT INTO catalog_external_ids VALUES(?,'tvdb','series','42')`, show.ID, show.ID, show.ID)
	state, e := s.TVDBState(show.Public)
	if e != nil {
		t.Fatal(e)
	}
	// The identity on record, chosen again with an order: its links stand.
	if e = s.SelectTVDB(show.Public, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "official"}, nil); e != nil {
		t.Fatal(e)
	}
	if similar, external := screenLinkRows(t, db, show.ID); similar != "tmdb#1=tmdb:tv:1396" || external != "tvdb:series:42" {
		t.Fatalf("re-choosing the same show dropped its links: %q / %q", similar, external)
	}
	if state, e = s.TVDBState(show.Public); e != nil {
		t.Fatal(e)
	}
	if e = s.SelectTVDB(show.Public, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 43, Order: "official"}, nil); e != nil {
		t.Fatal(e)
	}
	if similar, external := screenLinkRows(t, db, show.ID); similar != "" || external != "" {
		t.Fatalf("another show's selection kept the old links: %q / %q", similar, external)
	}
}
