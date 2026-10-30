package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// episodeFixtureProvider answers for one show and counts how often each kind
// of document is asked for.
type episodeFixtureProvider struct {
	show         metadataprovider.ScreenRecord
	showCalls    int
	episodeCalls map[string]int
}

func (p *episodeFixtureProvider) SearchScreen(context.Context, string, string, int, string, string) ([]metadataprovider.ScreenRecord, error) {
	p.showCalls++
	return []metadataprovider.ScreenRecord{p.show}, nil
}
func (p *episodeFixtureProvider) ScreenDetails(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error) {
	p.showCalls++
	return p.show, nil
}
func (p *episodeFixtureProvider) ScreenEpisode(_ context.Context, show string, season, episode int, order, language string) (metadataprovider.ScreenRecord, error) {
	p.episodeCalls[fmt.Sprintf("%s/%d/%d/%s/%s", show, season, episode, order, language)]++
	r := metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "episode", ID: strconv.Itoa(9000 + season*100 + episode)}, Title: "Provider episode " + strconv.Itoa(episode)}
	r.Coordinates = &metadataprovider.ScreenCoordinates{ShowID: show, Season: &season, Episode: episode, Order: order}
	r.Relations = []metadataprovider.ScreenRelation{{Kind: "show", Target: metadataprovider.ScreenID{Provider: "tmdb", Type: "show", ID: show}}}
	return r, nil
}
func (p *episodeFixtureProvider) total() int {
	n := 0
	for _, v := range p.episodeCalls {
		n += v
	}
	return n
}

func episodeFreshnessFixture(t *testing.T) (*Service, *sql.DB, *episodeFixtureProvider, *time.Time, func(number int), func()) {
	t.Helper()
	s, db, now := publicationFixture(t)
	c := catalogtest.New(t, db)
	library := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(library, "Series", 2020)
	season := c.Season(show, 1)
	c.Exec(`INSERT OR IGNORE INTO library_sources(id,library_id,configured_root,root) VALUES('tv-source','tv','/tv','/tv')`)
	add := func(number int) {
		c.Episode(show, season, number, "/tv/"+strconv.Itoa(number)+".mp4")
		c.Drain()
	}
	for n := 1; n <= 4; n++ {
		add(n)
	}
	discoveryConfirmLibrary(t, s, "tv", []string{"tmdb"}, "US")
	p := &episodeFixtureProvider{show: metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "show", ID: "500"}, Title: "Series", Year: 2020, Overview: "A series."}, episodeCalls: map[string]int{}}
	s.screenProviders = map[string]ScreenProvider{"tmdb": p}
	settle := func() {
		t.Helper()
		settleScreenFanout(t, s)
		for n := 0; n < 200; n++ {
			var due int
			if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_work WHERE library_id='tv' AND status IN('pending','searching','pending_children')`).Scan(&due); err != nil {
				t.Fatal(err)
			}
			if due == 0 {
				return
			}
			if err := s.ScreenStep(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("the show's metadata work never settled")
	}
	return s, db, p, now, add, settle
}

// Adding an episode to a matched show asks for the new episode only: the show
// is not matched again, and no other episode is republished.
func TestAddingAnEpisodeDoesNotRefetchTheOthers(t *testing.T) {
	_, db, p, now, add, settle := episodeFreshnessFixture(t)
	settle()
	if p.total() != 4 {
		t.Fatalf("first match asked for %d episode documents, want 4: %v", p.total(), p.episodeCalls)
	}
	var matched int
	if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_work w JOIN catalog_episodes e ON e.entity_id=w.target_id WHERE w.target_kind='item' AND w.status='matched'`).Scan(&matched); err != nil || matched != 4 {
		t.Fatal("fixture episodes did not match", matched, err)
	}
	var publications int
	if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_publications WHERE target_kind='item'`).Scan(&publications); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(48 * time.Hour)
	add(5)
	settle()
	if p.total() != 5 || p.episodeCalls["500/1/5/official/en-US"] != 1 {
		t.Fatalf("one new episode cost %d episode requests: %v", p.total()-4, p.episodeCalls)
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_publications WHERE target_kind='item'`).Scan(&after); err != nil || after != publications+1 {
		t.Fatalf("new episode republished old episodes: %d -> %d, %v", publications, after, err)
	}
	var title string
	if err := db.QueryRow(`SELECT e.title FROM catalog_entities e JOIN catalog_episodes c ON c.entity_id=e.id WHERE c.number=1`).Scan(&title); err != nil || title != "Provider episode 1" {
		t.Fatalf("a held episode lost its published title: %q %v", title, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_work w JOIN catalog_episodes e ON e.entity_id=w.target_id WHERE w.target_kind='item' AND w.status='matched'`).Scan(&matched); err != nil || matched != 5 {
		t.Fatal("episodes were not all matched after the fan-out", matched, err)
	}
}

// What is held stands in only for the same question. A document past its
// window, a change of language or a re-identified show is asked for again.
func TestHeldEpisodeDocumentIsRefetchedWhenItNoLongerAnswers(t *testing.T) {
	s, db, p, now, _, settle := episodeFreshnessFixture(t)
	settle()
	wake := func() {
		publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE library_id='tv' AND target_kind='show'`)
		settle()
	}
	*now = now.Add(48 * time.Hour)
	wake()
	if p.total() != 4 {
		t.Fatalf("a wake inside the window asked for %d episode documents", p.total()-4)
	}
	*now = now.Add(freshnessEpisode + time.Hour)
	wake()
	if p.total() != 8 {
		t.Fatalf("documents past their window were not refreshed: %d requests in all", p.total())
	}
	policy, err := s.ScreenPolicy(context.Background(), "tv")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = s.UpdateScreenPolicy(context.Background(), "tv", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "fr-FR", Region: "FR", RefreshMode: "replace_unlocked"}, nil); err != nil {
		t.Fatal(err)
	}
	settle()
	if p.total() != 12 || p.episodeCalls["500/1/1/official/fr-FR"] != 1 {
		t.Fatalf("a language change reused documents in the old language: %v", p.episodeCalls)
	}
}

