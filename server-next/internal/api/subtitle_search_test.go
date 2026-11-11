package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles/opensubtitlestest"
)

// Anyone who may play a film searches for its subtitles once the owner has set up
// OpenSubtitles, sees the provider's credit, and downloads one, which everyone who plays the
// film is then offered. What they send back is checked. A profile that may not see the film
// is told nothing; only the owner and administrators remove a download.
func TestSubtitleSearchAndDownload(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv")
	var films ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &films, 200)
	film := films.Items[0].ID
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
	sh.do("PUT", "/v1/admin/metadata/settings", MetadataSettingsEdit{OpenSubtitles: &OpenSubtitlesSettingsEdit{APIKey: &key, Username: &user,
		Password: &password}}, nil, 200)

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
	for name, bad := range map[string]SubtitleDownload{
		"a control character in the release": {SubtitleResult: SubtitleResult{Ref: result.Ref, Language: result.Language, Release: "Matrix\x1b[2J"}},
		"a release longer than any":          {SubtitleResult: SubtitleResult{Ref: result.Ref, Language: result.Language, Release: strings.Repeat("x", 201)}},
		"text turned around for the reader":  {SubtitleResult: SubtitleResult{Ref: result.Ref, Language: result.Language, Release: "abc‮dcb"}},
		"a language that is not a code":      {SubtitleResult: SubtitleResult{Ref: result.Ref, Language: "English"}},
		"a ref that is a path":               {SubtitleResult: SubtitleResult{Ref: "../../etc/passwd", Language: result.Language}},
	} {
		if code := as(sh.member, "POST", "/v1/items/"+film+"/subtitles", bad, nil); code != 400 {
			t.Errorf("%s answered %d", name, code)
		}
	}
	var got ItemSubtitle
	if code := as(sh.member, "POST", "/v1/items/"+film+"/subtitles", SubtitleDownload{SubtitleResult: result}, &got); code != 200 ||
		got.Source != "downloaded" || got.Language != result.Language {
		t.Fatalf("download: %d %+v", code, got)
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
	if code := as(sh.member, "POST", "/v1/items/"+film+"/subtitles", SubtitleDownload{SubtitleResult: results.Items[1]}, nil); code != 429 {
		t.Fatalf("a used-up allowance answered %d", code)
	}
}
