package playback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// CapabilityDocumentVersion is the wire version of the capability document: the
// one description of what a device can play. A device publishes it once per
// distinct document; planning reads the stored copy, so every route that
// creates or re-plans a session sees the same facts. A version this server
// does not know is refused rather than half-read.
const CapabilityDocumentVersion = 2

// MaxCapabilityDocumentBytes bounds one published document (two engines).
const MaxCapabilityDocumentBytes = 64 << 10

// Evidence says how a fact was obtained. "declared" is the app's own table for
// its platform; "probed" was asked of the device at runtime (MediaCapabilities,
// MediaCodecList, AVFoundation, the HDMI sink). Planning trusts both equally; the
// distinction exists so a wrong decision can be traced to its source.
const (
	EvidenceDeclared = "declared"
	EvidenceProbed   = "probed"
	EvidenceMixed    = "mixed"
	EvidenceBuiltin  = "builtin"
)

// Transports a device can take. "direct" is the original file by byte range.
const (
	TransportDirect  = "direct"
	TransportHLSTS   = "hls_ts"
	TransportHLSFMP4 = "hls_fmp4"
)

// Engines a device may describe. Every device has the platform's own player
// ("native"); some also carry mpv.
const (
	EngineNative = "native"
	EngineMPV    = "mpv"
)

// The viewer's player setting. Automatic lets the server pick per title.
const (
	EngineSettingAutomatic = "automatic"
	EngineSettingNative    = "native"
	EngineSettingMPV       = "mpv"
)

// Subtitle kinds an engine can draw itself (the subtitleRendering list).
const (
	SubtitleText   = "text"
	SubtitleASS    = "ass"
	SubtitlePGS    = "pgs"
	SubtitleVobSub = "vobsub"
	SubtitleDVB    = "dvb"
)

var subtitleRenderingDomain = map[string]bool{SubtitleText: true, SubtitleASS: true, SubtitlePGS: true, SubtitleVobSub: true, SubtitleDVB: true}

// ClientIdentity names the device and the app. It is diagnostic: nothing in
// planning branches on it.
type ClientIdentity struct {
	Family string `json:"family"`
	// Form is the kind of device: tv, phone, tablet, desktop, browser, cast, speaker.
	Form            string `json:"form,omitempty"`
	Platform        string `json:"platform,omitempty"`
	PlatformVersion string `json:"platformVersion,omitempty"`
	OS              string `json:"os,omitempty"`
	OSVersion       string `json:"osVersion,omitempty"`
	Model           string `json:"model,omitempty"`
	App             string `json:"app,omitempty"`
	AppVersion      string `json:"appVersion,omitempty"`
}

// ClientDisplay describes the panel or HDMI sink: what it shows, as distinct
// from what an engine decodes. An empty DynamicRanges means "not stated".
type ClientDisplay struct {
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	DynamicRanges []string `json:"dynamicRanges"`
	MaxFrameRate  float64  `json:"maxFrameRate"`
}

// ClientVideoCodec is one video decoder of an engine.
type ClientVideoCodec struct {
	ChromaSubsampling []string `json:"chromaSubsampling,omitempty"`
	FrameRateModes    []string `json:"frameRateModes,omitempty"`
	Codec             string   `json:"codec"`
	Profiles          []string `json:"profiles"`
	MaxLevel          int      `json:"maxLevel"`
	BitDepths         []int    `json:"bitDepths"`
	MaxWidth          int      `json:"maxWidth"`
	MaxHeight         int      `json:"maxHeight"`
	MaxFrameRate      float64  `json:"maxFrameRate"`
	MaxBitrateBPS     int      `json:"maxBitrateBps"`
	// DynamicRanges is what the decoder decodes. Whether the result can be shown
	// is the display's list, or the engine's own tone mapping (toneMapsHdr).
	DynamicRanges       []string `json:"dynamicRanges"`
	DolbyVisionProfiles []int    `json:"dolbyVisionProfiles"`
	// Optional exact profile-8 base compatibility: 1 HDR10, 2 SDR, 4 HLG.
	DolbyVisionProfile8Compatibility []int `json:"dolbyVisionProfile8Compatibility,omitempty"`
	Interlaced                       bool  `json:"interlaced"`
	// CodecTags, when stated, are the only sample entries the engine plays from
	// an original file (AVFoundation takes hvc1 but not hev1). Streams the server
	// repackages are tagged by the server and are not checked against it.
	CodecTags []string `json:"codecTags,omitempty"`
	// Limits, when stated, are the decoder's operating points: a picture must
	// also fit one that applies to its bit depth. The fields above remain the
	// codec's overall ceilings.
	Limits   []ClientVideoLimit `json:"limits,omitempty"`
	Evidence string             `json:"evidence,omitempty"`
}

// ClientAudioCodec is one audio decoder of an engine: the device turns the
// codec into sound itself.
type ClientAudioCodec struct {
	Variants      []string `json:"variants,omitempty"`
	Codec         string   `json:"codec"`
	MaxChannels   int      `json:"maxChannels"`
	MaxSampleRate int      `json:"maxSampleRate"`
	// SampleRates narrows a decoder to configurations it actually accepted.
	// An omitted list retains the existing ceiling-only declaration.
	SampleRates []int    `json:"sampleRates,omitempty"`
	MaxBitDepth int      `json:"maxBitDepth,omitempty"`
	ObjectAudio []string `json:"objectAudio"`
	Evidence    string   `json:"evidence,omitempty"`
}

