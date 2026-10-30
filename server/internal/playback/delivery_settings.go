package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

// DeliveryConfiguration is the owner-administered half of delivery planning.
// Playback never reads the administration package: the owner settings registry
// is wired to this struct through DeliverySettings, so the two concerns can be
// built independently and neither owns the other's file.
type DeliveryConfiguration struct {
	// MemoryCap bounds aggregate tmpfs output; read on admission and each sweep.
	MemoryCap         string `json:"memoryCap"`
	TranscodeLocation string `json:"transcodeLocation"`
	// Empty ConversionMode retains the legacy owner boolean until the canonical
	// adapter supplies a validated mode. Canonical settings default to all.
	ConversionMode     string `json:"conversionMode"`
	OutputFormat       string `json:"outputFormat"`
	DownloadMaxQuality string `json:"downloadMaxQuality"`
	// HardwareBackend is "auto", "software" or a decoder backend name. A named
	// backend that fails its probe falls back to software; it never fails a session.
	HardwareBackend string `json:"hardwareBackend"`
	HardwareDevice  string `json:"hardwareDevice"`
	// HardwareSelection narrows conversion to one adapter by its opaque id, or
	// to the processor ("software"); "auto" or empty keeps HardwareBackend and
	// HardwareDevice as they are. SoftwareDecode (the owner's Hardware decoding
	// off) keeps decoding on the processor even where the adapter's decode has
	// been proven.
	HardwareSelection string `json:"hardwareSelection"`
	SoftwareDecode    bool   `json:"softwareDecode"`

	ToneMapping      bool   `json:"toneMapping"`
	ToneMapAlgorithm string `json:"toneMapAlgorithm"`
	SoftwarePreset   string `json:"softwarePreset"`

	// PlanningPolicy biases the plan when more than one route is admissible.
	PlanningPolicy string `json:"planningPolicy"`

	SessionStorageMiB      int  `json:"sessionStorageMiB"`
	ThrottleBufferSeconds  int  `json:"throttleBufferSeconds"`
	PlayedRetentionSeconds int  `json:"playedRetentionSeconds"`
	RemuxEnabled           bool `json:"remuxEnabled"`

	// Server clamps narrow every viewer preference. Zero means "no clamp".
	MaxVideoBitrateBPS int    `json:"maxVideoBitrateBps"`
	MaxAudioBitrateBPS int    `json:"maxAudioBitrateBps"`
	MaxVideoHeight     int    `json:"maxVideoHeight"`
	MaxMusicQuality    string `json:"maxMusicQuality"`
	AllowHDR           bool   `json:"allowHDR"`

	// Owner ceilings on simultaneous conversions. Zero means unlimited; the
	// host-capacity bound on running converters still applies underneath. A
	// session that only repackages (both streams copied) is not a conversion.
	MaxConversions         int `json:"maxConversions"`
	MaxHardwareConversions int `json:"maxHardwareConversions"`
	MaxSoftwareConversions int `json:"maxSoftwareConversions"`
}

// Planning policies. maximum_fidelity keeps the most of the source;
// maximum_compatibility prefers the most widely playable output; and
// minimize_server_work prefers the cheapest route the policy still allows.
const (
	PlanningMaximumFidelity     = "maximum_fidelity"
	PlanningMaximumCompatible   = "maximum_compatibility"
	PlanningMinimizeServerWork  = "minimize_server_work"
	DefaultThrottleBufferSecond = 60
	DefaultPlayedRetention      = 300
)

// PlanningPolicies is the published domain for the owner setting.
var PlanningPolicies = []string{PlanningMaximumFidelity, PlanningMaximumCompatible, PlanningMinimizeServerWork}

func validPlanningPolicy(p string) bool {
	for _, v := range PlanningPolicies {
		if v == p {
			return true
		}
	}
	return false
}

// DefaultDeliveryConfiguration is what a server without owner administration
// does. It is deliberately the same shape the registry will fill in.
func DefaultDeliveryConfiguration() DeliveryConfiguration {
	return DeliveryConfiguration{
		MemoryCap:         "quarter",
		TranscodeLocation: "memory",
		HardwareBackend:   "auto", HardwareSelection: "auto",
		OutputFormat: "auto", DownloadMaxQuality: "original",
		MaxMusicQuality:        "original",
		SessionStorageMiB:      4096,
		ToneMapping:            true,
		ToneMapAlgorithm:       decoder.DefaultToneMapAlgorithm,
		SoftwarePreset:         decoder.DefaultSoftwarePreset,
		PlanningPolicy:         PlanningMaximumFidelity,
		ThrottleBufferSeconds:  DefaultThrottleBufferSecond,
		PlayedRetentionSeconds: DefaultPlayedRetention,
		RemuxEnabled:           true,
		AllowHDR:               true,
	}
}

