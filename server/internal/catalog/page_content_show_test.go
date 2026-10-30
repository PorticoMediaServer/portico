package catalog

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Spec — Page Content §2: episode stills are each episode's own (never the
// show's art), seasons carry their own posters, the show's cast are /v1/people
// persons with photos (for restricted profiles too), and the hero has status,
// first/last air dates and the community rating.
func TestShowPageContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	showArtworkID := names["show"].Public
	seasonArtworkID := names["season1"].Public
	episodeArtworkID := names["ep-001"].Public
	still, seasonArt, backdrop, portrait := strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)
	if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/jpeg',1280,720,10,'now','ready'),(?, 'image/jpeg',680,1000,10,'now','ready'),(?, 'image/jpeg',1920,1080,10,'now','ready'),(?, 'image/jpeg',300,450,10,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, still, seasonArt, backdrop, portrait); err != nil {
		t.Fatal(err)
	}
	art := []struct {
		id, kind              string
		entity                int64
		role, subject, digest string
	}{
		{"still", "item", names["ep-001"].ID, "still", "", still},
		{"season", "season", names["season1"].ID, "poster", "", seasonArt},
		{"backdrop", "show", names["show"].ID, "backdrop", "", backdrop},
		{"portrait", "show", names["show"].ID, "portrait", "tvdb:77", portrait},
	}
	for _, row := range art {
		if _, err := db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,?,?,?,?,'tvdb',?,'provider','tvdb','fence','now')`, row.id, row.kind, row.entity, row.role, row.subject, row.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES(?,?,?,?,?,?,?,0,1,'','now'))`, row.kind, row.entity, row.role, row.subject, row.id, row.digest, row.digest); err != nil {
			t.Fatal(err)
		}
	}
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "credits", `[{"id":"77","name":"Ada Actor","role":"Captain","department":"Acting","ordinal":0},{"id":"","name":"Cleo Creator","role":"Creator","department":"Creator","ordinal":1}]`)
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "status", `"Ended"`)
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "date", `"2008-01-20"`)
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "rating", `{"value":8.4,"scale":10,"votes":120}`)
	movieActor := workspaceShowCredit(t, c, db, names["show"], "tvdb", "77", "Ada Actor", "Captain", "Acting", 0)
	workspaceShowCredit(t, c, db, names["show"], "fixture", "", "Cleo Creator", "Creator", "Creator", 1)
	if _, err := db.Exec(`INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',1,'{"aired":"2008-01-20","image":"https://artworks.thetvdb.com/banners/s.jpg"}','now'),(?,'tvdb',2,'{"aired":"2013-05-01"}','now')`, names["ep-001"].ID, names["ep-200"].ID); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	var storedActor string
	if err := db.QueryRow(`SELECT token FROM catalog_people WHERE identity_key='tvdb:77'`).Scan(&storedActor); err != nil {
		t.Fatal("the show's cast are people", err)
	}
	for _, restricted := range []bool{false, true} {
		r := workspaceRequest(names)
		if restricted {
			c.Attributes(names["ep-002"].ID, "label", "Adult")
			r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}
		}
		w, err := s.ShowWorkspace(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(w.ShowCredits) != 2 || w.ShowCredits[0].Name != "Ada Actor" || w.ShowCredits[0].ID != movieActor || w.ShowCredits[0].ID != storedActor || w.ShowCredits[0].PortraitURL != fixtureArtURL("show", names["show"].Public, "portrait", "tvdb:77", portrait, 300, 450, 10) {
			t.Fatalf("restricted=%t cast: %+v", restricted, w.ShowCredits)
		}
		if w.ShowCredits[1].Name != "Cleo Creator" || w.ShowCredits[1].ID == "" || w.ShowCredits[1].PortraitURL != "" {
			t.Fatalf("crew after cast, no borrowed photo: %+v", w.ShowCredits[1])
		}
		if restricted {
			continue
		}
		show := w.Show
		if show.Status != "Ended" || show.FirstAired != "2008-01-20" || show.LastAired != "2013-05-01" || show.Rating == nil || show.Rating.Value != 8.4 || show.Rating.Scale != 10 {
			t.Fatalf("hero facts: %+v rating %+v", show, show.Rating)
		}
		entries := w.Episodes.Sections[0].Entries
		if entries[0].ID != names["ep-001"].Public || !strings.HasPrefix(entries[0].StillURL, "/v1/metadata/item/"+episodeArtworkID+"/art/still?v="+still) || entries[0].AirDate != "2008-01-20" {
			t.Fatalf("own still and air date: %+v", entries[0])
		}
		if entries[1].StillURL != "" || !strings.Contains(entries[1].BackdropURL, "/v1/metadata/show/"+showArtworkID+"/art/backdrop") {
			t.Fatalf("an episode without a still has none (the backdrop still inherits for landscape rows): %+v", entries[1])
		}
		for _, season := range w.Seasons {
			switch season.ID {
			case names["season1"].Public:
				if !strings.HasPrefix(season.PosterURL, "/v1/metadata/season/"+seasonArtworkID+"/art/poster?v="+seasonArt) {
					t.Fatalf("season poster: %+v", season)
				}
			default:
				if season.PosterURL != "" {
					t.Fatalf("a season without a poster has none: %+v", season)
				}
			}
		}
	}
	item, err := s.Get("viewer", names["ep-001"].Public)
	if err != nil || !strings.HasPrefix(item.StillURL, "/v1/metadata/item/"+episodeArtworkID+"/art/still") {
		t.Fatal("item stillUrl", item.StillURL, err)
	}
}