// ClientAudioPassthrough is a codec the engine can hand to the audio output
// undecoded (a receiver or soundbar decodes it). It is used only while the
// output route carries bitstreams and the viewer has not turned it off.
type ClientAudioPassthrough struct {
	Variants    []string `json:"variants,omitempty"`
	Codec       string   `json:"codec"`
	MaxChannels int      `json:"maxChannels"`
	ObjectAudio []string `json:"objectAudio"`
	Evidence    string   `json:"evidence,omitempty"`
}

type ClientTransport struct {
	Transport  string   `json:"transport"`
	Containers []string `json:"containers"`
	Video      []string `json:"video"`
	Audio      []string `json:"audio"`
}

// ClientAudioOutput is where sound leaves the device right now.
type ClientAudioOutput struct {
	// Route is hdmi, arc, earc, optical, spdif, speaker, headphones, bluetooth,
	// airplay or another short name. Passthrough needs one of the first five.
	Route       string `json:"route"`
	MaxChannels int    `json:"maxChannels"`
	Spatial     bool   `json:"spatial"`
}

// CapabilityOverrides are the viewer's "if something looks or sounds wrong"
// switches. They narrow what the device is planned as; they never widen it.
type CapabilityOverrides struct {
	// ForceStereo plans every title as two channels, decoded by the device.
	ForceStereo bool `json:"forceStereo"`
	// DisablePassthrough ignores audioPassthrough: the device decodes or the
	// server converts.
	DisablePassthrough bool `json:"disablePassthrough"`
	// DisableHDR plans the device as standard dynamic range only.
	DisableHDR bool `json:"disableHdr"`
}

// EngineCapabilities is what one player engine on the device can really do.
type EngineCapabilities struct {
	// Name and Version identify the engine itself: avplayer, exoplayer, hls.js, mpv.
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Evidence string `json:"evidence"`

	Video            []ClientVideoCodec       `json:"video"`
	Audio            []ClientAudioCodec       `json:"audio"`
	AudioPassthrough []ClientAudioPassthrough `json:"audioPassthrough"`
	Transports       []ClientTransport        `json:"transports"`
	// SubtitleRendering lists the subtitle kinds the engine draws itself: text,
	// ass, pgs, vobsub, dvb. A kind not listed is burned in when it cannot be
	// delivered as text.
	SubtitleRendering []string `json:"subtitleRendering"`
	// ToneMapsHDR is true when the engine shows HDR it decodes acceptably on a
	// display that is not HDR (it tone-maps by itself).
	ToneMapsHDR bool `json:"toneMapsHdr"`
	// EmbeddedAudioSwitching is true when the engine can choose between audio
	// tracks inside an original file. Without it, direct playback uses the first
	// default-flag track, or the first track if none is flagged.
	EmbeddedAudioSwitching bool `json:"embeddedAudioSwitching"`
	// LiveChannels is true when a second engine (mpv) plays live channels: a
	// timeshift window positioned on the channel's own clock. The native
	// engine always does; a second engine that does not say so is never
	// given a channel.
	LiveChannels bool `json:"liveChannels,omitempty"`
}

// CapabilityEngines holds one profile per engine. Native is required.
type CapabilityEngines struct {
	Native *EngineCapabilities `json:"native"`
	MPV    *EngineCapabilities `json:"mpv,omitempty"`
}

// ClientAudioDecode is one audioDecode entry: what the client's own music
// engine decodes, for audio presentations the client renders itself.
type ClientAudioDecode struct {
	Codec         string   `json:"codec"`
	Containers    []string `json:"containers"`
	MaxSampleRate int      `json:"maxSampleRate,omitempty"`
	SampleRates   []int    `json:"sampleRates,omitempty"`
	MaxChannels   int      `json:"maxChannels,omitempty"`
	MaxBitDepth   int      `json:"maxBitDepth,omitempty"`
	Via           string   `json:"via,omitempty"`
}

// CapabilityDocument is the wire format and the stored format: what a device
// publishes with PUT /v1/me/devices/current/capabilities.
type CapabilityDocument struct {
	Version int            `json:"version"`
	Client  ClientIdentity `json:"client"`
	// EngineSetting is the viewer's player setting on this device: automatic,
	// native or mpv. Empty reads as automatic.
	EngineSetting string              `json:"engineSetting"`
	Overrides     CapabilityOverrides `json:"overrides"`
	Display       ClientDisplay       `json:"display"`
	AudioOutput   ClientAudioOutput   `json:"audioOutput"`
	// MaxBitrateBPS is the device's own ceiling (a slow Wi-Fi chip). Zero means
	// none; quality and the owner's ceilings still apply on top of it.
	MaxBitrateBPS int               `json:"maxBitrateBps"`
	Engines       CapabilityEngines `json:"engines"`
	// AudioDecode is what the client's own music engine decodes.
	AudioDecode []ClientAudioDecode `json:"audioDecode,omitempty"`

	// Revision is a digest of the accepted document, assigned by the server.
	Revision string `json:"-"`
}

