package streaming

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

// AudioPlanRequest is everything one audio plan depends on. The caller reads
// each part from wherever it lives.
type AudioPlanRequest struct {
	// StreamID, Generation and Grant name the stream the plan is for; the plan's
	// addresses are built from Grant.
	StreamID   string
	Generation int
	Grant      string
	// Facts are the measured facts of the file's audio and Measured says they
	// exist. Size is the file's size in bytes.
	Facts    audiofacts.Facts
	Measured bool
	Size     int64
	// Source is the file's planning facts, used only to describe the source in
	// the comparison shown to the listener. It may be nil.
	Source *DeliverySource
	// SourceStart and SourceEnd bound a track that is part of a longer file (a
	// CUE sheet track), in seconds. SourceEnd is nil for "to the end".
	SourceStart float64
	SourceEnd   *float64
	// Gain is the gain from AudioGainFrom, and Envelope the measured loudness at
	// the track's edges, for crossfades. Either may be nil.
	Gain     *AudioGain
	Envelope *AudioLoudnessEnvelope
	// Choice is what the listener and device asked for. Settings are the owner's.
	Choice             *Choice
	Settings           DeliveryConfiguration
	Preferences        DeliveryPreferenceReader
	TranscodingEnabled bool
}

// AlbumTrackLoudness is one track of an album or book: its duration in seconds
// and the loudness summary the server measured for it, empty when it has none.
type AlbumTrackLoudness struct {
	Duration float64
	Summary  string
}

