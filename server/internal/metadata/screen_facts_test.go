package metadata

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// The screen publication fixes: regional certification into the content_rating
// projection, studio/network facts into the browse projection columns, typed
// keyword/theme storage that covers shows (shows are not items), and canonical
// people links for credited crew — all without changing any row presentation,
// and all fenced by the existing locks and consent gates.

func screenFactsRecord() metadataprovider.ScreenRecord {
	return metadataprovider.ScreenRecord{
		Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "movie", ID: "42"},
		Title:    "Film", Overview: "Verified description", Date: "2008-05-01", Year: 2008,
		Certifications: []metadataprovider.ScreenCertification{{Region: "US", Rating: "PG-13"}, {Region: "CA", Rating: "14A"}},
		Relations: []metadataprovider.ScreenRelation{
			{Kind: "production_company", Target: metadataprovider.ScreenID{Provider: "tmdb", Type: "company", ID: "101"}, Name: "Nordfilm"},
			{Kind: "production_company", Target: metadataprovider.ScreenID{Provider: "tmdb", Type: "company", ID: "102"}, Name: "Second Studio"},
		},
		Tags: []metadataprovider.ScreenName{{ID: "1", Name: "Space"}, {ID: "2", Name: "Heist"}},
		Credits: []metadataprovider.ScreenCredit{
			{ID: "55", Name: "Lead Actor", Role: "Hero", Department: "Acting", Ordinal: 0},
			{ID: "77", Name: "Fixture Director", Role: "Director", Department: "Directing", Ordinal: 1},
		},
	}
}

func screenFactShowFixture(t *testing.T, libraryID, libraryName, kind, root string) (*Service, *sql.DB, catalogtest.Item, catalogtest.Item) {
	t.Helper()
	s, db, _ := publicationFixture(t)
	c := catalogtest.New(t, db)
	library := c.Library(libraryID, libraryName, kind, root)
	show := c.Show(library, "Series", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, root+"/one.mp4")
	c.Fields(episode.ID, map[string]any{"overview": "Story"})
	c.Exec(`INSERT OR IGNORE INTO library_sources(id,library_id,configured_root,root) VALUES(?,?,?,?)`, libraryID+"-source", libraryID, root, root)
	c.Drain()
	return s, db, show, episode
}

func screenFactGenreCount(t *testing.T, db *sql.DB, entity int64, provider string) string {
	t.Helper()
	return publicationScalar(t, db, `SELECT count(*) FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id=? AND s.provider=? AND t.vocab=?`, entity, provider, int(compactcatalog.VocabGenre))
}

func screenFactsFixture(t *testing.T, region string) (*Service, *sql.DB) {
	t.Helper()
	s, db, _ := screenFixture(t)
	s.screenProviders["tmdb"].(*screenFixtureProvider).record = screenFactsRecord()
	discoveryConfirmRegion(t, s, region)
	return s, db
}

func discoveryConfirmRegion(t *testing.T, s *Service, region string) {
	t.Helper()
	discoveryConfirmLibrary(t, s, "lib", []string{"tmdb"}, region)
}

func discoveryConfirmLibrary(t *testing.T, s *Service, library string, providers []string, region string) {
	t.Helper()
	ctx := context.Background()
	policy, e := s.ScreenPolicy(ctx, library)
	if e != nil {
		t.Fatal(e)
	}
	yes := true
	e = s.UpdateScreenPolicy(ctx, library, ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: providers, Language: "en-US", Region: region, RefreshMode: "replace_unlocked"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
}

func TestScreenRegionalCertificationStudioAndPeopleLinks(t *testing.T) {
	for _, tc := range []struct {
		region, rating string
	}{
		{"CA", "14A"},
		{"US", "PG-13"},
		// No configured region: the projection stays unknown instead of
		// letting one region's rating stand in for another.
		{"", ""},
	} {
		s, db := screenFactsFixture(t, tc.region)
		item := publicationFixtureMovie(t, db)
		ctx := context.Background()
		if e := s.ScreenStep(ctx); e != nil {
			t.Fatal(e)
		}
		p := s.screenProviders["tmdb"].(*screenFixtureProvider)
		if p.calls == 0 {
			t.Fatal("provider never consulted")
		}
		if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != tc.rating {
			t.Fatalf("region %q produced content_rating %q, not %q", tc.region, got, tc.rating)
		}
		// The typed regional certifications stay evidence; the scalar is the
		// projection the browse attribute and restriction queue read.
		if n := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fields WHERE target_id=? AND field='certifications'`, item.ID); n != "1" {
			t.Fatalf("certification evidence lost: %s", n)
		}
		if tc.region != "" {
			if n := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fields WHERE target_id=? AND field='certification'`, item.ID); n != "1" {
				t.Fatalf("regional certification not published as a field: %s", n)
			}
		}
	}
}

