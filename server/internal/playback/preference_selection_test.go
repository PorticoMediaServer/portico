package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"testing"
)

func TestApprovedAudioLanguageAndTrackPreferences(t *testing.T) {
	tracks := []SourceAudio{{Index: 1, Language: "ja", Codec: "aac", Channels: 2, Default: true}, {Index: 2, Language: "eng", Codec: "truehd", Channels: 8}, {Index: 3, Language: "eng", Codec: "aac", Channels: 2}, {Index: 4, Language: "eng", Codec: "aac", Channels: 2, AudioDescription: true}}
	values := operations.PreferenceValues{}
	values["region.locale"] = "de"
	langs := resolvePreferenceLanguages(values, "playback.preferredAudioLanguages", "en-CA")
	if len(langs) != 2 || langs[0] != "en-CA" || langs[1] != "@original" {
		t.Fatal(langs)
	}
	for _, tc := range []struct {
		policy      string
		description bool
		want        int
	}{{"best", false, 3}, {"channels", false, 2}, {"stereo", false, 3}, {"best", true, 4}} {
		got, _ := chooseAudioWithPolicy(tracks, -1, "", langs, "", tc.policy, tc.description, chromeProfile())
		if got == nil || got.Index != tc.want {
			t.Fatal(tc, got)
		}
	}
	got, _ := chooseAudioWithPolicy(tracks, 2, "", langs, "", "best", true, chromeProfile())
	if got == nil || got.Index != 2 {
		t.Fatal("explicit session choice overridden", got)
	}
	got, _ = chooseAudioWithPolicy(tracks, -1, "", []string{"@original", "en"}, "", "best", false, chromeProfile())
	if got == nil || got.Index != 1 {
		t.Fatal("original marker lost its ordered priority", got)
	}
	got, _ = chooseAudioWithPolicy(tracks, -1, "", nil, "", "best", false, chromeProfile())
	if got == nil || got.Index != 1 {
		t.Fatal("empty audio languages lost file default", got)
	}
}

func TestForeignAndForcedSubtitleModesAreDistinct(t *testing.T) {
	values := operations.PreferenceValues{}
	trace := &DecisionTrace{Audio: &TraceAudio{Language: "jpn"}}
	foreign := automaticSubtitles(values, trace, "en-US")
	if defaults := automaticSubtitles(nil, trace, "en-US"); !defaults.ForeignAudio || len(defaults.Languages) != 1 {
		t.Fatal("missing injection did not use registry defaults", defaults)
	}
	if !foreign.ForeignAudio || len(foreign.Languages) != 1 || foreign.Languages[0] != "en-US" {
		t.Fatal(foreign)
	}
	tracks := []subtitles.AutoCandidate{{ID: "full", Language: "eng"}, {ID: "signs", Language: "jpn", Forced: true}, {ID: "sdh", Language: "eng", HearingImpaired: true}}
	if got := subtitles.ChooseAutomatic(foreign, tracks); got == nil || got.ID != "full" {
		t.Fatal(got)
	}
	values["playback.subtitleMode"] = "forced"
	forced := automaticSubtitles(values, trace, "en-US")
	if forced.ForeignAudio {
		t.Fatal(forced)
	}
	if got := subtitles.ChooseAutomatic(forced, tracks); got == nil || got.ID != "signs" {
		t.Fatal(got)
	}
	values["playback.subtitleMode"] = "always"
	values["playback.preferSdhSubtitles"] = true
	if got := subtitles.ChooseAutomatic(automaticSubtitles(values, trace, "en-US"), tracks); got == nil || got.ID != "sdh" {
		t.Fatal(got)
	}
	values["playback.preferredSubtitleLanguages"] = []string{}
	if got := subtitles.ChooseAutomatic(automaticSubtitles(values, trace, "en-US"), tracks); got != nil {
		t.Fatal("explicit no preference chose a subtitle", got)
	}
	values["playback.preferredSubtitleLanguages"] = []string{"en"}
	values["playback.subtitleMode"] = "forced"
	values["playback.forcedSubtitleFallback"] = "full"
	if got := subtitles.ChooseAutomatic(automaticSubtitles(values, trace, "en-US"), tracks[:1]); got == nil || got.ID != "full" {
		t.Fatal(got)
	}
}

func TestAudioSelectionPolicyReachesActualPlan(t *testing.T) {
	source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "truehd", Channels: 8, Language: "en", Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: "en"})
	plan := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.PreferredAudioLanguages = []string{"en"}; in.AudioTrackChoice = "best" })
	if plan.AudioStream != 2 || plan.SourceAudioCodec != "aac" || plan.AudioAction == "transcode" {
		t.Fatal(plan.AudioStream, plan.SourceAudioCodec, plan.AudioAction)
	}
}