// PlanAudio builds the audio plan for one music or audiobook stream: played as
// it is when the device decodes it, otherwise converted to Opus, AAC or FLAC,
// with the trim and gain a gapless player needs. Without measured facts the plan
// is unavailable, never guessed. It narrows r.Choice to the owner's music cap,
// as the caller's later steps expect.
func PlanAudio(r AudioPlanRequest) (*AudioPlan, error) {
	v1, grant, ownerTranscoding := r.Choice, r.Grant, r.TranscodingEnabled
	if v1 == nil {
		v1 = &Choice{}
	}
	p := &AudioPlan{Version: 2, Mode: "unavailable", ID: r.StreamID + "." + itoa(r.Generation), Generation: r.Generation}
	f, ok, size := r.Facts, r.Measured, r.Size
	if !ok {
		p.Reason = "Portico hasn't measured this track yet, so it plays without gapless or crossfade."
		return p, nil
	}
	var comparisonSource *PlaybackSoundFacts
	if r.Source != nil {
		comparisonSource = audioComparisonSource(*r.Source, f)
	}
	measuredSourceFrames := f.DurationFrames
	virtual := false
	sourceStart, sourceEnd := r.SourceStart, r.SourceEnd
	if sourceStart > 0 || sourceEnd != nil {
		end := f.DurationFrames
		if sourceEnd != nil {
			end = min(end, int64(math.Round(*sourceEnd*float64(f.SampleRate))))
		}
		start := int64(math.Round(sourceStart * float64(f.SampleRate)))
		if end <= start {
			return nil, errAudioPlan
		}
		f.StartFrames += start
		f.DurationFrames = end - start
		f.EndFrames = f.RawFrames - f.StartFrames - f.DurationFrames
		virtual = true
	}
	gain := r.Gain
	// Music has its own owner cap. The generic owner bitrate ceiling has
	// already been narrowed into v1 by create; never widen it here.
	cfg := r.Settings
	if v1.Remote {
		if cap, ok := qualityladder.FindMusic(cfg.MaxMusicQuality); ok {
			if cap.ID == "cd-quality" {
				v1.AudioCDQuality = true
			}
			if cap.AudioKbps > 0 {
				v1.MaxAudioBitrateBPS = narrowCeiling(v1.MaxAudioBitrateBPS, cap.AudioKbps*1000)
				v1.Limit = true
			}
		}
	}
	seconds := float64(measuredSourceFrames) / float64(f.SampleRate)
	sourceBPS := 0
	if seconds > 0 {
		sourceBPS = int(float64(size) * 8 / seconds)
	}
	if f.BitRate > 0 {
		sourceBPS = int(f.BitRate)
	}
	// The file-average fallback above is conservative planning input (artwork
	// and tags also travel). A displayed media bitrate requires packet evidence.
	if f.BitRate > 0 && f.BitRate < 1<<40 {
		p.SourceBitrateBPS = f.BitRate
	}
	p.LoudnessEnvelope = r.Envelope
	bakedGain := 0.0
	if v1.AudioServerRender {
		if prefs := r.Preferences; prefs != nil {
			bakedGain = normalizationGain(gain, prefs.Text("music.audioNormalization"), prefs.Bool("music.neverBoost"))
		}
	}
	limited := v1.Limit && v1.MaxAudioBitrateBPS > 0 && sourceBPS > v1.MaxAudioBitrateBPS
	cdLimited := v1.AudioCDQuality && isLossless(f.Codec) && (f.BitDepth > 16 || f.SampleRate > cdRate(f.SampleRate))
	direct := false
	if !limited && !cdLimited && !isDSD(f.Codec) && bakedGain == 0 && !(virtual && v1.AudioServerRender) {
		for _, c := range v1.AudioDecode {
			if c.decodes(f) {
				direct = true
				break
			}
		}
	}
	if direct {
		p.Mode, p.URL = "direct", "/v1/media/"+grant+"/audio"
		p.Container, p.Codec, p.DecoderConfig = f.Container, f.Codec, f.DecoderConfig
		p.SampleRate, p.Channels, p.BitDepth = f.SampleRate, f.Channels, f.BitDepth
		p.Bytes, p.DurationFrames = size, f.DurationFrames
		p.Trim = &AudioTrim{StartFrames: f.StartFrames, EndFrames: f.EndFrames, Source: f.TrimSource}
		p.Gain = gain
		// The start plus a trailing index (an MP4 moov at the end): 30 s at the
		// file's own rate, never under 4 MiB, never over 16 MiB or the file.
		p.PrefetchBytes = min(size, max(4<<20, min(16<<20, int64(sourceBPS/8)*30+(1<<20))))
		if f.HasSeekIndex {
			p.SeekIndex = &AudioSeekIndex{URL: "/v1/media/" + grant + "/audio-index"}
		}
		p.comparison = audioPlanComparison(comparisonSource, p, v1.Remote, false)
		return p, nil
	}
	if !ownerTranscoding {
		p.Reason = "This device can't decode this track's format, and the server owner doesn't allow converting audio. It plays without gapless or crossfade."
		return p, nil
	}
	flacCap, canFLAC := findCap(v1.AudioDecode, "flac", "flac")
	opusCap, canOpus := findCap(v1.AudioDecode, "opus", "ogg")
	useOpus := canOpus && capAllowsRate(opusCap, 48000) && (limited || !canFLAC)
	// B-10.11: a device without Opus (Apple's own renderer) gets AAC-LC within
	// its bitrate limit instead of no plan at all.
	aacCap, canAAC := findCap(v1.AudioDecode, "aac", "adts")
	useAAC := !useOpus && canAAC && aacRate(aacCap, f.SampleRate) > 0 && (limited || !canFLAC)
	conv := &AudioConversion{SourceBitDepth: f.BitDepth, SourceRate: f.SampleRate, SourceStart: f.StartFrames, SourceFrames: f.DurationFrames}
	switch {
	case useOpus:
		conv.Codec, conv.Rate, conv.Channels = "opus", 48000, min(8, f.Channels)
		if opusCap.MaxChannels > 0 {
			conv.Channels = min(conv.Channels, opusCap.MaxChannels)
		}
		conv.BitrateBPS = 320000
		if v1.Limit && v1.MaxAudioBitrateBPS > 0 {
			conv.BitrateBPS = min(conv.BitrateBPS, v1.MaxAudioBitrateBPS)
			if conv.BitrateBPS < 6000 {
				conv = nil
				break
			}
		}
		p.Container, p.Codec = "ogg", "opus"
		p.Reason = "Portico converts this track to Opus for this device."
	case useAAC:
		conv.Codec, conv.Rate, conv.Channels = "aac", aacRate(aacCap, f.SampleRate), min(8, f.Channels)
		if aacCap.MaxChannels > 0 {
			conv.Channels = min(conv.Channels, aacCap.MaxChannels)
		}
		conv.BitrateBPS = 320000
		if v1.Limit && v1.MaxAudioBitrateBPS > 0 {
			conv.BitrateBPS = min(conv.BitrateBPS, v1.MaxAudioBitrateBPS)
		}
		if conv.BitrateBPS < minimumAACBitrate {
			conv = nil
		}
		p.Container, p.Codec = "adts", "aac"
		p.Reason = "Portico converts this track to AAC for this device."
	case canFLAC && !limited:
		conv.Codec, conv.Rate, conv.Channels = "flac", convertedRate(flacCap, f.SampleRate), min(8, f.Channels)
		if flacCap.MaxChannels > 0 {
			conv.Channels = min(conv.Channels, flacCap.MaxChannels)
		}
		conv.BitDepth = 16
		if (isLossless(f.Codec) && f.BitDepth > 16) || isDSD(f.Codec) {
			conv.BitDepth = min(32, max(24, f.BitDepth))
		}
		if isDSD(f.Codec) {
			conv.Rate = convertedRate(flacCap, min(f.SampleRate, 176400))
			conv.BitDepth = 24
		}
		if v1.AudioCDQuality {
			conv.Rate = convertedRate(flacCap, cdRate(f.SampleRate))
			conv.BitDepth = 16
		}
		conv.BitDepth = flacDepthAtMost(conv.BitDepth, flacCap.MaxBitDepth)
		if conv.Rate == 0 || conv.BitDepth == 0 {
			conv = nil
		}
		p.Container, p.Codec = "flac", "flac"
		p.Reason = "This device can't decode " + strings.ToUpper(f.Codec) + ", so Portico converts it to FLAC."
	default:
		conv = nil
	}
	if conv == nil {
		p.Container, p.Codec = "", ""
		p.Reason = "The available audio formats cannot meet this device's capabilities and bitrate choice. This track plays without gapless or crossfade."
		return p, nil
	}
	if (conv.Codec == "opus" || conv.Codec == "aac") && !isLossless(f.Codec) && !isDSD(f.Codec) && sourceBPS > 0 {
		conv.BitrateBPS = min(conv.BitrateBPS, sourceBPS)
	}
	if conv.Codec == "opus" {
		conv.BitrateBPS = min(conv.BitrateBPS, 256000*conv.Channels)
	}
	if isDSD(f.Codec) {
		conv.GainDB = -6
		if prefs := r.Preferences; prefs != nil {
			conv.GainDB = float64(prefs.Int("music.dsdGainDb"))
		}
	}
	if bakedGain != 0 {
		conv.GainDB += bakedGain
		conv.TruePeakLimit = true
		p.LoudnessAppliedDB = &bakedGain
		p.Gain = nil
	}
	p.ConversionGainDB = conv.GainDB - bakedGain
	frames := int64(math.Round(float64(f.DurationFrames) * float64(conv.Rate) / float64(f.SampleRate)))
	p.Mode, p.URL, p.Convert = "converted", "/v1/media/"+grant+"/audio-converted", conv
	p.SampleRate, p.Channels, p.BitDepth, p.DurationFrames = conv.Rate, conv.Channels, conv.BitDepth, frames
	p.Downmixed = conv.Channels < f.Channels
	p.Trim = &AudioTrim{Source: "flac"}
	if conv.Codec == "opus" {
		p.BitDepth = 0
		total := (OpusPreSkip + frames + opusFrame - 1) / opusFrame * opusFrame
		p.Trim = &AudioTrim{StartFrames: OpusPreSkip, EndFrames: total - OpusPreSkip - frames, Source: "opus-preskip"}
	}
	if conv.Codec == "aac" {
		p.BitDepth = 0
		total := (AACPriming + frames + aacFrame - 1) / aacFrame * aacFrame
		p.Trim = &AudioTrim{StartFrames: AACPriming, EndFrames: total - AACPriming - frames, Source: "aac-priming"}
	}
	if p.LoudnessAppliedDB == nil {
		p.Gain = gain
	}
	p.PrefetchBytes = 4 << 20
	if conv.Codec == "flac" {
		// About 30 s of FLAC at this rate, depth and channel count (~60% of PCM).
		p.PrefetchBytes = max(4<<20, int64(conv.Rate)*int64(conv.Channels)*int64(conv.BitDepth/8)*30*6/10)
	}
	p.comparison = audioPlanComparison(comparisonSource, p, v1.Remote, limited || cdLimited)
	return p, nil
}

