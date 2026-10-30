package operations

import (
	"database/sql"
	"encoding/json"
	"golang.org/x/text/language"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

// PreferenceRegistryRevision changes whenever a field is added, removed, or its
// domain moves. Clients render settings from the published registry, so the
// revision is the only thing they have to compare between releases.
const PreferenceRegistryRevision = "settings.14a.preferences.audiobooks.3"

const (
	HomeLayoutMaxRows        = 64
	HomeLayoutRowIDMaxLength = 128
	ScopeProfileServer       = "profile-server"
	ScopeDeviceClass         = "profile-device-class"
	ScopeDevice              = "profile-device"
)

// PreferenceScopes is the precedence order: later scopes win over earlier ones.
var PreferenceScopes = []string{ScopeProfileServer, ScopeDeviceClass, ScopeDevice}

// PreferenceField is the published description of one viewer preference. The
// server is the only authority on defaults, domains and clamps; a client that
// cannot find a field in this registry must not invent one.
type PreferenceField struct {
	Consumer      string   `json:"consumer"`
	Step          *float64 `json:"step,omitempty"`
	Key           string   `json:"key"`
	Type          string   `json:"type"`
	Default       any      `json:"default"`
	AllowedValues []any    `json:"allowedValues,omitempty"`
	Min           *float64 `json:"min,omitempty"`
	Max           *float64 `json:"max,omitempty"`
	Scopes        []string `json:"scopes"`
	Group         string   `json:"group"`
	LabelKey      string   `json:"labelKey"`

	maxLength int
	maxItems  int
}

func (f PreferenceField) allows(scope string) bool {
	for _, s := range f.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func group(key string) string {
	if i := strings.IndexByte(key, '.'); i > 0 {
		return key[:i]
	}
	return key
}
func label(key string) string { return "preferences." + key }
func numeric(v float64) *float64 {
	out := v
	return &out
}
func field(key, kind string, def any, scope string) PreferenceField {
	return PreferenceField{Key: key, Type: kind, Default: def, Scopes: []string{scope}, Group: group(key), LabelKey: label(key)}
}
func prefBool(key string, def bool, scope string) PreferenceField {
	return field(key, "boolean", def, scope)
}
func prefEnum(key, def string, allowed []string, scope string) PreferenceField {
	f := field(key, "string", def, scope)
	for _, v := range allowed {
		f.AllowedValues = append(f.AllowedValues, v)
	}
	return f
}
func prefInt(key string, def, low, high int, scope string) PreferenceField {
	f := field(key, "integer", def, scope)
	f.Min, f.Max, f.Step = numeric(float64(low)), numeric(float64(high)), numeric(1)
	return f
}
func prefIntEnum(key string, def int, allowed []int, scope string) PreferenceField {
	f := field(key, "integer", def, scope)
	f.Step = numeric(1)
	for _, v := range allowed {
		f.AllowedValues = append(f.AllowedValues, v)
	}
	return f
}
func prefNumberEnum(key string, def float64, allowed []float64, scope string) PreferenceField {
	f := field(key, "number", def, scope)
	for _, v := range allowed {
		f.AllowedValues = append(f.AllowedValues, v)
	}
	return f
}

func prefList(key string, maxItems, maxLength int, scope string) PreferenceField {
	f := field(key, "string-list", []string{}, scope)
	f.maxItems, f.maxLength = maxItems, maxLength
	return f
}

var (
	skipModes      = []string{"ask", "auto", "off"}
	deliveryModes  = []string{"allow", "prefer", "never", "require"}
	playbackSpeeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2}
	// Speech stays clear well past the speed at which video and music are worth watching or hearing.
	audiobookSpeeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3}
)

// SavedQualities is the domain of the saved quality defaults: the
// original file, automatic (starts at the original and moves down and back up
// as the connection requires), and each step of the server's quality ladder.
// It mirrors playback.SavedQualities; a test holds the two together.
var SavedQualities = qualityladder.IDs()

