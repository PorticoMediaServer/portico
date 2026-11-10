package streaming

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
)

// AudioDecodeCap is one entry of a device's audioDecode (spec §3): what the
// client's own audio engine decodes.
type AudioDecodeCap struct {
	Codec         string
	Via           string
	Containers    []string
	MaxSampleRate int
	SampleRates   []int
	MaxChannels   int
	MaxBitDepth   int
}

// AudioPlan is the version 2 audio render plan (spec §18.1): the client decodes
// the original file (direct) or a FLAC/Opus/AAC conversion (converted) with the
// server's exact trim and gains. Pinned to one presentation.
type AudioPlan struct {
	// comparison is private persisted evidence for this exact audio generation.
	// It is not an extra field on the audio-render wire plan.
	comparison        *PlaybackInfo
	SourceBitrateBPS  int64                  `json:"sourceBitrateBps,omitempty"`
	ConversionGainDB  float64                `json:"conversionGainDb,omitempty"`
	LoudnessAppliedDB *float64               `json:"loudnessAppliedDb,omitempty"`
	LoudnessEnvelope  *AudioLoudnessEnvelope `json:"loudnessEnvelope,omitempty"`
	Version           int                    `json:"version"`
	Mode              string                 `json:"mode"`
	ID                string                 `json:"id"`
	Reason            string                 `json:"reason,omitempty"`
	URL               string                 `json:"url,omitempty"`
	Container         string                 `json:"container,omitempty"`
	Codec             string                 `json:"codec,omitempty"`
	DecoderConfig     string                 `json:"decoderConfig,omitempty"`
	SampleRate        int                    `json:"sampleRate,omitempty"`
	Channels          int                    `json:"channels,omitempty"`
	BitDepth          int                    `json:"bitDepth,omitempty"`
	Bytes             int64                  `json:"bytes,omitempty"`
	PrefetchBytes     int64                  `json:"prefetchBytes,omitempty"`
	DurationFrames    int64                  `json:"durationFrames,omitempty"`
	Downmixed         bool                   `json:"downmixed,omitempty"`
	Trim              *AudioTrim             `json:"trim,omitempty"`
	Gain              *AudioGain             `json:"gain,omitempty"`
	Generation        int                    `json:"generation"`
	Convert           *AudioConversion       `json:"convert,omitempty"`
	// SeekIndex is where a direct plan whose container carries no index a
	// client can seek by (MP3, ADTS, Ogg) finds the server's: byte offsets
	// with the raw frame each starts at (audiofacts.SeekIndex). The client
	// reads the file from the nearest entry with a Range request and decodes
	// to the exact frame; nothing is converted.
	SeekIndex *AudioSeekIndex `json:"seekIndex,omitempty"`
}

// AudioSeekIndex names the route serving a direct plan's seek index.
type AudioSeekIndex struct {
	URL string `json:"url"`
}

type AudioTrim struct {
	StartFrames int64  `json:"startFrames"`
	EndFrames   int64  `json:"endFrames"`
	Source      string `json:"source"`
}

// AudioGain is in dB against the ReplayGain 2.0 reference (−18 LUFS); nil is unknown.
type AudioLoudnessEnvelope struct {
	IntervalMs int       `json:"intervalMs"`
	StartLUFS  []float64 `json:"startLufs"`
	EndLUFS    []float64 `json:"endLufs"`
}
type AudioGain struct {
	TrackDB   *float64 `json:"trackDb,omitempty"`
	AlbumDB   *float64 `json:"albumDb,omitempty"`
	TrackPeak *float64 `json:"trackPeak,omitempty"`
	AlbumPeak *float64 `json:"albumPeak,omitempty"`
	Source    string   `json:"source"`
}

// AudioConversion is how the converted route produces the plan's stream (never
// on the wire): the source's trimmed frames, resampled and encoded exactly.
type AudioConversion struct {
	TruePeakLimit  bool    `json:"truePeakLimit"`
	SourceBitDepth int     `json:"sourceBitDepth"`
	GainDB         float64 `json:"gainDb"`
	Codec          string  `json:"codec"`
	SourceRate     int     `json:"sourceRate"`
	SourceStart    int64   `json:"sourceStart"`
	SourceFrames   int64   `json:"sourceFrames"`
	Rate           int     `json:"rate"`
	Channels       int     `json:"channels"`
	BitDepth       int     `json:"bitDepth"`
	BitrateBPS     int     `json:"bitrateBps"`
}

// OpusPreSkip is libopus's pre-skip at 48 kHz; Opus frames are 20 ms (960).
const OpusPreSkip, opusFrame = 312, 960

// AACPriming is FFmpeg's AAC encoder delay (its initial padding): the first
// 1024 decoded frames of a converted AAC stream are not the track. AAC-LC
// frames are 1024 samples. A converted AAC stream is ADTS, which carries no
// priming of its own, so the plan's trim is the only statement of it.
const AACPriming, aacFrame = 1024, 1024

// minimumAACBitrate is the lowest rate Portico encodes AAC at; a viewer's limit
// below it is respected by not converting, never by quietly exceeding it.
const minimumAACBitrate = 16000

// aacRate is the AAC target's rate: the source's when it is a usual AAC rate
// the device accepts, else 48 kHz, else 44.1 kHz; 0 when neither is accepted.
func aacRate(c AudioDecodeCap, source int) int {
	// A supported standard source needs no rate conversion merely to encode AAC.
	switch source {
	case 96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000:
		if capAllowsRate(c, source) {
			return source
		}
	}
	rates := []int{48000, 32000, 24000, 16000, 12000, 8000}
	if source%11025 == 0 {
		rates = []int{44100, 22050, 11025}
	}
	for _, rate := range rates {
		if rate <= source && capAllowsRate(c, rate) {
			return rate
		}
	}
	return 0
}

