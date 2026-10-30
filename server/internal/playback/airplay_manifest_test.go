package playback

import (
	"strings"
	"testing"
)

// Native receivers read the master, not the browser's selected track or page cues.
func TestAirPlayManifestUsesSelectedAudioAndTextRatherThanSourceDefaults(t *testing.T) {
	source := videoSource("mkv", "h264", avcDetail,
		SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Default: true},
		SourceAudio{Codec: "aac", Channels: 2, Language: "fra"})
	plan := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.HasAudioChoice = true; in.AudioStream = 2 })
	if plan.AudioStream != 2 {
		t.Fatal(plan.AudioStream)
	}
	plan.TextRenditions = []TextRenditionPlan{{Label: "Original default", Language: "en"}, {Label: "Chosen text", Language: "fr", Selected: true}}
	for _, textOn := range []bool{true, false} {
		plan.TextRenditions[1].Selected = textOn
		manifest := string(hlsMasterManifest(&plan))
		audioDefaults, textDefaults := 0, 0
		for _, line := range strings.Split(manifest, "\n") {
			if strings.Contains(line, "TYPE=AUDIO") && strings.Contains(line, "DEFAULT=YES") {
				audioDefaults++
				if !strings.Contains(line, `URI="audio-1.m3u8"`) {
					t.Fatal(line)
				}
			}
			if strings.Contains(line, "TYPE=SUBTITLES") && strings.Contains(line, "DEFAULT=YES") {
				textDefaults++
				if !textOn || !strings.Contains(line, `NAME="Chosen text"`) || !strings.Contains(line, "AUTOSELECT=YES") {
					t.Fatal(line)
				}
			}
		}
		if audioDefaults != 1 || textOn && textDefaults != 1 || !textOn && textDefaults != 0 {
			t.Fatal(manifest)
		}
	}
}

func TestAirPlayManifestPublishesOnlyTheSelectedTextOrNoSubtitlesForOff(t *testing.T) {
	plan := &DeliveryPlan{AirPlay: true, TextRenditions: []TextRenditionPlan{{Label: "Unselected forced", Forced: true}, {Label: "Chosen", Selected: true}}}
	raw := string(hlsMasterManifest(plan))
	if strings.Count(raw, "TYPE=SUBTITLES") != 1 || strings.Contains(raw, "Unselected") || !strings.Contains(raw, `NAME="Chosen"`) || !strings.Contains(raw, `URI="text-0.m3u8"`) {
		t.Fatal(raw)
	}
	plan.TextRenditions[1].Selected = false
	raw = string(hlsMasterManifest(plan))
	if strings.Contains(raw, "TYPE=SUBTITLES") || strings.Contains(raw, "SUBTITLES=") {
		t.Fatal("Off retained a subtitle rendition", raw)
	}
}
