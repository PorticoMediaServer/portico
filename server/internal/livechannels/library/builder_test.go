package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// fixtureLibrary is a small, fully published catalog: a movie library (years,
// overviews, ratings) and a TV library (Malcolm in the Middle and another show),
// with assets, playback origins and whole-source episode boundaries, so every
// title is schedulable.
type fixtureLibrary struct {
	db        *sql.DB
	now       time.Time
	ids       map[string]string
	entityIDs map[string]int64
}

type fixtureMovie struct {
	id, title, overview string
	year                int
	rating              float64 // 0 = unrated
	minutes             int
}

func openFixture(t testing.TB) *fixtureLibrary {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixtureLibrary{db: db, now: time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC), ids: map[string]string{}, entityIDs: map[string]int64{}}
	f.exec(t, `INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/m'),('tv','TV','tv','/t')`)
	f.exec(t, `INSERT OR IGNORE INTO library_revisions(library_id,revision) VALUES('movies',1),('tv',1)`)
	f.settle(t)
	return f
}

func (f *fixtureLibrary) exec(t testing.TB, q string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func (f *fixtureLibrary) asset(t testing.TB, item string, minutes int) {
	entityID := f.entityIDs[item]
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		assetID, token, err := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: "/media/" + item + ".mkv", Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: float64(minutes * 60)})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(context.Background(), tx, entityID, assetID, compactcatalog.Link{}); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO playback_origin_assets(id) VALUES(?)`, token); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO playback_origin_items(id) VALUES(?)`, entityID); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT OR IGNORE INTO playback_origin_associations(item_id,asset_id) VALUES(?,?)`, entityID, token)
		return err
	})
	if err != nil {
		t.Fatalf("seed fixture asset for %s: %v", item, err)
	}
}

func (f *fixtureLibrary) movie(t testing.TB, m fixtureMovie) {
	public, entityID := f.entity(t, "movies", compactcatalog.Movie, m.id, m.title, m.year, f.now.Add(-time.Hour).Format(time.RFC3339), 0, map[string]any{"overview": m.overview})
	f.ids[m.id], f.entityIDs[m.id] = public, entityID
	if m.minutes == 0 {
		m.minutes = 90
	}
	f.asset(t, m.id, m.minutes)
	if m.rating > 0 {
		f.exec(t, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'tmdb',?,10,100,'','2026-01-01T00:00:00Z')`, entityID, m.rating)
	}
}

func (f *fixtureLibrary) show(t testing.TB, id, title string, seasons, episodes int) {
	public, showID := f.entity(t, "tv", compactcatalog.Show, id, title, 0, "", 0, map[string]any{"local_key": id, "overview": title + " follows a family."})
	f.ids[id], f.entityIDs[id] = public, showID
	for s := 1; s <= seasons; s++ {
		season := fmt.Sprintf("%s-s%d", id, s)
		seasonPublic, seasonID := f.entity(t, "tv", compactcatalog.Season, season, fmt.Sprintf("Season %d", s), 0, "", showID, map[string]any{"show_id": showID, "number": s})
		f.ids[season], f.entityIDs[season] = seasonPublic, seasonID
		for e := 1; e <= episodes; e++ {
			item := fmt.Sprintf("%s-s%de%d", id, s, e)
			itemPublic, itemID := f.entity(t, "tv", compactcatalog.Episode, item, fmt.Sprintf("%s S%dE%d", title, s, e), 2000, f.now.Format(time.RFC3339), seasonID, map[string]any{"show_id": showID, "season_id": seasonID, "numbering": "seasonal", "number": e, "local_identity_status": "parsed"})
			f.ids[item], f.entityIDs[item] = itemPublic, itemID
			f.asset(t, item, 22)
			f.exec(t, `INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,'whole_source')`, itemID, f.assetToken(t, item))
		}
	}
}

func (f *fixtureLibrary) entity(t testing.TB, library string, kind compactcatalog.Kind, key, title string, year int, added string, parent int64, facts map[string]any) (string, int64) {
	t.Helper()
	var id int64
	var public string
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		lib, err := compactcatalog.LibraryTx(context.Background(), tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: lib, Kind: kind, Parent: parent, Key: "fixture:" + key, Title: title, Year: year, Added: added})
		if err != nil {
			return err
		}
		if len(facts) > 0 {
			if err = compactcatalog.SetFactsTx(context.Background(), tx, id, facts); err != nil {
				return err
			}
		}
		return tx.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&public)
	})
	if err != nil {
		t.Fatalf("seed fixture entity %s: %v", key, err)
	}
	return public, id
}

func (f *fixtureLibrary) assetToken(t testing.TB, item string) string {
	t.Helper()
	var token string
	err := f.db.QueryRow(`SELECT a.token FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE l.entity_id=?`, f.entityIDs[item]).Scan(&token)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f *fixtureLibrary) id(key string) string { return f.ids[key] }