func TestScreenCompanyFactsAndTypedTags(t *testing.T) {
	s, db := screenFactsFixture(t, "CA")
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT studio FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "Nordfilm" {
		t.Fatalf("studio projection: %q", got)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=? AND f.field='studio' AND t.value_key='nordfilm' AND e.source_value='Nordfilm'`, item.ID); n != "1" {
		t.Fatal("studio not propagated to the browse projection")
	}
	// Keywords are typed rows with provider identity, never genre rows.
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='item' AND entity_id=? AND provider='tmdb'`, item.ID); n != "2" {
		t.Fatalf("typed keywords not stored: %s", n)
	}
	if n := screenFactGenreCount(t, db, item.ID, "tmdb"); n != "0" {
		t.Fatal("keywords leaked into the genre vocabulary")
	}
	// The credited director is a canonical person, not a scoped name credit.
	if got := publicationScalar(t, db, `SELECT p.identity_key FROM catalog_credits c JOIN catalog_people p ON p.id=c.person_id JOIN catalog_credit_labels role ON role.id=c.role_id WHERE c.entity_id=? AND role.label='Director'`, item.ID); got != "tmdb:77" {
		t.Fatalf("director person link: %q", got)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM catalog_people p JOIN catalog_credits c ON c.person_id=p.id JOIN catalog_credit_labels role ON role.id=c.role_id WHERE p.identity_key='tmdb:77' AND c.entity_id=? AND role.label='Director'`, item.ID); n != "1" {
		t.Fatal("director is not a canonical person entity")
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM catalog_people WHERE identity_key='tmdb:55'`); n != "1" {
		t.Fatal("cast person link lost")
	}
}

func TestScreenShowNetworkAndTagsAreNotItemScoped(t *testing.T) {
	s, db, show, episode := screenFactShowFixture(t, "tv", "TV", "tv", "/tv")
	p := &screenFixtureProvider{record: metadataprovider.ScreenRecord{
		Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "show", ID: "7"},
		Title:    "Series", Overview: "Verified series", Date: "2020-01-05", Year: 2020,
		Certifications: []metadataprovider.ScreenCertification{{Region: "US", Rating: "TV-14"}},
		Relations: []metadataprovider.ScreenRelation{
			{Kind: "network", Target: metadataprovider.ScreenID{Provider: "tmdb", Type: "company", ID: "202"}, Name: "NetworkTV"},
			{Kind: "production_company", Target: metadataprovider.ScreenID{Provider: "tmdb", Type: "company", ID: "101"}, Name: "Nordfilm"},
		},
		Tags: []metadataprovider.ScreenName{{ID: "9", Name: "Cyberpunk"}},
	}}
	s.screenProviders = map[string]ScreenProvider{"tmdb": p}
	discoveryConfirmLibrary(t, s, "tv", []string{"tmdb"}, "US")
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT network FROM catalog_shows WHERE entity_id=?`, show.ID); got != "NetworkTV" {
		t.Fatalf("network projection: %q", got)
	}
	if got := publicationScalar(t, db, `SELECT studio FROM catalog_shows WHERE entity_id=?`, show.ID); got != "Nordfilm" {
		t.Fatalf("show studio projection: %q", got)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_shows WHERE entity_id=?`, show.ID); got != "TV-14" {
		t.Fatalf("show regional certification: %q", got)
	}
	// Shows are not items: the tags projection is entity-scoped and its rows
	// cascade with the show, not with an item id.
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='show' AND entity_id=? AND provider='tmdb'`, show.ID); n != "1" {
		t.Fatalf("show themes not stored under the show scope: %s", n)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='item' AND entity_id=?`, show.ID); n != "0" {
		t.Fatal("show themes leaked into the item scope")
	}
	// A show's children are republished by the derivation worker.
	if err := compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=? AND f.field='network' AND t.value_key='networktv' AND e.source_value='NetworkTV'`, episode.ID); n != "1" {
		t.Fatal("show network not inherited by its episode browse projection")
	}
	// Deleting the show must not orphan the entity-scoped rows: its seasons
	// and episodes go with it, and the tags trigger clears the projection.
	c := catalogtest.New(t, db)
	c.Delete(episode.ID)
	var seasonID int64
	if e := db.QueryRow(`SELECT id FROM catalog_entities WHERE kind=? AND title='Season 1'`, int(compactcatalog.Season)).Scan(&seasonID); e != nil {
		t.Fatal(e)
	}
	c.Delete(seasonID)
	c.Delete(show.ID)
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags`); n != "0" {
		t.Fatalf("show tags survived the show: %s", n)
	}
}

