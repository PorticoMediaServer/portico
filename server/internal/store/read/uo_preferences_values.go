package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"golang.org/x/text/language"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Values-only LOCAL DRAFT vocabulary. This grants neither writer custody nor
// current principal authority. Keys/scopes are closed enums, never column names.
type UOPreferenceKey string
type UOPreferenceScope string

const (
	UOProfileServer UOPreferenceScope = "profile-server"
	UODeviceClass   UOPreferenceScope = "profile-device-class"
	UODevice        UOPreferenceScope = "profile-device"
)
const (
	UOPrefQualityHome                        UOPreferenceKey = "quality.home"
	UOPrefQualityRemote                      UOPreferenceKey = "quality.remote"
	UOPrefQualityMobile                      UOPreferenceKey = "quality.mobile"
	UOPrefQualityDownloads                   UOPreferenceKey = "quality.downloads"
	UOPrefPlaybackSkipBackMs                 UOPreferenceKey = "playback.skipBackMs"
	UOPrefPlaybackSkipForwardMs              UOPreferenceKey = "playback.skipForwardMs"
	UOPrefPlaybackAutoplayNext               UOPreferenceKey = "playback.autoplayNext"
	UOPrefPlaybackUpNextCountdownMs          UOPreferenceKey = "playback.upNextCountdownMs"
	UOPrefPlaybackStartNextDuringCredits     UOPreferenceKey = "playback.startNextDuringCredits"
	UOPrefPlaybackPassoutProtection          UOPreferenceKey = "playback.passoutProtection"
	UOPrefPlaybackPassoutAfterEpisodes       UOPreferenceKey = "playback.passoutAfterEpisodes"
	UOPrefPlaybackIntroSkip                  UOPreferenceKey = "playback.introSkip"
	UOPrefPlaybackCreditsSkip                UOPreferenceKey = "playback.creditsSkip"
	UOPrefPlaybackRecapSkip                  UOPreferenceKey = "playback.recapSkip"
	UOPrefPlaybackDefaultSpeed               UOPreferenceKey = "playback.defaultSpeed"
	UOPrefAudiobooksDefaultSpeed             UOPreferenceKey = "audiobooks.defaultSpeed"
	UOPrefAudiobooksSkipBackMs               UOPreferenceKey = "audiobooks.skipBackMs"
	UOPrefAudiobooksSkipForwardMs            UOPreferenceKey = "audiobooks.skipForwardMs"
	UOPrefAudiobooksResumeRewindMs           UOPreferenceKey = "audiobooks.resumeRewindMs"
	UOPrefPlaybackSleepTimerMinutes          UOPreferenceKey = "playback.sleepTimerMinutes"
	UOPrefPlaybackStartedThresholdPercent    UOPreferenceKey = "playback.startedThresholdPercent"
	UOPrefPlaybackPlayedThresholdPercent     UOPreferenceKey = "playback.playedThresholdPercent"
	UOPrefPlaybackPreferredAudioLanguages    UOPreferenceKey = "playback.preferredAudioLanguages"
	UOPrefPlaybackPreferredSubtitleLanguages UOPreferenceKey = "playback.preferredSubtitleLanguages"
	UOPrefPlaybackSubtitleMode               UOPreferenceKey = "playback.subtitleMode"
	UOPrefPlaybackSubtitleSize               UOPreferenceKey = "playback.subtitleSize"
	UOPrefPlaybackSubtitleBackground         UOPreferenceKey = "playback.subtitleBackground"
	UOPrefPlaybackSubtitleColor              UOPreferenceKey = "playback.subtitleColor"
	UOPrefPlaybackSubtitlePosition           UOPreferenceKey = "playback.subtitlePosition"
	UOPrefPlaybackSubtitleFont               UOPreferenceKey = "playback.subtitleFont"
	UOPrefPlaybackKeepSubtitleStyling        UOPreferenceKey = "playback.keepSubtitleStyling"
	UOPrefPlaybackPreferSdhSubtitles         UOPreferenceKey = "playback.preferSdhSubtitles"
	UOPrefPlaybackForcedSubtitleFallback     UOPreferenceKey = "playback.forcedSubtitleFallback"
	UOPrefPlaybackUseUntaggedForced          UOPreferenceKey = "playback.useUntaggedForced"
	UOPrefPlaybackAudioDescription           UOPreferenceKey = "playback.audioDescription"
	UOPrefPlaybackAudioTrackChoice           UOPreferenceKey = "playback.audioTrackChoice"
	UOPrefPlaybackUseSystemCaptionStyle      UOPreferenceKey = "playback.useSystemCaptionStyle"
	UOPrefPlaybackShowSyncedLyrics           UOPreferenceKey = "playback.showSyncedLyrics"
	UOPrefDeliveryDirectPlay                 UOPreferenceKey = "delivery.directPlay"
	UOPrefDeliveryDirectStream               UOPreferenceKey = "delivery.directStream"
	UOPrefDeliveryTranscode                  UOPreferenceKey = "delivery.transcode"
	UOPrefMusicQualityHome                   UOPreferenceKey = "music.qualityHome"
	UOPrefMusicQualityDownloads              UOPreferenceKey = "music.qualityDownloads"
	UOPrefMusicNeverBoost                    UOPreferenceKey = "music.neverBoost"
	UOPrefMusicSmoothTransitions             UOPreferenceKey = "music.smoothTransitions"
	UOPrefMusicDsdGainDb                     UOPreferenceKey = "music.dsdGainDb"
	UOPrefMusicQualityAway                   UOPreferenceKey = "music.qualityAway"
	UOPrefMusicQualityMobile                 UOPreferenceKey = "music.qualityMobile"
	UOPrefMusicAudioNormalization            UOPreferenceKey = "music.audioNormalization"
	UOPrefMusicCrossfadeMs                   UOPreferenceKey = "music.crossfadeMs"
	UOPrefMusicGapless                       UOPreferenceKey = "music.gapless"
	UOPrefPrivacyPauseWatchHistory           UOPreferenceKey = "privacy.pauseWatchHistory"
	UOPrefPrivacyShowActivityToMembers       UOPreferenceKey = "privacy.showActivityToMembers"
	UOPrefPrivacyIncludeInWatchTogether      UOPreferenceKey = "privacy.includeInWatchTogether"
	UOPrefSearchRememberHistory              UOPreferenceKey = "search.rememberHistory"
	UOPrefRegionLocale                       UOPreferenceKey = "region.locale"
	UOPrefRegionFormats                      UOPreferenceKey = "region.formats"
	UOPrefRegionDateOrder                    UOPreferenceKey = "region.dateOrder"
	UOPrefRegionFirstDay                     UOPreferenceKey = "region.firstDay"
	UOPrefRegionSizeUnits                    UOPreferenceKey = "region.sizeUnits"
	UOPrefRegionHourCycle                    UOPreferenceKey = "region.hourCycle"
	UOPrefHomeRowOrder                       UOPreferenceKey = "home.rowOrder"
	UOPrefHomeHiddenRowIds                   UOPreferenceKey = "home.hiddenRowIds"
	UOPrefAppearanceShowBackdrops            UOPreferenceKey = "appearance.showBackdrops"
	UOPrefAppearanceCardSizePercent          UOPreferenceKey = "appearance.cardSizePercent"
	UOPrefAppearanceMotion                   UOPreferenceKey = "appearance.motion"
	UOPrefAppearanceTextSize                 UOPreferenceKey = "appearance.textSize"
	UOPrefAppearanceLayout                   UOPreferenceKey = "appearance.layout"
	UOPrefAppearanceLibraryView              UOPreferenceKey = "appearance.libraryView"
	UOPrefAppearanceEpisodesLayout           UOPreferenceKey = "appearance.episodesLayout"
	UOPrefReadingTheme                       UOPreferenceKey = "reading.theme"
	UOPrefReadingFont                        UOPreferenceKey = "reading.font"
	UOPrefReadingTextSize                    UOPreferenceKey = "reading.textSize"
	UOPrefReadingLineSpacing                 UOPreferenceKey = "reading.lineSpacing"
	UOPrefReadingMargins                     UOPreferenceKey = "reading.margins"
	UOPrefReadingAlignment                   UOPreferenceKey = "reading.alignment"
	UOPrefReadingLayout                      UOPreferenceKey = "reading.layout"
	UOPrefReadingTwoPages                    UOPreferenceKey = "reading.twoPages"
	UOPrefReadingTapToTurn                   UOPreferenceKey = "reading.tapToTurn"
	UOPrefReadingHyphenation                 UOPreferenceKey = "reading.hyphenation"
	UOPrefReadingHighlightColour             UOPreferenceKey = "reading.highlightColour"
	UOPrefReadingPdfView                     UOPreferenceKey = "reading.pdfView"
	UOPrefComicsFit                          UOPreferenceKey = "comics.fit"
	UOPrefComicsDirection                    UOPreferenceKey = "comics.direction"
	UOPrefComicsFirstPageAlone               UOPreferenceKey = "comics.firstPageAlone"
	UOPrefNotificationsBadges                UOPreferenceKey = "notifications.badges"
)