var errAudioPlan = errors.New("audio plan unavailable")

func capAllowsRate(c AudioDecodeCap, rate int) bool {
	// An enumerated rate must still fit the device's declared ceiling.
	if rate <= 0 || c.MaxSampleRate > 0 && rate > c.MaxSampleRate {
		return false
	}
	if len(c.SampleRates) > 0 {
		return slices.Contains(c.SampleRates, rate)
	}
	return true
}

// codecMatches maps FFmpeg's codec name to the capability vocabulary.
func codecMatches(declared, codec string) bool {
	declared = strings.ToLower(declared)
	return declared == codec || declared == "pcm" && strings.HasPrefix(codec, "pcm_")
}

// containerFamily compares declared container names with the measured demuxer
// identity. Matroska never establishes WebM compatibility.
func containerFamily(name string) string {
	switch name = strings.ToLower(strings.TrimSpace(name)); name {
	case "mov", "mp4", "m4a", "m4b", "m4v":
		return "mp4"
	case "matroska", "mkv", "mka":
		return "matroska"
	case "mpegts", "ts", "m2ts", "mts":
		return "mpegts"
	case "aac", "adts":
		return "adts"
	}
	return name
}

func containersAllow(declared []string, measured string) bool {
	want := containerFamily(measured)
	if want == "" {
		return false
	}
	for _, name := range declared {
		if containerFamily(name) == want {
			return true
		}
	}
	return false
}

func (c AudioDecodeCap) decodes(f audiofacts.Facts) bool {
	// The web reader has only qualified AAC-LC. Native audio engines read
	// the profile themselves; complex AAC must use a conversion on the web.
	if f.Codec == "aac" && strings.EqualFold(c.Via, "webcodecs") && f.Profile != "" && !strings.EqualFold(f.Profile, "LC") {
		return false
	}
	return codecMatches(c.Codec, f.Codec) && containersAllow(c.Containers, f.Container) && capAllowsRate(c, f.SampleRate) &&
		(c.MaxChannels <= 0 || f.Channels <= c.MaxChannels) && (c.MaxBitDepth <= 0 || f.BitDepth <= c.MaxBitDepth)
}

func findCap(caps []AudioDecodeCap, codec, container string) (AudioDecodeCap, bool) {
	for _, c := range caps {
		if codecMatches(c.Codec, codec) && containersAllow(c.Containers, container) {
			return c, true
		}
	}
	return AudioDecodeCap{}, false
}

var lossless = map[string]bool{"flac": true, "alac": true, "wavpack": true, "ape": true, "tta": true, "mlp": true, "truehd": true}

func isLossless(codec string) bool { return lossless[codec] || strings.HasPrefix(codec, "pcm_") }

// convertedRate is the source rate when the FLAC capability allows it, else the
// highest same-family standard rate it allows at or under the source, else zero.
func convertedRate(c AudioDecodeCap, source int) int {
	if source <= 0 {
		return 0
	}
	if capAllowsRate(c, source) {
		return source
	}
	rates := []int{768000, 384000, 192000, 96000, 48000, 32000, 24000, 16000, 12000, 8000}
	if source%11025 == 0 {
		rates = []int{705600, 352800, 176400, 88200, 44100, 22050, 11025}
	}
	for _, rate := range rates {
		if rate <= source && capAllowsRate(c, rate) {
			return rate
		}
	}
	return 0
}
func cdRate(source int) int {
	if source%11025 == 0 {
		return min(source, 44100)
	}
	return min(source, 48000)
}
func isDSD(codec string) bool { return strings.HasPrefix(codec, "dsd_") }

func flacDepthAtMost(source, ceiling int) int {
	for _, depth := range []int{32, 24, 16} {
		if depth <= source && (ceiling <= 0 || depth <= ceiling) {
			return depth
		}
	}
	return 0
}

// replayGain reads ReplayGain, then R128 (Q7.8 dB against −23 LUFS, so +5 dB to
// the ReplayGain reference).
func replayGain(tags map[string]string, kind string) *float64 {
	if v := audioTagNumber(tags["replaygain_"+kind+"_gain"], -60, 30); v != nil {
		return v
	}
	if v := audioTagNumber(tags["r128_"+kind+"_gain"], -32768, 32767); v != nil && *v == math.Trunc(*v) {
		g := *v/256 + 5
		if g >= -60 && g <= 30 {
			return &g
		}
	}
	return nil
}

func audioTagNumber(raw string, min, max float64) *float64 {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), "db"))
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < min || v > max {
		return nil
	}
	return &v
}

func normalizationGain(g *AudioGain, mode string, neverBoost bool) float64 {
	if g == nil || mode == "off" || mode == "" {
		return 0
	}
	db, peak := g.TrackDB, g.TrackPeak
	if mode == "album" && g.AlbumDB != nil {
		db, peak = g.AlbumDB, g.AlbumPeak
	}
	if db == nil {
		return 0
	}
	v := *db
	if neverBoost || peak == nil {
		v = min(v, 0)
	}
	if peak != nil && *peak > 0 {
		v = min(v, -20*math.Log10(*peak))
	}
	return max(-60, min(30, v))
}
func itoa(n int) string { return strconv.Itoa(n) }