func TestScreenProviderFreeNFOPublicationAndLocks(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	screenWithdraw(t, s)
	// An all-local corpus: a rich sidecar, no consent, no provider request.
	v, e := localmetadata.ParseVideoNFO([]byte(`<movie><title>Local Film</title><year>2019</year><outline>Local story</outline><mpaa>16</mpaa><genre>Heist</genre><genre>Crime</genre><studio>Local Studio</studio><credits><name credit="local-dir" role="Director">Local Director</name></credits></movie>`), false)
	if e != nil {
		t.Fatal(e)
	}
	v[0].Locator = "fixture.nfo"
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	size, modified := publicationAssetStat(t, db, item)
	if e = localmetadata.PersistVideo(ctx, tx, "lib", item.Token, assets.Facts{VideoMetadata: v, ObservedSize: size, ObservedModifiedNS: modified}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 {
		t.Fatalf("provider-free publication made %d provider calls", p.calls)
	}
	if got := publicationScalar(t, db, `SELECT studio FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "Local Studio" {
		t.Fatalf("local studio fact: %q", got)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "16" {
		t.Fatalf("local certification fact: %q", got)
	}
	if got := publicationScalar(t, db, `SELECT overview FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "Local story" {
		t.Fatalf("local overview: %q", got)
	}
	if n := screenFactGenreCount(t, db, item.ID, "nfo"); n != "2" {
		t.Fatalf("local genres: %s", n)
	}
	// The row stays blocked on the disclosure without ever touching a provider,
	// and its local facts stay published.
	if got := publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID); got != "needs_consent" {
		t.Fatalf("disclosure gate status: %q", got)
	}
	// Owner edits and locks are authoritative across the local/provider refresh
	// boundary: lock the genre class and the title, then enable the provider
	// and let it publish a contradicting record.
	title := "Owner title"
	publicationManual(t, db, &title)
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	auth := func(*sql.Tx) error { return nil }
	target := RepairTarget{"item", item.Public}
	state, e := s.RepairState(ctx, target, auth)
	if e != nil {
		t.Fatal(e)
	}
	locked := true
	if _, e = s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "relationship_lock", Role: "genre", Locked: &locked, Confirm: true}, actor, auth); e != nil {
		t.Fatal(e)
	}
	discoveryConfirmRegion(t, s, "CA")
	p.record = screenFactsRecord()
	p.record.Certifications = []metadataprovider.ScreenCertification{{Region: "CA", Rating: "14A"}}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls == 0 {
		t.Fatal("provider never consulted after confirmation")
	}
	// The owner's manual title and the locked local genres survive the refresh.
	if got := publicationScalar(t, db, `SELECT title FROM catalog_entities WHERE id=?`, item.ID); got != "Owner title" {
		t.Fatalf("manual title overwritten by refresh: %q", got)
	}
	if n := screenFactGenreCount(t, db, item.ID, "nfo"); n != "2" {
		t.Fatalf("locked local genres lost: %s", n)
	}
	if n := screenFactGenreCount(t, db, item.ID, "tmdb"); n != "0" {
		t.Fatalf("provider genres were not suppressed by the lock: %s", n)
	}
	// Local-first precedence: an NFO-published certification is owner-provided
	// local evidence; the regional provider certification does not displace it.
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "16" {
		t.Fatalf("local certification displaced by provider value: %q", got)
	}
}

