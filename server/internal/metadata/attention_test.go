package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestAttentionDismissalAndLowConfidenceSurviveRefresh(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	ctx := context.Background()
	publicationExec(t, db, `UPDATE screen_metadata_work SET year_seed=0,title_seed='Film',status='pending' WHERE target_id=?`, item.ID)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	page, e := s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 1 || page.Entries[0].Reason != "low_confidence" || len(page.Entries[0].Candidates) != 1 {
		t.Fatal(page, e)
	}
	entry := page.Entries[0]
	if e = s.AttentionAction(ctx, "lib", AttentionCommand{Kind: entry.Kind, EntityID: entry.EntityID, ExpectedRevision: entry.Revision, Action: "dismiss"}, MBActor{}, nil); e != nil {
		t.Fatal(e)
	}
	page, e = s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 0 {
		t.Fatal(page, e)
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1 WHERE target_id=?`, item.ID)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	page, e = s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 0 {
		t.Fatal("provider retry undid Keep as is", page, e)
	}
	publicationExec(t, db, `UPDATE catalog_assets SET size=size+1 WHERE id IN(SELECT asset_id FROM catalog_asset_links WHERE entity_id=?)`, item.ID)
	settleMetadataCatalogue(t, db)
	page, e = s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 1 || page.Entries[0].Reason != "low_confidence" {
		t.Fatal("changed files did not return to review", page, e)
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',generation=generation+1 WHERE target_id=?`, item.ID)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	page, e = s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 1 {
		t.Fatal(page, e)
	}
	entry = page.Entries[0]
	if e = s.AttentionAction(ctx, "lib", AttentionCommand{Kind: entry.Kind, EntityID: entry.EntityID, ExpectedRevision: entry.Revision, Action: "accept", CandidateKey: entry.Candidates[0].Key, Order: "official"}, MBActor{Authority: "direct", AccountID: "owner"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	page, e = s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 0 {
		t.Fatal(page, e)
	}
}
func TestAttentionScopeAndRevisionAreEnforced(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	page, e := s.Attention(ctx, "lib", "", 1)
	if e != nil || len(page.Entries) != 1 {
		t.Fatal(page, e)
	}
	entry := page.Entries[0]
	in := AttentionCommand{Kind: "item", EntityID: item.Public, ExpectedRevision: entry.Revision + 1, Action: "dismiss"}
	if e = s.AttentionAction(ctx, "lib", in, MBActor{}, nil); !errors.Is(e, ErrScreenConflict) {
		t.Fatal(e)
	}
	in.ExpectedRevision = entry.Revision
	if e = s.AttentionAction(ctx, "another-library", in, MBActor{}, nil); !errors.Is(e, ErrRepairInput) {
		t.Fatal(e)
	}
	denied := errors.New("denied")
	if e = s.AttentionAction(ctx, "lib", in, MBActor{}, func(_ *sql.Tx) error { return denied }); !errors.Is(e, denied) {
		t.Fatal(e)
	}
}
func TestNFORetirementReleasesFieldOwnership(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	ctx := context.Background()
	write, e := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	b, e := readScreenBase(ctx, write.Tx(), "item", item.Public)
	if e != nil {
		t.Fatal(e)
	}
	b.NFO = []assets.VideoNFO{{Title: "Local title", Overview: "Local plot"}}
	if e = s.applyScreenLocal(ctx, write.Tx(), b); e != nil {
		t.Fatal(e)
	}
	if e = write.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := publicationScalar(t, db, `SELECT overview FROM catalog_item_details WHERE entity_id=?`, item.ID); got != p.record.Overview {
		t.Fatal("removed NFO still owns overview", got)
	}
}
func TestMetadataSettingsNeverReturnCredentials(t *testing.T) {
	s, _, _ := screenFixture(t)
	ctx := context.Background()
	current, e := s.MetadataSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	token := "owner-test-token"
	key := "owner-test-key"
	out, e := s.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: 4 << 30, TMDBReadToken: &token, TVDBProjectKey: &key, IncludeAdult: true}, nil)
	if e != nil || out.LocalArtworkLimitBytes != 4<<30 || !out.TMDBCredentialConfigured || !out.TVDBCredentialConfigured || !out.IncludeAdult {
		t.Fatal(out, e)
	}
	raw, e := json.Marshal(out)
	if e != nil || strings.Contains(string(raw), token) || strings.Contains(string(raw), key) {
		t.Fatal("credential returned", e)
	}
	if _, e = s.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: 1 << 30}, nil); !errors.Is(e, ErrScreenConflict) {
		t.Fatal(e)
	}
	// The subtitle provider's key and account: saved, handed to the provider, never sent back.
	var handed [3]string
	s.SubtitleCredentials = func(key, username, password string) { handed = [3]string{key, username, password} }
	subKey, user, password := "subtitle-key-secret", "subtitle-user", "subtitle-password-secret"
	out, e = s.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: out.Revision, LocalArtworkLimitBytes: 4 << 30, OpenSubtitlesAPIKey: &subKey, OpenSubtitlesUsername: &user, OpenSubtitlesPassword: &password}, nil)
	if e != nil || !out.OpenSubtitlesKeyConfigured || out.OpenSubtitlesUsername != user || !out.OpenSubtitlesPasswordConfigured {
		t.Fatal(out, e)
	}
	if handed != [3]string{subKey, user, password} {
		t.Fatal("provider not given the saved credentials", handed)
	}
	raw, _ = json.Marshal(out)
	if strings.Contains(string(raw), subKey) || strings.Contains(string(raw), password) {
		t.Fatal("subtitle credential returned")
	}
	empty := ""
	out, e = s.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: out.Revision, LocalArtworkLimitBytes: 4 << 30, OpenSubtitlesUsername: &empty}, nil)
	if e != nil || out.OpenSubtitlesUsername != "" || out.OpenSubtitlesPasswordConfigured || !out.OpenSubtitlesKeyConfigured {
		t.Fatal("clearing the account must drop its password and keep the key", out, e)
	}
	// The owner sees where OpenSubtitles' credentials come from and how it last answered, and can test them.
	if out.OpenSubtitles.KeySource != "none" || out.OpenSubtitles.DownloadsRemaining != -1 {
		t.Fatal("no provider: nothing in force", out.OpenSubtitles)
	}
	s.SubtitleStatus = func() OpenSubtitlesStatus {
		return OpenSubtitlesStatus{KeySource: "owner", AccountSource: "none", Account: "none", DownloadsRemaining: 4, DownloadsAllowed: -1}
	}
	checked := 0
	s.SubtitleCheck = func(context.Context) OpenSubtitlesStatus {
		checked++
		return OpenSubtitlesStatus{KeySource: "owner", AccountSource: "none", Account: "none", Problem: "provider_key_rejected", DownloadsRemaining: -1, DownloadsAllowed: -1}
	}
	if read, e := s.MetadataSettings(ctx); e != nil || read.OpenSubtitles.KeySource != "owner" || read.OpenSubtitles.DownloadsRemaining != 4 {
		t.Fatal(read.OpenSubtitles, e)
	}
	if status, e := s.CheckOpenSubtitles(ctx); e != nil || checked != 1 || status.Problem != "provider_key_rejected" {
		t.Fatal(status, e)
	}
}