// ClientProfile is one engine of a capability document as planning reads it:
// the engine's own tables beside the device's display, output and overrides,
// with the overrides already applied. It is not a second document; it is the
// part of the one document a plan was made against, and it is what a plan
// stores beside itself.
type ClientProfile struct {
	Version int            `json:"version"`
	Client  ClientIdentity `json:"client"`
	// Engine is which engine of the document this is: native or mpv.
	Engine                 string                   `json:"engine"`
	EngineName             string                   `json:"engineName,omitempty"`
	EngineVersion          string                   `json:"engineVersion,omitempty"`
	Evidence               string                   `json:"evidence"`
	Display                ClientDisplay            `json:"display"`
	Video                  []ClientVideoCodec       `json:"video"`
	Audio                  []ClientAudioCodec       `json:"audio"`
	AudioPassthrough       []ClientAudioPassthrough `json:"audioPassthrough"`
	AudioOutput            ClientAudioOutput        `json:"audioOutput"`
	Transports             []ClientTransport        `json:"transports"`
	SubtitleRendering      []string                 `json:"subtitleRendering"`
	ToneMapsHDR            bool                     `json:"toneMapsHdr"`
	MaxBitrateBPS          int                      `json:"maxBitrateBps"`
	EmbeddedAudioSwitching bool                     `json:"embeddedAudioSwitching"`
	LiveChannels           bool                     `json:"liveChannels,omitempty"`
	Overrides              CapabilityOverrides      `json:"overrides"`
	AudioDecode            []ClientAudioDecode      `json:"audioDecode,omitempty"`

	// Revision is the document's revision.
	Revision string `json:"-"`
}

// ClientProfileVersion is the version a stored engine view carries.
const ClientProfileVersion = CapabilityDocumentVersion

