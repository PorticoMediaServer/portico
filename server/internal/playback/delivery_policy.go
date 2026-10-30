package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"sort"
	"strings"
)

// NetworkClass is the server's own conclusion about where this playback is
// going: inside the owner's home networks or outside them. It is never taken
// from the client. One rule decides it for every purpose (the owner's "home
// networks" setting, read at the HTTP edge); nothing here looks at addresses.
type NetworkClass string

const (
	NetworkLocal   NetworkClass = "local"
	NetworkRemote  NetworkClass = "remote"
	NetworkUnknown NetworkClass = "unknown"
)

// TransportClassHeader is the optional client hint about the link it is on. It
// selects the no-explicit-choice mobile video default; it grants no locality
// or authority and cannot widen owner/member ceilings.
const TransportClassHeader = "X-Portico-Transport-Class"

// TransportClasses is the accepted domain of the header.
var TransportClasses = []string{"wifi", "cellular", "ethernet", "unknown"}

// NormalizeTransportClass folds the header into the accepted domain. "wi-fi"
// and "wired" are accepted spellings because clients on three platforms write
// them differently.
func NormalizeTransportClass(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "wifi", "wi-fi":
		return "wifi"
	case "cellular", "mobile", "wwan":
		return "cellular"
	case "ethernet", "wired":
		return "ethernet"
	}
	return "unknown"
}

// ServerLocality is the same conclusion in the words the stored policy uses.
const (
	LocalityLocal   = "local"
	LocalityRemote  = "remote"
	LocalityUnknown = "unknown"
)

// NetworkClassOf is the class of a request the edge has classified.
func NetworkClassOf(remote bool) (NetworkClass, string) {
	if remote {
		return NetworkRemote, LocalityRemote
	}
	return NetworkLocal, LocalityLocal
}

// QualityHome and QualityRemote are the server-reach preference lanes. Cellular
// transport has its independent quality.mobile choice, regardless of reach.
const (
	QualityHome   = "home"
	QualityRemote = "remote"
)

// qualityLane is which saved default a network class reads. A class nobody
// classified reads the remote one: the cautious of the two.
func (c NetworkClass) qualityLane() string {
	if c == NetworkLocal {
		return QualityHome
	}
	return QualityRemote
}

// DeliveryClamp records one narrowing the server applied over a viewer's own
// choice. Clamps only ever narrow, and each is reported with the value asked
// for and the value applied so a client can explain itself.
type DeliveryClamp struct {
	Field     string `json:"field"`
	Source    string `json:"source"`
	Requested int64  `json:"requested"`
	Applied   int64  `json:"applied"`
}

// ResolvedDeliveryPolicy is the published decision for one session: what the
// viewer asked for on this network, narrowed by what the owner permits.
type ResolvedDeliveryPolicy struct {
	NetworkClass       NetworkClass    `json:"networkClass"`
	ServerLocality     string          `json:"serverLocality"`
	TransportClass     string          `json:"transportClass"`
	PreferenceLane     string          `json:"preferenceLane"`
	DirectPlay         string          `json:"directPlay"`
	DirectStream       string          `json:"directStream"`
	Transcode          string          `json:"transcode"`
	QualityMode        string          `json:"qualityMode"`
	MaxVideoBitrateBPS int             `json:"maxVideoBitrateBps"`
	MaxAudioBitrateBPS int             `json:"maxAudioBitrateBps"`
	MaxVideoHeight     int             `json:"maxVideoHeight"`
	AllowHDR           bool            `json:"allowHDR"`
	PlanningPolicy     string          `json:"planningPolicy"`
	Clamps             []DeliveryClamp `json:"clamps"`
}

// DeliveryPreferenceReader is the viewer half of the resolution. It is the
// accessor shape operations.PreferenceValues already has, so the caller passes
// the effective values straight through without a conversion step.
type DeliveryPreferenceReader interface {
	Bool(key string) bool
	Int(key string) int
	Text(key string) string
}

// WithPreferenceSources preserves whether an effective value was stored or
// supplied by the registry. A stored Original is an explicit choice.
func WithPreferenceSources(values DeliveryPreferenceReader, sources map[string]string) DeliveryPreferenceReader {
	return sourcedPreferences{DeliveryPreferenceReader: values, sources: sources}
}

type sourcedPreferences struct {
	DeliveryPreferenceReader
	sources map[string]string
}

func (v sourcedPreferences) PreferenceSource(key string) string { return v.sources[key] }
func (v sourcedPreferences) List(key string) []string {
	if reader, ok := v.DeliveryPreferenceReader.(interface{ List(string) []string }); ok {
		return reader.List(key)
	}
	return nil
}

