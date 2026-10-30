package localmetadata

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBookOPFNamesRolesSeriesAndIdentifiers(t *testing.T) {
	raw := `<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"><dc:title>Book</dc:title><dc:creator id="author" opf:role="aut">Author One</dc:creator><dc:creator opf:role="aut">Author Two</dc:creator><dc:contributor id="narrator">Narrator</dc:contributor><meta refines="#narrator" property="role">nrt</meta><meta property="belongs-to-collection" id="s">The Series</meta><meta refines="#s" property="group-position">2.5</meta><dc:language>fr-CA</dc:language><dc:publisher>Publisher</dc:publisher><dc:identifier opf:scheme="ISBN">9780000000002</dc:identifier><dc:identifier>urn:local:edition-2</dc:identifier><meta name="edition" content="Unabridged"/></metadata></package>`
	values, e := parseOPF([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	for key, want := range map[string]string{"album": "Book", "author": "Author One; Author Two", "narrator": "Narrator", "series": "The Series", "series_position": "2.5", "language": "fr-CA", "publisher": "Publisher", "edition": "Unabridged", "isbn": "9780000000002"} {
		if values[key] != want {
			t.Errorf("%s: %q != %q", key, values[key], want)
		}
	}
	var authors []string
	if json.Unmarshal([]byte(values["authors"]), &authors) != nil || len(authors) != 2 {
		t.Fatal(values)
	}
}
func TestAudioSidecarsRejectExternalEntitiesDepthAndWrongKinds(t *testing.T) {
	for _, raw := range []string{`<!DOCTYPE package [<!ENTITY file SYSTEM "file:///secret">]><package><metadata><title>&file;</title></metadata></package>`, `<package/> <package/>`, strings.Repeat("<a>", 17) + strings.Repeat("</a>", 17)} {
		if _, e := parseAudioXML([]byte(raw)); e == nil {
			t.Fatal("unsafe/unbounded XML accepted")
		}
	}
	// Oversized text is truncated with a partial marker; the rest of a bounded
	// document remains usable under the tolerant local metadata rule.
	if root, e := parseAudioXML([]byte(`<a>` + strings.Repeat("x", 4097) + `</a>`)); e != nil || !root.partial || len(root.text) != 4096 {
		t.Fatalf("bounded partial XML: %+v %v", root, e)
	}
	if _, e := parseMusicNFO([]byte(`<movie><title>Wrong kind</title></movie>`), "music"); e == nil {
		t.Fatal("video sidecar entered music identity")
	}
}
func TestAudioJSONIsTolerantButNeverImportsPersonalState(t *testing.T) {
	values, e := parseAudioJSON([]byte(`{"album":"Book","authors":["One","Two"],"narrators":["Reader"],"seriesList":[{"name":"Cycle","position":"1.5"}],"identifiers":[{"scheme":"isbn","value":"9780000000002"}],"discNumber":2,"unknownFutureKey":{"x":1},"watched":true,"rating":5,"resume":123}`))
	if e != nil {
		t.Fatal(e)
	}
	if values["author"] != "One; Two" || values["narrator"] != "Reader" || values["disc"] != "2" || values["identifiers"] == "" || values["series_list"] == "" {
		t.Fatal(values)
	}
	for _, field := range []string{"watched", "rating", "resume", "unknownFutureKey"} {
		if _, ok := values[field]; ok {
			t.Fatal("personal or unknown state imported", field)
		}
	}
	if _, e := parseAudioJSON([]byte(`{} {}`)); e == nil {
		t.Fatal("trailing JSON accepted")
	}
}
func TestMusicNFOTypedIDsAndMultipleCredits(t *testing.T) {
	v, e := parseMusicNFO([]byte(`<album><title>Local Edition</title><artist><name>First</name></artist><artist>Second</artist><albumartist>Various Artists</albumartist><musicbrainzalbumid>11111111-1111-1111-1111-111111111111</musicbrainzalbumid><musicbrainzreleasegroupid>22222222-2222-2222-2222-222222222222</musicbrainzreleasegroupid><barcode>012345678901</barcode><userrating>5</userrating></album>`), "music")
	if e != nil {
		t.Fatal(e)
	}
	if v["album"] != "Local Edition" || v["artist"] != "First; Second" || v["musicbrainz_albumid"] == v["musicbrainz_releasegroupid"] || v["userrating"] != "" {
		t.Fatal(v)
	}
}