// qualityFields are a viewer's saved default qualities, one for the owner's
// home networks, one for everywhere else, and one for actual cellular transport.
// Home/remote default to Original; cellular defaults to 720p-high. A
// start that names no quality gets the applicable saved choice; the
// player can still choose another for the session.
func qualityFields() []PreferenceField {
	return []PreferenceField{
		prefEnum("quality.home", "original", SavedQualities, ScopeDeviceClass),
		prefEnum("quality.remote", "original", SavedQualities, ScopeDeviceClass),
		prefEnum("quality.mobile", "720p-high", SavedQualities, ScopeDeviceClass),
		// The saved default for a download; the server's own download ceiling still applies.
		prefEnum("quality.downloads", "original", SavedQualities, ScopeDeviceClass),
	}
}

var preferenceRegistry = func() []PreferenceField {
	out := []PreferenceField{
		prefIntEnum("playback.skipBackMs", 10000, []int{5000, 10000, 15000, 30000}, ScopeProfileServer),
		prefIntEnum("playback.skipForwardMs", 30000, []int{10000, 15000, 30000, 60000}, ScopeProfileServer),
		prefBool("playback.autoplayNext", true, ScopeProfileServer),
		prefIntEnum("playback.upNextCountdownMs", 10000, []int{0, 5000, 10000, 15000}, ScopeProfileServer),
		// Off: the credits card says when the next title starts, and it starts when this one ends.
		prefBool("playback.startNextDuringCredits", false, ScopeProfileServer),
		prefBool("playback.passoutProtection", true, ScopeProfileServer),
		prefInt("playback.passoutAfterEpisodes", 3, 2, 5, ScopeProfileServer),
		prefEnum("playback.introSkip", "ask", skipModes, ScopeProfileServer),
		prefEnum("playback.creditsSkip", "ask", skipModes, ScopeProfileServer),
		prefEnum("playback.recapSkip", "ask", skipModes, ScopeProfileServer),
		prefNumberEnum("playback.defaultSpeed", 1, playbackSpeeds, ScopeProfileServer),
		prefNumberEnum("audiobooks.defaultSpeed", 1, audiobookSpeeds, ScopeProfileServer),
		prefIntEnum("audiobooks.skipBackMs", 15000, []int{10000, 15000, 30000, 60000}, ScopeProfileServer),
		prefIntEnum("audiobooks.skipForwardMs", 30000, []int{15000, 30000, 60000, 90000}, ScopeProfileServer),
		prefIntEnum("audiobooks.resumeRewindMs", 0, []int{0, 5000, 15000, 30000}, ScopeProfileServer),
		prefIntEnum("playback.sleepTimerMinutes", 0, []int{0, 30, 60, 90, 120}, ScopeProfileServer),
		prefInt("playback.startedThresholdPercent", 5, 1, 25, ScopeProfileServer),
		prefInt("playback.playedThresholdPercent", 95, 75, 100, ScopeProfileServer),
		languagePreference("playback.preferredAudioLanguages"),
		languagePreference("playback.preferredSubtitleLanguages"),
		// Subtitles without being asked: never; only the forced track beside audio in its own
		// language (the part of a film the audio does not carry); or always, in a preferred language.
		prefEnum("playback.subtitleMode", "foreign", []string{"off", "foreign", "forced", "always"}, ScopeProfileServer),
		prefEnum("playback.subtitleSize", "medium", []string{"small", "medium", "large", "extra-large"}, ScopeProfileServer),
		prefEnum("playback.subtitleBackground", "translucent", []string{"none", "outline", "shadow", "translucent", "opaque"}, ScopeProfileServer),
		prefEnum("playback.subtitleColor", "white", []string{"white", "yellow", "cyan", "green", "pink"}, ScopeProfileServer),
		prefEnum("playback.subtitlePosition", "bottom", []string{"bottom", "raised", "top"}, ScopeProfileServer),
		prefEnum("playback.subtitleFont", "default", []string{"default", "serif", "mono", "rounded"}, ScopeProfileServer),
		prefBool("playback.keepSubtitleStyling", true, ScopeProfileServer),
		prefBool("playback.preferSdhSubtitles", false, ScopeProfileServer),
		prefEnum("playback.forcedSubtitleFallback", "none", []string{"none", "full"}, ScopeProfileServer),
		prefBool("playback.useUntaggedForced", true, ScopeProfileServer),
		prefEnum("playback.audioDescription", "avoid", []string{"avoid", "prefer"}, ScopeProfileServer),
		prefEnum("playback.audioTrackChoice", "best", []string{"best", "channels", "stereo"}, ScopeProfileServer),
		prefBool("playback.useSystemCaptionStyle", false, ScopeDevice),
		prefBool("playback.showSyncedLyrics", true, ScopeProfileServer),

		prefEnum("delivery.directPlay", "prefer", deliveryModes, ScopeDeviceClass),
		prefEnum("delivery.directStream", "allow", deliveryModes, ScopeDeviceClass),
		prefEnum("delivery.transcode", "allow", deliveryModes, ScopeDeviceClass),
	}
	out = append(out, qualityFields()...)
	out = append(out,
		prefEnum("music.qualityHome", "original", qualityladder.MusicIDs(), ScopeDeviceClass),
		prefEnum("music.qualityDownloads", "320", qualityladder.MusicIDs(), ScopeDeviceClass),
		prefBool("music.neverBoost", false, ScopeProfileServer),
		prefBool("music.smoothTransitions", true, ScopeProfileServer),
		prefIntEnum("music.dsdGainDb", -6, []int{0, -3, -6, -9, -12}, ScopeProfileServer),
		prefEnum("music.qualityAway", "320", qualityladder.MusicIDs(), ScopeDeviceClass),
		prefEnum("music.qualityMobile", "320", qualityladder.MusicIDs(), ScopeDeviceClass),
		prefEnum("music.audioNormalization", "off", []string{"off", "track", "album"}, ScopeProfileServer),
		prefInt("music.crossfadeMs", 0, 0, 12000, ScopeProfileServer),
		prefBool("music.gapless", true, ScopeProfileServer),

		prefBool("privacy.pauseWatchHistory", false, ScopeProfileServer),
		prefBool("privacy.showActivityToMembers", false, ScopeProfileServer),
		prefBool("privacy.includeInWatchTogether", true, ScopeProfileServer),

		prefBool("search.rememberHistory", true, ScopeProfileServer),

		// No region.timeZone: every client shows times in the device's own zone.
		semanticPreference("region.locale", "locale", 35),
		semanticPreference("region.formats", "locale", 35),
		prefEnum("region.dateOrder", "auto", []string{"auto", "dmy", "mdy", "ymd"}, ScopeProfileServer),
		prefEnum("region.firstDay", "auto", []string{"auto", "mon", "sun", "sat"}, ScopeProfileServer),
		prefEnum("region.sizeUnits", "binary", []string{"binary", "decimal"}, ScopeProfileServer),
		prefEnum("region.hourCycle", "auto", []string{"auto", "h12", "h23"}, ScopeProfileServer),

		prefList("home.rowOrder", HomeLayoutMaxRows, HomeLayoutRowIDMaxLength, ScopeProfileServer),
		prefList("home.hiddenRowIds", HomeLayoutMaxRows, HomeLayoutRowIDMaxLength, ScopeProfileServer),

		prefBool("appearance.showBackdrops", true, ScopeDeviceClass),
		prefInt("appearance.cardSizePercent", 100, 75, 150, ScopeDeviceClass),

		prefEnum("appearance.motion", "follow", []string{"follow", "reduce"}, ScopeDevice),
		prefEnum("appearance.textSize", "standard", []string{"standard", "large"}, ScopeDevice),
		prefEnum("appearance.layout", "auto", []string{"auto", "television", "touch"}, ScopeDevice),
		prefEnum("appearance.libraryView", "grid", []string{"grid", "list"}, ScopeDevice),
		prefEnum("appearance.episodesLayout", "auto", []string{"auto", "tiles", "list"}, ScopeDevice),
		prefEnum("reading.theme", "dark", []string{"dark", "black", "sepia", "light"}, ScopeProfileServer),
		prefEnum("reading.font", "literata", []string{"publisher", "literata", "manrope"}, ScopeProfileServer),
		prefIntEnum("reading.textSize", 5, []int{1, 2, 3, 4, 5, 6, 7}, ScopeProfileServer),
		prefEnum("reading.lineSpacing", "normal", []string{"tight", "normal", "loose"}, ScopeProfileServer),
		prefEnum("reading.margins", "normal", []string{"narrow", "normal", "wide"}, ScopeProfileServer),
		prefEnum("reading.alignment", "justified", []string{"left", "justified"}, ScopeProfileServer),
		prefEnum("reading.layout", "pages", []string{"pages", "scroll"}, ScopeProfileServer),
		prefBool("reading.twoPages", true, ScopeProfileServer),
		prefBool("reading.tapToTurn", true, ScopeProfileServer),
		prefBool("reading.hyphenation", true, ScopeProfileServer),
		prefEnum("reading.highlightColour", "yellow", []string{"yellow", "green", "blue", "pink"}, ScopeProfileServer),
		prefEnum("reading.pdfView", "automatic", []string{"automatic", "spread", "width", "page"}, ScopeProfileServer),
		prefEnum("comics.fit", "auto", []string{"auto", "width", "height"}, ScopeProfileServer),
		prefEnum("comics.direction", "file", []string{"file", "ltr", "rtl", "vertical"}, ScopeProfileServer),
		prefBool("comics.firstPageAlone", true, ScopeProfileServer),
		prefBool("notifications.badges", true, ScopeProfileServer),
	)
	for i := range out {
		out[i].Consumer = preferenceConsumers[out[i].Key]
	}
	return out
}()