func TestLocaleChangeAndExplicitShowRefreshBypassHeldDocuments(t *testing.T) {
	s, db, p, _, _, settle := episodeFreshnessFixture(t)
	settle()
	before := p.showCalls
	policy, e := s.ScreenPolicy(context.Background(), "tv")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateScreenPolicy(context.Background(), "tv", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, Enabled: true, Providers: []string{"tmdb"}, Language: "fr-FR", Region: "FR", RefreshMode: "replace_unlocked"}, nil); e != nil {
		t.Fatal(e)
	}
	settle()
	if p.showCalls <= before {
		t.Fatal("language change reused old provider body")
	}
	var show string
	if e = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=2 AND library_id=(SELECT id FROM catalog_libraries WHERE library_id='tv')`).Scan(&show); e != nil {
		t.Fatal(e)
	}
	before = p.showCalls
	if e = s.QueueRefresh(show); e != nil {
		t.Fatal(e)
	}
	settle()
	if p.showCalls <= before {
		t.Fatal("explicit show refresh reused held body")
	}
}

func TestAttentionRevisionIgnoresWorkerProgress(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	page, e := s.Attention(ctx, "lib", "", 25)
	if e != nil || len(page.Entries) != 1 {
		t.Fatal(page, e)
	}
	entry := page.Entries[0]
	publicationExec(t, db, `UPDATE screen_metadata_work SET revision=revision+10,lease='worker-lease' WHERE target_id=?`, item.ID)
	if e = s.AttentionAction(ctx, "lib", AttentionCommand{Kind: entry.Kind, EntityID: entry.EntityID, ExpectedRevision: entry.Revision, Action: "dismiss"}, MBActor{}, nil); e != nil {
		t.Fatal("worker-only revision produced a conflict", e)
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET generation=generation+1 WHERE target_id=?`, item.ID)
	if e = s.AttentionAction(ctx, "lib", AttentionCommand{Kind: entry.Kind, EntityID: entry.EntityID, ExpectedRevision: entry.Revision, Action: "dismiss"}, MBActor{}, nil); !errors.Is(e, ErrScreenConflict) {
		t.Fatal("real input change was ignored", e)
	}
}