func (f *fixtureLibrary) idsFor(keys ...string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = f.id(key)
	}
	return out
}
func (f *fixtureLibrary) sortedIDsFor(keys ...string) []string {
	out := f.idsFor(keys...)
	slices.Sort(out)
	return out
}

// The catalog Justin's examples run against.
func justinsLibrary(t testing.TB) *fixtureLibrary {
	f := openFixture(t)
	for i := 0; i < 12; i++ {
		f.movie(t, fixtureMovie{id: fmt.Sprintf("m%02d", i), title: fmt.Sprintf("Ordinary Movie %d", i), overview: "A story about people.", year: 1980 + i, rating: 6.5})
	}
	for i := 0; i < 4; i++ {
		f.movie(t, fixtureMovie{id: fmt.Sprintf("y85-%d", i), title: fmt.Sprintf("Eighty-Five %d", i), overview: "Big hair.", year: 1985, rating: 5})
	}
	f.movie(t, fixtureMovie{id: "sharktopus", title: "Sharktopus", overview: "Half shark, half octopus.", year: 2010, rating: 2.5})
	f.movie(t, fixtureMovie{id: "sand-sharks", title: "Sand Sharks", overview: "They swim in sand.", year: 2011, rating: 2.1})
	f.movie(t, fixtureMovie{id: "mega", title: "Mega Monster", overview: "A giant shark fights a giant octopus.", year: 2009, rating: 2.8})
	f.movie(t, fixtureMovie{id: "jaws", title: "Jaws", overview: "A great white shark terrorizes a beach town.", year: 1975, rating: 8.1})
	f.movie(t, fixtureMovie{id: "shark-night", title: "Shark Night", overview: "Lake sharks.", year: 2011, rating: 4.0})
	f.movie(t, fixtureMovie{id: "unrated-shark", title: "Shark Doc", overview: "Sharks, calmly.", year: 2020})
	f.show(t, "malcolm", "Malcolm in the Middle", 2, 3)
	f.show(t, "other", "The Other Show", 1, 3)
	f.settle(t)
	return f
}

func ownerAuthority(allowsItem func(string) bool) Authority {
	return func(ctx context.Context, tx *sql.Tx, owner bool) (Scope, error) {
		// A local owner: lane C (5f1fc9fb) refuses restrictions for an empty
		// authority, so the fixture scope carries a real viewer (INT P26).
		s := Scope{Fence: "fixture", Owner: true, Principal: identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "owner", Role: "owner"}}, AllowsLibrary: func(string) bool { return true }}
		if allowsItem != nil {
			s.AllowsItem = func(_ context.Context, _ *sql.Tx, id string) bool { return allowsItem(id) }
		}
		return s, nil
	}
}

func channel(id string, q Query, mode, episodes string) Config {
	return Config{Version: ProtocolVersion, ID: id, Name: "Test " + id, Enabled: true, Timezone: "UTC", Seed: "seed-" + id, DefaultRuleID: "main", ViewerAccess: "server-members",
		Quality: Quality{Mode: "automatic"}, Rules: []Rule{{ID: "main", Name: "Main", Query: q, Mode: mode, EpisodeMode: episodes, Exhaustion: "loop", MaxConsecutive: 100}}}
}

func raw(v string) json.RawMessage { return json.RawMessage(v) }