func TestScreenLocalIdentityDoesNotRequireProvider(t *testing.T) {
	// No accepted provider id, no candidates, no consent: the item stays
	// locally identified and no external id is invented for it.
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	screenWithdraw(t, s)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 {
		t.Fatal("unconfirmed disclosure reached a provider")
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM provider_evidence WHERE item_id=?`, item.ID); n != "0" {
		t.Fatalf("local identity replaced by a provider id without consent: %s", n)
	}
	if got := publicationScalar(t, db, `SELECT title FROM catalog_entities WHERE id=?`, item.ID); got != "Film" {
		t.Fatalf("local title disturbed without any provider: %q", got)
	}
	_ = time.Now
}

func screenAnimeShowFixture(t *testing.T) (*Service, *sql.DB, catalogtest.Item, *screenFixtureProvider, *screenFixtureProvider) {
	t.Helper()
	s, db, show, _ := screenFactShowFixture(t, "anime", "Anime", "anime", "/anime")
	tmdb := &screenFixtureProvider{record: metadataprovider.ScreenRecord{
		Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "show", ID: "7"},
		Title:    "Series", Overview: "Verified series", Date: "2020-01-05", Year: 2020,
		Tags: []metadataprovider.ScreenName{{ID: "1", Name: "Space"}},
	}}
	anilist := &screenFixtureProvider{record: metadataprovider.ScreenRecord{
		Identity: metadataprovider.ScreenID{Provider: "anilist", Type: "anime", ID: "9"},
		Title:    "Series", Overview: "Verified anime work", Year: 2020,
		Tags: []metadataprovider.ScreenName{{ID: "2", Name: "Cyberpunk"}},
	}}
	s.screenProviders = map[string]ScreenProvider{"tmdb": tmdb, "anilist": anilist}
	// One provider per library; Fix Match may still search another offered provider for one title.
	discoveryConfirmLibrary(t, s, "anime", []string{"tmdb"}, "US")
	return s, db, show, tmdb, anilist
}

func TestScreenShowReidentificationSupersedesAllProviderTags(t *testing.T) {
	s, db, show, tmdb, anilist := screenAnimeShowFixture(t)
	tmdb.record.Certifications = []metadataprovider.ScreenCertification{{Region: "US", Rating: "TV-14"}}
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='show' AND entity_id=? AND provider='tmdb'`, show.ID); n != "1" {
		t.Fatalf("tmdb show tags not stored: %s", n)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_shows WHERE entity_id=?`, show.ID); got != "TV-14" {
		t.Fatalf("initial provider certification missing: %q", got)
	}
	// Owner reidentification across providers: a review search publishes only
	// candidates; selecting the other provider's candidate supersedes the old
	// identity, and the typed tags of the old provider must not survive as
	// current facts.
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
	state, e = s.ScreenState(ctx, "show", show.Public)
	if e != nil || len(state.Candidates) == 0 {
		t.Fatal(state, e)
	}
	var key string
	for _, c := range state.Candidates {
		if c.Provider == "anilist" {
			anilist.calls++ // remember the search happened
			key = c.Key
		}
	}
	if key == "" {
		t.Fatal("no anilist candidate published")
	}
	if e = s.SelectScreen(ctx, "show", show.Public, ScreenSelection{ExpectedRevision: state.Revision, CandidateKey: key, Order: "seasonal"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT provider FROM screen_metadata_work WHERE target_kind='show' AND target_id=?`, show.ID); got != "anilist" {
		t.Fatalf("reidentification did not switch providers: %q", got)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_shows WHERE entity_id=?`, show.ID); got != "" {
		t.Fatalf("old provider certification survived reidentification: %q", got)
	}
	rows, e := db.Query(`SELECT provider,id,name FROM metadata_tags WHERE entity_kind='show' AND entity_id=?`, show.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var providers []string
	for rows.Next() {
		var provider, id, name string
		if e = rows.Scan(&provider, &id, &name); e != nil {
			t.Fatal(e)
		}
		if provider != "anilist" || id != "2" || name != "Cyberpunk" {
			t.Fatalf("superseded provider tags survived reidentification: %s %s %s", provider, id, name)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected only the current accepted provider's tags, got %v", providers)
	}
}

func TestScreenTaglessRefreshSupersedesTypedTags(t *testing.T) {
	s, db, show, tmdb, _ := screenAnimeShowFixture(t)
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='show' AND entity_id=?`, show.ID); n != "1" {
		t.Fatalf("tags not stored before the transition: %s", n)
	}
	// Same accepted identity, replace_unlocked refresh, payload now carries no
	// tags: the old typed tags must not persist as current facts.
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind='show' AND target_id=?;
DELETE FROM metadata_document_freshness`, show.ID)
	tmdb.record.Tags = nil
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if tmdb := publicationScalar(t, db, `SELECT provider FROM screen_metadata_work WHERE target_kind='show' AND target_id=?`, show.ID); tmdb != "tmdb" {
		t.Fatalf("accepted identity changed: %q", tmdb)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='show' AND entity_id=?`, show.ID); n != "0" {
		t.Fatalf("an accepted payload with no tags left stale typed facts: %s", n)
	}
	// fill_missing preserves accepted non-empty state instead.
	publicationExec(t, db, `UPDATE screen_metadata_policies SET refresh_mode='fill_missing',revision=revision+1 WHERE library_id='anime';
UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind='show' AND target_id=?;
DELETE FROM metadata_document_freshness`, show.ID)
	tmdb.record.Tags = []metadataprovider.ScreenName{{ID: "1", Name: "Space"}}
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_tags WHERE entity_kind='show' AND entity_id=?`, show.ID); n != "1" {
		t.Fatalf("fill_missing did not fill accepted tags: %s", n)
	}
}

func TestScreenCertificationRegionTransitionClearsObsoleteAutomaticValue(t *testing.T) {
	// A provider-published US rating followed by a region change to a region
	// the provider does not certify must not keep serving the US rating as the
	// current value. The obsolete provider-derived automatic value clears; the
	// browse projection follows in the same transaction.
	s, db := screenFactsFixture(t, "US")
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "PG-13" {
		t.Fatalf("initial regional certification: %q", got)
	}
	policy, e := s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	yes := true
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", Region: "DE", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
	publicationExec(t, db, `DELETE FROM metadata_document_freshness`)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "" {
		t.Fatalf("obsolete foreign certification survived the region change: %q", got)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fields WHERE target_id=? AND field='certification'`, item.ID); n != "0" {
		t.Fatalf("obsolete certification evidence row survived: %s", n)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=? AND f.field='contentRating'`, item.ID); n != "0" {
		t.Fatalf("browse projection still carries the withdrawn rating: %s", n)
	}
	// An unknown value stays unknown rather than resurrecting any other
	// region's rating on the next refresh.
	publicationExec(t, db, `DELETE FROM metadata_document_freshness`)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "" {
		t.Fatalf("foreign rating resurrected on refresh: %q", got)
	}
}

func TestScreenCertificationTransitionPreservesNFOAndOwnerLocks(t *testing.T) {
	// Local-first: an NFO-published certification is owner-provided evidence;
	// a region change must not clear it just because the provider carries no
	// matching certification.
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	v, e := localmetadata.ParseVideoNFO([]byte(`<movie><title>Film</title><year>2008</year><mpaa>16</mpaa></movie>`), false)
	if e != nil {
		t.Fatal(e)
	}
	v[0].Locator = "fixture.nfo"
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	size, modified := publicationAssetStat(t, db, item)
	if e = localmetadata.PersistVideo(ctx, tx, "lib", item.Token, assets.Facts{VideoMetadata: v, ObservedSize: size, ObservedModifiedNS: modified}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	discoveryConfirmRegion(t, s, "US")
	p.record = screenFactsRecord()
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "16" {
		t.Fatalf("NFO certification displaced by provider: %q", got)
	}
	policy, e := s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	yes := true
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", Region: "DE", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
	publicationExec(t, db, `DELETE FROM metadata_document_freshness`)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != "16" {
		t.Fatalf("local certification displaced by the region transition: %q", got)
	}
	// Owner-locked scalars: the automatic clear is restored by the editor's
	// lock trigger, so the owner decision remains authoritative.
	title := "Owner title"
	publicationManual(t, db, &title)
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	auth := func(*sql.Tx) error { return nil }
	target := RepairTarget{"item", item.Public}
	state, e := s.RepairState(ctx, target, auth)
	if e != nil {
		t.Fatal(e)
	}
	rating := "FSK-12"
	after, e := s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"contentRating": {Value: &rating}}}, actor, auth)
	if e != nil {
		t.Fatal(e)
	}
	if after.Snapshot.Fields["contentRating"].Value != rating || !after.Snapshot.Fields["contentRating"].Locked {
		t.Fatal("owner content rating lock not projected")
	}
	policy, e = s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", Region: "DE", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
	publicationExec(t, db, `DELETE FROM metadata_document_freshness`)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT content_rating FROM catalog_item_details WHERE entity_id=?`, item.ID); got != rating {
		t.Fatalf("owner-locked certification destroyed by the automatic clear: %q", got)
	}
}