var (
	ErrCapabilityDocument        = errors.New("The device capability document is not valid.")
	ErrCapabilityDocumentVersion = errors.New("This server does not understand that capability document version.")
	// ErrClientProfile is ErrCapabilityDocument under the name older callers use.
	ErrClientProfile = ErrCapabilityDocument
	profileToken     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.+-]{0,31}$`)
)

// CapabilityFieldError names the first field of a document that was refused.
type CapabilityFieldError struct{ Path string }

func (e *CapabilityFieldError) Error() string { return ErrCapabilityDocument.Error() }
func (e *CapabilityFieldError) Unwrap() error { return ErrCapabilityDocument }

func capabilityField(path string) error { return &CapabilityFieldError{Path: path} }

func profileText(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func profileTokens(values []string, max int) bool {
	if len(values) > max {
		return false
	}
	for _, v := range values {
		if !profileToken.MatchString(v) {
			return false
		}
	}
	return true
}

var dynamicRangeDomain = map[string]bool{"sdr": true, "hdr10": true, "hdr10plus": true, "hlg": true, "dolby_vision": true}

func profileRanges(values []string) bool {
	if len(values) > 8 {
		return false
	}
	for _, v := range values {
		if !dynamicRangeDomain[v] {
			return false
		}
	}
	return true
}

func evidenceValue(v string) bool {
	return v == EvidenceDeclared || v == EvidenceProbed || v == EvidenceMixed
}

// ParseCapabilityDocument validates one published document. It is strict about
// shape and bounds, and deliberately ignorant about content: an unknown codec
// name is kept (the planner simply never matches it), so a newer client is not
// refused by an older server for knowing about a codec the server has not met.
// Unknown fields are ignored for the same reason.
func ParseCapabilityDocument(raw []byte) (CapabilityDocument, error) {
	var d CapabilityDocument
	if len(raw) == 0 || len(raw) > MaxCapabilityDocumentBytes {
		return d, capabilityField("")
	}
	var head struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return d, capabilityField("")
	}
	if head.Version != CapabilityDocumentVersion {
		return d, ErrCapabilityDocumentVersion
	}
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&d) != nil {
		return CapabilityDocument{}, capabilityField("")
	}
	if err := d.Accept(); err != nil {
		return CapabilityDocument{}, err
	}
	return d, nil
}

// Accept validates and normalizes a decoded document in place and assigns its
// revision.
func (d *CapabilityDocument) Accept() error {
	if d.Version != CapabilityDocumentVersion {
		return ErrCapabilityDocumentVersion
	}
	d.normalize()
	if err := d.validate(); err != nil {
		return err
	}
	canonical, _ := json.Marshal(d)
	if len(canonical) > MaxCapabilityDocumentBytes {
		return capabilityField("")
	}
	sum := sha256.Sum256(canonical)
	d.Revision = hex.EncodeToString(sum[:16])
	return nil
}

// DecodeCaps are the document's audioDecode entries in the audio planner's terms.
func (d CapabilityDocument) DecodeCaps() []AudioDecodeCap {
	out := make([]AudioDecodeCap, 0, len(d.AudioDecode))
	for _, a := range d.AudioDecode {
		out = append(out, AudioDecodeCap{Codec: strings.ToLower(a.Codec), Containers: lowerAll(a.Containers), MaxSampleRate: a.MaxSampleRate, SampleRates: a.SampleRates, MaxChannels: a.MaxChannels, MaxBitDepth: a.MaxBitDepth, Via: a.Via})
	}
	return out
}

// DecodeCaps is the same list read from one engine view.
func (p ClientProfile) DecodeCaps() []AudioDecodeCap {
	return CapabilityDocument{AudioDecode: p.AudioDecode}.DecodeCaps()
}

func (d *CapabilityDocument) validate() error {
	c := d.Client
	if !profileToken.MatchString(c.Family) {
		return capabilityField("client.family")
	}
	if c.Form != "" && !profileToken.MatchString(c.Form) {
		return capabilityField("client.form")
	}
	for _, v := range []string{c.Platform, c.PlatformVersion, c.OS, c.OSVersion, c.Model, c.App, c.AppVersion} {
		if !profileText(v, 64) {
			return capabilityField("client")
		}
	}
	switch d.EngineSetting {
	case EngineSettingAutomatic, EngineSettingNative, EngineSettingMPV:
	default:
		return capabilityField("engineSetting")
	}
	display := d.Display
	if display.Width < 0 || display.Width > 32768 || display.Height < 0 || display.Height > 32768 || display.MaxFrameRate < 0 || display.MaxFrameRate > 1000 || !profileRanges(display.DynamicRanges) {
		return capabilityField("display")
	}
	o := d.AudioOutput
	if (o.Route != "" && !profileToken.MatchString(o.Route)) || o.MaxChannels < 0 || o.MaxChannels > 64 {
		return capabilityField("audioOutput")
	}
	if d.MaxBitrateBPS < 0 || d.MaxBitrateBPS > 2_000_000_000 {
		return capabilityField("maxBitrateBps")
	}
	if len(d.AudioDecode) > 32 {
		return capabilityField("audioDecode")
	}
	for _, a := range d.AudioDecode {
		if !profileToken.MatchString(a.Codec) || len(a.Containers) == 0 || !profileTokens(a.Containers, 16) || len(a.SampleRates) > 32 || a.MaxSampleRate < 0 || a.MaxSampleRate > 1<<22 || a.MaxChannels < 0 || a.MaxChannels > 64 || a.MaxBitDepth < 0 || a.MaxBitDepth > 64 || !profileText(a.Via, 32) {
			return capabilityField("audioDecode")
		}
		for _, r := range a.SampleRates {
			if r <= 0 || r > 1<<22 {
				return capabilityField("audioDecode")
			}
		}
	}
	if d.Engines.Native == nil {
		return capabilityField("engines.native")
	}
	if err := d.Engines.Native.validate("engines.native"); err != nil {
		return err
	}
	// "builtin" is the server's own baseline; a device never publishes it.
	if d.Engines.Native.Evidence == EvidenceBuiltin {
		return capabilityField("engines.native.evidence")
	}
	if d.Engines.MPV != nil {
		if d.Engines.MPV.Evidence == EvidenceBuiltin {
			return capabilityField("engines.mpv.evidence")
		}
		return d.Engines.MPV.validate("engines.mpv")
	}
	return nil
}

func (e *EngineCapabilities) validate(path string) error {
	if !evidenceValue(e.Evidence) && e.Evidence != EvidenceBuiltin {
		return capabilityField(path + ".evidence")
	}
	if !profileText(e.Name, 64) || !profileText(e.Version, 64) {
		return capabilityField(path + ".name")
	}
	if len(e.Video) > 32 {
		return capabilityField(path + ".video")
	}
	if len(e.Audio) > 48 {
		return capabilityField(path + ".audio")
	}
	if len(e.AudioPassthrough) > 24 {
		return capabilityField(path + ".audioPassthrough")
	}
	if len(e.Transports) > 16 {
		return capabilityField(path + ".transports")
	}
	for _, v := range e.Video {
		if !profileToken.MatchString(v.Codec) || !profileTokens(v.Profiles, 32) || v.MaxLevel < 0 || v.MaxLevel > 1000 || len(v.BitDepths) > 8 || v.MaxWidth < 0 || v.MaxWidth > 32768 || v.MaxHeight < 0 || v.MaxHeight > 32768 || v.MaxFrameRate < 0 || v.MaxFrameRate > 1000 || v.MaxBitrateBPS < 0 || v.MaxBitrateBPS > 2_000_000_000 || !profileRanges(v.DynamicRanges) || len(v.DolbyVisionProfiles) > 16 || !profileTokens(v.CodecTags, 8) || (v.Evidence != "" && !evidenceValue(v.Evidence)) {
			return capabilityField(path + ".video")
		}
		for _, depth := range v.BitDepths {
			if depth < 1 || depth > 16 {
				return capabilityField(path + ".video")
			}
		}
		if !validVideoLimits(v.Limits) {
			return capabilityField(path + ".video.limits")
		}
		if !validChroma(v.ChromaSubsampling) || !validFrameRateModes(v.FrameRateModes) {
			return capabilityField(path + ".video")
		}
		seenCompatibility := map[int]bool{}
		for _, compatibility := range v.DolbyVisionProfile8Compatibility {
			if compatibility != 1 && compatibility != 2 && compatibility != 4 || seenCompatibility[compatibility] {
				return capabilityField(path + ".video.dolbyVisionProfile8Compatibility")
			}
			seenCompatibility[compatibility] = true
		}
		for _, dv := range v.DolbyVisionProfiles {
			if dv < 1 || dv > 31 {
				return capabilityField(path + ".video")
			}
		}
	}
	for _, a := range e.Audio {
		if !validAudioVariants(a.Variants) || !profileToken.MatchString(a.Codec) || a.MaxChannels < 0 || a.MaxChannels > 64 || a.MaxSampleRate < 0 || a.MaxSampleRate > 1<<22 || a.MaxBitDepth < 0 || a.MaxBitDepth > 64 || !profileTokens(a.ObjectAudio, 4) || (a.Evidence != "" && !evidenceValue(a.Evidence)) {
			return capabilityField(path + ".audio")
		}
		if len(a.SampleRates) > 32 {
			return capabilityField(path + ".audio.sampleRates")
		}
		seen := map[int]bool{}
		for _, rate := range a.SampleRates {
			if rate <= 0 || rate > 1<<22 || a.MaxSampleRate > 0 && rate > a.MaxSampleRate || seen[rate] {
				return capabilityField(path + ".audio.sampleRates")
			}
			seen[rate] = true
		}
	}
	for _, a := range e.AudioPassthrough {
		if !validAudioVariants(a.Variants) || !profileToken.MatchString(a.Codec) || a.MaxChannels < 0 || a.MaxChannels > 64 || !profileTokens(a.ObjectAudio, 4) || (a.Evidence != "" && !evidenceValue(a.Evidence)) {
			return capabilityField(path + ".audioPassthrough")
		}
	}
	for _, t := range e.Transports {
		if t.Transport != TransportDirect && t.Transport != TransportHLSTS && t.Transport != TransportHLSFMP4 {
			return capabilityField(path + ".transports")
		}
		if !profileTokens(t.Containers, 32) || !profileTokens(t.Video, 32) || !profileTokens(t.Audio, 48) {
			return capabilityField(path + ".transports")
		}
	}
	if len(e.SubtitleRendering) > 8 {
		return capabilityField(path + ".subtitleRendering")
	}
	for _, kind := range e.SubtitleRendering {
		if !subtitleRenderingDomain[kind] {
			return capabilityField(path + ".subtitleRendering")
		}
	}
	return nil
}

func lowerAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", "_"))
	}
	return out
}

// deviceVideoName and deviceAudioName fold the spellings a device may use for a
// codec into the one name the scanner records for a file, so a declaration can
// never fail to match a source over a synonym.
func deviceVideoName(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	switch codec {
	case "h265", "hvc1", "hev1":
		return "hevc"
	case "avc", "avc1":
		return "h264"
	case "mpeg2", "mpeg1video", "mpeg1":
		return "mpeg2video"
	case "mpeg4part2", "xvid", "divx":
		return "mpeg4"
	}
	return videoFamily(codec)
}

func deviceAudioName(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	switch codec {
	case "ec3", "ec-3", "e-ac-3", "eac-3":
		return "eac3"
	case "ac-3":
		return "ac3"
	case "mlp":
		return "truehd"
	}
	for _, dts := range []string{"dts-hd", "dtshd", "dts_hd"} {
		if codec == dts || strings.HasPrefix(codec, dts+" ") || strings.HasPrefix(codec, dts+"_") {
			// The scanner records every DTS stream as "dts" and keeps the
			// variant (core, HRA, MA, X, Express) beside it. A device that
			// declares its DTS family must match that codec, or the
			// declaration is dead and its DTS audio is converted for no reason.
			return "dts"
		}
	}
	return audioFamily(codec)
}

func normalizeNames(values []string, fold func(string) string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		v = fold(v)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// normalize makes equal documents byte-equal so the revision is stable: nil
// slices become empty, names fold to one spelling, lists are sorted.
func (d *CapabilityDocument) normalize() {
	if d.EngineSetting == "" {
		d.EngineSetting = EngineSettingAutomatic
	}
	d.Client.Family = strings.ToLower(strings.TrimSpace(d.Client.Family))
	d.Client.Form = strings.ToLower(strings.TrimSpace(d.Client.Form))
	d.AudioOutput.Route = strings.ToLower(strings.TrimSpace(d.AudioOutput.Route))
	if d.Display.DynamicRanges == nil {
		d.Display.DynamicRanges = []string{}
	}
	sort.Strings(d.Display.DynamicRanges)
	for i := range d.AudioDecode {
		d.AudioDecode[i].Codec = strings.ToLower(d.AudioDecode[i].Codec)
		d.AudioDecode[i].Containers = lowerAll(d.AudioDecode[i].Containers)
	}
	if d.Engines.Native != nil {
		d.Engines.Native.normalize()
	}
	if d.Engines.MPV != nil {
		d.Engines.MPV.normalize()
	}
}

func (e *EngineCapabilities) normalize() {
	fill := func(v *[]string) {
		if *v == nil {
			*v = []string{}
		}
		sort.Strings(*v)
	}
	if e.Video == nil {
		e.Video = []ClientVideoCodec{}
	}
	if e.Audio == nil {
		e.Audio = []ClientAudioCodec{}
	}
	if e.AudioPassthrough == nil {
		e.AudioPassthrough = []ClientAudioPassthrough{}
	}
	if e.Transports == nil {
		e.Transports = []ClientTransport{}
	}
	for i := range e.Video {
		v := &e.Video[i]
		v.Codec = deviceVideoName(v.Codec)
		v.Profiles = lowerAll(v.Profiles)
		v.CodecTags = lowerAll(v.CodecTags)
		fill(&v.Profiles)
		fill(&v.DynamicRanges)
		if len(v.CodecTags) == 0 {
			v.CodecTags = nil
		} else {
			sort.Strings(v.CodecTags)
		}
		if v.BitDepths == nil {
			v.BitDepths = []int{}
		}
		if v.DolbyVisionProfiles == nil {
			v.DolbyVisionProfiles = []int{}
		}
		sort.Ints(v.BitDepths)
		sort.Ints(v.DolbyVisionProfiles)
		sort.Ints(v.DolbyVisionProfile8Compatibility)
		v.Limits = normalizeVideoLimits(v.Limits)
	}
	for i := range e.Audio {
		e.Audio[i].Codec = deviceAudioName(e.Audio[i].Codec)
		fill(&e.Audio[i].ObjectAudio)
	}
	for i := range e.AudioPassthrough {
		e.AudioPassthrough[i].Codec = deviceAudioName(e.AudioPassthrough[i].Codec)
		fill(&e.AudioPassthrough[i].ObjectAudio)
	}
	for i := range e.Transports {
		t := &e.Transports[i]
		t.Containers = lowerAll(t.Containers)
		fill(&t.Containers)
		t.Video = normalizeNames(t.Video, deviceVideoName)
		t.Audio = normalizeNames(t.Audio, deviceAudioName)
	}
	e.SubtitleRendering = normalizeNames(e.SubtitleRendering, func(s string) string { return strings.ToLower(strings.TrimSpace(s)) })
}

// engine returns the named engine's tables, or nil.
func (d CapabilityDocument) engine(name string) *EngineCapabilities {
	if name == EngineMPV {
		return d.Engines.MPV
	}
	return d.Engines.Native
}

// HasEngine reports whether the document describes the named engine.
func (d CapabilityDocument) HasEngine(name string) bool { return d.engine(name) != nil }

// Profile is one engine of the document as planning reads it, with the viewer's
// overrides applied. An engine the document does not describe reads as native.
func (d CapabilityDocument) Profile(name string) ClientProfile {
	e := d.engine(name)
	if e == nil {
		name, e = EngineNative, d.Engines.Native
	}
	if e == nil {
		return BaselineClientProfile()
	}
	p := ClientProfile{
		Version: CapabilityDocumentVersion, Client: d.Client, Engine: name, EngineName: e.Name, EngineVersion: e.Version, Evidence: e.Evidence,
		Display: d.Display, AudioOutput: d.AudioOutput, MaxBitrateBPS: d.MaxBitrateBPS, Overrides: d.Overrides, AudioDecode: d.AudioDecode,
		Video: append([]ClientVideoCodec{}, e.Video...), Audio: append([]ClientAudioCodec{}, e.Audio...), AudioPassthrough: append([]ClientAudioPassthrough{}, e.AudioPassthrough...),
		Transports: e.Transports, SubtitleRendering: e.SubtitleRendering, ToneMapsHDR: e.ToneMapsHDR, EmbeddedAudioSwitching: e.EmbeddedAudioSwitching,
		LiveChannels: e.LiveChannels || name == EngineNative, Revision: d.Revision,
	}
	p.applyOverrides()
	return p
}

// applyOverrides narrows the engine to what the viewer asked it be planned as.
func (p *ClientProfile) applyOverrides() {
	if p.Overrides.DisableHDR {
		p.ToneMapsHDR = false
		p.Display.DynamicRanges = []string{"sdr"}
		for i := range p.Video {
			p.Video[i].DynamicRanges = []string{"sdr"}
			p.Video[i].DolbyVisionProfiles = []int{}
		}
	}
	if p.Overrides.DisablePassthrough || p.Overrides.ForceStereo {
		p.AudioPassthrough = []ClientAudioPassthrough{}
	}
	if p.Overrides.ForceStereo {
		p.AudioOutput.MaxChannels = 2
		p.AudioOutput.Spatial = false
		for i := range p.Audio {
			if p.Audio[i].MaxChannels == 0 || p.Audio[i].MaxChannels > 2 {
				p.Audio[i].MaxChannels = 2
			}
			p.Audio[i].ObjectAudio = []string{}
		}
	}
}

// RendersSubtitle reports whether the engine draws a subtitle kind itself.
func (p ClientProfile) RendersSubtitle(kind string) bool {
	return kind == "" || containsString(p.SubtitleRendering, kind)
}

// presents reports whether a dynamic range the engine decodes can be shown:
// the display shows it, the display did not say, or the engine tone-maps.
func (p ClientProfile) presents(dynamicRange string) bool {
	// HDR10+ is HDR10 with dynamic metadata: a display that declares the
	// extension shows the base picture too (VIDEO §3 VS-2a).
	hdr10Plus := dynamicRange == "hdr10" && containsString(p.Display.DynamicRanges, "hdr10plus")
	return dynamicRange == "" || dynamicRange == "sdr" || p.ToneMapsHDR || len(p.Display.DynamicRanges) == 0 || containsString(p.Display.DynamicRanges, dynamicRange) || hdr10Plus
}

// ParseClientProfile reads back an engine view a plan or a channel session
// stored. A view that no longer parses is an error; callers plan against the
// baseline then.
func ParseClientProfile(raw []byte) (ClientProfile, error) {
	var p ClientProfile
	if len(raw) == 0 || len(raw) > MaxCapabilityDocumentBytes || json.Unmarshal(raw, &p) != nil {
		return ClientProfile{}, ErrCapabilityDocument
	}
	if p.Version != CapabilityDocumentVersion {
		return ClientProfile{}, ErrCapabilityDocumentVersion
	}
	engine := EngineCapabilities{Name: p.EngineName, Version: p.EngineVersion, Evidence: p.Evidence, Video: p.Video, Audio: p.Audio, AudioPassthrough: p.AudioPassthrough, Transports: p.Transports, SubtitleRendering: p.SubtitleRendering}
	engine.normalize()
	if err := engine.validate("engine"); err != nil {
		return ClientProfile{}, err
	}
	p.Video, p.Audio, p.AudioPassthrough, p.Transports, p.SubtitleRendering = engine.Video, engine.Audio, engine.AudioPassthrough, engine.Transports, engine.SubtitleRendering
	if p.Engine != EngineMPV {
		p.Engine = EngineNative
	}
	return p, nil
}

// BaselineCapabilityDocument is what a device that has published nothing is
// planned against: MP4 with H.264 and AAC or MP3 plays as it is, MPEG-TS HLS
// carries H.264 with stereo AAC, text subtitles are drawn by the app, and
// nothing else is assumed.
func BaselineCapabilityDocument() CapabilityDocument {
	d := CapabilityDocument{
		Version:       CapabilityDocumentVersion,
		Client:        ClientIdentity{Family: "unknown"},
		EngineSetting: EngineSettingAutomatic,
		Display:       ClientDisplay{DynamicRanges: []string{"sdr"}},
		AudioOutput:   ClientAudioOutput{MaxChannels: 2},
		Engines: CapabilityEngines{Native: &EngineCapabilities{
			Evidence: EvidenceBuiltin,
			Video:    []ClientVideoCodec{{Codec: "h264", MaxFrameRate: 60, BitDepths: []int{8}, DynamicRanges: []string{"sdr"}}},
			Audio:    []ClientAudioCodec{{Codec: "aac", MaxChannels: 2}, {Codec: "mp3", MaxChannels: 2}},
			Transports: []ClientTransport{
				{Transport: TransportDirect, Containers: []string{"m4v", "mov", "mp4"}, Video: []string{"h264"}, Audio: []string{"aac", "mp3"}},
				{Transport: TransportDirect, Containers: []string{"aac", "m4a", "m4b", "mp4"}, Video: []string{}, Audio: []string{"aac"}},
				{Transport: TransportDirect, Containers: []string{"mp3"}, Video: []string{}, Audio: []string{"mp3"}},
				{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac", "mp3"}},
			},
			SubtitleRendering: []string{SubtitleText},
		}},
	}
	d.normalize()
	d.Revision = "baseline"
	return d
}

// BaselineClientProfile is the baseline document's native engine.
func BaselineClientProfile() ClientProfile {
	return BaselineCapabilityDocument().Profile(EngineNative)
}

func (p ClientProfile) videoCodec(name string) *ClientVideoCodec {
	for i := range p.Video {
		if p.Video[i].Codec == name {
			return &p.Video[i]
		}
	}
	return nil
}

func (p ClientProfile) audioCodec(name string) *ClientAudioCodec {
	for i := range p.Audio {
		if p.Audio[i].Codec == name {
			return &p.Audio[i]
		}
	}
	return nil
}

// passthroughCodec is the passthrough entry for a codec while the output route
// carries bitstreams; nil otherwise.
func (p ClientProfile) passthroughCodec(name string) *ClientAudioPassthrough {
	if !passthroughRoute(p.AudioOutput.Route) {
		return nil
	}
	for i := range p.AudioPassthrough {
		if p.AudioPassthrough[i].Codec == name {
			return &p.AudioPassthrough[i]
		}
	}
	return nil
}

// ClientSummary is the part of a profile kept with a decision: enough to say
// which device, which engine and which document the plan was made for, without
// the tables.
type ClientSummary struct {
	Family   string `json:"family"`
	Platform string `json:"platform,omitempty"`
	Model    string `json:"model,omitempty"`
	// Engine is native or mpv; EngineName is the engine's own name.
	Engine     string `json:"engine,omitempty"`
	EngineName string `json:"engineName,omitempty"`
	Evidence   string `json:"evidence"`
	Revision   string `json:"revision"`
	Published  bool   `json:"published"`
}

func (p ClientProfile) Summary() ClientSummary {
	return ClientSummary{Family: p.Client.Family, Platform: p.Client.Platform, Model: p.Client.Model, Engine: p.Engine, EngineName: p.EngineName, Evidence: p.Evidence, Revision: p.Revision, Published: p.Evidence != EvidenceBuiltin}
}

// StoredCapabilities is a device's stored document and its revision counter.
type StoredCapabilities struct {
	Revision int64              `json:"revision"`
	Document CapabilityDocument `json:"capabilities"`
}

// ErrCapabilityRevision is a stale If-Match on a capability write. Current is
// what is stored now.
type ErrCapabilityRevision struct{ Current StoredCapabilities }

func (e *ErrCapabilityRevision) Error() string { return "capability revision mismatch" }

// deviceOf is the identity device a sign-in belongs to ("" when it has none).
func deviceOf(ctx context.Context, q profileQuery, hash string) string {
	var device string
	if hash == "" || q.QueryRowContext(ctx, `SELECT f.device_id FROM authorization_family_tokens t JOIN identity_device_families f ON f.family_id=t.family_id WHERE t.token_hash=?`, hash).Scan(&device) != nil {
		return ""
	}
	return device
}

// PutCapabilities replaces a device's whole document. want is the revision the
// caller read (negative: unconditional). The write is skipped when the stored
// document is already equal, so a client that republishes on every launch
// costs one indexed read. The document belongs to the device: it survives
// token rotation, and every sign-in of that device plans from it.
func (s *Service) PutCapabilities(ctx context.Context, p identity.Principal, device string, want int64, d CapabilityDocument) (int64, error) {
	if err := d.Accept(); err != nil {
		return 0, err
	}
	if device == "" {
		return 0, identity.ErrUnauthorized
	}
	canonical, err := json.Marshal(d)
	if err != nil {
		return 0, err
	}
	var revision int64
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		var current int64
		var stored string
		err := tx.QueryRowContext(ctx, `SELECT revision,document FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&current, &stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if want >= 0 && want != current {
			held := StoredCapabilities{Revision: current}
			if doc, parsed := ParseCapabilityDocument([]byte(stored)); parsed == nil {
				held.Document = doc
			}
			return &ErrCapabilityRevision{Current: held}
		}
		if err == nil && stored == string(canonical) {
			revision = current
			return nil
		}
		revision = current + 1
		_, err = tx.ExecContext(ctx, `INSERT INTO playback_device_capabilities(device_id,account_id,profile_id,revision,document,updated_at_ms) VALUES(?,?,?,?,?,?) ON CONFLICT(device_id) DO UPDATE SET account_id=excluded.account_id,profile_id=excluded.profile_id,revision=excluded.revision,document=excluded.document,updated_at_ms=excluded.updated_at_ms`, device, p.AccountID, p.ProfileID, revision, string(canonical), time.Now().UnixMilli())
		return err
	})
	return revision, err
}

