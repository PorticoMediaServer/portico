package preferences

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

func setting(key, kind string, def any, scope string) Setting {
	group, _, _ := strings.Cut(key, ".")
	return Setting{Key: key, Type: kind, Default: def, Scopes: []string{scope}, Group: group, LabelKey: "preferences." + key}
}

func flag(key string, def bool, scope string) Setting { return setting(key, "boolean", def, scope) }

func choice[T any](kind, key string, def T, allowed []T, scope string) Setting {
	f := setting(key, kind, def, scope)
	for _, v := range allowed {
		f.AllowedValues = append(f.AllowedValues, v)
	}
	return f
}

func oneOf(key, def string, allowed []string, scope string) Setting {
	return choice("string", key, def, allowed, scope)
}

func intOf(key string, def int, allowed []int, scope string) Setting {
	return choice("integer", key, def, allowed, scope)
}

func intRange(key string, def, low, high int, scope string) Setting {
	f := setting(key, "integer", def, scope)
	lo, hi := float64(low), float64(high)
	f.Min, f.Max = &lo, &hi
	return f
}

func list(kind, key string, def []string, maxItems, maxLength int) Setting {
	f := setting(key, kind, def, ScopeProfile)
	f.maxItems, f.maxLength = maxItems, maxLength
	return f
}

func locale(key string) Setting {
	f := setting(key, "locale", "auto", ScopeProfile)
	f.maxLength = 35
	return f
}

var (
	skipModes     = []string{"ask", "auto", "off"}
	deliveryModes = []string{"allow", "prefer", "never", "require"}
	speeds        = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2}
	// Speech stays clear well past the speed at which video and music are worth playing.
	audiobookSpeeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3}
)