func TestScreenLocalCastKeepsProviderCrew(t *testing.T) {
	// A local NFO owns the departments it lists (the cast); the provider's
	// director and writers still reach the title, and its cast does not.
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	v, e := localmetadata.ParseVideoNFO([]byte(`<movie><title>Film</title><year>2008</year><actor><name>Local Lead</name><role>Hero</role></actor></movie>`), false)
	if e != nil {
		t.Fatal(e)
	}
	v[0].Locator = "fixture.nfo"
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	size, modified := publicationAssetStat(t, db, item)
	if e = localmetadata.PersistVideo(ctx, tx, "lib", item.Token, assets.Facts{VideoMetadata: v, ObservedSize: size, ObservedModifiedNS: modified}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	discoveryConfirmRegion(t, s, "US")
	record := screenFactsRecord()
	record.Credits = []metadataprovider.ScreenCredit{{ID: "11", Name: "Online Lead", Role: "Hero", Department: "Acting"}, {ID: "12", Name: "Online Director", Role: "Director", Department: "Directing", Ordinal: 1}}
	p.record = record
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	got := publicationScalar(t, db, `SELECT group_concat(provider||':'||credited_name,'|') FROM (SELECT provider,credited_name FROM catalog_credits WHERE entity_id=? ORDER BY provider,credited_name)`, item.ID)
	if got != "nfo:Local Lead|tmdb:Online Director" {
		t.Fatalf("credits beside a local cast = %q", got)
	}
}

func TestScreenProviderChangeRematchesTheLibrary(t *testing.T) {
	// A library has one provider. Titles matched with the one it left are
	// searched for again; what they show stays until the new match publishes.
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	discoveryConfirmRegion(t, s, "US")
	p.record = screenFactsRecord()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT provider||':'||provider_id FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID); got != "tmdb:42" {
		t.Fatalf("matched identity = %q", got)
	}
	policy, e := s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, Enabled: true, Providers: []string{"tvdb"}, Language: "en-US", Region: "US", RefreshMode: "replace_unlocked", DisclosureVersion: ScreenDisclosureVersion}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
	if got := publicationScalar(t, db, `SELECT provider||'|'||provider_id||'|'||selection_mode||'|'||status||'|'||(accepted_publication<>'') FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID); got != "||none|pending|1" {
		t.Fatalf("after the provider changed = %q", got)
	}
	// The same provider again changes no identity.
	policy, _ = s.ScreenPolicy(ctx, "lib")
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", Region: "US", RefreshMode: "replace_unlocked", DisclosureVersion: ScreenDisclosureVersion}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT provider||':'||provider_id||':'||status FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID); got != "tmdb:42:matched" {
		t.Fatalf("matched again = %q", got)
	}
}
