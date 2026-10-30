package catalog

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func savedFixture(t *testing.T) (*Service, *sql.DB, identity.Viewer) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("library", "Library", "movie", "/fixture")
	c.Movie(library, "/fixture/item.mkv", "Movie", 2020)
	c.Drain()
	return New(c.DB), c.DB, identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}
}

func phase34SavedFixtureItem(t *testing.T, db *sql.DB) catalogtest.Item {
	t.Helper()
	var item catalogtest.Item
	if err := db.QueryRow(`SELECT l.entity_id,a.id,a.token FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.path='/fixture/item.mkv'`).Scan(&item.ID, &item.Asset, &item.Token); err != nil {
		t.Fatal(err)
	}
	item.Public = catalogtest.New(t, db).Public(item.ID)
	return item
}

func TestPersonalQualifiedScopeConflictResolutionAndReplay(t *testing.T) {
	s, _, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, s.db)
	key := identity.PersonalKey(v)
	yes, no := true, false
	offline := func(device string, watched *bool) PersonalMutation {
		return PersonalMutation{OperationID: device, ExpectedRevision: 0, Watched: watched, Offline: &PersonalOfflineMutation{DeviceID: device, DeviceMutationID: device, Sequence: 1, BaseRevision: 0, AuthoredAt: time.Now().UTC().Format(time.RFC3339)}}
	}
	first, e := s.SetPersonal("local:account", key, item.Public, offline("device-a", &yes), nil)
	if e != nil || !first.Watched {
		t.Fatal(first, e)
	}
	conflict, e := s.SetPersonal("local:account", key, item.Public, offline("device-b", &no), nil)
	if e != nil || conflict.Watched || conflict.Status != "needs-resolution" || len(conflict.Conflicts) != 1 || len(conflict.Conflicts[0].Choices) != 2 {
		t.Fatal(conflict, e)
	}
	resolve := PersonalMutation{OperationID: "resolve", ExpectedRevision: conflict.Revision, Resolution: &PersonalResolution{ConflictID: conflict.Conflicts[0].ID, ChoiceOperationID: "device-a"}}
	resolved, e := s.SetPersonal("local:account", key, item.Public, resolve, nil)
	if e != nil || !resolved.Watched || resolved.Status != "current" {
		t.Fatal(resolved, e)
	}
	retry, e := s.SetPersonal("local:account", key, item.Public, resolve, nil)
	if e != nil || retry.Revision != resolved.Revision {
		t.Fatal(retry, e)
	}
	v.Authority = "hosted"
	other, e := s.Personal(identity.PersonalKey(v), item.Public)
	if e != nil || other.Watched || other.Revision != 0 {
		t.Fatal("scope leakage", other, e)
	}
}
func TestPersonalManualFenceAndIndependentHistoryClear(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); e != nil {
		t.Fatal(e)
	}
	evidence := func(seq int64, pos float64) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = s.AcceptPlaybackProgress(tx, v, "play", item.Public, seq, pos, 100, "playing", true); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	evidence(1, 40)
	p, e := s.Personal(key, item.Public)
	if e != nil || p.ProgressSeconds != 40 || p.Watched {
		t.Fatal(p, e)
	}
	yes := true
	p, e = s.SetPersonal("local:account", key, item.Public, PersonalMutation{OperationID: "manual", ExpectedRevision: p.Revision, Watched: &yes}, nil)
	if e != nil {
		t.Fatal(e)
	}
	evidence(2, 50)
	p, e = s.Personal(key, item.Public)
	if e != nil || !p.Watched || p.ProgressSeconds != 0 {
		t.Fatal("old writer overwrote manual choice", p, e)
	}
	var revision int64
	if e = db.QueryRow(`SELECT revision FROM personal_activity_revisions WHERE profile_id=?`, key).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	noop := func(*sql.Tx) error { return nil }
	if _, e = s.MutateActivity(key, ActivityMutation{OperationID: "clear", ExpectedRevision: revision, Action: "clear-history"}, noop); e != nil {
		t.Fatal(e)
	}
	p, e = s.Personal(key, item.Public)
	if e != nil || !p.Watched {
		t.Fatal("history clear reset watched", p, e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id=?`, key).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}
func TestSavedCollectionStableSetMembershipAndCurrentRoles(t *testing.T) {
	s, _, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, s.db)
	a := ResourceActor{Authority: v.Authority, AccountID: v.AccountID, ProfileID: v.ProfileID}
	name := "Collection"
	noop := func(*sql.Tx) error { return nil }
	create := SavedResourceMutation{OperationID: "create", Kind: "collection", Name: &name}
	receipt, e := s.MutateSavedResource(a, "", "create", create, noop)
	if e != nil {
		t.Fatal(e)
	}
	added, e := s.MutateSavedResource(a, receipt.ResourceID, "entries", SavedResourceMutation{OperationID: "add", ExpectedRevision: receipt.Revision, AddItemIDs: []string{item.Public, item.Public}}, noop)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.SavedResourceContent(Viewer{Libraries: []string{"library"}}, "server", "fence", receipt.ResourceID, a, ResourceContentQuery{}, "", 40)
	if e != nil || len(first.Entries) != 1 {
		t.Fatal(first, e)
	}
	added, e = s.MutateSavedResource(a, receipt.ResourceID, "entries", SavedResourceMutation{OperationID: "add-again", ExpectedRevision: added.Revision, AddItemIDs: []string{item.Public}}, noop)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.SavedResourceContent(Viewer{Libraries: []string{"library"}}, "server", "fence", receipt.ResourceID, a, ResourceContentQuery{}, "", 40)
	if e != nil || len(second.Entries) != 1 || second.Entries[0].ID != first.Entries[0].ID {
		t.Fatal(second, e)
	}
	other := ResourceActor{Authority: "local", AccountID: "other", ProfileID: "other"}
	if _, e = s.MutateSavedResource(other, receipt.ResourceID, "delete", SavedResourceMutation{OperationID: "denied", ExpectedRevision: added.Revision}, noop); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("unshared actor admitted", e)
	}
	if _, e = s.MutateSavedResource(a, receipt.ResourceID, "delete", SavedResourceMutation{OperationID: "delete", ExpectedRevision: added.Revision}, noop); e != nil {
		t.Fatal(e)
	}
	replay, e := s.MutateSavedResource(a, "", "create", create, noop)
	if e != nil || replay.ResourceID != receipt.ResourceID || replay.Deleted {
		t.Fatal("original receipt changed", replay, e)
	}
	deleted, _, e := s.SavedResourceReceiptStatus(receipt.ResourceID, a)
	if e != nil || !deleted {
		t.Fatal("replay resurrected resource", e)
	}
}

func TestHomeRemovedTitleIsPersonalPolicyFencedAndOnlyActualDeletion(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(lib, "/movies/gone.mkv", "Gone title", 2026)
	c.Attributes(item.ID, "contentRating", "R")
	c.Attributes(item.ID, "label", "adult")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17) ON CONFLICT DO UPDATE SET minimum_age=17`)
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id)VALUES('p',?,1000,0,'play')`, item.ID)
	c.Exec(`INSERT INTO progress_activity VALUES('p','movies',?,'2026-10-22T00:00:00.000Z','paused')`, item.ID)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}
	if _, err := s.RemovedTitle(v, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("a live/missing title became removed", err)
	}
	c.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, item.ID)
	if _, err := s.RemovedTitle(v, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("source outage became deletion", err)
	}
	c.Delete(item.ID)
	removed, err := s.RemovedTitle(v, item.Public)
	if err != nil || removed.Title != "Gone title" || removed.Kind != "movie" || !removed.CanRemoveFromContinue {
		t.Fatal(removed, err)
	}

	row, err := s.HomeSingleRow(HomeRequest{Viewer: v}, "continue", HomeRowPage{Limit: 1})
	if err != nil || len(row.Entries) != 1 || !row.Entries[0].Removed || row.Entries[0].Title != "Gone title" || row.Entries[0].Playback != nil {
		t.Fatal("gone title disappeared from Continue", row, err)
	}
	if err = s.DismissRemovedTitle(v, item.Public, func(*sql.Tx) error { return identity.ErrForbidden }); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("missing current-authority write fence", err)
	}
	other := v
	other.Profile = "other"
	if _, err = s.RemovedTitle(other, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("another profile disclosed a private reference", err)
	}
	restricted := v
	age := 13
	restricted.Restrictions = identity.ContentRestrictions{MaximumAge: &age}
	if _, err = s.RemovedTitle(restricted, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("age-restricted removed title disclosed", err)
	}
	restricted.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	if _, err = s.RemovedTitle(restricted, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("blocked label disclosed", err)
	}
	revoked := v
	revoked.Libraries = nil
	if _, err = s.RemovedTitle(revoked, item.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked library disclosed", err)
	}
	if err = s.DismissRemovedTitle(other, item.Public, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("other profile could dismiss", err)
	}
	if err = s.DismissRemovedTitle(v, item.Public, nil); err != nil {
		t.Fatal(err)
	}

	row, err = s.HomeSingleRow(HomeRequest{Viewer: v}, "continue", HomeRowPage{Limit: 1})
	if err != nil || len(row.Entries) != 0 {
		t.Fatal("dismissed title remains in Continue", row, err)
	}
	if title, err := s.RemovedTitle(v, item.Public); err != nil || title.CanRemoveFromContinue {
		t.Fatal("dismissal lost the stale-link name or kept Continue", title, err)
	}
}

func TestHomeSavedSummaryAndShapePartitionsUseCurrentViewer(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	music := c.Library("music", "Music", "music", "/music")
	movie := c.Movie(movies, "/movies/a.mkv", "A", 2026)
	denied := c.Movie(movies, "/movies/denied.mkv", "Denied", 2026)
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2026)
	song := c.Song(album, 1, "/music/song.flac", "Song")
	a := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	profile := actorKey(a)
	for _, item := range []catalogtest.Item{movie, denied, song} {
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision)VALUES(?,?,1,1,1)`, profile, item.ID)
	}
	c.Attributes(denied.ID, "label", "adult")
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: profile, Fence: "f", Libraries: []string{"movies", "music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"adult"}}}
	summary, err := s.SavedSummary("server", v, a)
	if err != nil || summary.MyList.Posters != 1 || summary.MyList.Squares != 1 || summary.Favorites.Posters != 1 || summary.Favorites.Squares != 1 {
		t.Fatal(summary, err)
	}
	for _, shape := range []string{"posters", "squares"} {
		page, e := s.Saved(ContentRequest{Viewer: v, ServerID: "server", View: "watchlist", Shape: shape, Limit: 1}, v.Libraries)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Sections) != 1 || len(page.Sections[0].Entries) != 1 {
			t.Fatal("shape page missing", page)
		}
		want := movie.Public
		if shape == "squares" {
			want = song.Public
		}
		if page.Sections[0].Entries[0].ID != want {
			t.Fatal("mixed shape server page", page.Sections[0].Entries)
		}
	}
	other := v
	other.Profile = "other"
	summary, err = s.SavedSummary("server", other, ResourceActor{Authority: "local", AccountID: "other", ProfileID: "other"})
	if err != nil || summary.MyList.Posters != 0 || summary.MyList.Squares != 0 {
		t.Fatal("cross-profile counts", summary, err)
	}
}

