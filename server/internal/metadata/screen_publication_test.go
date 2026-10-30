package metadata

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type screenFixtureProvider struct {
	calls  int
	record metadataprovider.ScreenRecord
}

func (p *screenFixtureProvider) SearchScreen(context.Context, string, string, int, string, string) ([]metadataprovider.ScreenRecord, error) {
	p.calls++
	return []metadataprovider.ScreenRecord{p.record}, nil
}
func (p *screenFixtureProvider) ScreenDetails(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error) {
	p.calls++
	return p.record, nil
}
func screenFixture(t *testing.T) (*Service, *sql.DB, *screenFixtureProvider) {
	t.Helper()
	s, db, _ := publicationFixture(t)
	publicationExec(t, db, `INSERT OR IGNORE INTO library_sources(id,library_id,configured_root,root) VALUES('screen-source','lib','/owned','/owned');UPDATE screen_metadata_policies SET providers='["tmdb"]' WHERE library_id='lib'`)
	p := &screenFixtureProvider{record: metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "movie", ID: "42"}, Title: "Film", Year: 2008, Overview: "Verified description"}}
	s.screenProviders = map[string]ScreenProvider{"tmdb": p}
	return s, db, p
}
func screenConfirm(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	p, e := s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	yes := true
	e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", Region: "CA", RefreshMode: "replace_unlocked"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
}

// screenWithdraw withdraws the server-wide online-lookup consent through the
// owner API. A fresh server starts with it confirmed (migration 0079).
func screenWithdraw(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	p, e := s.ScreenPolicy(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	no := false
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, ConfirmRemote: &no, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	settleScreenFanout(t, s)
}

// B84 / D1: lookups are on by default; the reported status distinguishes a
// withdrawn server-wide consent from a library turned off, and an enabled
// library saved with no providers gets the defaults.
func TestScreenLookupStatusAndDefaultProviders(t *testing.T) {
	s, _, _ := screenFixture(t)
	ctx := context.Background()
	p, e := s.ScreenPolicy(ctx, "lib")
	if e != nil || !p.Confirmed || p.Status != "enabled" {
		t.Fatal("fresh server is not enabled by default", p.Status, p.Confirmed, e)
	}
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: []string{}, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	if p, e = s.ScreenPolicy(ctx, "lib"); e != nil || len(p.Providers) != 1 || p.Providers[0] != "tmdb" {
		t.Fatal("film library default providers", p.Providers, e)
	}
	if e = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, DisclosureVersion: ScreenDisclosureVersion, Enabled: false, Providers: p.Providers, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	if p, e = s.ScreenPolicy(ctx, "lib"); e != nil || p.Status != "disabled" || !p.Confirmed {
		t.Fatal("library off", p.Status, e)
	}
	screenWithdraw(t, s)
	if p, e = s.ScreenPolicy(ctx, "lib"); e != nil || p.Status != "declined" || p.Confirmed {
		t.Fatal("withdrawn consent", p.Status, e)
	}
	if got := screenLookupStatus(false, "", true); got != "needs_consent" {
		t.Fatal(got)
	}
	for kind, want := range map[string]string{"movie": "tmdb", "tv": "tmdb", "anime": "tmdb"} {
		if got := strings.Join(defaultScreenProviders(kind), ","); got != want {
			t.Fatal(kind, got)
		}
	}
}
func TestScreenDisclosurePublicationAndRefreshIdentity(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	screenWithdraw(t, s)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 || publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID) != "needs_consent" {
		t.Fatal("disclosure gate bypassed")
	}
	screenConfirm(t, s)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID) != "42" {
		t.Fatal("not published")
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending' WHERE target_id=?`, item.ID)
	p.record.Identity.ID = "43"
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID) != "42" {
		t.Fatal("accepted identity silently replaced")
	}
}
func TestScreenStaleSourceCannotPublish(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	ctx := context.Background()
	claim, e := s.claimScreen(ctx)
	if e != nil || claim == nil {
		t.Fatal(claim, e)
	}
	publicationExec(t, db, `UPDATE catalog_assets SET size=2 WHERE id=?;UPDATE catalog_assets SET size=1 WHERE id=?`, item.Asset, item.Asset)
	candidate := scoreScreen(p.record, nil, 0, true)
	if e = s.publishScreen(ctx, claim, []screenCandidate{candidate}, 0, "matched"); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_publications`) != "0" {
		t.Fatal("stale source published")
	}
}
func TestScreenOwnerSelectionRequiresCurrentCandidateAndPreservesManualTitle(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	ctx := context.Background()
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	state, e := s.ScreenState(ctx, "item", item.Public)
	if e != nil || len(state.Candidates) != 1 {
		t.Fatal(state, e)
	}
	selection := ScreenSelection{ExpectedRevision: state.Revision, CandidateKey: state.Candidates[0].Key}
	if e = s.SelectScreen(ctx, "item", item.Public, selection, nil); e != nil {
		t.Fatal("self-publication made candidates stale", e)
	}
	title := "Owner title"
	publicationManual(t, db, &title)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT title FROM catalog_entities WHERE id=?`, item.ID) != "Owner title" {
		t.Fatal("owner override lost")
	}
}
func TestScreenNFOPersistenceUsesNormalizedKindsAndFullCoordinate(t *testing.T) {
	_, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	v, e := localmetadata.ParseVideoNFO([]byte(`<episodedetails><title>One</title><season>1</season><episode>1</episode></episodedetails><episodedetails><title>Two</title><season>2</season><episode>1</episode></episodedetails>`), false)
	if e != nil {
		t.Fatal(e)
	}
	for i := range v {
		v[i].Locator = "locator"
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = localmetadata.PersistVideo(ctx, tx, "lib", item.Token, assets.Facts{VideoMetadata: v, ObservedSize: 1, ObservedModifiedNS: 1}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT count(*) FROM video_nfo_evidence WHERE kind='episode'`) != "2" {
		t.Fatal("NFO coordinate collision")
	}
}
