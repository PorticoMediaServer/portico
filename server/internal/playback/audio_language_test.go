package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// A two-letter audio preference matches a track tagged with either
// three-letter form of the same language, and such audio is not treated as
// foreign when subtitles are chosen automatically.
func TestAudioPreferenceMatchesThreeLetterTrackLanguages(t *testing.T) {
	picture := assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}
	for _, pair := range [][2]string{{"mal", "ml"}, {"kan", "kn"}, {"afr", "af"}, {"sqi", "sq"}, {"alb", "sq"}, {"cym", "cy"}, {"wel", "cy"}, {"khm", "km"}, {"bul", "bg"}} {
		source := videoSource("mp4", "h264", picture, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: pair[0]})
		p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.PreferredAudioLanguages = []string{pair[1]} })
		if p.AudioStream != 2 || p.Trace.Audio.ChosenBy != "preferred_language" {
			t.Fatalf("a %q preference did not choose the %q track: %+v", pair[1], pair[0], p.Trace.Audio)
		}
		choice := automaticSubtitles(listPreferences{"playback.preferredAudioLanguages": {pair[1]}}, p.Trace)
		if choice.ForeignAudio {
			t.Fatalf("%q audio was treated as foreign to a %q preference", pair[0], pair[1])
		}
	}
	if languageKey("unknown") != "" || languageKey("und") != "" || languageKey("pt-BR") != "pt" || languageKey("ENG") != "en" {
		t.Fatal("language folding changed for tags it already handled")
	}
}