type UOValueKind string

const (
	UOBool      UOValueKind = "boolean"
	UOInteger   UOValueKind = "integer"
	UONumber    UOValueKind = "number"
	UOText      UOValueKind = "string"
	UOLocale    UOValueKind = "locale"
	UOList      UOValueKind = "string-list"
	UOLanguages UOValueKind = "languageList"
)

// Exactly one arm is populated; an explicit empty List is distinct from an
// absent override. Remove is a patch operation, never a stored value kind.
type UOValue struct {
	Kind   UOValueKind
	Bool   bool
	Number float64
	Text   string
	List   []string
}
type uoPreferenceSpec struct {
	key                UOPreferenceKey
	scope              UOPreferenceScope
	def                UOValue
	strings            []string
	numbers            []float64
	low, high          float64
	ranged             bool
	maxItems, maxRunes int
}

func uoPB(k UOPreferenceKey, d bool, s UOPreferenceScope) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UOBool, Bool: d}}
}
func uoPE(k UOPreferenceKey, d string, a []string, s UOPreferenceScope) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UOText, Text: d}, strings: a}
}
func uoPI(k UOPreferenceKey, d, lo, hi int, s UOPreferenceScope) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UOInteger, Number: float64(d)}, low: float64(lo), high: float64(hi), ranged: true}
}
func uoPIN(k UOPreferenceKey, d int, a []int, s UOPreferenceScope) uoPreferenceSpec {
	f := uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UOInteger, Number: float64(d)}}
	for _, n := range a {
		f.numbers = append(f.numbers, float64(n))
	}
	return f
}
func uoPN(k UOPreferenceKey, d float64, a []float64, s UOPreferenceScope) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UONumber, Number: d}, numbers: a}
}
func uoPL(k UOPreferenceKey, n, r int, s UOPreferenceScope) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: s, def: UOValue{Kind: UOList, List: []string{}}, maxItems: n, maxRunes: r}
}
func uoPS(k UOPreferenceKey, _ string, r int) uoPreferenceSpec {
	return uoPreferenceSpec{key: k, scope: UOProfileServer, def: UOValue{Kind: UOLocale, Text: "auto"}, maxRunes: r}
}
func uoPLa(k UOPreferenceKey) uoPreferenceSpec {
	f := uoPL(k, 8, 35, UOProfileServer)
	f.def.Kind = UOLanguages
	f.def.List = []string{"@appLanguage"}
	if k == UOPrefPlaybackPreferredAudioLanguages {
		f.def.List = append(f.def.List, "@original")
	}
	return f
}