func TestOriginalAudioLanguageSelection(t *testing.T) {
	dubbed := []SourceAudio{{Index: 1, Language: "eng", Default: true}, {Index: 2, Language: "jpn"}, {Index: 3, Language: "fra"}}
	for _, tc := range []struct {
		name, original, carried string
		preferred               []string
		tracks                  []SourceAudio
		explicit, want          int
		reason                  string
	}{
		{name: "known Japanese over English dub", original: "ja", preferred: []string{"@original"}, tracks: dubbed, want: 2, reason: "original_language"},
		{name: "known primary subtag", original: "ja-JP", preferred: []string{"@original"}, tracks: dubbed, want: 2, reason: "original_language"},
		{name: "unknown keeps default", preferred: []string{"@original", "fr"}, tracks: dubbed, want: 1, reason: "original_default"},
		{name: "unknown xx keeps default", original: "xx", preferred: []string{"@original"}, tracks: dubbed, want: 1, reason: "original_default"},
		{name: "known missing falls through", original: "ko", preferred: []string{"@original", "fr"}, tracks: dubbed, want: 3, reason: "preferred_language"},
		{name: "known missing exhausts list", original: "ko", preferred: []string{"@original"}, tracks: dubbed, want: 1, reason: "default"},
		{name: "empty list is original", original: "ja", tracks: dubbed, want: 2, reason: "original_language"},
		{name: "empty list unknown keeps default", tracks: dubbed, want: 1, reason: "original_default"},
		{name: "explicit keeps precedence", original: "ja", preferred: []string{"@original"}, tracks: dubbed, explicit: 3, want: 3, reason: "explicit"},
		{name: "carried keeps precedence", original: "ja", preferred: []string{"@original"}, tracks: dubbed, carried: "eng-US", want: 1, reason: "carried"},
		{name: "known excludes commentary", original: "ja", preferred: []string{"@original", "fr"}, tracks: []SourceAudio{{Index: 1, Language: "jpn", Default: true, Commentary: true}, {Index: 2, Language: "fra"}}, want: 2, reason: "preferred_language"},
		{name: "unknown uses actual default even commentary", tracks: []SourceAudio{{Index: 1, Default: true, Commentary: true}, {Index: 2, Language: "en"}}, want: 1, reason: "original_default"},
		{name: "unknown without default chooses ordinary", tracks: []SourceAudio{{Index: 1, Commentary: true}, {Index: 2, Language: "ja"}}, want: 2, reason: "original_first"},
		{name: "exhausted list chooses first", original: "ja", preferred: []string{"@original"}, tracks: []SourceAudio{{Index: 1, Commentary: true}, {Index: 2, Language: "en"}}, want: 1, reason: "first"},
		{name: "Cantonese provider spelling", original: "cn", tracks: []SourceAudio{{Index: 1, Language: "zh", Default: true}, {Index: 2, Language: "yue-Hant"}}, want: 2, reason: "original_language"},
		{name: "Chinese bibliographic spelling", original: "zh-TW", tracks: []SourceAudio{{Index: 1, Language: "en", Default: true}, {Index: 2, Language: "chi"}}, want: 2, reason: "original_language"},
		{name: "Chinese terminologic spelling", original: "zho", tracks: []SourceAudio{{Index: 1, Language: "en", Default: true}, {Index: 2, Language: "zh-CN"}}, want: 2, reason: "original_language"},
		{name: "untagged never matches known language", original: "ja", preferred: []string{"@original", "fr"}, tracks: []SourceAudio{{Index: 1, Language: "und", Default: true}, {Index: 2}, {Index: 3, Language: "fra"}}, want: 3, reason: "preferred_language"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			explicit := -1
			if tc.explicit > 0 {
				explicit = tc.explicit
			}
			got, reason := chooseAudioWithPolicy(tc.tracks, explicit, tc.carried, tc.preferred, tc.original, "best", false, chromeProfile())
			if got == nil || got.Index != tc.want || reason != tc.reason {
				t.Fatal(got, reason, "want", tc.want, tc.reason)
			}
		})
	}
}

func TestOriginalAudioFormatTiesAndFallbackIdentity(t *testing.T) {
	tracks := []SourceAudio{{Index: 1, Language: "eng", Codec: "aac", Channels: 2, Default: true}, {Index: 2, Language: "jpn", Codec: "truehd", Channels: 8}, {Index: 3, Language: "ja", Codec: "aac", Channels: 2}}
	got, reason := chooseAudioWithPolicy(tracks, -1, "", []string{"@original"}, "ja", "best", false, chromeProfile())
	if got == nil || got.Index != 3 || reason != "original_language" {
		t.Fatal("known original format tie", got, reason)
	}
	tracks = []SourceAudio{{Index: 1, Language: "eng", Codec: "aac", Channels: 2, Default: true}, {Index: 2, Language: "en", Codec: "aac", Channels: 6}}
	got, reason = chooseAudioWithPolicy(tracks, -1, "", nil, "", "channels", false, chromeProfile())
	if got == nil || got.Index != 1 || reason != "original_default" {
		t.Fatal("format policy replaced unknown-original default", got, reason)
	}
	tracks[0].Default = false
	got, reason = chooseAudioWithPolicy(tracks, -1, "", nil, "", "channels", false, chromeProfile())
	if got == nil || got.Index != 1 || reason != "original_first" {
		t.Fatal("format policy replaced unknown-original first ordinary track", got, reason)
	}
}

// The server-only input is a supplied planner fact here. Publication and the
// authorized same-snapshot metadata reader are covered by their producer lane.
func TestOriginalLanguageInputReachesPlanTrace(t *testing.T) {
	source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: "jpn"})
	plan := decide(t, source, appleTVProfile(), func(in *DeliveryInput) {
		in.PreferredAudioLanguages = []string{"@original"}
		in.OriginalLanguage = "ja"
	})
	if plan.AudioStream != 2 || plan.Trace.Audio == nil || plan.Trace.Audio.ChosenBy != "original_language" {
		t.Fatal(plan.Trace)
	}
	if !automaticSubtitles(operations.PreferenceValues{}, plan.Trace, "en-US").ForeignAudio {
		t.Fatal("foreign subtitles did not compare against selected Japanese track")
	}
}
