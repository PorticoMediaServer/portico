package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func revokeHomeActionOwnerSession(t *testing.T, f *searchPrivacyFixture, p identity.Principal) {
	t.Helper()
	ctx := context.Background()
	var family identity.FamilyState
	if err := dbwork.WithReadSnapshot(ctx, f.db, func(ctx context.Context) error {
		var err error
		family, err = f.d.Identity.SessionFamilyTx(ctx, dbwork.Snapshot(ctx), p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Use the genuine sign-out transaction and its family-retirement hooks.
	// authorization_access is a read projection, never a mutation surface.
	if err := f.d.Identity.LogoutToken(ctx, f.token); err != nil {
		t.Fatal(err)
	}
	var revoked, projected bool
	var reason, at string
	if err := f.db.QueryRow(`SELECT f.revoked,f.revoked_reason,f.revoked_at,a.revoked
	 FROM authorization_session_families f JOIN authorization_family_tokens token ON token.family_id=f.id
	 JOIN authorization_access a ON a.hash=token.token_hash WHERE f.id=? AND token.token_hash=?`, family.ID, p.Hash).Scan(&revoked, &reason, &at, &projected); err != nil {
		t.Fatal(err)
	}
	if !revoked || !projected || reason != string(identity.RevokedExplicitSignout) || at == "" {
		t.Fatalf("sign-out did not revoke the actual family/view: revoked=%v projected=%v reason=%q at=%q", revoked, projected, reason, at)
	}
	if _, err := f.d.Identity.Authenticate(f.token); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("signed-out owner still authenticates: %v", err)
	}
	if _, err := f.d.Identity.Authenticate(f.kids.AccessToken); err != nil {
		t.Fatalf("owner sign-out incorrectly revoked the independent member family: %v", err)
	}
}

func TestHomeActionAuthorityUsesLiveOwnerAndUnavailableServices(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	d := f.d
	d.Metadata = metadata.New(f.db, "")
	d.Administration = administration.New(f.db)
	d.Scheduler = operations.NewScheduler(operations.New(f.db))
	d.v1 = d.PlaybackV1
	owner, err := d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	member, err := d.Identity.Authenticate(f.kids.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p identity.Principal) catalog.HomeActionAuthority {
		t.Helper()
		var authority catalog.HomeActionAuthority
		if err := dbwork.WithReadSnapshot(context.Background(), f.db, func(ctx context.Context) error {
			var e error
			authority, e = d.homeActionAuthority(ctx, p)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return authority
	}
	if a := read(owner); !a.Personal || !a.Metadata || !a.Refresh || a.DeleteLibrary == nil || !a.Organize || !a.Playback || !a.Queue || a.Downloads {
		t.Fatalf("owner/service authority: %+v", a)
	}
	if a := read(member); !a.Personal || !a.Playback || a.Metadata || a.Refresh || a.DeleteLibrary != nil {
		t.Fatalf("member authority: %+v", a)
	}
	// A Hosted-shaped owner claim cannot reuse a valid local token's rights.
	hosted := owner
	hosted.Authority = "hosted"
	if a := read(hosted); a.Personal || a.Metadata || a.Refresh || a.DeleteLibrary != nil || a.Playback {
		t.Fatalf("role string granted Hosted authority: %+v", a)
	}
	d.Downloads, err = downloads.New(downloads.Options{DB: f.db})
	if err != nil {
		t.Fatal(err)
	}
	if a := read(member); !a.Downloads {
		t.Fatal("allowed download service omitted")
	}
	policy, err := d.Identity.ProfileRestrictions(context.Background(), f.token, member.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Identity.SetProfileRestrictions(context.Background(), f.token, member.ProfileID, identity.ProfileRestrictionEdit{
		ExpectedRevision: policy.Revision, RatingSystem: policy.RatingSystem, MaximumAgeRating: policy.MaximumAgeRating, AllowUnrated: policy.AllowUnrated, BlockedLabels: policy.BlockedLabels,
		AllowDownloads: false, AllowLiveTV: policy.AllowLiveTV, AllowDVR: policy.AllowDVR, AllowWatchTogether: policy.AllowWatchTogether,
	}); err != nil {
		t.Fatal(err)
	}
	if a := read(member); a.Downloads {
		t.Fatal("download revocation retained a grant")
	}
	d.Metadata = nil
	d.v1 = nil
	if a := read(owner); a.Metadata || a.Refresh || a.Playback || a.Queue {
		t.Fatalf("unavailable services advertised: %+v", a)
	}
	revokeHomeActionOwnerSession(t, f, owner)
	if _, err := d.homeActionFenceAfter(httptest.NewRequest("GET", "/v1/home", nil), owner); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("post-composition family proof accepted revocation: %v", err)
	}
	if err := dbwork.WithReadSnapshot(context.Background(), f.db, func(ctx context.Context) error {
		_, err := d.homeActionAuthority(ctx, owner)
		return err
	}); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("revoked session was not rejected: %v", err)
	}
}

func TestHomeActionHTTPRecomposesLibraryPolicyAndWithholdsPrivateOffers(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	f.d.Metadata = metadata.New(f.db, "")
	f.d.Scheduler = operations.NewScheduler(operations.New(f.db))
	f.d.Administration = administration.New(f.db)
	f.h = New(f.d)
	manifest := homeAPIRequest(t, f.h, f.token, "GET", "/v1/home?limit=12", "")
	if manifest.Code != http.StatusOK {
		t.Fatalf("Home spotlight %d: %s", manifest.Code, manifest.Body.String())
	}
	var document catalog.HomeDocument
	if err := json.Unmarshal(manifest.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Spotlight) == 0 {
		t.Fatal("fixture returned no direct spotlight entries")
	}
	for _, candidate := range document.Spotlight {
		if candidate.Entry.HomeActions == nil || !candidate.Entry.HomeActions.MyList || !candidate.Entry.HomeActions.EditMetadata {
			t.Fatalf("direct spotlight grants: %+v", candidate.Entry)
		}
	}
	read := func(token string, ifNoneMatch string) (catalog.HomeRow, string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/home/rows/new_on_server?limit=12", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if ifNoneMatch != "" {
			r.Header.Set("If-None-Match", ifNoneMatch)
		}
		req := httptest.NewRecorder()
		f.h.ServeHTTP(req, r)
		if req.Code != http.StatusOK {
			t.Fatalf("Home row %d: %s", req.Code, req.Body.String())
		}
		if req.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("command offers may be reused without recomposition")
		}
		var row catalog.HomeRow
		if err := json.Unmarshal(req.Body.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		return row, req.Body.String()
	}
	before, _ := read(f.token, "")
	if before.ViewerFence != document.ViewerFence || before.Revision != document.Revision {
		t.Fatal("spotlight and row did not retain the same Home authority/revision")
	}
	if len(before.Entries) == 0 {
		t.Fatal("fixture returned no Home media")
	}
	for _, e := range before.Entries {
		if e.HomeActions == nil || !e.HomeActions.EditMetadata || e.HomeActions.Delete {
			t.Fatalf("initial owner offers: %+v", e.HomeActions)
		}
	}
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	config, err := f.d.Administration.LibrarySettingsFor(context.Background(), f.d.administrationAuthority(p), "movies")
	if err != nil {
		t.Fatal(err)
	}
	config.Settings.AllowMediaDeletion = true
	if _, err = f.d.Administration.SaveLibrarySettings(context.Background(), f.d.administrationAuthority(p), "movies", administration.Change[administration.LibrarySettings]{ExpectedRevision: config.Revision, OperationID: "home-offer-deletion-policy", Settings: config.Settings}); err != nil {
		t.Fatal(err)
	}
	after, _ := read(f.token, `"old-home-validator"`)
	for _, e := range after.Entries {
		if e.LibraryID == "movies" && (e.HomeActions == nil || !e.HomeActions.Delete) {
			t.Fatalf("new library grant missing: %+v", e)
		}
	}
	f.restrictLiveTV(false)
	kid, raw := read(f.kids.AccessToken, "")
	for _, e := range kid.Entries {
		if e.HomeActions == nil || e.HomeActions.EditMetadata || e.HomeActions.Delete || e.LibraryID != "movies" || strings.Contains(e.Title, "Adult") || strings.Contains(e.Title, "Private") {
			t.Fatalf("restricted/member offers: %+v", e)
		}
	}
	for _, secret := range []string{"Northwind Adult", "Northwind Private", "/private/", "filePath", "apiKey", "password"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("private Home data leaked: %s", secret)
		}
	}
	revokeHomeActionOwnerSession(t, f, p)
	w := homeAPIRequest(t, f.h, f.token, "GET", "/v1/home/rows/new_on_server", "")
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "homeActions") {
		t.Fatalf("revoked Home returned offers: %d %s", w.Code, w.Body.String())
	}
}

