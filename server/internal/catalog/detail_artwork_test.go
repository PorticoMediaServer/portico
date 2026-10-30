package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func selectedDetailArtwork(c *catalogtest.Catalog, item catalogtest.Item, kind, role, subject, digest, thumbnail string) {
	c.T.Helper()
	c.Exec(`INSERT OR IGNORE INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/png',800,600,100,'now','ready'),(?,'image/png',400,300,50,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest, thumbnail)
	candidate := fmt.Sprintf("%d-%s-%s", item.ID, role, subject)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,?,?,?,?,'fixture','image','local','fixture','fence','now')`, candidate, kind, item.ID, role, subject)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES(?,?,?,?,?,?,?,0,1,'fixture','now'))`, kind, item.ID, role, subject, candidate, digest, thumbnail)
}

func TestDetailArtworkUsesReadySelectedDescriptors(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(lib, "/movies/Dune.mkv", "Dune", 2020)
	bare := c.Movie(lib, "/movies/Bare.mkv", "Bare", 2021)
	poster, backdrop := strings.Repeat("a", 64), strings.Repeat("b", 64)
	selectedDetailArtwork(c, movie, "item", "poster", "", poster, strings.Repeat("c", 64))
	selectedDetailArtwork(c, movie, "item", "backdrop", "", backdrop, strings.Repeat("d", 64))
	c.Exec(`UPDATE catalog_item_details SET poster_url='https://image.tmdb.org/t/p/original/evidence.png',backdrop_url='local:unselected' WHERE entity_id IN(?,?)`, movie.ID, bare.ID)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "viewer", Fence: "f", Libraries: []string{"movies"}}
	detail, err := s.Detail(viewer, "server", movie.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPoster := fixtureArtURL("item", movie.Public, "poster", "", poster, 800, 600, 100)
	wantBackdrop := fixtureArtURL("item", movie.Public, "backdrop", "", backdrop, 800, 600, 100)
	if detail.Item.PosterURL != wantPoster || detail.Item.BackdropURL != wantBackdrop {
		t.Fatal("detail did not publish selected versions", detail.Item.PosterURL, detail.Item.BackdropURL)
	}
	blank, err := s.Detail(viewer, "server", bare.Public, false)
	if err != nil || blank.Item.PosterURL != "" || blank.Item.BackdropURL != "" {
		t.Fatal("unselected evidence invented artwork", blank.Item, err)
	}
	c.Exec(`DELETE FROM artwork_selections WHERE digest=?`, backdrop)
	c.Drain()
	detail, err = s.Detail(viewer, "server", movie.Public, false)
	if err != nil || detail.Item.PosterURL != wantPoster || detail.Item.BackdropURL != "" {
		t.Fatal("a withdrawn selection fell back to legacy evidence", detail.Item, err)
	}
	c.Attributes(movie.ID, "label", "Hidden")
	c.Drain()
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"Hidden"}}
	if _, err = s.Detail(viewer, "server", movie.Public, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restricted detail remained available", err)
	}
}

func TestDetailEpisodeArtworkNamesActualInheritedSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	poster, backdrop, still := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	selectedDetailArtwork(c, names["season1"], "season", "poster", "", poster, strings.Repeat("d", 64))
	selectedDetailArtwork(c, names["show"], "show", "backdrop", "", backdrop, strings.Repeat("e", 64))
	selectedDetailArtwork(c, names["ep-001"], "item", "still", "", still, strings.Repeat("f", 64))
	c.Drain()
	detail, err := s.Detail(workspaceRequest(names).Viewer, "server", names["ep-001"].Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Item.PosterURL != fixtureArtURL("season", names["season1"].Public, "poster", "", poster, 800, 600, 100) || detail.Item.BackdropURL != fixtureArtURL("show", names["show"].Public, "backdrop", "", backdrop, 800, 600, 100) || detail.Item.StillURL != fixtureArtURL("item", names["ep-001"].Public, "still", "", still, 800, 600, 100) {
		t.Fatal("detail guessed an artwork owner or inherited a still", detail.Item)
	}
}

func TestCreditAndPersonArtworkUseVisibleReadySelectedVersion(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	hidden := c.Movie(lib, "/movies/Hidden.mkv", "Hidden", 2020)
	visible := c.Movie(lib, "/movies/Visible.mkv", "Visible", 2021)
	for _, item := range []catalogtest.Item{hidden, visible} {
		compactCredits(c, item.ID, personCredit("tmdb:42", "42", "Actor", "Lead", "Acting", 0))
	}
	selectedDetailArtwork(c, hidden, "item", "portrait", "tmdb:42", strings.Repeat("a", 64), strings.Repeat("b", 64))
	full, thumbnail := strings.Repeat("c", 64), strings.Repeat("d", 64)
	selectedDetailArtwork(c, visible, "item", "portrait", "tmdb:42", full, thumbnail)
	c.Attributes(hidden.ID, "label", "Hidden")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "viewer", Fence: "f", Libraries: []string{"movies"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"Hidden"}}}
	person := personToken(t, c.DB, "tmdb:42")
	c.Exec(`UPDATE catalog_people SET portrait_url='https://untrusted.invalid/portrait.png' WHERE token=?`, person)
	want := fixtureArtURL("item", visible.Public, "portrait", "tmdb:42", full, 800, 600, 100)
	assertPortraits := func(expected string) {
		t.Helper()
		detail, err := s.Detail(viewer, "server", visible.Public, false)
		if err != nil || len(detail.Metadata.Credits) != 1 || detail.Metadata.Credits[0].PortraitURL != expected {
			t.Fatal("detail portrait", detail.Metadata.Credits, err)
		}
		credits, err := s.ItemCredits(viewer, visible.Public, "cast", "", 10)
		if err != nil || len(credits.Credits) != 1 || credits.Credits[0].PortraitURL != expected {
			t.Fatal("paged portrait", credits, err)
		}
		page, err := s.Person(viewer, "server", person, "all", "", 10)
		if err != nil || page.Person.PortraitURL != expected {
			t.Fatal("person portrait", page.Person.PortraitURL, err)
		}
		directory, err := s.People(viewer, "server", "Actor", 10)
		if err != nil || len(directory.People) != 1 || directory.People[0].PortraitURL != expected {
			t.Fatal("directory portrait", directory.People, err)
		}
	}
	assertPortraits(want)
	c.Exec(`DELETE FROM artwork_selections WHERE digest=?`, full)
	c.Drain()
	assertPortraits("")
	if _, err := s.ItemCredits(viewer, hidden.Public, "cast", "", 10); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("hidden portrait source remained accessible", err)
	}
}