// fields is every setting, in the order settings pages show them. The settings for reading
// and for downloads join the list when the server can read and download.
var fields = []Setting{
	intOf("playback.skipBackMs", 10000, []int{5000, 10000, 15000, 30000}, ScopeProfile),
	intOf("playback.skipForwardMs", 30000, []int{10000, 15000, 30000, 60000}, ScopeProfile),
	flag("playback.autoplayNext", true, ScopeProfile),
	intOf("playback.upNextCountdownMs", 10000, []int{0, 5000, 10000, 15000}, ScopeProfile),
	// Off: the credits card says when the next title starts, and it starts when this one ends.
	flag("playback.startNextDuringCredits", false, ScopeProfile),
	flag("playback.passoutProtection", true, ScopeProfile),
	intRange("playback.passoutAfterEpisodes", 3, 2, 5, ScopeProfile),
	oneOf("playback.introSkip", "ask", skipModes, ScopeProfile),
	oneOf("playback.creditsSkip", "ask", skipModes, ScopeProfile),
	oneOf("playback.recapSkip", "ask", skipModes, ScopeProfile),
	choice("number", "playback.defaultSpeed", 1.0, speeds, ScopeProfile),
	choice("number", "audiobooks.defaultSpeed", 1.0, audiobookSpeeds, ScopeProfile),
	intOf("audiobooks.skipBackMs", 15000, []int{10000, 15000, 30000, 60000}, ScopeProfile),
	intOf("audiobooks.skipForwardMs", 30000, []int{15000, 30000, 60000, 90000}, ScopeProfile),
	intOf("audiobooks.resumeRewindMs", 0, []int{0, 5000, 15000, 30000}, ScopeProfile),
	intOf("playback.sleepTimerMinutes", 0, []int{0, 30, 60, 90, 120}, ScopeProfile),
	intRange("playback.startedThresholdPercent", 5, 1, 25, ScopeProfile),
	intRange("playback.playedThresholdPercent", 95, 50, 100, ScopeProfile),
	list("languageList", "playback.preferredAudioLanguages", []string{"@appLanguage", "@original"}, 8, 35),
	list("languageList", "playback.preferredSubtitleLanguages", []string{"@appLanguage"}, 8, 35),
	// Subtitles without being asked: never; only for speech in a language other than the
	// audio's; only the forced track; or always.
	oneOf("playback.subtitleMode", "foreign", []string{"off", "foreign", "forced", "always"}, ScopeProfile),
	oneOf("playback.subtitleSize", "medium", []string{"small", "medium", "large", "extra-large"}, ScopeProfile),
	oneOf("playback.subtitleBackground", "translucent", []string{"none", "outline", "shadow", "translucent", "opaque"}, ScopeProfile),
	oneOf("playback.subtitleColor", "white", []string{"white", "yellow", "cyan", "green", "pink"}, ScopeProfile),
	oneOf("playback.subtitlePosition", "bottom", []string{"bottom", "raised", "top"}, ScopeProfile),
	oneOf("playback.subtitleFont", "default", []string{"default", "serif", "mono", "rounded"}, ScopeProfile),
	flag("playback.keepSubtitleStyling", true, ScopeProfile),
	flag("playback.preferSdhSubtitles", false, ScopeProfile),
	oneOf("playback.forcedSubtitleFallback", "none", []string{"none", "full"}, ScopeProfile),
	flag("playback.useUntaggedForced", true, ScopeProfile),
	oneOf("playback.audioDescription", "avoid", []string{"avoid", "prefer"}, ScopeProfile),
	oneOf("playback.audioTrackChoice", "best", []string{"best", "channels", "stereo"}, ScopeProfile),
	flag("playback.useSystemCaptionStyle", false, ScopeDevice),
	flag("playback.showSyncedLyrics", true, ScopeProfile),

	// How a title may be delivered and the quality it starts at belong to one device and stay
	// on it: two phones sharing a profile each keep their own. A device with nothing saved has
	// the defaults, never another device's choice.
	oneOf("delivery.directPlay", "prefer", deliveryModes, ScopeDevice),
	oneOf("delivery.directStream", "allow", deliveryModes, ScopeDevice),
	oneOf("delivery.transcode", "allow", deliveryModes, ScopeDevice),

	// The quality a title starts at when the player names none: at home, away, and on a
	// phone's own data connection.
	oneOf("quality.home", "original", qualityladder.IDs(), ScopeDevice),
	oneOf("quality.remote", "original", qualityladder.IDs(), ScopeDevice),
	oneOf("quality.mobile", "720p-high", qualityladder.IDs(), ScopeDevice),

	oneOf("music.qualityHome", "original", qualityladder.MusicIDs(), ScopeDevice),
	oneOf("music.qualityAway", "original", qualityladder.MusicIDs(), ScopeDevice),
	oneOf("music.qualityMobile", "320", qualityladder.MusicIDs(), ScopeDevice),
	flag("music.neverBoost", false, ScopeProfile),
	flag("music.smoothTransitions", true, ScopeProfile),
	intOf("music.dsdGainDb", -6, []int{0, -3, -6, -9, -12}, ScopeProfile),
	oneOf("music.audioNormalization", "off", []string{"off", "track", "album"}, ScopeProfile),
	intRange("music.crossfadeMs", 0, 0, 12000, ScopeProfile),
	flag("music.gapless", true, ScopeProfile),

	flag("privacy.pauseWatchHistory", false, ScopeProfile),
	flag("privacy.showActivityToMembers", false, ScopeProfile),
	flag("privacy.includeInWatchTogether", true, ScopeProfile),

	flag("search.rememberHistory", true, ScopeProfile),

	// There is no time zone: every app shows times in its device's own zone.
	locale("region.locale"),
	locale("region.formats"),
	oneOf("region.dateOrder", "auto", []string{"auto", "dmy", "mdy", "ymd"}, ScopeProfile),
	oneOf("region.firstDay", "auto", []string{"auto", "mon", "sun", "sat"}, ScopeProfile),
	oneOf("region.sizeUnits", "binary", []string{"binary", "decimal"}, ScopeProfile),
	oneOf("region.hourCycle", "auto", []string{"auto", "h12", "h23"}, ScopeProfile),

	list("string-list", "home.rowOrder", []string{}, 64, 128),
	list("string-list", "home.hiddenRowIds", []string{}, 64, 128),

	flag("appearance.showBackdrops", true, ScopeDeviceClass),
	intRange("appearance.cardSizePercent", 100, 75, 150, ScopeDeviceClass),
	oneOf("appearance.motion", "follow", []string{"follow", "reduce"}, ScopeDevice),
	oneOf("appearance.textSize", "standard", []string{"standard", "large"}, ScopeDevice),
	oneOf("appearance.layout", "auto", []string{"auto", "television", "touch"}, ScopeDevice),
	oneOf("appearance.libraryView", "grid", []string{"grid", "list"}, ScopeDevice),
	oneOf("appearance.episodesLayout", "auto", []string{"auto", "tiles", "list"}, ScopeDevice),

	flag("notifications.badges", true, ScopeProfile),
}