// PublishCapabilities stores a raw document for the device the sign-in belongs
// to. It is PutCapabilities for a caller that holds bytes and a principal.
func (s *Service) PublishCapabilities(ctx context.Context, p identity.Principal, raw []byte) (CapabilityDocument, error) {
	d, err := ParseCapabilityDocument(raw)
	if err != nil {
		return CapabilityDocument{}, err
	}
	device := deviceOf(ctx, s.db, p.Hash)
	if device == "" {
		return CapabilityDocument{}, identity.ErrUnauthorized
	}
	_, err = s.PutCapabilities(ctx, p, device, -1, d)
	return d, err
}

// Capabilities reads a device's stored document; sql.ErrNoRows when it has
// published none.
func (s *Service) Capabilities(ctx context.Context, device string) (StoredCapabilities, error) {
	var out StoredCapabilities
	var raw string
	if err := dbwork.QueryRow(ctx, s.db, `SELECT revision,document FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&out.Revision, &raw); err != nil {
		return out, err
	}
	doc, err := ParseCapabilityDocument([]byte(raw))
	if err != nil {
		// A stored document this build no longer reads is treated as absent.
		return StoredCapabilities{}, sql.ErrNoRows
	}
	out.Document = doc
	return out, nil
}

// CapabilitiesRevision reads only the revision of the device's stored
// document, so a read the client already holds is answered without parsing
// or encoding it.
func (s *Service) CapabilitiesRevision(ctx context.Context, device string) (int64, error) {
	var revision int64
	err := dbwork.QueryRow(ctx, s.db, `SELECT revision FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&revision)
	return revision, err
}

type profileQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// capabilityDocumentFor reads the stored document of the device this sign-in
// belongs to, or the baseline. A stored document that no longer parses (a
// server downgrade, a hand edit) is treated as absent: planning must never
// fail because of a capability document.
func capabilityDocumentFor(ctx context.Context, q profileQuery, p identity.Principal) CapabilityDocument {
	if p.Hash == "" {
		return BaselineCapabilityDocument()
	}
	var document string
	err := q.QueryRowContext(ctx, `SELECT c.document FROM authorization_family_tokens t JOIN identity_device_families f ON f.family_id=t.family_id JOIN playback_device_capabilities c ON c.device_id=f.device_id WHERE t.token_hash=?`, p.Hash).Scan(&document)
	if err != nil {
		return BaselineCapabilityDocument()
	}
	d, err := ParseCapabilityDocument([]byte(document))
	if err != nil {
		return BaselineCapabilityDocument()
	}
	return d
}

// CapabilityDocumentFor is the document planning uses for this sign-in.
func (s *Service) CapabilityDocumentFor(ctx context.Context, p identity.Principal) CapabilityDocument {
	return capabilityDocumentFor(ctx, s.db, p)
}

// DeviceCapabilityDocument is the stored document of a device, or the baseline.
func (s *Service) DeviceCapabilityDocument(ctx context.Context, device string) CapabilityDocument {
	if stored, err := s.Capabilities(ctx, device); err == nil {
		return stored.Document
	}
	return BaselineCapabilityDocument()
}

// WithVideoBitrateCeiling returns p with every video codec's MaxBitrateBPS
// narrowed to ceiling (0 = no ceiling; a codec's 0 means unlimited, so it
// becomes ceiling). p's slices are copied; p itself is never modified.
func WithVideoBitrateCeiling(p ClientProfile, ceiling int) ClientProfile {
	if ceiling <= 0 {
		return p
	}
	narrowed := make([]ClientVideoCodec, len(p.Video))
	for i, v := range p.Video {
		v.MaxBitrateBPS = narrowCeiling(v.MaxBitrateBPS, ceiling)
		narrowed[i] = v
	}
	p.Video = narrowed
	return p
}

// CapabilityDocumentTx reads the same device document inside an existing read
// snapshot, so download menus and playback make their choices from one source.
func CapabilityDocumentTx(ctx context.Context, tx *sql.Tx, p identity.Principal) CapabilityDocument {
	return capabilityDocumentFor(ctx, tx, p)
}
