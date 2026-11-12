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
	f.MaxItems, f.maxLength = maxItems, maxLength
	return f
}

func pins() Setting {
	f := list("string-list", "pins", []string{}, 200, 128)
	f.prefixes = []string{"playlist:", "collection:", "view:", "library:"}
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

// downloadQualities are the choices for a download: ask each time, the original, or a step
// of the ladder (Automatic means nothing for a file kept on a device).
func downloadQualities() []string {
	out := []string{"ask", "original"}
	for _, p := range qualityladder.Fixed() {
		out = append(out, p.ID)
	}
	return out
}

// fields is every setting, in the order settings pages show them.
var fields = []Setting{
	intOf("playback.skipBackMs", 10000, []int{5000, 10000, 15000, 30000}, ScopeProfile),
	intOf("playback.skipForwardMs", 30000, []int{10000, 15000, 30000, 60000}, ScopeProfile),
	flag("playback.autoplayNext", true, ScopeProfile),
	// Stop at season end: with autoplay on, the player does not go on by itself from a season's
	// last episode into the next season. The player reads it, as it reads autoplayNext.
	flag("playback.stopAtSeasonEnd", false, ScopeProfile),
	// Rewind on resume: a film or episode resumes this much before where it stopped.
	intOf("playback.resumeRewindMs", 0, []int{0, 5000, 10000, 30000}, ScopeProfile),
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

	// Whether the account's other devices may play on this one and control what it plays.
	flag("devices.playFromOtherDevices", true, ScopeDevice),
	// Downloads belong to one device, and so do their rules. "ask" asks each time.
	oneOf("downloads.quality", "original", downloadQualities(), ScopeDevice),
	oneOf("downloads.musicQuality", "320", qualityladder.MusicIDs(), ScopeDevice),
	flag("downloads.wifiOnly", true, ScopeDevice),
	flag("downloads.deleteWatched", false, ScopeDevice),
	// The most the device's downloads may take, in gigabytes; 0 is no limit.
	intRange("downloads.storageLimitGB", 0, 0, 100_000, ScopeDevice),

	flag("privacy.pauseWatchHistory", false, ScopeProfile),
	flag("privacy.showActivityToMembers", false, ScopeProfile),
	flag("privacy.includeInWatchTogether", true, ScopeProfile),

	flag("search.rememberHistory", true, ScopeProfile),

	// On the account's main profile: a device that remembers the PIN asks for it again
	// before a sensitive setting is changed.
	flag("security.askPinForSensitiveSettings", true, ScopeProfile),

	// There is no time zone: every app shows times in its device's own zone.
	locale("region.locale"),
	locale("region.formats"),
	oneOf("region.dateOrder", "auto", []string{"auto", "dmy", "mdy", "ymd"}, ScopeProfile),
	oneOf("region.firstDay", "auto", []string{"auto", "mon", "sun", "sat"}, ScopeProfile),
	oneOf("region.sizeUnits", "binary", []string{"binary", "decimal"}, ScopeProfile),
	oneOf("region.hourCycle", "auto", []string{"auto", "h12", "h23"}, ScopeProfile),

	list("string-list", "home.rowOrder", []string{}, 64, 128),
	list("string-list", "home.hiddenRowIds", []string{}, 64, 128),
	// Saved views put on Home as rows, at most 20, by their ids.
	list("string-list", "home.savedViews", []string{}, 20, 128),
	// The profile's pins, in its own order: each names a playlist, collection, saved view or
	// library by its id. The app names them from the lists and libraries the profile may see,
	// so a pin to something since deleted or hidden from the profile is simply not shown.
	pins(),

	flag("appearance.showBackdrops", true, ScopeDeviceClass),
	intRange("appearance.cardSizePercent", 100, 75, 150, ScopeDeviceClass),
	oneOf("appearance.motion", "follow", []string{"follow", "reduce"}, ScopeDevice),
	oneOf("appearance.textSize", "standard", []string{"standard", "large"}, ScopeDevice),
	oneOf("appearance.layout", "auto", []string{"auto", "television", "touch"}, ScopeDevice),
	oneOf("appearance.libraryView", "grid", []string{"grid", "list"}, ScopeDevice),
	oneOf("appearance.episodesLayout", "auto", []string{"auto", "tiles", "list"}, ScopeDevice),

	flag("notifications.badges", true, ScopeProfile),

	// How a book's page looks belongs to each kind of device: a second phone starts from the
	// first phone's text size, and a desktop keeps its own (10-reading rule 36). A change made
	// inside the reader is the reader's own until the book closes, and is
	// not saved here.
	oneOf("reading.theme", "dark", []string{"dark", "black", "sepia", "light"}, ScopeDeviceClass),
	oneOf("reading.font", "literata", []string{"publisher", "literata", "manrope"}, ScopeDeviceClass),
	intRange("reading.textSize", 5, 1, 7, ScopeDeviceClass),
	oneOf("reading.lineSpacing", "normal", []string{"tight", "normal", "loose"}, ScopeDeviceClass),
	oneOf("reading.margins", "normal", []string{"narrow", "normal", "wide"}, ScopeDeviceClass),
	oneOf("reading.alignment", "justified", []string{"left", "justified"}, ScopeDeviceClass),
	oneOf("reading.layout", "pages", []string{"pages", "scroll"}, ScopeDeviceClass),
	// Two pages side by side when the screen is wide enough: a tablet on its side, a foldable
	// opened like a book.
	flag("reading.twoPages", true, ScopeDeviceClass),
	// Tapping the edge of the page turns it; the reader's own word breaks; the colour a new
	// highlight starts in; how a PDF's pages fit the screen.
	flag("reading.tapToTurn", true, ScopeDeviceClass),
	flag("reading.hyphenation", true, ScopeDeviceClass),
	oneOf("reading.highlightColour", "yellow", []string{"yellow", "green", "blue", "pink"}, ScopeDeviceClass),
	oneOf("reading.pdfView", "automatic", []string{"automatic", "spread", "width", "page"}, ScopeDeviceClass),
	oneOf("comics.fit", "auto", []string{"auto", "width", "height"}, ScopeDeviceClass),
	// "file" follows the comic: one that says it reads right to left opens that way.
	oneOf("comics.direction", "file", []string{"file", "ltr", "rtl", "vertical"}, ScopeDeviceClass),
	// Two pages side by side keep a comic's cover on its own, as a printed comic opens.
	flag("comics.firstPageAlone", true, ScopeDeviceClass),
}