// Normalized repairs an owner configuration without refusing playback. An
// invalid stored value must never take delivery down; it falls back to the
// default for that field alone.
func (c DeliveryConfiguration) Normalized() DeliveryConfiguration {
	d := DefaultDeliveryConfiguration()
	if c.TranscodeLocation != "disk" {
		c.TranscodeLocation = "memory"
	}
	switch c.MemoryCap {
	case "quarter", "half", "2gb", "4gb", "8gb", "16gb":
	default:
		c.MemoryCap = d.MemoryCap
	}
	switch c.MaxMusicQuality {
	case "original", "cd-quality", "320", "96":
	default:
		c.MaxMusicQuality = "original"
	}
	// Until the canonical registry supplies a validated mode, the real owner
	// TranscodingEnabled input remains authoritative. Absence is not an "all" write.
	if c.ConversionMode != "all" && c.ConversionMode != "sound" && c.ConversionMode != "never" {
		c.ConversionMode = ""
	}
	if c.OutputFormat != "auto" && c.OutputFormat != "h264" {
		c.OutputFormat = d.OutputFormat
	}
	if !validDownloadQuality(c.DownloadMaxQuality) {
		c.DownloadMaxQuality = d.DownloadMaxQuality
	}
	if c.SessionStorageMiB < 64 || c.SessionStorageMiB > 1048576 {
		c.SessionStorageMiB = d.SessionStorageMiB
	}
	if c.HardwareBackend != "auto" && !decoder.ValidBackend(c.HardwareBackend) {
		c.HardwareBackend = d.HardwareBackend
	}
	if !decoder.ValidToneMapAlgorithm(c.ToneMapAlgorithm) {
		c.ToneMapAlgorithm = d.ToneMapAlgorithm
	}
	if !decoder.ValidSoftwarePreset(c.SoftwarePreset) {
		c.SoftwarePreset = d.SoftwarePreset
	}
	if !validPlanningPolicy(c.PlanningPolicy) {
		c.PlanningPolicy = d.PlanningPolicy
	}
	if c.ThrottleBufferSeconds < 10 || c.ThrottleBufferSeconds > 3600 {
		c.ThrottleBufferSeconds = d.ThrottleBufferSeconds
	}
	if c.PlayedRetentionSeconds < 0 || c.PlayedRetentionSeconds > 86400 {
		c.PlayedRetentionSeconds = d.PlayedRetentionSeconds
	}
	if c.MaxVideoBitrateBPS < 0 || c.MaxVideoBitrateBPS > 200_000_000 {
		c.MaxVideoBitrateBPS = 0
	}
	if c.MaxAudioBitrateBPS < 0 || c.MaxAudioBitrateBPS > 4_096_000 {
		c.MaxAudioBitrateBPS = 0
	}
	if c.MaxVideoHeight < 0 || c.MaxVideoHeight > 4320 {
		c.MaxVideoHeight = 0
	}
	for _, limit := range []*int{&c.MaxConversions, &c.MaxHardwareConversions, &c.MaxSoftwareConversions} {
		if *limit < 0 || *limit > 1000 {
			*limit = 0
		}
	}
	return c
}

// DeliverySettings is the single seam between owner administration and delivery.
// One method, so the administration agent can satisfy it from the settings
// registry without playback knowing that registry exists.
type DeliverySettings interface {
	Delivery() DeliveryConfiguration
}

type defaultDeliverySettings struct{}

func (defaultDeliverySettings) Delivery() DeliveryConfiguration {
	return DefaultDeliveryConfiguration()
}

// DefaultDeliverySettings is the implementation a server uses until the owner
// settings registry is wired in.
func DefaultDeliverySettings() DeliverySettings { return defaultDeliverySettings{} }

// deliveryConfiguration reads the seam defensively: a nil settings source, or
// one that returns nonsense, still yields a usable configuration.
func deliveryConfiguration(s DeliverySettings) DeliveryConfiguration {
	if s == nil {
		return DefaultDeliveryConfiguration()
	}
	return s.Delivery().Normalized()
}

func validDownloadQuality(v string) bool {
	if v == "original" {
		return true
	}
	for _, q := range qualityladder.Fixed() {
		if v == q.ID {
			return true
		}
	}
	return false
}

// AudioConversionAllowed resolves the sole mode authority. A legacy adapter
// that has not supplied a mode keeps its actual stored owner boolean.
func (c DeliveryConfiguration) AudioConversionAllowed(legacyEnabled bool) bool {
	switch c.ConversionMode {
	case "all", "sound":
		return true
	case "never":
		return false
	}
	return legacyEnabled
}

func (c DeliveryConfiguration) VideoConversionAllowed(legacyEnabled bool) bool {
	switch c.ConversionMode {
	case "all":
		return true
	case "sound", "never":
		return false
	}
	return legacyEnabled
}

// ClampDownloadQuality narrows a video request to the fixed owner ceiling.
// Audio callers keep their separate music quality policy.
func (c DeliveryConfiguration) ClampDownloadQuality(requested string) string {
	c = c.Normalized()
	cap, capped := LadderPreset(c.DownloadMaxQuality)
	if !capped {
		return requested
	}
	rung, fixed := LadderPreset(requested)
	if !fixed || rung.Height > cap.Height || rung.VideoBitrateBPS > cap.VideoBitrateBPS {
		return cap.ID
	}
	return requested
}

func (c DeliveryConfiguration) DownloadQualityAllowed(quality string) bool {
	return c.ClampDownloadQuality(quality) == quality
}
