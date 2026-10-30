package playbackruntime

import (
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"strings"
	"testing"
)

func TestCaptionMasterPreservesObservedServicesAndRelativeSeekChild(t *testing.T) {
	c := linearinput.CaptionDeclarations{GroupID: "source-captions", Bandwidth: 800000, Renditions: []linearinput.CaptionRendition{{Name: "English", Language: "en", InstreamID: "CC1", Default: true, Autoselect: true}, {Name: "Français", Language: "fr", InstreamID: "SERVICE2", Autoselect: true}}}
	raw, ok := captionMaster(c, "at=12345")
	s := string(raw)
	if !ok || strings.Count(s, "TYPE=CLOSED-CAPTIONS") != 2 || !strings.Contains(s, `INSTREAM-ID="SERVICE2"`) || !strings.Contains(s, `BANDWIDTH=800000,CLOSED-CAPTIONS="source-captions"`) || !strings.HasSuffix(s, "stream.m3u8?at=12345\n") || strings.Contains(s, "http") {
		t.Fatal(ok, s)
	}
	c.Renditions[0].Name = "injected\nURI=https://private.example"
	if _, ok = captionMaster(c, ""); ok {
		t.Fatal("unsafe attribute emitted")
	}
}
func TestCaptionMasterNeverGuessesUnknownBandwidthOrCaptionService(t *testing.T) {
	for _, c := range []linearinput.CaptionDeclarations{{}, {Bandwidth: 800000}, {Renditions: []linearinput.CaptionRendition{{Name: "Unknown", InstreamID: "CC1"}}}, {Bandwidth: 800000, Renditions: []linearinput.CaptionRendition{{Name: "Unknown", InstreamID: "SERVICE64"}}}} {
		if _, ok := captionMaster(c, ""); ok {
			t.Fatalf("guessed unknown %+v", c)
		}
	}
	valid := linearinput.CaptionDeclarations{GroupID: "source-captions", Bandwidth: 800000, Renditions: []linearinput.CaptionRendition{{Name: "English", InstreamID: "CC1"}}}
	for _, query := range []string{"at=1&token=private", "at=-1", "at=1\nsecret"} {
		if _, ok := captionMaster(valid, query); ok {
			t.Fatal("unsafe seek child", query)
		}
	}
}

func TestCaptionMasterRejectsAttributeInjectionWithoutChangingSourceGroup(t *testing.T) {
	valid := linearinput.CaptionDeclarations{GroupID: "source-708", Bandwidth: 734567, Renditions: []linearinput.CaptionRendition{{Name: "English, hearing impaired", Language: "en", InstreamID: "SERVICE3", Autoselect: true}}}
	if raw, ok := captionMaster(valid, ""); !ok || !strings.Contains(string(raw), `GROUP-ID="source-708"`) || !strings.Contains(string(raw), `CLOSED-CAPTIONS="source-708"`) || !strings.Contains(string(raw), `BANDWIDTH=734567`) || !strings.Contains(string(raw), `NAME="English, hearing impaired"`) {
		t.Fatalf("source facts %v %s", ok, raw)
	}
	for _, text := range []string{"x\n#EXT-X-KEY:URI=private", "x\rURI=private", `x",URI="private`, `x\private`, "x\u2028URI=private", "x\u2029URI=private", string([]byte{0xff})} {
		for _, field := range []string{"group", "name", "language"} {
			c := valid
			c.Renditions = append([]linearinput.CaptionRendition(nil), valid.Renditions...)
			switch field {
			case "group":
				c.GroupID = text
			case "name":
				c.Renditions[0].Name = text
			case "language":
				c.Renditions[0].Language = text
			}
			if raw, ok := captionMaster(c, ""); ok || len(raw) > 0 {
				t.Fatalf("hostile %s %q emitted %s", field, text, raw)
			}
		}
	}
}