func previewOf(t *testing.T, f *fixtureLibrary, c Config) Preview {
	t.Helper()
	f.settle(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	p, err := s.Preview(context.Background(), ownerAuthority(nil), c)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return p
}

func sampleIDs(p Preview) []string {
	ids := []string{}
	for _, c := range p.Rules[0].Sample {
		ids = append(ids, c.ItemID)
	}
	slices.Sort(ids)
	return ids
}

func dayItems(p Preview) []string {
	out := []string{}
	for _, e := range p.FirstDay {
		out = append(out, e.ItemID)
	}
	return out
}

func TestJustinOnlyMoviesFrom1985(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := justinsLibrary(t)
	c := channel("eighty-five", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"year","operator":"equals","value":1985}`)}, "shuffle-bag", "none")
	p := previewOf(t, f, c)
	if got := sampleIDs(p); !slices.Equal(got, f.sortedIDsFor("m05", "y85-0", "y85-1", "y85-2", "y85-3")) {
		t.Fatalf("1985 movies: %v", got)
	}
	if p.Rules[0].Eligible != 5 || p.Rules[0].DurationMS != 5*90*60_000 || !p.Complete {
		t.Fatalf("counts: %+v", p.Rules[0])
	}
	if len(p.FirstDay) == 0 {
		t.Fatal("no first day")
	}
	allowed := f.idsFor("m05", "y85-0", "y85-1", "y85-2", "y85-3")
	for _, e := range p.FirstDay {
		if !slices.Contains(allowed, e.ItemID) {
			t.Fatalf("a non-1985 title was scheduled: %+v", e)
		}
	}
}

func TestJustinNothingButMalcolmInTheMiddle(t *testing.T) {
	f := justinsLibrary(t)
	// Pinning the show is enough, even with only the movie kind named: the show adds its episodes.
	c := channel("malcolm", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"movie"}, Order: "episode", IncludeItemIDs: []string{f.id("malcolm")}}, "sequential", "in-order")
	p := previewOf(t, f, c)
	if p.Rules[0].Eligible != 6 {
		t.Fatalf("eligible %d", p.Rules[0].Eligible)
	}
	want := f.idsFor("malcolm-s1e1", "malcolm-s1e2", "malcolm-s1e3", "malcolm-s2e1", "malcolm-s2e2", "malcolm-s2e3")
	got := dayItems(p)
	if len(got) < 12 || !slices.Equal(got[:6], want) || !slices.Equal(got[6:12], want) {
		t.Fatalf("first day is not Malcolm in order, looping: %v", got)
	}
	// The same channel by title filter.
	byTitle := channel("malcolm2", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode", Filter: raw(`{"variant":"predicate","field":"title","operator":"equals","value":"Malcolm in the Middle"}`)}, "sequential", "in-order")
	if got := dayItems(previewOf(t, f, byTitle)); !slices.Equal(got[:6], want) {
		t.Fatalf("by title: %v", got)
	}
}

func TestJustinBadMoviesThatMentionSharks(t *testing.T) {
	f := justinsLibrary(t)
	c := channel("bad-sharks", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "rating", Text: "sharks", Filter: raw(`{"variant":"predicate","field":"communityRating","operator":"less-than","value":3}`)}, "sequential", "none")
	p := previewOf(t, f, c)
	if got := sampleIDs(p); !slices.Equal(got, f.sortedIDsFor("mega", "sand-sharks", "sharktopus")) {
		t.Fatalf("bad shark movies: %v (Jaws 8.1, Shark Night 4.0 and the unrated doc must not match)", got)
	}
	// Order "rating" is highest first: 2.8, 2.5, 2.1.
	if got := dayItems(p); len(got) < 3 || !slices.Equal(got[:3], f.idsFor("mega", "sharktopus", "sand-sharks")) {
		t.Fatalf("rating order: %v", got)
	}
}

func TestSelectionGroupsExclusionsAndPins(t *testing.T) {
	f := justinsLibrary(t)
	// any(year 1985, title starts with "Shark") AND NOT rating < 3, minus a pinned-out title, plus a pinned-in one.
	q := Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title",
		Filter:         raw(`{"variant":"group","all":[{"variant":"group","any":[{"variant":"predicate","field":"year","operator":"equals","value":1985},{"variant":"predicate","field":"title","operator":"starts-with","value":"Shark"}]},{"variant":"group","not":{"variant":"predicate","field":"communityRating","operator":"less-than","value":3}}]}`),
		ExcludeItemIDs: []string{f.id("y85-3")}, IncludeItemIDs: []string{f.id("jaws")}}
	p := previewOf(t, f, channel("mix", q, "sequential", "none"))
	got := []string{}
	for _, e := range p.FirstDay {
		if !slices.Contains(got, e.ItemID) {
			got = append(got, e.ItemID)
		}
	}
	slices.Sort(got)
	want := f.sortedIDsFor("jaws", "m05", "shark-night", "unrated-shark", "y85-0", "y85-1", "y85-2")
	if !slices.Equal(got, want) {
		t.Fatalf("groups/pins: %v want %v", got, want)
	}
}

func TestPersonalFieldsAreRefusedWithAClearMessage(t *testing.T) {
	c := channel("mine", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"playState","operator":"equals","value":"unplayed"}`)}, "shuffle-bag", "none")
	err := Validate(c)
	var issue *ValidationError
	if !errors.As(err, &issue) || issue.Message != PersonalHint || !errors.Is(err, ErrInvalid) {
		t.Fatalf("personal field: %v", err)
	}
	bad := channel("bad", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"year","operator":"equals","value":"soon"}`)}, "shuffle-bag", "none")
	if err = Validate(bad); !errors.As(err, &issue) || !strings.Contains(issue.Path, "filter") {
		t.Fatalf("invalid value names its path: %v", err)
	}
}

func TestRotateAndSequentialThenShuffle(t *testing.T) {
	f := justinsLibrary(t)
	rotate := channel("rotate", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "sequential", "rotate")
	got := dayItems(previewOf(t, f, rotate))
	want := f.idsFor("malcolm-s1e1", "other-s1e1", "malcolm-s1e2", "other-s1e2", "malcolm-s1e3", "other-s1e3", "malcolm-s2e1", "malcolm-s2e2", "malcolm-s2e3")
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("rotation: %v", got)
	}
	then := channel("then", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"year","operator":"equals","value":1985}`)}, "sequential-then-shuffle", "none")
	day := dayItems(previewOf(t, f, then))
	ordered := f.idsFor("y85-0", "y85-1", "y85-2", "y85-3", "m05")
	reversed := f.idsFor("m05", "y85-0", "y85-1", "y85-2", "y85-3")
	if !slices.Equal(day[:5], ordered) && !slices.Equal(day[:5], reversed) {
		t.Fatalf("first cycle in order: %v", day[:5])
	}
	firstCycle := append([]string{}, day[:5]...)
	if slices.Equal(day[5:10], firstCycle) && slices.Equal(day[10:15], firstCycle) {
		t.Fatalf("later cycles never shuffle: %v", day)
	}
}

