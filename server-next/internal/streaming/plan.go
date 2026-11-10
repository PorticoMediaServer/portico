package streaming

import (
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// ErrTranscodingDisabled says the plan needs a conversion the owner turned off.
var ErrTranscodingDisabled = errors.New("This source requires conversion, which the server owner has disabled.")

// PlanRequest is everything one playback decision depends on. The caller reads
// each part from wherever it lives; planning reads no database and keeps nothing,
// so on one host the same request always gives the same plan. (What this host's
// ffmpeg can produce is set once at start: decoder.CurrentToolchain and
// SetConversionOutputs.)
type PlanRequest struct {
	// File is the catalogue's facts for the chosen version of the title.
	File FileFacts
	// Measured replaces File's streams for a source the server measured itself:
	// the media behind a STRM, or one title of a disc. Disc says which.
	Measured *DeliverySource
	Disc     bool
	// Device is the capability document the device last published. Nil plans from
	// the baseline document, as for a device that has published nothing yet.
	Device *CapabilityDocument
	// Choice is what the viewer picked: version, quality, audio track, subtitle.
	Choice Choice
	// Settings are the owner's delivery settings. Hardware and Toolchain describe
	// this host's encoder and ffmpeg build.
	Settings  DeliveryConfiguration
	Hardware  decoder.HardwareProbe
	Toolchain *decoder.ToolchainFacts
	// Preferences are the viewer's. Nil answers every key with its zero value, so
	// a caller that wants registry defaults passes a reader that applies them.
	Preferences DeliveryPreferenceReader
	// Connection is how the request reached the server.
	Connection DeliveryContext
	// TranscodingEnabled is the owner's switch for audio and video conversion.
	TranscodingEnabled bool
	// OriginalLanguage is the title's published original language, if known.
	OriginalLanguage string
	// MemberCeiling is what access admission allows this member away from home.
	MemberCeiling ReplanCeilings
	// RouteFailures are the routes this device reported broken for this file,
	// per player engine.
	RouteFailures map[string]map[DeliveryStrategy]RouteRejection
	// FragmentedOutput says this server can produce fragmented MP4 HLS.
	// NoConverter says ffmpeg was not found, so only the original route exists.
	FragmentedOutput bool
	NoConverter      bool
}

// UnmeasuredRemotePlan is the plan for a STRM whose target was never measured:
// it is relayed as it is, and the start measures it and plans again.
func UnmeasuredRemotePlan(id string, duration float64) DeliveryPlan {
	return DeliveryPlan{
		SourceID: id, SourceContainer: "strm", Mode: "remote", Strategy: DeliveryOriginal,
		VideoAction: "copy", AudioAction: "copy", AudioStream: -1, Duration: duration,
	}
}

// Plan decides how one file reaches one device: as it is, repackaged, with its
// audio converted or with its picture converted, and why each cheaper route was
// rejected. It returns the plan together with ErrTranscodingDisabled when the
// plan needs a conversion the owner has turned off.
func Plan(r PlanRequest) (DeliveryPlan, error) {
	choice := r.Choice
	values := preferenceReaderOrDefaults(r.Preferences)
	edge := r.Connection
	cfg := r.Settings
	policy := ResolveDeliveryPolicy(values, edge.NetworkClass, edge.ServerLocality, edge.TransportClass, cfg)

	input := DeliveryInput{
		SourceID: r.File.ID, Container: r.File.Container, VideoCodec: r.File.VideoCodec, AudioCodec: r.File.AudioCodec,
		Duration: r.File.Duration, Height: r.File.Height,
		Remote: r.Measured != nil && !r.Disc, Policy: policy, Target: choice.target(policy, cfg), Config: cfg,
		TranscodingEnabled: r.TranscodingEnabled, Hardware: r.Hardware,
	}
	if r.Measured != nil {
		input.Source = r.Measured
		input.Disc = r.Disc
		if r.Measured.Video != nil {
			input.Height = r.Measured.Video.Height
		}
	} else {
		source := SourceFromFacts(r.File)
		input.Source = &source
		for _, stream := range r.File.Streams {
			if stream.Type == "video" {
				input.ColorTransfer, input.ColorPrimaries = stream.ColorTransfer, stream.ColorPrimaries
				break
			}
		}
	}
	input.OriginalLanguage = r.OriginalLanguage
	input.Engine, input.SubtitleKind, input.SubtitleBesideStream = choice.Engine, choice.SubtitleKind, choice.SubtitleBesideStream
	input.CarriedAudioLanguage = choice.CarriedAudioLanguage

	input.Device = r.Device
	if input.Device == nil {
		baseline := BaselineCapabilityDocument()
		input.Device = &baseline
	}
	input.PreferredAudioLanguages = resolvePreferenceLanguages(values, "playback.preferredAudioLanguages", edge.AppLanguage)
	input.AudioTrackChoice = values.Text("playback.audioTrackChoice")
	input.PreferAudioDescription = values.Text("playback.audioDescription") == "prefer"
	input.Server = r.Toolchain
	if input.Server == nil {
		input.Server = decoder.CurrentToolchain()
	}
	input.FMP4Output = r.FragmentedOutput
	input.NoHLS = r.NoConverter
	if choice.AudioStream != nil {
		input.AudioStream, input.HasAudioChoice = *choice.AudioStream, true
	}
	input.ExcludedByEngine = r.RouteFailures

	// A quality that is the same as the source is the original: only the viewer's
	// chosen rung is removed, never the owner's or the member's own ceiling.
	if rung, ok := LadderPreset(strings.TrimPrefix(choice.QualityID, "automatic:")); ok && input.Source.Video != nil {
		rate := input.Source.BitRate
		if input.Source.Video.BitRate > 0 {
			rate = input.Source.Video.BitRate
		}
		if rung.SameAsSource(input.Source.Video.Height, rate) {
			original := choice
			original.Limit = false
			original.MaxVideoBitrateBPS = 0
			original.MaxAudioBitrateBPS = 0
			original.MaxVideoHeight = 0
			input.Target = original.target(policy, cfg)
		}
	}
	if input.Source.Video != nil {
		// Applied after the same-as-source step. A STRM over the ceiling is
		// converted like any other file.
		input.Target = narrowMemberTarget(input.Target, r.MemberCeiling)
	}
	if choice.AirPlay {
		input.Device = nil
		input.Client = BaselineClientProfile()
		input.Client.Transports = []ClientTransport{{
			Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac"},
		}}
		input.Client.SubtitleRendering = []string{SubtitleText}
		input.Engine = EngineNative
	}

	plan, err := planDelivery(input)
	if err != nil {
		return DeliveryPlan{}, err
	}
	if !r.TranscodingEnabled && (plan.AudioAction == "convert" || plan.VideoAction == "convert") {
		return plan, ErrTranscodingDisabled
	}
	return plan, nil
}