func TestEpisodeCacheDoesNotReuseAnotherRegion(t *testing.T) {
	s, db, p, _, _, settle := episodeFreshnessFixture(t)
	settle()
	publicationExec(t, db, `UPDATE screen_metadata_candidates SET region='CA' WHERE provider_type='episode'`)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending' WHERE target_kind='item'`)
	settle()
	if p.total() != 8 {
		t.Fatalf("reused another region's episodes: %d requests", p.total())
	}
	_ = s
}

func TestScreenAbsoluteOrderSelection(t *testing.T) {
	r := metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "show", ID: "500"}, Orders: []metadataprovider.ScreenOrder{{ID: "official"}, {ID: "tmdb-group:dvd"}}}
	for _, order := range []string{"official", "tmdb-absolute:official:-12", "tmdb-absolute:tmdb-group:dvd:12"} {
		if !screenOrderSupported(r, order) {
			t.Fatal(order)
		}
	}
	for _, order := range []string{"tmdb-absolute:official:-1000001", "tmdb-group:other", "tmdb-absolute:official:1;input"} {
		if screenOrderSupported(r, order) {
			t.Fatal(order)
		}
	}
}

func (p *episodeFixtureProvider) ScreenAbsoluteEpisode(ctx context.Context, show string, number int, order, language string) (metadataprovider.ScreenRecord, error) {
	base, offset, ok := metadataprovider.TMDBAbsoluteOrder(order)
	mapped := number + offset
	if !ok || base != "official" || mapped < 1 || mapped > 4 {
		return metadataprovider.ScreenRecord{}, &metadataprovider.Error{Provider: "tmdb", Code: "episode_outside_order"}
	}
	r, err := p.ScreenEpisode(ctx, show, 1, mapped, order, language)
	originalSeason, originalEpisode := 1, mapped
	r.Coordinates.Season = nil
	r.Coordinates.Episode = number
	r.Coordinates.Absolute = &number
	r.Coordinates.OriginalSeason = &originalSeason
	r.Coordinates.OriginalEpisode = &originalEpisode
	r.StillPath = "/episode.jpg"
	return r, err
}

func TestExplicitAnimeOffsetPublishesMappedEpisodesAndPreservesLocalNumbers(t *testing.T) {
	s, db, _, _, _, settle := episodeFreshnessFixture(t)
	settle()
	var show string
	if err := db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=2 LIMIT 1`).Scan(&show); err != nil {
		t.Fatal(err)
	}
	publicationExec(t, db, `UPDATE catalog_episodes SET numbering='absolute',season_id=NULL,number=number+12`)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1 WHERE target_kind='show'`)
	settle()
	state, err := s.ScreenState(context.Background(), "show", show)
	if err != nil || len(state.Candidates) != 1 {
		t.Fatal(state, err)
	}
	if err = s.SelectScreen(context.Background(), "show", show, ScreenSelection{ExpectedRevision: state.Revision, CandidateKey: state.Candidates[0].Key, Order: "tmdb-absolute:official:-12"}, nil); err != nil {
		t.Fatal(err)
	}
	settle()
	var matched int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_episodes e JOIN screen_metadata_work w ON w.target_kind='item' AND w.target_id=e.entity_id WHERE e.number BETWEEN 13 AND 16 AND e.season_id IS NULL AND e.numbering='absolute' AND w.status='matched'`).Scan(&matched); err != nil || matched != 4 {
		t.Fatal("local numbers or mapped publications lost", matched, err)
	}
	var title string
	if err = db.QueryRow(`SELECT title FROM catalog_entities WHERE id=(SELECT entity_id FROM catalog_episodes WHERE number=15)`).Scan(&title); err != nil || title != "Provider episode 3" {
		t.Fatal(title, err)
	}
	publicationExec(t, db, `UPDATE catalog_episodes SET number=1 WHERE number=13`)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1 WHERE target_kind='item' AND target_id=(SELECT entity_id FROM catalog_episodes WHERE number=1)`)
	settle()
	var status string
	if err = db.QueryRow(`SELECT status FROM screen_metadata_work WHERE target_kind='item' AND target_id=(SELECT entity_id FROM catalog_episodes WHERE number=1)`).Scan(&status); err != nil || status != "needs_order" {
		t.Fatal("invalid mapping was retried instead of requesting a correction", status, err)
	}
}