func TestProviderFieldLocaleIsKnownOrUnknownNeverRelabelled(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	p.record.FieldLanguages = map[string]string{"title": "en"}
	if e := s.ScreenStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	var titleLocale, overviewLocale string
	if e := db.QueryRow(`SELECT language FROM screen_metadata_fields WHERE target_id=? AND field='title'`, item.ID).Scan(&titleLocale); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT language FROM screen_metadata_fields WHERE target_id=? AND field='overview'`, item.ID).Scan(&overviewLocale); e != nil {
		t.Fatal(e)
	}
	if titleLocale != "en" || overviewLocale != "" {
		t.Fatal("request locale relabelled returned text", titleLocale, overviewLocale)
	}
}

func TestFreshOldEpisodesAreNotRepublishedOnAParentWake(t *testing.T) {
	_, db, _, now, add, settle := episodeFreshnessFixture(t)
	settle()
	publicationExec(t, db, `UPDATE catalog_item_details SET added_text='2020-01-01T00:00:00Z' WHERE entity_id IN(SELECT entity_id FROM catalog_episodes)`)
	var before int
	if e := db.QueryRow(`SELECT count(*) FROM screen_metadata_publications WHERE target_kind='item'`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(7 * 24 * time.Hour)
	add(5)
	settle()
	var after int
	if e := db.QueryRow(`SELECT count(*) FROM screen_metadata_publications WHERE target_kind='item'`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != before+1 {
		t.Fatal("old fresh episodes were republished", before, after)
	}
}

func TestRemovedNFORelationshipsRespectOwnerLocks(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	ctx := context.Background()
	write, e := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	b, e := readScreenBase(ctx, write.Tx(), "item", item.Public)
	if e != nil {
		t.Fatal(e)
	}
	b.NFO = []assets.VideoNFO{{Genres: []string{"Local genre"}, Credits: []metadataprovider.ScreenCredit{{Name: "Local actor", Role: "Lead", Department: "Acting"}}}}
	if e = s.applyScreenLocal(ctx, write.Tx(), b); e != nil {
		t.Fatal(e)
	}
	for _, class := range []string{"genre", "credit"} {
		if e = setRelationshipLock(ctx, write.Tx(), RepairTarget{"item", item.Public}, class, true, "local:owner:profile", tvdbStamp(s.publicationTime())); e != nil {
			t.Fatal(e)
		}
	}
	if e = write.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	var genres, credits int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_term_sources WHERE entity_id=? AND provider='nfo'`, item.ID).Scan(&genres); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='nfo'`, item.ID).Scan(&credits); e != nil {
		t.Fatal(e)
	}
	if genres != 1 || credits != 1 {
		t.Fatal("removing an NFO cleared locked relationships", genres, credits)
	}
}

func TestAttentionCountsExcludeWorkInProgressAndDismissals(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	publicationExec(t, db, `UPDATE screen_metadata_attention SET reason='low_confidence' WHERE entity_id=?`, item.ID)
	counts, err := s.AttentionCounts(ctx)
	if err != nil || counts.Total != 1 {
		t.Fatal(counts, err)
	}
	page, err := s.AttentionFiltered(ctx, "", "", 1, false)
	if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].LibraryID != "lib" {
		t.Fatal(page, err)
	}
	publicationExec(t, db, `UPDATE screen_metadata_attention SET reason='searching' WHERE entity_id=?`, item.ID)
	counts, err = s.AttentionCounts(ctx)
	if err != nil || counts.Total != 0 {
		t.Fatal(counts, err)
	}
	page, err = s.AttentionFiltered(ctx, "lib", "", 1, false)
	if err != nil || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatal(page, err)
	}
}