func TestHomeChannelOffersUseActualGuideCommandsAndFeatureAuthority(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	w := homeAPIRequest(t, f.h, f.token, "GET", "/v1/home/rows/on_now?limit=2", "")
	if w.Code != http.StatusOK {
		t.Fatalf("On now %d: %s", w.Code, w.Body.String())
	}
	var row catalog.HomeRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Live == nil || len(row.Live.Channels) == 0 {
		t.Fatal("no actual guide channels")
	}
	for _, card := range row.Live.Channels {
		if card.Channel.SourceID == "" || card.Channel.ID == "" || !card.Actions.Favorite || card.Actions.Play || card.Channel.TuneAvailable {
			t.Fatalf("channel offers/identity: %+v", card)
		}
		encoded, err := json.Marshal(card.Actions)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != `{"play":false,"favorite":true}` {
			t.Fatalf("catalogue action fabricated for a channel: %s", encoded)
		}
	}
	f.restrictLiveTV(false)
	w = homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/home/rows/on_now?limit=2", "")
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "channel") || strings.Contains(w.Body.String(), "favorite") {
		t.Fatalf("denied channel offers leaked: %d %s", w.Code, w.Body.String())
	}
}

func TestHomeActionAggregateDeleteUsesActualPreviewAndCurrentOwnerPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSearchPrivacyFixture(t)
	ctx := context.Background()
	f.d.Administration = administration.New(f.db)
	f.d.Scheduler = operations.NewScheduler(operations.New(f.db))
	tv := f.c.Library("tv", "Shows", "tv", "/tv")
	show := f.c.Show(tv, "Long series", 2026)
	season := f.c.Season(show, 1)
	secondSeason := f.c.Season(show, 2)
	var first catalogtest.Item
	for number := 1; number <= administration.MaxDeleteTargets+1; number++ {
		targetSeason := season
		if number%2 == 0 {
			targetSeason = secondSeason
		}
		item := f.c.Episode(show, targetSeason, number, fmt.Sprintf("/tv/series/episode-%d.mkv", number))
		if number == 1 {
			first = item
		}
	}
	f.c.Drain()
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	auth := f.d.administrationAuthority(p)
	config, err := f.d.Administration.LibrarySettingsFor(ctx, auth, "tv")
	if err != nil {
		t.Fatal(err)
	}
	savePolicy := func(allow bool, operation string) {
		t.Helper()
		config.Settings.AllowMediaDeletion = allow
		config, err = f.d.Administration.SaveLibrarySettings(ctx, auth, "tv", administration.Change[administration.LibrarySettings]{ExpectedRevision: config.Revision, OperationID: operation, Settings: config.Settings})
		if err != nil {
			t.Fatal(err)
		}
	}
	savePolicy(true, "home-aggregate-delete-allowed")
	f.h = New(f.d)
	readShow := func() catalog.ContentEntry {
		t.Helper()
		w := homeAPIRequest(t, f.h, f.token, "GET", "/v1/home/rows/new_on_server?limit=12", "")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("aggregate Home read %d: %s", w.Code, w.Body.String())
		}
		var row catalog.HomeRow
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		for _, entry := range row.Entries {
			if entry.ID == show.Public {
				return entry
			}
		}
		t.Fatal("real aggregate absent from bounded Home row")
		return catalog.ContentEntry{}
	}
	entry := readShow()
	if entry.Kind != "show" || entry.HomeActions == nil || !entry.HomeActions.Delete || !entry.HomeActions.Selection.Delete {
		t.Fatalf("genuine aggregate delete offer missing: %+v", entry)
	}
	w := homeAPIRequest(t, f.h, f.token, "POST", "/v1/items/"+entry.ID+"/delete/preview", "")
	if w.Code != http.StatusOK {
		t.Fatalf("aggregate preview %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Result administration.DeletePreview `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	preview := envelope.Result
	if len(preview.Targets) != 1 || preview.Targets[0].ItemID != show.Public || preview.Targets[0].Titles != administration.MaxDeleteTargets+1 || preview.ConfirmationKind != "title" || preview.Confirmation != "Long series" || len(preview.Blocked) != 0 {
		t.Fatalf("preview replaced the displayed target or imposed a 200-descendant cap: %+v", preview)
	}
	// Two hundred is the explicit displayed-target cap, independently of an
	// aggregate's MaxDeleteMembers. The exact preview enforces both limits.
	ids := make([]string, administration.MaxDeleteTargets+1)
	for index := range ids {
		ids[index] = fmt.Sprintf("displayed-%d", index)
	}
	if _, err := f.d.Administration.PreviewDelete(ctx, auth, ids); !errors.Is(err, administration.ErrInput) {
		t.Fatalf("over-200 displayed targets were not refused: %v", err)
	}
	// An offer is not a preview receipt. Current authority/policy, exact title
	// confirmation and revision still gate the real mutation.
	request := administration.DeleteRequest{ItemIDs: []string{show.Public}, DeleteFiles: false, Confirmation: "wrong", ExpectedRevision: preview.Revision, OperationID: "home-delete-wrong-title"}
	if _, err := f.d.Administration.Delete(ctx, auth, administrationActor(p), request); !errors.Is(err, administration.ErrConfirmation) {
		t.Fatalf("wrong aggregate confirmation accepted: %v", err)
	}
	request.OperationID, request.Confirmation, request.ExpectedRevision = "home-delete-stale-preview", preview.Confirmation, preview.Revision+1
	if _, err := f.d.Administration.Delete(ctx, auth, administrationActor(p), request); !errors.Is(err, administration.ErrConflict) {
		t.Fatalf("stale aggregate preview accepted: %v", err)
	}
	savePolicy(false, "home-aggregate-delete-revoked")
	if a := readShow().HomeActions; a == nil || a.Delete || a.Selection.Delete {
		t.Fatalf("revoked library policy retained aggregate offers: %+v", a)
	}
	request.OperationID, request.ExpectedRevision = "home-delete-policy-revoked", preview.Revision
	if _, err := f.d.Administration.Delete(ctx, auth, administrationActor(p), request); !errors.Is(err, administration.ErrDenied) {
		t.Fatalf("old offer bypassed current library deletion policy: %v", err)
	}
	member, err := f.d.Identity.Authenticate(f.kids.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Administration.PreviewDelete(ctx, f.d.administrationAuthority(member), []string{show.Public}); !errors.Is(err, identity.ErrUnauthorized) && !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member preview accepted owner command: %v", err)
	}
	// Every refused command left actual descendants untouched.
	var present bool
	if err := f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?))`, first.Public).Scan(&present); err != nil || !present {
		t.Fatalf("refused aggregate delete changed a child: %v %v", present, err)
	}
	revokeHomeActionOwnerSession(t, f, p)
	if _, err := f.d.Administration.PreviewDelete(ctx, auth, []string{show.Public}); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("revoked owner retained actual preview authority: %v", err)
	}
	w = homeAPIRequest(t, f.h, f.token, "GET", "/v1/home/rows/new_on_server?limit=12", "")
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "homeActions") {
		t.Fatalf("revoked owner retained aggregate Home offers: %d %s", w.Code, w.Body.String())
	}
}

func TestHomeActionAggregateAvailabilityRetainsSnapshotAndCurrentRestrictionFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSearchPrivacyFixture(t)
	ctx := context.Background()
	tv := f.c.Library("tv", "Shows", "tv", "/tv")
	show := f.c.Show(tv, "Permitted series", 2026)
	f.c.Genres(show.ID, "tmdb", "Drama", "Mystery", "Crime")
	season := f.c.Season(show, 1)
	secondSeason := f.c.Season(show, 2)
	unavailable := f.c.Episode(show, season, 1, "/tv/permitted-unavailable.mkv")
	f.c.Attributes(unavailable.ID, "contentRating", "G")
	secondUnavailable := f.c.Episode(show, secondSeason, 1, "/tv/permitted-unavailable-second-season.mkv")
	f.c.Attributes(secondUnavailable.ID, "contentRating", "G")
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetAssetAvailableTx(ctx, tx, unavailable.Asset, false); err != nil {
			return err
		}
		return compactcatalog.SetAssetAvailableTx(ctx, tx, secondUnavailable.Asset, false)
	})
	hiddenIDs := []string{}
	for number := 2; number <= 70; number++ {
		hidden := f.c.Episode(show, season, number, fmt.Sprintf("/tv/hidden-%d.mkv", number))
		f.c.Attributes(hidden.ID, "contentRating", "R")
		hiddenIDs = append(hiddenIDs, hidden.Public)
	}
	permitted := f.c.Episode(show, secondSeason, 71, "/tv/permitted-available.mkv")
	f.c.Attributes(permitted.ID, "contentRating", "G")
	f.c.Drain()
	if _, err := f.d.Catalog.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	f.c.Drain()
	f.c.Exec(`UPDATE direct_profiles SET allowed_libraries='["movies","tv"]',revision=revision+1 WHERE id=?`, f.kids.Viewer.ProfileID)
	f.restrictLiveTV(false)
	personal := identity.PersonalKey(f.kids.Viewer)
	f.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,120000,0,'home-context-progress')`, personal, permitted.ID)
	f.c.Exec(`INSERT INTO progress_activity VALUES(?,'tv',?,'2026-10-22T00:00:00.000Z','paused')`, personal, permitted.ID)
	f.c.Drain()
	f.h = New(f.d)
	var earlier int
	if err := f.db.QueryRow(`SELECT count(DISTINCT item_id) FROM catalog_browse_memberships WHERE entity_id=? AND item_id<?`, show.ID, permitted.ID).Scan(&earlier); err != nil || earlier != 71 {
		t.Fatalf("permitted member is not behind 71 earlier members: %d %v", earlier, err)
	}
	read := func(wantPresent bool) catalog.HomeRow {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/home", nil)
		r.Header.Set("Authorization", "Bearer "+f.kids.AccessToken)
		_, request, err := f.d.homeRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		if !request.Viewer.AllowsLibrary("tv") || !request.Restrictions.BlockUnrated || request.Restrictions.MaximumAge == nil {
			t.Fatalf("fixture did not resolve genuine restricted TV authority: %+v", request.Viewer)
		}
		// Visibility alone admits the unavailable G-rated children and their
		// show. Recently Added additionally requires a permitted available
		// addition; it does not retain a work just because it is visible.
		for _, item := range []catalogtest.Item{unavailable, secondUnavailable} {
			if err := f.d.Catalog.VisibleItem(ctx, request.Viewer, item.Public); err != nil {
				t.Fatalf("permitted unavailable child lost visibility: %v", err)
			}
		}
		if err := f.d.Catalog.VisibleEntity(ctx, request.Viewer, "show", show.Public); err != nil {
			t.Fatalf("show with permitted unavailable children lost visibility: %v", err)
		}
		if err := f.d.Catalog.VisibleItem(ctx, request.Viewer, hiddenIDs[0]); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("R-rated available child is not restricted: %v", err)
		}
		w := homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/home/rows/new_on_server?limit=12", "")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("restricted Home read %d: %s", w.Code, w.Body.String())
		}
		var row catalog.HomeRow
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.ViewerFence != request.ViewerFence {
			t.Fatal("row did not retain current authorized restriction fence")
		}
		found := 0
		for _, entry := range row.Entries {
			if entry.LibraryID != "tv" {
				continue
			}
			found++
			// Exactly one permitted available addition means the real episode,
			// not a fabricated show/season card or its unavailable sibling.
			if entry.ID != permitted.Public || entry.Kind != "episode" || entry.Title != "Episode 71" || entry.Subtitle != "Permitted series" || entry.EpisodeNumber == nil || *entry.EpisodeNumber != 71 || entry.SeasonNumber == nil || *entry.SeasonNumber != 2 || entry.ContentRating != "G" || entry.Available == nil || !*entry.Available || entry.Playback == nil || entry.Playback.ItemID != permitted.Public {
				t.Fatalf("recent addition is not the actual permitted episode: %+v", entry)
			}
			if a := entry.HomeActions; a == nil || !a.Play || !a.Queue || !a.Selection.Play || !a.MyList || a.Delete || a.Selection.Delete || a.EditMetadata {
				t.Fatalf("permitted member offers: %+v", a)
			}
			parent := entry.HomeTitleContext
			if parent == nil || parent.Kind != "show" || parent.Year == nil || *parent.Year != 2026 || len(parent.Genres) != 2 || parent.Genres[0] != "Drama" || parent.Genres[1] != "Mystery" || parent.SeasonCount == nil || *parent.SeasonCount != 2 {
				t.Fatalf("episode did not carry authorized parent facts/visible seasons: %+v", parent)
			}
		}
		want := 0
		if wantPresent {
			want = 1
		}
		if found != want {
			t.Fatalf("permitted available additions: got %d want %d; row=%+v", found, want, row)
		}
		continued := homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/home/rows/continue?limit=12", "")
		if continued.Code != http.StatusOK {
			t.Fatalf("actual Continue Watching read %d: %s", continued.Code, continued.Body.String())
		}
		var continuation catalog.HomeRow
		if err := json.Unmarshal(continued.Body.Bytes(), &continuation); err != nil {
			t.Fatal(err)
		}
		if len(continuation.Entries) != want || continuation.ViewerFence != row.ViewerFence || continuation.Revision != row.Revision {
			t.Fatalf("Continue Watching did not follow current episode availability/policy: %+v", continuation)
		}
		if wantPresent {
			entry := continuation.Entries[0]
			parent := entry.HomeTitleContext
			if entry.ID != permitted.Public || entry.Kind != "episode" || entry.Playback == nil || entry.Playback.ItemID != permitted.Public || parent == nil || parent.Year == nil || *parent.Year != 2026 || len(parent.Genres) != 2 || parent.Genres[0] != "Drama" || parent.Genres[1] != "Mystery" || parent.SeasonCount == nil || *parent.SeasonCount != 2 {
				t.Fatalf("Continue Watching fabricated/inferred parent facts: %+v parent=%+v", entry, parent)
			}
		}
		for _, secret := range append(append([]string{}, hiddenIDs...), unavailable.Public, secondUnavailable.Public, "/tv/hidden-", "filePath", "sourcePath") {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("restricted Home leaked hidden availability evidence: %s", secret)
			}
		}
		if wantPresent {
			// The manifest deliberately promotes this episode to its displayed
			// show. Its resume child and visible season count must remain exact.
			w := homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/home?limit=12", "")
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("restricted spotlight %d: %s", w.Code, w.Body.String())
			}
			var document catalog.HomeDocument
			if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.ViewerFence != row.ViewerFence || document.Revision != row.Revision {
				t.Fatal("spotlight and row disagree on the current Home fence/revision")
			}
			shows := 0
			for _, candidate := range document.Spotlight {
				if candidate.Entry.ID != show.Public {
					continue
				}
				shows++
				entry, target := candidate.Entry, candidate.ResumeTarget
				if entry.Kind != "show" || entry.LibraryID != "tv" || entry.SeasonCount == nil || *entry.SeasonCount != 1 || entry.HomeActions == nil || !entry.HomeActions.Play || !entry.HomeActions.Selection.Play || entry.HomeActions.Delete || target == nil || target.ID != permitted.Public || target.Playback == nil || target.Playback.ItemID != permitted.Public || target.HomeActions == nil || !target.HomeActions.Play {
					t.Fatalf("spotlight borrowed hidden seasons/availability or changed identity: %+v", candidate)
				}
			}
			if shows != 1 {
				t.Fatalf("actual recent episode did not produce one displayed show spotlight: %d", shows)
			}
			for _, secret := range append(append([]string{}, hiddenIDs...), "/tv/hidden-", "filePath", "sourcePath") {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("restricted spotlight leaked hidden evidence: %s", secret)
				}
			}
		}
		return row
	}
	// No visibility class is published by the compact worker. Exercise its
	// genuine cold source first, then the same source through a published
	// class. Neither may combine unavailable G children with available R ones.
	var classes, datedMembers int
	if err := f.db.QueryRow(`SELECT count(*) FROM compact_visibility_classes`).Scan(&classes); err != nil || classes != 0 {
		t.Fatalf("fixture did not start with an unpublished visibility class: %d %v", classes, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_browse_member_metrics WHERE entity_id=? AND added IS NOT NULL`, show.ID).Scan(&datedMembers); err != nil || datedMembers != 70 {
		t.Fatalf("drained Recent source lost its available dated members: %d %v", datedMembers, err)
	}
	before := read(true) // The only permitted available member is after 64.
	r := httptest.NewRequest("GET", "/v1/home", nil)
	r.Header.Set("Authorization", "Bearer "+f.kids.AccessToken)
	_, current, err := f.d.homeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Catalog.RebuildVisibilityClass(ctx, "tv", current.Restrictions); err != nil {
		t.Fatal(err)
	}
	published := read(true)
	if published.ViewerFence != before.ViewerFence || published.Revision != before.Revision {
		t.Fatal("publishing the visibility class changed current Home authority/content")
	}
	p, err := f.d.Identity.Authenticate(f.kids.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, permitted.Asset, false)
	})
	f.c.Drain()
	afterUnavailable := read(false)
	if afterUnavailable.Revision.Catalog == before.Revision.Catalog {
		t.Fatal("availability change did not advance the returned Home revision")
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, permitted.Asset, true)
	})
	f.c.Drain()
	afterRestored := read(true)
	if afterRestored.ViewerFence != before.ViewerFence || afterRestored.Revision.Catalog == afterUnavailable.Revision.Catalog {
		t.Fatal("physical availability restoration did not retain policy and advance content revision")
	}
	policy, err := f.d.Identity.ProfileRestrictions(ctx, f.token, p.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Identity.SetProfileRestrictions(ctx, f.token, p.ProfileID, identity.ProfileRestrictionEdit{
		ExpectedRevision: policy.Revision, RatingSystem: policy.RatingSystem, MaximumAgeRating: policy.MaximumAgeRating, AllowUnrated: policy.AllowUnrated, BlockedLabels: []string{"newly-blocked"},
		AllowDownloads: policy.AllowDownloads, AllowLiveTV: policy.AllowLiveTV, AllowDVR: policy.AllowDVR, AllowWatchTogether: policy.AllowWatchTogether,
	}); err != nil {
		t.Fatal(err)
	}
	f.c.Attributes(permitted.ID, "label", "newly-blocked")
	f.c.Drain()
	afterRestriction := read(false)
	if afterRestriction.ViewerFence == before.ViewerFence {
		t.Fatal("restriction revocation retained the prior Home fence")
	}
	request := httptest.NewRequest("GET", "/v1/home", nil)
	request.Header.Set("Authorization", "Bearer "+f.kids.AccessToken)
	fence, err := f.d.homeActionFenceAfter(request, p)
	if err != nil || fence != afterRestriction.ViewerFence || fence == before.ViewerFence {
		t.Fatalf("post-read proof did not retain current restriction fence: %s %v", fence, err)
	}
}