// AudioGainFrom decides the gain a player applies: tags first (ReplayGain, or R128 converted to the −18 LUFS
// reference), then the server's loudness analysis; album values from tags, or
// from analysis once every track of the album (or book) is measured.
//
// track is the loudness summary the server measured for this track, empty when
// it has none; album lists every track of the album or book.
func AudioGainFrom(tags map[string]string, track string, album []AlbumTrackLoudness) *AudioGain {
	g := &AudioGain{Source: "tags"}
	g.TrackDB, g.AlbumDB = replayGain(tags, "track"), replayGain(tags, "album")
	g.TrackPeak = audioTagNumber(tags["replaygain_track_peak"], 0.000001, 64)
	g.AlbumPeak = audioTagNumber(tags["replaygain_album_peak"], 0.000001, 64)
	if g.TrackDB != nil {
		return g
	}
	lufs, peak, ok := parseLoudness(track)
	if !ok {
		if g.AlbumDB == nil {
			return nil
		}
		return g
	}
	v := -18 - lufs
	g.TrackDB, g.Source = &v, "analysis"
	if peak > 0 {
		g.TrackPeak = &peak
	}
	if g.AlbumDB == nil {
		if a, ap, ok := albumLoudness(album); ok {
			av := -18 - a
			g.AlbumDB = &av
			if ap > 0 {
				g.AlbumPeak = &ap
			}
		}
	}
	return g
}

