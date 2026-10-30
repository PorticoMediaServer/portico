package metadataprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const musicArtistMBID = "11111111-2222-3333-4444-555555555555"

func TestMusicBrainzArtistWikidataCountryYears(t *testing.T) {
	s := &MusicBrainz{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/artist/"+musicArtistMBID || r.URL.Query().Get("inc") != "url-rels" || r.URL.Query().Get("fmt") != "json" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"id":"`+musicArtistMBID+`","name":"Ada Artist","country":"US","life-span":{"begin":"1975-03-01","end":""},"relations":[{"type":"discogs","url":{"resource":"https://www.discogs.com/artist/1"}},{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}}]}`)
	})}
	a, e := s.Artist(context.Background(), musicArtistMBID)
	if e != nil || a.Name != "Ada Artist" || a.Country != "US" || a.BeginYear != 1975 || a.EndYear != 0 || a.WikidataID != "Q123" {
		t.Fatal(a, e)
	}
	if _, e = s.Artist(context.Background(), "not-an-mbid"); e == nil {
		t.Fatal("invalid mbid accepted")
	}
	bad := &MusicBrainz{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"`+musicArtistMBID+`","name":"","country":"USA!","life-span":{},"relations":[]}`)
	})}
	if _, e = bad.Artist(context.Background(), musicArtistMBID); e == nil {
		t.Fatal("empty artist accepted")
	} else {
		errorCode(t, e, "invalid_artist")
	}
}

func TestWikidataEntitySitelinkAndPortrait(t *testing.T) {
	s := &Wikidata{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/Special:EntityData/Q123.json" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"entities":{"Q123":{"id":"Q123","sitelinks":{"enwiki":{"title":"Ada Artist"}},"claims":{"P18":[{"mainsnak":{"snaktype":"value","datavalue":{"value":"Ada portrait.jpg"}}},{"mainsnak":{"snaktype":"novalue"}}]}}}}`)
	})}
	a, e := s.EntityArtist(context.Background(), "Q123")
	if e != nil || a.WikipediaTitle != "Ada Artist" || a.ImageFile != "Ada portrait.jpg" {
		t.Fatal(a, e)
	}
	if _, e = s.EntityArtist(context.Background(), "Q0"); e == nil {
		t.Fatal("invalid qid accepted")
	}
	empty := &Wikidata{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"entities":{"Q124":{"id":"Q124","sitelinks":{},"claims":{}}}}`)
	})}
	a, e = empty.EntityArtist(context.Background(), "Q124")
	if e != nil || a.WikipediaTitle != "" || a.ImageFile != "" {
		t.Fatal(a, e)
	}
}

func TestWikipediaSummaryAndCommonsImage(t *testing.T) {
	wiki := &Wikipedia{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/rest_v1/page/summary/") {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"title":"Ada Artist","extract":"Ada was a singer.","description":"Singer","content_urls":{"desktop":{"page":"https://en.wikipedia.org/wiki/Ada_Artist"}}}`)
	})}
	s, e := wiki.Summary(context.Background(), "Ada Artist")
	if e != nil || s.Extract != "Ada was a singer." || s.PageURL != "https://en.wikipedia.org/wiki/Ada_Artist" {
		t.Fatal(s, e)
	}
	if _, e = wiki.Summary(context.Background(), ""); e == nil {
		t.Fatal("empty title accepted")
	}
	commons := &Commons{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/w/api.php" || r.URL.Query().Get("titles") != "File:Ada portrait.jpg" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"query":{"pages":[{"pageid":1,"title":"File:Ada portrait.jpg","imageinfo":[{"url":"https://upload.wikimedia.org/wikipedia/commons/a/ab/Ada_portrait.jpg","extmetadata":{"LicenseShortName":{"value":"CC BY-SA 4.0"}}}]}]}}`)
	})}
	im, e := commons.ImageInfo(context.Background(), "Ada portrait.jpg")
	if e != nil || im.FileURL != "https://upload.wikimedia.org/wikipedia/commons/a/ab/Ada_portrait.jpg" || im.Licence != "CC BY-SA 4.0" {
		t.Fatal(im, e)
	}
	missing := &Commons{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"query":{"pages":[{"title":"File:Gone.jpg","missing":true}]}}`)
	})}
	im, e = missing.ImageInfo(context.Background(), "Gone.jpg")
	if e != nil || im.FileURL != "" {
		t.Fatal("missing file is not empty", im, e)
	}
	if _, e = commons.ImageInfo(context.Background(), "../evil.jpg"); e == nil {
		t.Fatal("path filename accepted")
	}
}

func TestMusicBrainzArtistTypeIsOnlyObservedGroupOrPerson(t *testing.T) {
	for _, kind := range []string{"Group", "Person", "Choir", "", "Imaginary"} {
		name := kind
		if name == "" {
			name = "Absent"
		}
		t.Run(name, func(t *testing.T) {
			provider := &MusicBrainz{http: fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"id":"`+musicArtistMBID+`","name":"Artist","type":"`+kind+`","relations":[]}`)
			})}
			actual, err := provider.Artist(context.Background(), musicArtistMBID)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if kind == "Group" || kind == "Person" {
				want = kind
			}
			if actual.Type != want {
				t.Fatalf("type %q want %q", actual.Type, want)
			}
		})
	}
}
