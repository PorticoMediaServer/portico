package linearinput

import (
	"encoding/json"
	"strings"
	"testing"
)

const captionFixture = `#EXTM3U
#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID="low",NAME="Lower copy",INSTREAM-ID="CC1",AUTOSELECT=YES
#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID="high",NAME="English captions",LANGUAGE="en",INSTREAM-ID="SERVICE2",AUTOSELECT=YES
#EXT-X-STREAM-INF:BANDWIDTH=200000,CLOSED-CAPTIONS="low"
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,CLOSED-CAPTIONS="high"
high.m3u8
`

func TestSelectedCaptionDeclarationsUseOnlySelectedActualSourceGroup(t *testing.T) {
	got, master := selectedCaptions(selectVariant(strings.Split(captionFixture, "\n")))
	if !master || got.GroupID != "high" || got.Bandwidth != 800000 || len(got.Renditions) != 1 || got.Renditions[0].InstreamID != "SERVICE2" || got.Renditions[0].Name != "English captions" {
		t.Fatalf("selected %+v", got)
	}
	for _, id := range []string{"CC0", "CC5", "SERVICE0", "SERVICE64", "SERVICE02", "CC1\nURI=x"} {
		text := strings.Replace(captionFixture, "SERVICE2", id, 1)
		if d, _ := selectedCaptions(selectVariant(strings.Split(text, "\n"))); len(d.Renditions) != 0 {
			t.Fatalf("invalid service %q: %+v", id, d)
		}
	}
}
func TestCaptionDeclarationsNeverExposeURIsOrInventServices(t *testing.T) {
	g := &Gateway{address: "http://127.0.0.1:1", resources: map[string]resource{}, reverse: map[string]string{}}
	if _, err := g.rewrite([]byte(captionFixture), "https://source.example/private/master.m3u8?credential=private", 0); err != nil {
		t.Fatal(err)
	}
	got := g.Captions()
	got.Renditions[0].Name = "mutated"
	if g.Captions().Renditions[0].Name != "English captions" {
		t.Fatal("mutable metadata escaped")
	}
	if _, err := g.rewrite([]byte("#EXTM3U\n#EXTINF:2,\nsegment.ts\n"), "https://source.example/private/high.m3u8", 1); err != nil {
		t.Fatal(err)
	}
	if len(g.Captions().Renditions) != 1 {
		t.Fatal("media refresh erased master facts")
	}
	raw, _ := json.Marshal(g.Captions())
	if strings.Contains(string(raw), "credential") || strings.Contains(string(raw), "source.example") {
		t.Fatalf("source address escaped %s", raw)
	}
	none := strings.Replace(captionFixture, `CLOSED-CAPTIONS="high"`, `CLOSED-CAPTIONS=NONE`, 1)
	if _, err := g.rewrite([]byte(none), "https://source.example/private/master.m3u8", 0); err != nil {
		t.Fatal(err)
	}
	if len(g.Captions().Renditions) != 0 {
		t.Fatal("invented CC1 for unknown source")
	}
	invalid := strings.Replace(captionFixture, `INSTREAM-ID="SERVICE2"`, `INSTREAM-ID="SERVICE2",URI="https://private.example/token"`, 1)
	if d, _ := selectedCaptions(selectVariant(strings.Split(invalid, "\n"))); len(d.Renditions) != 0 {
		t.Fatalf("URI-bearing caption declaration accepted %+v", d)
	}
}

func TestCaptionDeclarationsRejectHostileAttributesAndStayOnInputGroup(t *testing.T) {
	for _, name := range []string{"English\n#EXT-X-MEDIA:TYPE=SUBTITLES", "English\rURI=private", "English\"URI=private", "English\\URI=private", "English\u2028URI=private"} {
		source := strings.Replace(captionFixture, "English captions", name, 1)
		if d, _ := selectedCaptions(selectVariant(strings.Split(source, "\n"))); len(d.Renditions) != 0 {
			t.Fatalf("hostile name retained %q: %+v", name, d)
		}
	}
	// Another media type reusing the selected group is not an in-band service.
	source := strings.Replace(captionFixture, `TYPE=CLOSED-CAPTIONS,GROUP-ID="high"`, `TYPE=SUBTITLES,GROUP-ID="high"`, 1)
	if d, _ := selectedCaptions(selectVariant(strings.Split(source, "\n"))); len(d.Renditions) != 0 {
		t.Fatalf("other type retained %+v", d)
	}
	g := &Gateway{address: "http://127.0.0.1:1", resources: map[string]resource{}, reverse: map[string]string{}}
	if _, err := g.rewrite([]byte(captionFixture), "https://source.example/master.m3u8", 0); err != nil {
		t.Fatal(err)
	}
	nested := strings.ReplaceAll(captionFixture, "high", "unrelated-audio")
	if _, err := g.rewrite([]byte(nested), "https://source.example/audio.m3u8", 1); err != nil {
		t.Fatal(err)
	}
	if got := g.Captions(); got.GroupID != "high" || len(got.Renditions) != 1 || got.Renditions[0].InstreamID != "SERVICE2" {
		t.Fatalf("nested master replaced input group %+v", got)
	}
	if _, err := g.rewrite([]byte("#EXTM3U\n#EXTINF:2,\nsegment.ts\n"), "https://source.example/master.m3u8", 0); err != nil {
		t.Fatal(err)
	}
	if got := g.Captions(); len(got.Renditions) != 0 {
		t.Fatalf("absent root declaration retained %+v", got)
	}
}
