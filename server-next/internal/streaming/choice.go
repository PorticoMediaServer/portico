package streaming

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

// Choice is what one presentation is planned from besides the file and the
// device (spec §5.1–§5.4): the version, the tracks, the quality's ceilings and
// the caller's reach. The quality is the viewer's (asked for in the request, or
// their saved default for where the request comes from); the owner's ceilings,
// the member's remote cap and the upload budget always narrow it.
type Choice struct {
	// AirPlay plans a separately owned native HLS rendition, never the page renderer.
	AirPlay bool
	// UseStoredResume is server-private intent, set only for an initial stored
	// resume. Replans, transfers and already chosen positions leave it false.
	UseStoredResume bool
	// PendingActivation hides a candidate and defers personal state until acceptance.
	PendingActivation bool
	SubtitleOff       bool // actual initial V1 request; never an authority flag
	// AssetID is the version to play; empty plays the item's first available one.
	AssetID string
	// AudioStream is the absolute source stream index the viewer chose.
	AudioStream *int
	// Limit is false for the original. The ceilings are the chosen quality's
	// (a fixed quality's own, or the client's custom limit); zero means none.
	Limit              bool
	MaxVideoBitrateBPS int
	MaxAudioBitrateBPS int
	MaxVideoHeight     int
	// QualityID names the quality the viewer is on, for the stored plan:
	// "original", a ladder id, "limit", or "automatic" with its current step.
	QualityID string
	// SubtitleKind is the kind of the subtitle the viewer chose (ass, pgs,
	// vobsub, dvb); empty for none and for text delivered beside the stream.
	SubtitleKind string
	// SubtitleBesideStream says that subtitle can be handed to the device as it
	// is beside a stream (see DeliveryInput.SubtitleBesideStream).
	SubtitleBesideStream bool
	// Engine pins the player engine (a re-plan of a playing presentation whose
	// engine must not change under the viewer); empty lets planning choose.
	Engine string
	// CarriedAudioLanguage is the audio language picked in the player earlier
	// in this playback; it is tried before the preferred languages.
	CarriedAudioLanguage string
	// AdminMaxVideoBitrateBPS is the member's remote bitrate cap from access
	// admission (spec §5.3); zero means none.
	AdminMaxVideoBitrateBPS int
	// Remote is true when the request came from outside the server's LAN.
	// Remote caps (the member's and the server-wide one) apply only then.
	Remote bool
	// PrivateFor makes this a private presentation (spec §18.2): the prepared
	// next item of the given (live) presentation, sharing its stream slot and
	// serving only its first audio-render window until released.
	PrivateFor     string
	PrivateUntilMs int64
	// ClientDecodesAudio pins the version 2 audio plan (spec §18.1: the client decodes)
	// to an audio presentation, from the device's audioDecode.
	AudioServerRender  bool
	AudioCDQuality     bool
	ClientDecodesAudio bool
	AudioDecode        []AudioDecodeCap
}

func narrowCeiling(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	default:
		return b
	}
}

// target is the plan target for this choice: ceilings, never a forced rung, so
// a source within them is still played as original or copied.
func (c *Choice) target(policy ResolvedDeliveryPolicy, cfg DeliveryConfiguration) QualityTarget {
	out := QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: policy.AllowHDR}
	if c.Limit {
		out.RungID = "limit"
		out.MaxVideoBitrateBPS, out.MaxAudioBitrateBPS, out.MaxVideoHeight = c.MaxVideoBitrateBPS, c.MaxAudioBitrateBPS, c.MaxVideoHeight
	}
	if c.QualityID != "" {
		out.RungID = c.QualityID
	}
	if c.remote(policy) {
		// The server-wide limit is a remote-connection limit (delivery settings
		// adapter); a LAN viewer is never capped by it.
		out.MaxVideoBitrateBPS = narrowCeiling(narrowCeiling(out.MaxVideoBitrateBPS, c.AdminMaxVideoBitrateBPS), cfg.MaxVideoBitrateBPS)
	}
	out.MaxAudioBitrateBPS = narrowCeiling(out.MaxAudioBitrateBPS, cfg.MaxAudioBitrateBPS)
	out.MaxVideoHeight = narrowCeiling(out.MaxVideoHeight, cfg.MaxVideoHeight)
	if c.remote(policy) {
		remoteBits := narrowCeiling(c.AdminMaxVideoBitrateBPS, cfg.MaxVideoBitrateBPS)
		if step, ok := qualityladder.AtBitrate(remoteBits); ok {
			out.MaxVideoHeight = narrowCeiling(out.MaxVideoHeight, step.Height)
		}
	}
	return out
}

func (c *Choice) remote(policy ResolvedDeliveryPolicy) bool {
	return c.Remote || policy.NetworkClass == NetworkRemote
}

// AdminCapped reports whether the admin cap, not the client's own request, set
// the video ceiling (the decision then says admin_cap_remote_bitrate).
func (c Choice) AdminCapped() bool {
	if c.AdminMaxVideoBitrateBPS <= 0 || !c.Remote {
		return false
	}
	return !c.Limit || c.MaxVideoBitrateBPS <= 0 || c.AdminMaxVideoBitrateBPS < c.MaxVideoBitrateBPS
}