// SavedQualityPreference returns only an explicit saved choice, retaining its
// provenance rather than mistaking the registry's Original for a stored value.
func SavedQualityPreference(values DeliveryPreferenceReader, class NetworkClass, transport ...string) string {
	if values == nil {
		return ""
	}
	key := "quality." + class.qualityLane()
	// Cellular is independent of server reach. With no transport supplied,
	// retain the existing home/remote API for non-transport callers.
	if len(transport) > 0 && NormalizeTransportClass(transport[0]) == "cellular" {
		key = "quality.mobile"
	}
	if source, ok := values.(interface{ PreferenceSource(string) string }); ok && source.PreferenceSource(key) == "default" {
		return ""
	}
	if choice := values.Text(key); ValidSavedQuality(choice) {
		return choice
	}
	return ""
}

// DefaultSavedQuality applies only when no explicit choice exists. Cellular
// is the existing normalized transport declaration, never inferred from remote
// reach or device class. Audio fallback keeps Original; music has its own source-defined defaults.
func DefaultSavedQuality(saved, transport string, audioOnly bool) string {
	if saved != "" {
		if ValidSavedQuality(saved) {
			return saved
		}
		return QualityOriginal
	}
	if !audioOnly && NormalizeTransportClass(transport) == "cellular" {
		return "720p-high"
	}
	return QualityOriginal
}

// DeliveryModes is the domain of the three delivery preferences.
var DeliveryModes = []string{"allow", "prefer", "never", "require"}

func validDeliveryMode(m string) bool {
	for _, v := range DeliveryModes {
		if v == m {
			return true
		}
	}
	return false
}

// ResolveDeliveryPolicy combines the viewer's delivery preferences with the
// owner's ceilings for one network class. Nil preferences resolve to the
// registry defaults, so a caller that could not read preferences still gets a
// coherent policy.
//
// QualityMode is the viewer's saved default quality: quality.mobile on cellular
// transport, otherwise quality.home or quality.remote for the network class.
// The ceilings are the owner's alone. The video bitrate ceiling is a limit on
// connections from outside the home networks and is absent inside them.
func ResolveDeliveryPolicy(values DeliveryPreferenceReader, class NetworkClass, locality, transport string, cfg DeliveryConfiguration) ResolvedDeliveryPolicy {
	cfg = cfg.Normalized()
	lane := class.qualityLane()
	out := ResolvedDeliveryPolicy{
		NetworkClass:   class,
		ServerLocality: locality,
		TransportClass: NormalizeTransportClass(transport),
		PreferenceLane: lane,
		DirectPlay:     "prefer",
		DirectStream:   "allow",
		Transcode:      "allow",
		QualityMode:    DefaultSavedQuality(SavedQualityPreference(values, class, transport), transport, false),
		AllowHDR:       true,
		PlanningPolicy: cfg.PlanningPolicy,
		Clamps:         []DeliveryClamp{},
	}
	if values != nil {
		if m := values.Text("delivery.directPlay"); validDeliveryMode(m) {
			out.DirectPlay = m
		}
		if m := values.Text("delivery.directStream"); validDeliveryMode(m) {
			out.DirectStream = m
		}
		if m := values.Text("delivery.transcode"); validDeliveryMode(m) {
			out.Transcode = m
		}
	}
	if class == NetworkRemote {
		out.MaxVideoBitrateBPS = cfg.MaxVideoBitrateBPS
	}
	out.MaxAudioBitrateBPS = cfg.MaxAudioBitrateBPS
	out.MaxVideoHeight = cfg.MaxVideoHeight
	if class == NetworkRemote {
		if step, ok := qualityladder.AtBitrate(cfg.MaxVideoBitrateBPS); ok {
			out.MaxVideoHeight = narrowCeiling(out.MaxVideoHeight, step.Height)
		}
	}
	if !cfg.AllowHDR {
		out.Clamps = append(out.Clamps, DeliveryClamp{Field: "allowHDR", Source: "server_clamp", Requested: 1, Applied: 0})
		out.AllowHDR = false
	}
	if !cfg.RemuxEnabled && out.DirectStream != "never" {
		out.Clamps = append(out.Clamps, DeliveryClamp{Field: "directStream", Source: "owner_remux_disabled"})
		out.DirectStream = "never"
	}
	sort.SliceStable(out.Clamps, func(i, j int) bool { return out.Clamps[i].Field < out.Clamps[j].Field })
	return out
}

// SavedQualities is the domain of quality.home and quality.remote: the
// original, automatic, and every step of the ladder.
func SavedQualities() []string {
	out := []string{QualityOriginal, QualityAutomatic}
	for _, preset := range QualityLadder {
		out = append(out, preset.ID)
	}
	return out
}

// ValidSavedQuality reports whether v is one of SavedQualities.
func ValidSavedQuality(v string) bool {
	if v == QualityOriginal || v == QualityAutomatic {
		return true
	}
	_, ok := LadderPreset(v)
	return ok
}