var preferenceIndex = func() map[string]PreferenceField {
	out := make(map[string]PreferenceField, len(preferenceRegistry))
	for _, f := range preferenceRegistry {
		out[f.Key] = f
	}
	return out
}()

// PreferenceRegistry is the published field set. The slice is copied so a caller
// cannot reshape the server's own authority.
func PreferenceRegistry() []PreferenceField {
	out := make([]PreferenceField, len(preferenceRegistry))
	copy(out, preferenceRegistry)
	return out
}

// PreferenceFieldFor reports the registry entry for a dotted key.
func PreferenceFieldFor(key string) (PreferenceField, bool) {
	f, ok := preferenceIndex[key]
	return f, ok
}

// PreferenceValues is a flat map of dotted key to canonical value: bool, int64,
// float64, string or []string. Never a raw JSON number.
type PreferenceValues map[string]any

func (v PreferenceValues) value(key string) any {
	if raw, ok := v[key]; ok && raw != nil {
		return raw
	}
	f, ok := preferenceIndex[key]
	if !ok {
		return nil
	}
	canonical, _, e := canonicalPreference(f, f.Default)
	if e != nil {
		return nil
	}
	return canonical
}

func (v PreferenceValues) Bool(key string) bool {
	out, _ := v.value(key).(bool)
	return out
}
func (v PreferenceValues) Int(key string) int {
	switch n := v.value(key).(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
func (v PreferenceValues) Float(key string) float64 {
	switch n := v.value(key).(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}
func (v PreferenceValues) Text(key string) string {
	out, _ := v.value(key).(string)
	return out
}
func (v PreferenceValues) List(key string) []string {
	out, _ := v.value(key).([]string)
	if out == nil {
		return []string{}
	}
	return out
}

func numberOf(raw any) (float64, bool) {
	switch n := raw.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func allowedNumber(f PreferenceField, n float64) bool {
	for _, raw := range f.AllowedValues {
		if v, ok := numberOf(raw); ok && v == n {
			return true
		}
	}
	return false
}

func safeValue(v string, max int) bool {
	return utf8.ValidString(v) && strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= max && !strings.ContainsAny(v, "\n\r\t")
}

// canonicalPreference validates one submitted value against its registry entry.
// Numeric ranges clamp (and say so); every other violation is rejected, because
// silently rewriting an enum would hide a client bug behind working behaviour.
func canonicalPreference(f PreferenceField, raw any) (any, bool, error) {
	clamped := false
	switch f.Type {
	case "boolean":
		out, ok := raw.(bool)
		if !ok {
			return nil, false, ErrInvalid
		}
		return out, false, nil
	case "integer", "number":
		n, ok := numberOf(raw)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, false, ErrInvalid
		}
		if f.Type == "integer" && n != float64(int64(n)) {
			return nil, false, ErrInvalid
		}
		if len(f.AllowedValues) > 0 {
			if !allowedNumber(f, n) {
				return nil, false, ErrInvalid
			}
		} else {
			if f.Min != nil && n < *f.Min {
				n, clamped = *f.Min, true
			}
			if f.Max != nil && n > *f.Max {
				n, clamped = *f.Max, true
			}
		}
		if f.Type == "integer" {
			return int64(n), clamped, nil
		}
		return n, clamped, nil
	case "string", "locale":
		out, ok := raw.(string)
		if !ok {
			return nil, false, ErrInvalid
		}
		if len(f.AllowedValues) > 0 {
			for _, allowed := range f.AllowedValues {
				if allowed == out {
					return out, false, nil
				}
			}
			return nil, false, ErrInvalid
		}
		if !safeValue(out, f.maxLength) {
			return nil, false, ErrInvalid
		}
		if f.Type == "locale" && out != "auto" {
			if strings.Contains(out, "_") {
				return nil, false, ErrInvalid
			}
			tag, err := language.Parse(out)
			if err != nil || tag == language.Und {
				return nil, false, ErrInvalid
			}
			out = tag.String()
		}
		return out, false, nil
	case "string-list", "languageList":
		items, ok := raw.([]any)
		if !ok {
			if typed, typedOK := raw.([]string); typedOK {
				items = make([]any, len(typed))
				for i, v := range typed {
					items[i] = v
				}
			} else {
				return nil, false, ErrInvalid
			}
		}
		if len(items) > f.maxItems {
			return nil, false, ErrInvalid
		}
		out, seen := make([]string, 0, len(items)), map[string]bool{}
		for _, item := range items {
			text, ok := item.(string)
			if f.Type == "languageList" && text != "@appLanguage" && text != "@original" {
				if strings.Contains(text, "_") {
					return nil, false, ErrInvalid
				}
				tag, err := language.Parse(text)
				if err != nil || tag == language.Und {
					return nil, false, ErrInvalid
				}
				text = tag.String()
			}
			if !ok || !safeValue(text, f.maxLength) || seen[text] || text == "@original" && f.Key == "playback.preferredSubtitleLanguages" {
				return nil, false, ErrInvalid
			}
			seen[text] = true
			out = append(out, text)
		}
		return out, false, nil
	}
	return nil, false, ErrInvalid
}

func validDeviceClass(device string) bool {
	return device == "web" || device == "mobile" || device == "tablet" || device == "television"
}

// PreferenceQuery is the read surface shared by *sql.DB and *sql.Tx, so the
// effective-value accessor works inside an existing write transaction.
type PreferenceQuery interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// EffectivePreferences merges the stored scope documents over the registry
// defaults for one viewer. deviceClass may be empty when the caller only needs
// profile-server fields; device-class documents are then skipped.
func EffectivePreferences(q PreferenceQuery, v identity.Viewer, deviceClass string) (PreferenceValues, map[string]string, []string, error) {
	return EffectiveDevicePreferences(q, v, deviceClass, "")
}

// EffectiveDevicePreferences includes only the authenticated device's overrides.
// Non-device consumers call EffectivePreferences, never guessing a device ID.
func EffectiveDevicePreferences(q PreferenceQuery, v identity.Viewer, deviceClass, deviceID string) (PreferenceValues, map[string]string, []string, error) {
	values, source := PreferenceValues{}, map[string]string{}
	for _, f := range preferenceRegistry {
		canonical, _, e := canonicalPreference(f, f.Default)
		if e != nil {
			return nil, nil, nil, e
		}
		values[f.Key], source[f.Key] = canonical, "default"
	}
	for _, scope := range PreferenceScopes {
		if scope == ScopeDeviceClass && !validDeviceClass(deviceClass) || scope == ScopeDevice && deviceID == "" {
			continue
		}
		a, err := preferenceAddressFor(v, scope, deviceClass, deviceID)
		if err != nil {
			return nil, nil, nil, err
		}
		_, stored, err := readPreferenceScope(q, a)
		if err != nil {
			return nil, nil, nil, err
		}
		for key, value := range stored {
			values[key], source[key] = value, scope
		}
	}
	return values, source, []string{}, nil
}

func semanticPreference(key, kind string, maxLength int) PreferenceField {
	f := field(key, kind, "auto", ScopeProfileServer)
	f.maxLength = maxLength
	return f
}
func languagePreference(key string) PreferenceField {
	f := prefList(key, 8, 35, ScopeProfileServer)
	f.Type = "languageList"
	if key == "playback.preferredAudioLanguages" {
		f.Default = []string{"@appLanguage", "@original"}
	} else {
		f.Default = []string{"@appLanguage"}
	}
	return f
}