func TestHomeSavedResourceCardsCarryCoversCountsAndShapeBoundCursor(t *testing.T) {
	s, db, viewer := savedFixture(t)
	c := catalogtest.New(t, db)
	movie := phase34SavedFixtureItem(t, db)
	c.Fields(movie.ID, map[string]any{"poster_url": "/allowed-poster"})
	a := ResourceActor{Authority: viewer.Authority, AccountID: viewer.AccountID, ProfileID: viewer.ProfileID}
	v := Viewer{Profile: identity.PersonalKey(viewer), Fence: "f", Libraries: []string{"library"}}
	for _, name := range []string{"Alpha", "Beta"} {
		made, err := s.MutateSavedResource(a, "", "create", SavedResourceMutation{OperationID: "create" + name, Kind: "collection", Name: &name}, allow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.MutateSavedResource(a, made.ResourceID, "entries", SavedResourceMutation{OperationID: "fill" + name, ExpectedRevision: 1, AddItemIDs: []string{movie.Public}}, allow); err != nil {
			t.Fatal(err)
		}
	}
	c.Drain()
	page, err := s.SavedResourcesForViewer("server", v, a, "collection", "", false, 1, "posters", nil)
	if err != nil || len(page.Resources) != 1 || page.ShapeCounts.Posters != 2 || page.ShapeCounts.Squares != 0 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	card := page.Resources[0]
	if len(card.Samples) != 1 || card.Samples[0].ID != movie.Public || len(card.ArtworkPaths) != 1 || card.ArtworkPaths[0] != "/v1/items/"+movie.Public+"/art/poster" || card.ViewerFence != "f" {
		t.Fatal("missing bounded covers", card)
	}
	if _, err = s.SavedResourcesForViewer("server", v, a, "collection", page.NextCursor, false, 1, "squares", nil); !errors.Is(err, ErrCursor) {
		t.Fatal("cursor crossed shape scope", err)
	}
	square, err := s.SavedResourcesForViewer("server", v, a, "collection", "", false, 1, "squares", nil)
	if err != nil || len(square.Resources) != 0 || square.NextCursor == "" {
		t.Fatal("empty filtered window falsely ended", square, err)
	}
	next, err := s.SavedResourcesForViewer("server", v, a, "collection", square.NextCursor, false, 1, "squares", nil)
	if err != nil || len(next.Resources) != 0 || next.NextCursor != "" {
		t.Fatal("true shape end", next, err)
	}
}

func TestHomeSavedCursorCannotCrossShape(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	for i, title := range []string{"Alpha", "Beta"} {
		movie := c.Movie(library, title, title, 2026+i)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,revision)VALUES('p',?,1,1)`, movie.ID)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}
	page, err := s.Saved(ContentRequest{Viewer: v, View: "watchlist", Shape: "posters", Limit: 1}, v.Libraries)
	if err != nil || len(page.Sections) != 1 || page.Sections[0].NextCursor == "" {
		t.Fatal(page, err)
	}
	_, err = s.Saved(ContentRequest{Viewer: v, View: "watchlist", Shape: "squares", Limit: 1, Cursor: page.Sections[0].NextCursor}, v.Libraries)
	if !errors.Is(err, ErrCursor) {
		t.Fatal("cursor crossed shapes", err)
	}
}

func TestHomeCoverSamplesUseCurrentViewerOnDirectBrowse(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	first := c.Album(artist, "Allowed", 2025)
	c.Song(first, 1, "/music/allowed.flac", "Allowed song")
	hidden := c.Album(artist, "Hidden", 2026)
	hiddenSong := c.Song(hidden, 1, "/music/hidden.flac", "Hidden song")
	c.Attributes(hiddenSong.ID, "label", "adult")
	for i, album := range []catalogtest.Item{first, hidden} {
		digest := strings.Repeat([]string{"a", "b"}[i], 64)
		c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/png',400,400,100,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest)
		c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at)VALUES(?,'album',?,'cover','','fixture','album','local','fixture','fence','now')`, album.Public, album.ID)
		c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('album',?,'cover','',?,?,?,0,1,'fixture','now'))`, album.ID, album.Public, digest, digest)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"adult"}}}
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "music", Pivot: "artists", Limit: 10})
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	covers := page.Entries[0].ArtworkPaths
	want := fixtureArtURL("album", first.Public, "cover", "", strings.Repeat("a", 64), 400, 400, 100)
	if len(covers) != 1 || covers[0] != want {
		t.Fatal("restricted cover leaked in direct browse", covers, want)
	}
}

func TestHomePlaylistCoverSamplesAreBoundedAndViewerFiltered(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	owner := ResourceActor{"local", "owner", "profile"}
	s := New(c.DB)
	name := "Saved covers"
	made, err := s.MutatePlaylist(owner, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{}
	for i, title := range []string{"Restricted", "Alpha", "Beta", "Gamma", "Delta", "Fifth"} {
		movie := c.Movie(library, "/movies/"+title+".mkv", title, 2026)
		c.Fields(movie.ID, map[string]any{"poster_url": "/" + title})
		if i == 0 {
			c.Attributes(movie.ID, "label", "adult")
		} else if i <= 4 {
			want = append(want, "/v1/items/"+movie.Public+"/art/poster")
		}
		_, err = s.MutatePlaylist(owner, made.PlaylistID, "add", "", PlaylistMutation{OperationID: title, ExpectedRevision: int64(i + 1), ItemID: movie.Public}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	c.Drain()
	v := Viewer{Profile: "profile", Fence: "f", Libraries: []string{"movies"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"adult"}}}
	entries := []ContentEntry{{ID: made.PlaylistID, Kind: "playlist"}}
	if err = s.ProjectPlaylistSamples(v, entries); err != nil {
		t.Fatal(err)
	}
	got := entries[0].ArtworkPaths
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatal("expected first four permitted real covers", got, want)
	}
}