// parseLoudness reads one stored loudness summary; an empty or implausible one
// is no measurement.
func parseLoudness(raw string) (lufs, peak float64, ok bool) {
	if raw == "" {
		return 0, 0, false
	}
	var s struct {
		LUFS *float64 `json:"integratedLUFS"`
		Peak *float64 `json:"truePeakLinear"`
	}
	if json.Unmarshal([]byte(raw), &s) != nil || s.LUFS == nil || math.IsNaN(*s.LUFS) || *s.LUFS < -70 || *s.LUFS > 10 {
		return 0, 0, false
	}
	if s.Peak != nil && *s.Peak > 0 && *s.Peak < 64 {
		peak = *s.Peak
	}
	return *s.LUFS, peak, true
}

// albumLoudness is the duration-weighted energy mean of the album's (or book's)
// tracks, once every one is measured; bounded to 2000 tracks.
func albumLoudness(tracks []AlbumTrackLoudness) (float64, float64, bool) {
	if len(tracks) == 0 || len(tracks) > 2000 {
		return 0, 0, false
	}
	var energy, total, peak float64
	for _, t := range tracks {
		l, p, ok := parseLoudness(t.Summary)
		if !ok || t.Duration <= 0 {
			return 0, 0, false
		}
		energy += t.Duration * math.Pow(10, l/10)
		total += t.Duration
		peak = max(peak, p)
	}
	return 10 * math.Log10(energy/total), peak, true
}

// audioComparisonSource describes the source for the comparison. The format is
// the audio renderer's own fresh measurement, enriched only by a current,
// unambiguous single stream of that same asset. Old metadata never supplies an
// alternate/default track, and a session read never joins today's catalogue.
func audioComparisonSource(source DeliverySource, measured audiofacts.Facts) *PlaybackSoundFacts {
	if !source.DetailKnown || len(source.Audio) != 1 || audioFamily(source.Audio[0].Codec) != audioFamily(measured.Codec) {
		return nil
	}
	track := source.Audio[0]
	out := &PlaybackSoundFacts{Codec: measured.Codec, Profile: publicAudioText(measured.Profile, 96), Variant: track.AudioVariant,
		ObjectAudio: track.ObjectAudio, Lossless: copiedBool(track.Lossless)}
	if out.Lossless == nil {
		out.Lossless = knownAudioEncodingLossless(measured.Codec, measured.Profile, track.AudioVariant)
	}
	// Keep the existing comparison wire bounds. Unsupported source rates stay
	// absent; a DSD clock/multiple is never derived from a decoder's sample rate.
	if measured.SampleRate > 0 && measured.SampleRate <= 1536000 {
		out.SampleRate = measured.SampleRate
	}
	if measured.Channels > 0 && measured.Channels <= 128 {
		out.Channels = measured.Channels
	}
	if measured.BitDepth > 0 && measured.BitDepth <= 64 {
		out.BitDepth = measured.BitDepth
	}
	if track.Channels == measured.Channels {
		out.Layout = publicAudioText(track.Layout, 64)
	}
	return out
}
