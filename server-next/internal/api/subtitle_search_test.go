package api

import (
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles/opensubtitlestest"
)

// Anyone who may play a film searches for its subtitles once the owner has set up
// OpenSubtitles, sees the provider's credit, and downloads one, which everyone who plays the
// film is then offered. What they send back is checked. A profile that may not see the film
// is told nothing; only the owner and administrators remove a download.
func TestSubtitleSearchAndDownload(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat (1995).mkv")
	var films ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &films, 200)
	film := ""
	for _, f := range films.Items {
		if f.Title == "The Matrix" {
			film = f.ID
		}
	}
	search := "/v1/items/" + film + "/subtitles/search?languages=eng"
	as := func(token, method, path string, body, out any) int {
		t.Helper()
		return call(t, sh.s, method, path, token, body, out).Code
	}
	if code := as(sh.member, "GET", search, nil, nil); code != 409 {
		t.Fatalf("a search before the owner set up OpenSubtitles answered %d", code)
	}
	server := opensubtitlestest.New(t)
	sh.s.UseSubtitleHost(server.URL)
	key, user, password := "key", "owner", "right"
	sh.do("PUT", "/v1/admin/metadata/settings", MetadataSettingsChange{OpenSubtitlesAPIKey: &key, OpenSubtitlesUsername: &user,
		OpenSubtitlesPassword: &password}, nil, 200)

	var results SubtitleResults
	if code := as(sh.member, "GET", search, nil, &results); code != 200 || len(results.Items) != 2 || results.Credit != "Subtitles provided by OpenSubtitles.com" {
		t.Fatalf("search: %d %+v", code, results)
	}
	if code := as(sh.limitedAdmin, "GET", search, nil, nil); code != 404 {
		t.Fatalf("a profile that may not see the film searched for its subtitles: %d", code)
	}
	if code := as(sh.member, "GET", "/v1/items/"+film+"/subtitles/search?languages="+url.QueryEscape("en;drop"), nil, nil); code != 400 {
		t.Fatalf("a language that is not a code answered %d", code)
	}

	result := results.Items[0]
	heat := ""
	for _, f := range films.Items {
		if f.ID != film {
			heat = f.ID
		}
	}
	// Only a result of this profile's recent search for this title can be downloaded.
	for name, c := range map[string]struct {
		token, item string
		body        SubtitleDownload
	}{
		"a ref never searched":                {sh.member, film, SubtitleDownload{Ref: "9999999"}},
		"a ref from another title's search":   {sh.member, heat, SubtitleDownload{Ref: result.Ref}},
		"a ref from another profile's search": {sh.owner, film, SubtitleDownload{Ref: result.Ref}},
		"a ref that is a path":                {sh.member, film, SubtitleDownload{Ref: "../../etc/passwd"}},
	} {
		if code := as(c.token, "POST", "/v1/items/"+c.item+"/subtitles", c.body, nil); code != 400 {
			t.Errorf("%s answered %d", name, code)
		}
	}
	// Whatever else is sent, the subtitle is labelled as the search answered.
	var got ItemSubtitle
	labelled := map[string]any{"ref": result.Ref, "language": "fre", "release": "Something else", "sdh": !result.SDH}
	if code := as(sh.member, "POST", "/v1/items/"+film+"/subtitles", labelled, &got); code != 200 || got.Source != "downloaded" ||
		got.Language != result.Language || got.Title != result.Release || got.SDH != result.SDH {
		t.Fatalf("download: %d %+v, the search answered %+v", code, got, result)
	}
	var list ItemSubtitles
	sh.do("GET", "/v1/items/"+film+"/subtitles", nil, &list, 200)
	if len(list.Items) != 1 || list.Items[0].ID != got.ID {
		t.Fatalf("the owner is not offered the member's download: %+v", list.Items)
	}

	if code := as(sh.member, "DELETE", "/v1/admin/items/"+film+"/subtitles/"+got.ID, nil, nil); code != 403 {
		t.Fatalf("a member removed a download: %d", code)
	}
	sh.do("DELETE", "/v1/admin/items/"+film+"/subtitles/"+got.ID, nil, nil, 204)
	sh.do("DELETE", "/v1/admin/items/"+film+"/subtitles/"+got.ID, nil, nil, 204)
	list = ItemSubtitles{}
	if sh.do("GET", "/v1/items/"+film+"/subtitles", nil, &list, 200); len(list.Items) != 0 {
		t.Fatalf("after removing: %+v", list.Items)
	}

	server.AllowanceUsed.Store(true)
	if code := as(sh.member, "POST", "/v1/items/"+film+"/subtitles", SubtitleDownload{Ref: results.Items[1].Ref}, nil); code != 429 {
		t.Fatalf("a used-up allowance answered %d", code)
	}
}