var uoPreferenceSpecs = []uoPreferenceSpec{
	uoPE(UOPrefQualityHome, "original", append(qualityladder.IDs(), "ask"), UODeviceClass),
	uoPE(UOPrefQualityRemote, "original", append(qualityladder.IDs(), "ask"), UODeviceClass),
	uoPE(UOPrefQualityMobile, "720p-high", append(qualityladder.IDs(), "ask"), UODeviceClass),
	uoPE(UOPrefQualityDownloads, "original", append(qualityladder.IDs(), "ask"), UODeviceClass),
	uoPIN(UOPrefPlaybackSkipBackMs, 10000, []int{5000, 10000, 15000, 30000}, UOProfileServer),
	uoPIN(UOPrefPlaybackSkipForwardMs, 30000, []int{10000, 15000, 30000, 60000}, UOProfileServer),
	uoPB(UOPrefPlaybackAutoplayNext, true, UOProfileServer),
	uoPIN(UOPrefPlaybackUpNextCountdownMs, 10000, []int{0, 5000, 10000, 15000}, UOProfileServer),
	uoPB(UOPrefPlaybackStartNextDuringCredits, false, UOProfileServer),
	uoPB(UOPrefPlaybackPassoutProtection, true, UOProfileServer),
	uoPI(UOPrefPlaybackPassoutAfterEpisodes, 3, 2, 5, UOProfileServer),
	uoPE(UOPrefPlaybackIntroSkip, "ask", []string{"ask", "auto", "off"}, UOProfileServer),
	uoPE(UOPrefPlaybackCreditsSkip, "ask", []string{"ask", "auto", "off"}, UOProfileServer),
	uoPE(UOPrefPlaybackRecapSkip, "ask", []string{"ask", "auto", "off"}, UOProfileServer),
	uoPN(UOPrefPlaybackDefaultSpeed, 1, []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2}, UOProfileServer),
	uoPN(UOPrefAudiobooksDefaultSpeed, 1, []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3}, UOProfileServer),
	uoPIN(UOPrefAudiobooksSkipBackMs, 15000, []int{10000, 15000, 30000, 60000}, UOProfileServer),
	uoPIN(UOPrefAudiobooksSkipForwardMs, 30000, []int{15000, 30000, 60000, 90000}, UOProfileServer),
	uoPIN(UOPrefAudiobooksResumeRewindMs, 0, []int{0, 5000, 15000, 30000}, UOProfileServer),
	uoPIN(UOPrefPlaybackSleepTimerMinutes, 0, []int{0, 30, 60, 90, 120}, UOProfileServer),
	uoPI(UOPrefPlaybackStartedThresholdPercent, 5, 1, 25, UOProfileServer),
	uoPI(UOPrefPlaybackPlayedThresholdPercent, 95, 75, 100, UOProfileServer),
	uoPLa(UOPrefPlaybackPreferredAudioLanguages),
	uoPLa(UOPrefPlaybackPreferredSubtitleLanguages),
	uoPE(UOPrefPlaybackSubtitleMode, "foreign", []string{"off", "foreign", "forced", "always"}, UOProfileServer),
	uoPE(UOPrefPlaybackSubtitleSize, "medium", []string{"small", "medium", "large", "extra-large"}, UOProfileServer),
	uoPE(UOPrefPlaybackSubtitleBackground, "translucent", []string{"none", "outline", "shadow", "translucent", "opaque"}, UOProfileServer),
	uoPE(UOPrefPlaybackSubtitleColor, "white", []string{"white", "yellow", "cyan", "green", "pink"}, UOProfileServer),
	uoPE(UOPrefPlaybackSubtitlePosition, "bottom", []string{"bottom", "raised", "top"}, UOProfileServer),
	uoPE(UOPrefPlaybackSubtitleFont, "default", []string{"default", "serif", "mono", "rounded"}, UOProfileServer),
	uoPB(UOPrefPlaybackKeepSubtitleStyling, true, UOProfileServer),
	uoPB(UOPrefPlaybackPreferSdhSubtitles, false, UOProfileServer),
	uoPE(UOPrefPlaybackForcedSubtitleFallback, "none", []string{"none", "full"}, UOProfileServer),
	uoPB(UOPrefPlaybackUseUntaggedForced, true, UOProfileServer),
	uoPE(UOPrefPlaybackAudioDescription, "avoid", []string{"avoid", "prefer"}, UOProfileServer),
	uoPE(UOPrefPlaybackAudioTrackChoice, "best", []string{"best", "channels", "stereo"}, UOProfileServer),
	uoPB(UOPrefPlaybackUseSystemCaptionStyle, false, UODevice),
	uoPB(UOPrefPlaybackShowSyncedLyrics, true, UOProfileServer),
	uoPE(UOPrefDeliveryDirectPlay, "prefer", []string{"allow", "prefer", "never", "require"}, UODeviceClass),
	uoPE(UOPrefDeliveryDirectStream, "allow", []string{"allow", "prefer", "never", "require"}, UODeviceClass),
	uoPE(UOPrefDeliveryTranscode, "allow", []string{"allow", "prefer", "never", "require"}, UODeviceClass),
	uoPE(UOPrefMusicQualityHome, "original", qualityladder.MusicIDs(), UODeviceClass),
	uoPE(UOPrefMusicQualityDownloads, "320", qualityladder.MusicIDs(), UODeviceClass),
	uoPB(UOPrefMusicNeverBoost, false, UOProfileServer),
	uoPB(UOPrefMusicSmoothTransitions, true, UOProfileServer),
	uoPIN(UOPrefMusicDsdGainDb, -6, []int{0, -3, -6, -9, -12}, UOProfileServer),
	uoPE(UOPrefMusicQualityAway, "320", qualityladder.MusicIDs(), UODeviceClass),
	uoPE(UOPrefMusicQualityMobile, "320", qualityladder.MusicIDs(), UODeviceClass),
	uoPE(UOPrefMusicAudioNormalization, "off", []string{"off", "track", "album"}, UOProfileServer),
	uoPI(UOPrefMusicCrossfadeMs, 0, 0, 12000, UOProfileServer),
	uoPB(UOPrefMusicGapless, true, UOProfileServer),
	uoPB(UOPrefPrivacyPauseWatchHistory, false, UOProfileServer),
	uoPB(UOPrefPrivacyShowActivityToMembers, false, UOProfileServer),
	uoPB(UOPrefPrivacyIncludeInWatchTogether, true, UOProfileServer),
	uoPB(UOPrefSearchRememberHistory, true, UOProfileServer),
	uoPS(UOPrefRegionLocale, "locale", 35),
	uoPS(UOPrefRegionFormats, "locale", 35),
	uoPE(UOPrefRegionDateOrder, "auto", []string{"auto", "dmy", "mdy", "ymd"}, UOProfileServer),
	uoPE(UOPrefRegionFirstDay, "auto", []string{"auto", "mon", "sun", "sat"}, UOProfileServer),
	uoPE(UOPrefRegionSizeUnits, "binary", []string{"binary", "decimal"}, UOProfileServer),
	uoPE(UOPrefRegionHourCycle, "auto", []string{"auto", "h12", "h23"}, UOProfileServer),
	uoPL(UOPrefHomeRowOrder, 64, 128, UOProfileServer),
	uoPL(UOPrefHomeHiddenRowIds, 64, 128, UOProfileServer),
	uoPB(UOPrefAppearanceShowBackdrops, true, UODeviceClass),
	uoPI(UOPrefAppearanceCardSizePercent, 100, 75, 150, UODeviceClass),
	uoPE(UOPrefAppearanceMotion, "follow", []string{"follow", "reduce"}, UODevice),
	uoPE(UOPrefAppearanceTextSize, "standard", []string{"standard", "large"}, UODevice),
	uoPE(UOPrefAppearanceLayout, "auto", []string{"auto", "television", "touch"}, UODevice),
	uoPE(UOPrefAppearanceLibraryView, "grid", []string{"grid", "list"}, UODevice),
	uoPE(UOPrefAppearanceEpisodesLayout, "auto", []string{"auto", "tiles", "list"}, UODevice),
	uoPE(UOPrefReadingTheme, "dark", []string{"dark", "black", "sepia", "light"}, UOProfileServer),
	uoPE(UOPrefReadingFont, "literata", []string{"publisher", "literata", "manrope"}, UOProfileServer),
	uoPIN(UOPrefReadingTextSize, 5, []int{1, 2, 3, 4, 5, 6, 7}, UOProfileServer),
	uoPE(UOPrefReadingLineSpacing, "normal", []string{"tight", "normal", "loose"}, UOProfileServer),
	uoPE(UOPrefReadingMargins, "normal", []string{"narrow", "normal", "wide"}, UOProfileServer),
	uoPE(UOPrefReadingAlignment, "justified", []string{"left", "justified"}, UOProfileServer),
	uoPE(UOPrefReadingLayout, "pages", []string{"pages", "scroll"}, UOProfileServer),
	uoPB(UOPrefReadingTwoPages, true, UOProfileServer),
	uoPB(UOPrefReadingTapToTurn, true, UOProfileServer),
	uoPB(UOPrefReadingHyphenation, true, UOProfileServer),
	uoPE(UOPrefReadingHighlightColour, "yellow", []string{"yellow", "green", "blue", "pink"}, UOProfileServer),
	uoPE(UOPrefReadingPdfView, "automatic", []string{"automatic", "spread", "width", "page"}, UOProfileServer),
	uoPE(UOPrefComicsFit, "auto", []string{"auto", "width", "height"}, UOProfileServer),
	uoPE(UOPrefComicsDirection, "file", []string{"file", "ltr", "rtl", "vertical"}, UOProfileServer),
	uoPB(UOPrefComicsFirstPageAlone, true, UOProfileServer),
	uoPB(UOPrefNotificationsBadges, true, UOProfileServer),
}

