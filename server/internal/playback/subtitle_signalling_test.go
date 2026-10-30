package playback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHLSSubtitleFlagsFollowSelectionForcedAndAccessibility(t *testing.T) {
	plan := &DeliveryPlan{TextRenditions: []TextRenditionPlan{{Label: "English", Language: "en", Selected: true, Forced: true, HearingImpaired: true}, {Label: "French", Language: "fr"}}}
	master := string(hlsMasterManifest(plan))
	if !strings.Contains(master, `DEFAULT=YES,AUTOSELECT=YES,FORCED=YES,CHARACTERISTICS="public.accessibility.transcribes-spoken-dialog,public.accessibility.describes-music-and-sound"`) {
		t.Fatal(master)
	}
	if !strings.Contains(master, `LANGUAGE="fr",DEFAULT=NO,AUTOSELECT=NO,FORCED=NO`) {
		t.Fatal(master)
	}
}

func TestHLSSubtitleFilesUseTheReservedMediaNamespace(t *testing.T) {
	dir := t.TempDir()
	plan := &DeliveryPlan{Duration: 121, TextRenditions: []TextRenditionPlan{{Grant: "linear", Resource: "english", Revision: 3}}}
	if err := publishTextManifests(dir, plan); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "text-0.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		want := "/v1/media/subtitles/linear/english/3.vtt?segment=" + string(rune('0'+i))
		if !strings.Contains(string(raw), want) {
			t.Fatal("wrong subtitle address", string(raw))
		}
	}
}
