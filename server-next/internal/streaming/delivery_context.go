package streaming

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// DeliveryContext is what the HTTP edge knows about one request that planning
// cannot work out for itself: how far the client is and what it says it is on.
// It travels on the context because session creation is reached through several
// routes (direct create, queue transition, recovery) that share no signature.
type DeliveryContext struct {
	NetworkClass   NetworkClass
	ServerLocality string
	TransportClass string
	DeviceClass    string
	AppLanguage    string
}

type deliveryContextKey struct{}

// WithDeliveryContext attaches the edge's observation. A context without one
// resolves to the unknown lane, which is a real policy and not an error.
func WithDeliveryContext(ctx context.Context, d DeliveryContext) context.Context {
	if d.NetworkClass == "" {
		d.NetworkClass = NetworkUnknown
	}
	if d.ServerLocality == "" {
		d.ServerLocality = LocalityUnknown
	}
	d.TransportClass = NormalizeTransportClass(d.TransportClass)
	return context.WithValue(ctx, deliveryContextKey{}, d)
}

// DeliveryContextFrom reads the edge observation, defaulting to unknown.
func DeliveryContextFrom(ctx context.Context) DeliveryContext {
	if ctx != nil {
		if d, ok := ctx.Value(deliveryContextKey{}).(DeliveryContext); ok {
			return d
		}
	}
	return DeliveryContext{NetworkClass: NetworkUnknown, ServerLocality: LocalityUnknown, TransportClass: "unknown"}
}

// withoutHardwareDecode removes the proven decode formats when the owner turned
// Hardware decoding off, so every conversion decodes on the processor.
func withoutHardwareDecode(probe decoder.HardwareProbe, cfg DeliveryConfiguration) decoder.HardwareProbe {
	if cfg.SoftwareDecode {
		probe.DecodeCodecs = nil
	}
	return probe
}

// automaticSubtitles is what the viewer wants from subtitles without being
// asked, beside the audio language this session will actually play.
func preferenceReaderOrDefaults(values DeliveryPreferenceReader) DeliveryPreferenceReader {
	if values == nil {
		return noPreferences{}
	}
	return values
}

func resolvePreferenceLanguages(values DeliveryPreferenceReader, key, appLanguage string) []string {
	values = preferenceReaderOrDefaults(values)
	var languages []string
	if lister, ok := values.(interface{ List(string) []string }); ok && values != nil {
		languages = lister.List(key)
	}
	locale := appLanguage
	if locale == "" && values != nil {
		locale = values.Text("region.locale")
	}
	if locale == "auto" {
		locale = ""
	}
	// The app's supported-catalogue fallback is English. A missing device fact
	// follows that fallback; it never becomes a guessed title language.
	if locale == "" {
		locale = "en-US"
	}
	out := make([]string, 0, len(languages))
	for _, lang := range languages {
		if lang == "@appLanguage" {
			lang = locale
		}
		out = append(out, lang)
	}
	return out
}

func automaticSubtitles(values DeliveryPreferenceReader, trace *DecisionTrace, appLanguages ...string) subtitles.AutoChoice {
	var out subtitles.AutoChoice
	appLanguage := ""
	if len(appLanguages) > 0 {
		appLanguage = appLanguages[0]
	}
	values = preferenceReaderOrDefaults(values)
	mode := values.Text("playback.subtitleMode")
	out.Enabled, out.Off = mode == "always", mode == "off"
	out.Languages = resolvePreferenceLanguages(values, "playback.preferredSubtitleLanguages", appLanguage)
	out.NoLanguage = len(out.Languages) == 0
	out.PreferSDH = valuesBool(values, "playback.preferSdhSubtitles", false)
	out.DisableUntaggedForced = !valuesBool(values, "playback.useUntaggedForced", true)
	out.FullFallback = values.Text("playback.forcedSubtitleFallback") == "full"
	if trace != nil && trace.Audio != nil {
		out.AudioLanguage = trace.Audio.Language
		if mode == "foreign" && languageKey(out.AudioLanguage) != "" {
			// Original language is not one of 'my languages'. With an explicit empty
			// audio list, no language is declared familiar.
			out.ForeignAudio = true
			for _, lang := range resolvePreferenceLanguages(values, "playback.preferredAudioLanguages", appLanguage) {
				if lang != "@original" && languageKey(lang) == languageKey(out.AudioLanguage) {
					out.ForeignAudio = false
					break
				}
			}
		}
	}
	return out
}

func valuesBool(values DeliveryPreferenceReader, key string, fallback bool) bool {
	if v, ok := values.(interface{ Bool(string) bool }); ok {
		return v.Bool(key)
	}
	return fallback
}

// noPreferences answers every key with its zero value. A caller that wants the
// preference registry's defaults passes a reader that applies them.
type noPreferences struct{}

func (noPreferences) Bool(string) bool   { return false }
func (noPreferences) Int(string) int     { return 0 }
func (noPreferences) Text(string) string { return "" }