func uoPreferenceSpecFor(k UOPreferenceKey) (uoPreferenceSpec, bool) {
	for _, f := range uoPreferenceSpecs {
		if f.key == k {
			return f, true
		}
	}
	return uoPreferenceSpec{}, false
}
func UOPreferenceKeys(s UOPreferenceScope) []UOPreferenceKey {
	var out []UOPreferenceKey
	for _, f := range uoPreferenceSpecs {
		if f.scope == s {
			out = append(out, f.key)
		}
	}
	return out
}
func UOPreferenceDefault(k UOPreferenceKey) (UOValue, bool) {
	f, ok := uoPreferenceSpecFor(k)
	v := f.def
	v.List = slices.Clone(v.List)
	return v, ok
}
func UOPreferenceAllowed(k UOPreferenceKey, s UOPreferenceScope) bool {
	f, ok := uoPreferenceSpecFor(k)
	return ok && f.scope == s
}
func uoValueEqual(a, b UOValue) bool {
	return a.Kind == b.Kind && a.Bool == b.Bool && a.Number == b.Number && a.Text == b.Text && slices.Equal(a.List, b.List)
}
func uoText(s string, maxRunes int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= maxRunes && !strings.ContainsAny(s, "\x00\n\r\t")
}

// UOValidatePreference canonicalizes only the typed selected arm. Numeric range
// fields retain the public clamping behavior; enum mistakes refuse explicitly.
func UOValidatePreference(k UOPreferenceKey, s UOPreferenceScope, v UOValue) (UOValue, bool, error) {
	f, ok := uoPreferenceSpecFor(k)
	if !ok || f.scope != s || v.Kind != f.def.Kind {
		return UOValue{}, false, ErrInput
	}
	if v.Kind != UOBool && v.Bool || v.Kind != UOInteger && v.Kind != UONumber && v.Number != 0 || v.Kind != UOText && v.Kind != UOLocale && v.Text != "" || v.Kind != UOList && v.Kind != UOLanguages && v.List != nil {
		return UOValue{}, false, ErrInput
	}
	clamped := false
	switch v.Kind {
	case UOBool:
	case UOInteger, UONumber:
		if math.IsNaN(v.Number) || math.IsInf(v.Number, 0) || v.Kind == UOInteger && math.Trunc(v.Number) != v.Number {
			return UOValue{}, false, ErrInput
		}
		if len(f.numbers) > 0 && !slices.Contains(f.numbers, v.Number) {
			return UOValue{}, false, ErrInput
		}
		if f.ranged {
			n := min(f.high, max(f.low, v.Number))
			clamped = n != v.Number
			v.Number = n
		}
	case UOText:
		if !slices.Contains(f.strings, v.Text) {
			return UOValue{}, false, ErrInput
		}
	case UOLocale:
		if !uoText(v.Text, f.maxRunes) || strings.Contains(v.Text, "_") {
			return UOValue{}, false, ErrInput
		}
		if v.Text != "auto" {
			tag, e := language.Parse(v.Text)
			if e != nil || tag == language.Und {
				return UOValue{}, false, ErrInput
			}
			v.Text = tag.String()
		}
	case UOList, UOLanguages:
		if len(v.List) > f.maxItems {
			return UOValue{}, false, ErrInput
		}
		v.List = slices.Clone(v.List)
		seen := map[string]bool{}
		for i, x := range v.List {
			if v.Kind == UOLanguages && x != "@appLanguage" && x != "@original" {
				if strings.Contains(x, "_") {
					return UOValue{}, false, ErrInput
				}
				tag, e := language.Parse(x)
				if e != nil || tag == language.Und {
					return UOValue{}, false, ErrInput
				}
				x = tag.String()
			}
			if !uoText(x, f.maxRunes) || seen[x] || k == UOPrefPlaybackPreferredSubtitleLanguages && x == "@original" {
				return UOValue{}, false, ErrInput
			}
			seen[x] = true
			v.List[i] = x
		}
	default:
		return UOValue{}, false, ErrInput
	}
	return v, clamped, nil
}

// UOPreferenceAddress is a native current-authority scope, supplied by the
// parent's verified principal (including the session-bound DeviceKey).
type UOPreferenceAddress struct {
	Authority, Account, Profile string
	Scope                       UOPreferenceScope
	DeviceKey                   string
}

func (a UOPreferenceAddress) Valid() bool {
	if !uoText(a.Authority, 128) || !uoText(a.Account, 256) || !uoText(a.Profile, 256) {
		return false
	}
	switch a.Scope {
	case UOProfileServer:
		return a.DeviceKey == ""
	case UODeviceClass:
		return slices.Contains([]string{"web", "mobile", "tablet", "television"}, a.DeviceKey)
	case UODevice:
		return uoText(a.DeviceKey, 256)
	}
	return false
}
func (a UOPreferenceAddress) args() []any {
	return []any{a.Authority, a.Account, a.Profile, string(a.Scope), a.DeviceKey}
}
