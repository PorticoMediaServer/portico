package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type musicArtistFixture struct {
	mbFixture
	artist func(context.Context, string) (metadataprovider.MusicArtist, error)
}

func (f musicArtistFixture) Artist(c context.Context, id string) (metadataprovider.MusicArtist, error) {
	return f.artist(c, id)
}

type wikidataFixture struct {
	entity func(context.Context, string) (metadataprovider.WikidataArtist, error)
}

func (f wikidataFixture) EntityArtist(c context.Context, id string) (metadataprovider.WikidataArtist, error) {
	return f.entity(c, id)
}

type wikipediaFixture struct {
	summary func(context.Context, string) (metadataprovider.WikipediaSummary, error)
}

func (f wikipediaFixture) Summary(c context.Context, title string) (metadataprovider.WikipediaSummary, error) {
	return f.summary(c, title)
}

type commonsFixture struct {
	image func(context.Context, string) (metadataprovider.CommonsImage, error)
}

func (f commonsFixture) ImageInfo(c context.Context, name string) (metadataprovider.CommonsImage, error) {
	return f.image(c, name)
}

func musicArtistDB(t *testing.T) *Service {
	t.Helper()
	db, _ := mbDB(t)
	t.Cleanup(func() { db.Close() })
	album := mbIntID(t, db, "Local Album")
	digest := strings.Repeat("a", 64)
	mbCurrentExec(t, db, `INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload,sealed) VALUES('rev1','rel1',?,'Local Album','Artist','grp','Group','Album','2020','CA','','','now','{}',0)`, digest)
	mbCurrentExec(t, db, `INSERT INTO mb_release_credits(revision_id,ordinal,artist_id,name,artist_name,sort_name,disambiguation,join_phrase) VALUES('rev1',0,?, 'Artist','Artist','Artist','','')`, mbArtistID)
	mbCurrentExec(t, db, `UPDATE mb_release_evidence SET sealed=1 WHERE revision_id='rev1'`)
	mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: album, NewRelease: "rev1"}, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO mb_album_links(album_id,release_revision,release_id,requested_id,observed_at) VALUES(?,'rev1','rel1','rel1','now')`, album)
		return e
	})
	mbCurrentExec(t, db, `DELETE FROM mb_jobs`)
	svc := New(db, "")
	return svc
}

func musicArtistFakes(calls map[string]int) (musicArtistFixture, wikidataFixture, wikipediaFixture, commonsFixture) {
	mb := musicArtistFixture{artist: func(_ context.Context, id string) (metadataprovider.MusicArtist, error) {
		calls["mb"]++
		if id != mbArtistID {
			return metadataprovider.MusicArtist{}, errors.New("unexpected artist")
		}
		return metadataprovider.MusicArtist{ID: id, Name: "Artist", Country: "US", BeginYear: 1975, WikidataID: "Q123"}, nil
	}}
	wd := wikidataFixture{entity: func(_ context.Context, id string) (metadataprovider.WikidataArtist, error) {
		calls["wikidata"]++
		return metadataprovider.WikidataArtist{ID: id, WikipediaTitle: "Artist", ImageFile: "Artist.jpg"}, nil
	}}
	wp := wikipediaFixture{summary: func(_ context.Context, title string) (metadataprovider.WikipediaSummary, error) {
		calls["wikipedia"]++
		return metadataprovider.WikipediaSummary{Title: title, Extract: "She sang.", PageURL: "https://en.wikipedia.org/wiki/Artist"}, nil
	}}
	co := commonsFixture{image: func(_ context.Context, name string) (metadataprovider.CommonsImage, error) {
		calls["commons"]++
		return metadataprovider.CommonsImage{FileURL: "https://upload.wikimedia.org/wikipedia/commons/a/ab/Artist.jpg", Licence: "CC BY-SA 4.0"}, nil
	}}
	return mb, wd, wp, co
}

// Spec — Page Content §2 item 4: the MusicBrainz → Wikidata → Wikipedia and
// Commons chain stores the biography (attributed), the portrait (licensed),
// the country and the active years, and seeds the portrait candidate. One
// fetch per source, then evidence stops refetches.
func TestMusicArtistChainStoresBiographyAndPortrait(t *testing.T) {
	svc := musicArtistDB(t)
	db := svc.db
	calls := map[string]int{}
	mb, wd, wp, co := musicArtistFakes(calls)
	svc.mb, svc.wikidata, svc.wikipedia, svc.commons = mb, wd, wp, co
	// Through the idle MusicBrainzStep, so the wiring is covered too.
	if e := svc.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	for source, want := range map[string]int{"mb": 1, "wikidata": 1, "wikipedia": 1, "commons": 1} {
		if calls[source] != want {
			t.Fatalf("%s calls: %d", source, calls[source])
		}
	}
	var name, country, bio, source, url, image, licence, wikidata string
	var begin, end int
	var complete int
	if e := db.QueryRow(`SELECT name,country,COALESCE(begin_year,0),COALESCE(end_year,0),wikidata_id,bio,bio_source,bio_source_url,image_url,image_licence,complete FROM music_artist_evidence WHERE artist_id=?`, mbIntID(t, db, "Artist")).Scan(&name, &country, &begin, &end, &wikidata, &bio, &source, &url, &image, &licence, &complete); e != nil {
		t.Fatal(e)
	}
	if name != "Artist" || country != "US" || begin != 1975 || end != 0 || wikidata != "Q123" || bio != "She sang." || source != "Wikipedia" || url != "https://en.wikipedia.org/wiki/Artist" || image != "https://upload.wikimedia.org/wikipedia/commons/a/ab/Artist.jpg" || licence != "CC BY-SA 4.0" || complete != 1 {
		t.Fatalf("evidence: %q %q %d %d %q %q %q %q %q %q %d", name, country, begin, end, wikidata, bio, source, url, image, licence, complete)
	}
	var candidates int
	if e := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE kind='artist' AND entity_id=? AND role='portrait' AND provider='commons'`, mbIntID(t, db, "Artist")).Scan(&candidates); e != nil || candidates != 1 {
		t.Fatal("portrait candidate", candidates, e)
	}
	// A second step fetches nothing.
	if e := svc.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	for source, want := range map[string]int{"mb": 1, "wikidata": 1, "wikipedia": 1, "commons": 1} {
		if calls[source] != want {
			t.Fatalf("%s refetched: %d", source, calls[source])
		}
	}
}

