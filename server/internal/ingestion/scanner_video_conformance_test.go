package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// These fixtures are generated, blank files. No real title's media, artwork or
// provider response is copied, fetched or played; only Plex's documented layouts
// are exercised through the real scanner and its stored catalogue rows.

// Plex's exact movie examples: Avatar with inline and folder extras, Pulp
// Fiction's two versions, the Blade Runner editions tree, Batman Begins' ID hint
// and The Dark Knight's split parts.
func TestPlex45MovieExamplesStoreVersionsEditionsExtrasPartsAndIDs(t *testing.T) {
	f := newLoopFixture(t, "movie",
		"Avatar (2009)/Avatar (2009).mkv",
		"Avatar (2009)/Avatar (2009)-trailer.mkv",
		"Avatar (2009)/Featurettes/Making of Pandora.mkv",
		"Avatar (2009)/Behind The Scenes/Capture.mkv",
		"Pulp Fiction (1994)/Pulp Fiction (1994) - 1080p.mkv",
		"Pulp Fiction (1994)/Pulp Fiction (1994) - SD.mkv",
		"Blade Runner (1982) {edition-Director's Cut}/Blade Runner (1982) - 1080p.mkv",
		"Blade Runner (1982) {edition-Director's Cut}/Blade Runner (1982) - SD.mkv",
		"Blade Runner (1982) {edition-The Final Cut}/Blade Runner (1982) - 1080p.mkv",
		"Batman Begins (2005) {imdb-tt0372784}/Batman Begins (2005).mkv",
		"The Dark Knight (2008)/The Dark Knight (2008) - pt1.mkv",
		"The Dark Knight (2008)/The Dark Knight (2008) - pt2.mkv",
	)
	f.finish(f.queue())

	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND retired=0`); n != 5 {
		t.Fatalf("films: %d; want 5 (extras are their own kind)", n)
	}

	// Two versions of one film, never two separate titles.
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='Pulp Fiction' AND l.part_index=0 AND l.edition=''`); n != 2 {
		t.Fatalf("Pulp Fiction versions: %d; want two whole-film links", n)
	}
	if n := f.count(`SELECT versions FROM catalog_item_version_counts vc JOIN catalog_entities e ON e.id=vc.item_id WHERE e.kind=1 AND e.title='Pulp Fiction'`); n != 2 {
		t.Fatalf("Pulp Fiction version count: %d; want 2", n)
	}

	// Editions are version labels on one title, not titles of their own.
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='Blade Runner' AND l.edition=?`, "Director's Cut"); n != 2 {
		t.Fatalf("Blade Runner Director's Cut versions: %d; want 2", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='Blade Runner' AND l.edition=?`, "The Final Cut"); n != 1 {
		t.Fatalf("Blade Runner The Final Cut versions: %d; want 1", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='Blade Runner'`); n != 3 {
		t.Fatalf("Blade Runner links: %d; want 3", n)
	}

	// Plex's braced ID hint reaches the stored provider identity.
	if n := f.count(`SELECT count(*) FROM catalog_external_ids x JOIN catalog_entities e ON e.id=x.entity_id WHERE e.kind=1 AND e.title='Batman Begins' AND e.year=2005 AND x.provider='imdb' AND x.provider_kind='title' AND x.provider_id='tt0372784'`); n != 1 {
		t.Fatalf("Batman Begins IMDb hint: %d; want one stored identity", n)
	}

	// Plex's split parts are one film made of two part links.
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='The Dark Knight' AND l.part_index IN(1,2)`); n != 2 {
		t.Fatalf("The Dark Knight parts: %d; want 2", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=1 AND e.title='The Dark Knight' AND l.part_index=0`); n != 0 {
		t.Fatalf("a split part became a whole-film link: %d", n)
	}

	// Avatar's extras are ordinary playable items owned by the film.
	for _, want := range []struct{ kind, title string }{
		{"trailer", "Avatar (2009)"},
		{"featurette", "Making of Pandora"},
		{"behind_the_scenes", "Capture"},
	} {
		if n := f.count(`SELECT count(*) FROM item_extras x JOIN catalog_entities o ON o.id=x.owner_id WHERE o.kind=1 AND o.title='Avatar' AND x.kind=? AND x.title=?`, want.kind, want.title); n != 1 {
			t.Fatalf("Avatar %s extra %q: %d; want one owned row", want.kind, want.title, n)
		}
	}
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=11 AND retired=0`); n != 3 {
		t.Fatalf("extra items: %d; want 3", n)
	}
}

// Plex's TV examples: Band of Brothers' season and episodes, and Game of
// Thrones' show-level and inline extras.
func TestPlex45TVSeasonsEpisodesAndShowExtrasStoreOwners(t *testing.T) {
	f := newLoopFixture(t, "tv",
		"Band of Brothers (2001)/Season 01/Band of Brothers - s01e01 - Currahee.mkv",
		"Band of Brothers (2001)/Season 01/Band of Brothers - s01e02 - Day of Days.mkv",
		"Game of Thrones (2011)/Season 01/Game of Thrones - s01e01 - Winter Is Coming.mkv",
		"Game of Thrones (2011)/Season 01/Game of Thrones - s01e01 - Winter Is Coming -featurette.mkv",
		"Game of Thrones (2011)/Trailers/Season 1 Trailer.mkv",
		"Game of Thrones (2011)/Behind The Scenes/The Making of Game of Thrones.mkv",
	)
	f.finish(f.queue())

	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0`); n != 2 {
		t.Fatalf("shows: %d; want 2", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_seasons`); n != 2 {
		t.Fatalf("seasons: %d; want Band of Brothers 1 and Game of Thrones 1", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_episodes`); n != 3 {
		t.Fatalf("episodes: %d; want Band of Brothers 2 and Game of Thrones 1", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id JOIN catalog_seasons se ON se.entity_id=ep.season_id WHERE sh.kind=2 AND sh.title='Band of Brothers' AND sh.year=2001 AND se.number=1 AND ep.number IN(1,2)`); n != 2 {
		t.Fatalf("Band of Brothers season 1 episodes: %d; want 2", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id WHERE sh.kind=2 AND sh.title='Game of Thrones' AND ep.number=1`); n != 1 {
		t.Fatalf("Game of Thrones episodes: %d; want one (its extras are not episodes)", n)
	}
	for _, want := range []struct{ kind, title string }{
		{"featurette", "Game of Thrones - s01e01 - Winter Is Coming"},
		{"trailer", "Season 1 Trailer"},
		{"behind_the_scenes", "The Making of Game of Thrones"},
	} {
		if n := f.count(`SELECT count(*) FROM item_extras x JOIN catalog_entities o ON o.id=x.owner_id WHERE o.kind=2 AND o.title='Game of Thrones' AND x.kind=? AND x.title=?`, want.kind, want.title); n != 1 {
			t.Fatalf("Game of Thrones %s extra %q: %d; want one owned row", want.kind, want.title, n)
		}
	}
}

// Plex's subtitle sidecars beside a film are recorded as sidecars, never as
// items, and the flags the player offers come from the file names.
func TestPlex45SubtitleSidecarsStoreFlagsWithoutBecomingItems(t *testing.T) {
	f := newLoopFixture(t, "movie", "Avatar (2009)/Avatar (2009).mkv")
	srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	ass := "[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:02.00,Default,,0,0,0,,Hello\n"
	f.write("Avatar (2009)/Avatar (2009).en.srt", srt)
	f.write("Avatar (2009)/Avatar (2009).en.forced.srt", srt)
	f.write("Avatar (2009)/Avatar (2009).en.sdh.srt", srt)
	f.write("Avatar (2009)/Avatar (2009).eng.ass", ass)
	f.finish(f.queue())

	if n := f.count(`SELECT count(*) FROM inventory_sidecars WHERE relative_path LIKE 'Avatar (2009)/Avatar (2009).%'`); n != 4 {
		t.Fatalf("stored subtitle sidecars: %d; want 4", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE '%.srt' OR relative_path LIKE '%.ass'`); n != 0 {
		t.Fatalf("subtitle sidecars became inventory media: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Avatar'`); n != 1 {
		t.Fatalf("films: %d; want the one Avatar file", n)
	}

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := subtitles.Discover(context.Background(), storage.New(binary), f.library, filepath.Join(f.root, "Avatar (2009)", "Avatar (2009).mkv"), nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Status != "known" || len(inventory.Tracks) != 4 {
		t.Fatalf("discovered sidecar inventory status=%q tracks=%+v", inventory.Status, inventory.Tracks)
	}
	want := map[string]struct {
		format, language, title string
		forced, hearingImpaired bool
	}{
		"Avatar (2009).en.srt":        {format: "srt", language: "en"},
		"Avatar (2009).en.forced.srt": {format: "srt", language: "en", forced: true},
		"Avatar (2009).en.sdh.srt":    {format: "srt", language: "en", title: "SDH", hearingImpaired: true},
		"Avatar (2009).eng.ass":       {format: "ass", language: "en"},
	}
	for _, track := range inventory.Tracks {
		name := filepath.Base(track.Locator)
		expect, ok := want[name]
		if !ok {
			t.Fatalf("unexpected sidecar track %q (%+v)", name, track)
		}
		if track.Origin != "sidecar" || track.StreamIndex != -1 || track.Reason != "" {
			t.Fatalf("%s: sidecar evidence %+v", name, track)
		}
		if track.Format != expect.format || track.Language != expect.language || track.Forced != expect.forced || track.HearingImpaired != expect.hearingImpaired || track.Title != expect.title {
			t.Fatalf("%s: format/language/flags = %q %q forced=%t sdh=%t title=%q; want %+v", name, track.Format, track.Language, track.Forced, track.HearingImpaired, track.Title, expect)
		}
		if track.Size <= 0 || track.Digest == "" {
			t.Fatalf("%s: custody evidence %+v", name, track)
		}
	}
}

// Plex's TV sidecars sit beside the episode. They attach to that episode and
// never become items.
func TestPlex45TVEpisodeSubtitleSidecars(t *testing.T) {
	f := newLoopFixture(t, "tv", "Band of Brothers (2001)/Season 01/Band of Brothers - s01e01 - Currahee.mkv")
	srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	f.write("Band of Brothers (2001)/Season 01/Band of Brothers - s01e01 - Currahee.en.srt", srt)
	f.finish(f.queue())

	if n := f.count(`SELECT count(*) FROM inventory_sidecars WHERE relative_path LIKE 'Band of Brothers (2001)/Season 01/%'`); n != 1 {
		t.Fatalf("stored TV subtitle sidecars: %d; want the episode sidecar", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE '%.srt'`); n != 0 {
		t.Fatalf("TV subtitle sidecars became inventory media: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_episodes`); n != 1 {
		t.Fatalf("episodes: %d; want the one episode", n)
	}

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := subtitles.Discover(context.Background(), storage.New(binary), f.library, filepath.Join(f.root, "Band of Brothers (2001)", "Season 01", "Band of Brothers - s01e01 - Currahee.mkv"), nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Status != "known" || len(inventory.Tracks) != 1 {
		t.Fatalf("TV subtitle inventory status=%q tracks=%+v", inventory.Status, inventory.Tracks)
	}
	track := inventory.Tracks[0]
	if track.Origin != "sidecar" || track.StreamIndex != -1 || track.Reason != "" || track.Format != "srt" || track.Language != "en" || track.Size <= 0 || track.Digest == "" {
		t.Fatalf("TV sidecar evidence %+v; want one srt en track", track)
	}
	if got, want := filepath.Base(track.Locator), "Band of Brothers - s01e01 - Currahee.en.srt"; got != want {
		t.Fatalf("TV sidecar locator %q; want %q", got, want)
	}
}

// Plex's TV guide says a show folder's {edition-...} is a separate show with
// separate watch state, while the owner's explicit merge still wins and must
// survive a rescan. This is the known catalogue gap and the acceptance case for
// it: no compactcatalog or provider-identity redesign here.
func TestPlex45ShowFolderEditionSeparatesShowsThenOwnerMergePersists(t *testing.T) {
	f := newLoopFixture(t, "tv",
		"Babylon 5 (1994) {tvdb-1234}/Season 01/Babylon 5 - s01e01 - Midnight on the Firing Line.mkv",
		"Babylon 5 (1994) {edition-Bluray Remaster} {tvdb-1234}/Season 01/Babylon 5 - s01e01 - Midnight on the Firing Line.mkv",
	)
	f.finish(f.queue())

	counts := func() (shows, episodes, links int) {
		t.Helper()
		err := f.db.QueryRow(`SELECT (SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0),
			(SELECT count(*) FROM catalog_episodes),
			(SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=4)`).Scan(&shows, &episodes, &links)
		if err != nil {
			t.Fatal(err)
		}
		return shows, episodes, links
	}
	shows, episodes, links := counts()
	if shows != 2 {
		t.Fatalf("show-folder edition scope: shows=%d episodes=%d episode_links=%d; want 2 shows with 1 episode each: the shared tvdb id coalesced the plain and {edition-Bluray Remaster} folders into one show (§45 addendum 4)", shows, episodes, links)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=4 AND l.edition=''`); n != 1 {
		t.Fatalf("plain episode edition labels: %d; want 1", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=4 AND l.edition=?`, "Bluray Remaster"); n != 1 {
		t.Fatalf("edition episode labels: %d; want 1", n)
	}

	type episodeOwner struct{ show, item int64 }
	var plain, edition episodeOwner
	rows, err := f.db.Query(`SELECT ep.show_id,ep.entity_id,l.edition FROM catalog_episodes ep JOIN catalog_asset_links l ON l.entity_id=ep.entity_id ORDER BY ep.show_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var owner episodeOwner
		var label string
		if err = rows.Scan(&owner.show, &owner.item, &label); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if label == "" {
			plain = owner
		} else {
			edition = owner
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if plain.show == 0 || edition.show == 0 || plain.show == edition.show {
		t.Fatalf("episode owners plain=%+v edition=%+v; want two distinct shows", plain, edition)
	}
	for _, owner := range []episodeOwner{plain, edition} {
		if n := f.count(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=?`, owner.item); n != 1 {
			t.Fatalf("edition episode %d has %d file links; want one", owner.item, n)
		}
	}

	// Watched state is the real per-profile state, not a second store.
	cat := catalogtest.New(t, f.db)
	plainEpisode, editionEpisode := cat.Public(plain.item), cat.Public(edition.item)
	watched := true
	if _, err = f.s.catalog.SetPersonal("account", "profile", plainEpisode, catalog.PersonalMutation{OperationID: "watch-plain-edition", Watched: &watched}, nil); err != nil {
		t.Fatal(err)
	}
	if state, err := f.s.catalog.Personal("profile", plainEpisode); err != nil || !state.Watched {
		t.Fatalf("watched episode state %+v err=%v", state, err)
	}
	if state, err := f.s.catalog.Personal("profile", editionEpisode); err != nil || state.Watched {
		t.Fatalf("edition independently watched? %+v err=%v", state, err)
	}

	// The owner's MergeTitles is the real service path, with a hook that checks
	// the merge targets are shows in this catalogue transaction.
	from, into := cat.Public(edition.show), cat.Public(plain.show)
	authorize := func(tx *sql.Tx) error {
		for _, public := range []string{from, into} {
			entity, err := entityid.Resolve(context.Background(), tx, public)
			if err != nil {
				return err
			}
			var kind int
			if err = tx.QueryRow(`SELECT kind FROM catalog_entities WHERE id=?`, entity).Scan(&kind); err != nil {
				return err
			}
			if compactcatalog.Kind(kind) != compactcatalog.Show {
				return fmt.Errorf("merge target %q is not a show", public)
			}
		}
		return nil
	}
	if err = f.s.catalog.MergeTitles(context.Background(), from, into, authorize); err != nil {
		t.Fatal(err)
	}
	if shows, episodes, links = counts(); shows != 1 || episodes != 1 || links != 2 {
		t.Fatalf("after the owner merge: shows=%d episodes=%d episode_links=%d; want one show with one episode and two files", shows, episodes, links)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=4 AND l.edition=''`); n != 1 {
		t.Fatalf("after the owner merge plain episode labels: %d; want 1", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.kind=4 AND l.edition=?`, "Bluray Remaster"); n != 1 {
		t.Fatalf("after the owner merge edition episode labels: %d; want 1", n)
	}

	// An unchanged rescan keeps the owner's decision.
	f.finish(f.queue())
	if shows, episodes, links = counts(); shows != 1 || episodes != 1 || links != 2 {
		t.Fatalf("after the rescan: shows=%d episodes=%d episode_links=%d; want the owner merge to persist", shows, episodes, links)
	}
}

// Two ordinary folders of one show with the same provider ID still coalesce as
// today; edition scoping must not turn every duplicate folder into a new show.
func TestPlex45PlainShowFoldersWithSameProviderCoalesce(t *testing.T) {
	f := newLoopFixture(t, "tv",
		"Babylon 5 (1994) {tvdb-1234}/Season 01/Babylon 5 - s01e01 - Midnight on the Firing Line.mkv",
		"Babylon 5 (1994) [tvdb-1234]/Season 01/Babylon 5 - s01e02 - Soul Mates.mkv",
	)
	f.finish(f.queue())
	if shows := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0`); shows != 1 {
		t.Fatalf("plain folders with one provider: shows=%d; want 1", shows)
	}
	if episodes := f.count(`SELECT count(*) FROM catalog_episodes`); episodes != 2 {
		t.Fatalf("plain folders with one provider: episodes=%d; want 2", episodes)
	}
}