// Generate, publish and read the guide: restricted titles become a slate for that
// profile only, and tuning into one is refused.
func TestRestrictedProfilesSeeASlateInTheSharedLineup(t *testing.T) {
	f := justinsLibrary(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("sharks", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Text: "shark"}, "sequential", "none")
	if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save-1", Config: c}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		worked, err := s.RunBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	key := make([]byte, 32)
	q := livechannels.GuideQuery{Kind: livechannels.LibraryChannel, Start: f.now, End: f.now.Add(6 * time.Hour), Timezone: "UTC", Limit: 10}
	everyone, err := s.Guide(context.Background(), ownerAuthority(nil), q, key)
	if err != nil || len(everyone.Channels) != 1 {
		t.Fatalf("guide: %v %+v", err, everyone)
	}
	kid, err := s.Guide(context.Background(), ownerAuthority(func(id string) bool { return id != f.id("jaws") }), q, key)
	if err != nil {
		t.Fatal(err)
	}
	a, b := everyone.Channels[0].Programmes, kid.Channels[0].Programmes
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("same lineup: %d vs %d", len(a), len(b))
	}
	slates := 0
	for i := range a {
		if a[i].Start != b[i].Start || a[i].End != b[i].End {
			t.Fatal("a restricted profile must see the same schedule at the same time")
		}
		if b[i].Title == restrictedTitle {
			slates++
			if a[i].Title != "Jaws" {
				t.Fatalf("only Jaws is restricted: %s", a[i].Title)
			}
		}
	}
	if slates == 0 {
		t.Fatal("Jaws never became a slate")
	}
}

// Preview cost is bounded by its scan budget, not the library size.
// The library must exceed the 20,000-row scan budget to prove the bound, so
// this runs in the release and deep performance tiers.
func TestPreviewIsBoundedOnALargeLibrary(t *testing.T) {
	requireScaleTier(t)
	if testing.Short() {
		t.Skip("large fixture")
	}
	f := openFixture(t)
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		library, err := compactcatalog.LibraryTx(context.Background(), tx, "movies")
		if err != nil {
			return err
		}
		for i := 0; i < 30000; i++ {
			key := fmt.Sprintf("big%06d", i)
			item, _, err := compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "fixture:" + key, Title: "Title " + key, Year: 1950 + i%70})
			if err != nil {
				return err
			}
			asset, token, err := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: "/b/" + key, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(context.Background(), tx, item, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO playback_origin_assets(id) VALUES(?)`, token); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO playback_origin_items(id) VALUES(?)`, item); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO playback_origin_associations(item_id,asset_id) VALUES(?,?)`, item, token); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c := channel("big", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"decade","operator":"equals","value":1980}`)}, "shuffle-bag", "none")
	started := time.Now()
	p := previewOf(t, f, c)
	elapsed := time.Since(started)
	r := p.Rules[0]
	if r.Complete || r.Scanned > previewScanBudget+CandidateBatch || r.Eligible == 0 || len(p.FirstDay) == 0 {
		t.Fatalf("bounded preview: %+v first day %d", r, len(p.FirstDay))
	}
	if elapsed > 5*time.Second {
		t.Fatalf("preview took %v", elapsed)
	}
	t.Logf("30k library: scanned %d, %d matched, first day %d entries, %v", r.Scanned, r.Eligible, len(p.FirstDay), elapsed)
}

func (f *fixtureLibrary) settle(t testing.TB) {
	t.Helper()
	worker := compactcatalog.NewWorker(f.db)
	for n := 0; n < 100000; n++ {
		_, err := worker.Step(context.Background(), 100)
		if err != nil {
			t.Fatal(err)
		}
		var pending int
		if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM catalog_dirty)+(SELECT count(*) FROM catalog_derivations WHERE rebuilding<>0)`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			return
		}
	}
	t.Fatal("compact channel fixture did not settle")
}