// An artist with no Wikidata link keeps its MusicBrainz facts, complete.
func TestMusicArtistChainWithoutWikidataKeepsFacts(t *testing.T) {
	svc := musicArtistDB(t)
	calls := map[string]int{}
	mb, wd, wp, co := musicArtistFakes(calls)
	mb.artist = func(context.Context, string) (metadataprovider.MusicArtist, error) {
		calls["mb"]++
		return metadataprovider.MusicArtist{ID: mbArtistID, Name: "Artist", Country: "CA", BeginYear: 2001, EndYear: 2010}, nil
	}
	svc.mb, svc.wikidata, svc.wikipedia, svc.commons = mb, wd, wp, co
	if e := svc.musicArtistStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls["wikidata"] != 0 || calls["wikipedia"] != 0 || calls["commons"] != 0 {
		t.Fatal("chain continued without a link", calls)
	}
	var country, bio, image string
	var begin, end, complete int
	if e := svc.db.QueryRow(`SELECT country,COALESCE(begin_year,0),COALESCE(end_year,0),bio,image_url,complete FROM music_artist_evidence WHERE artist_id=?`, mbIntID(t, svc.db, "Artist")).Scan(&country, &begin, &end, &bio, &image, &complete); e != nil {
		t.Fatal(e)
	}
	if country != "CA" || begin != 2001 || end != 2010 || bio != "" || image != "" || complete != 1 {
		t.Fatal(country, begin, end, bio, image, complete)
	}
}

// A MusicBrainz failure is retried, at most five times.
func TestMusicArtistChainFailuresRetryFiveTimes(t *testing.T) {
	svc := musicArtistDB(t)
	calls := map[string]int{}
	mb, wd, wp, co := musicArtistFakes(calls)
	mb.artist = func(context.Context, string) (metadataprovider.MusicArtist, error) {
		calls["mb"]++
		return metadataprovider.MusicArtist{}, errors.New("upstream gone")
	}
	svc.mb, svc.wikidata, svc.wikipedia, svc.commons = mb, wd, wp, co
	for i := 0; i < 6; i++ {
		if e := svc.musicArtistStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if calls["mb"] != 5 || calls["wikidata"] != 0 {
		t.Fatal("retry bound", calls)
	}
}

func TestMusicArtistTypeStoresObservedValueOrNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"Group", "Person", "", "Unknown"} {
		name := kind
		if name == "" {
			name = "Absent"
		}
		t.Run(name, func(t *testing.T) {
			svc := musicArtistDB(t)
			calls := map[string]int{}
			mb, wd, wp, co := musicArtistFakes(calls)
			mb.artist = func(context.Context, string) (metadataprovider.MusicArtist, error) {
				return metadataprovider.MusicArtist{ID: mbArtistID, Name: "Artist", Type: kind}, nil
			}
			svc.mb, svc.wikidata, svc.wikipedia, svc.commons = mb, wd, wp, co
			if err := svc.musicArtistStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			var actual sql.NullString
			if err := svc.db.QueryRow(`SELECT artist_type FROM music_artist_evidence WHERE artist_id=?`, mbIntID(t, svc.db, "Artist")).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			known := kind == "Group" || kind == "Person"
			if actual.Valid != known || known && actual.String != kind {
				t.Fatalf("kind %q stored %+v", kind, actual)
			}
		})
	}
}
