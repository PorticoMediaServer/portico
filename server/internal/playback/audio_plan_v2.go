package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
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

// pinAudioPlan builds and pins the version 2 plan for a v1 audio presentation.
// Facts were ensured before the transaction (audiofacts.Ensure); without them
// the plan is unavailable, never guessed.
func (s *Service) pinAudioPlan(ctx context.Context, tx *sql.Tx, principal identity.Principal, session Session, item, asset, grant string, v1 *V1Choice, ownerTranscoding bool) (*AudioPlan, error) {
	p := &AudioPlan{Version: 2, Mode: "unavailable", ID: session.ID + "." + itoa(session.Generation), Generation: session.Generation}
	f, ok, err := audiofacts.Read(ctx, tx, asset)
	if err != nil {
		return nil, err
	}
	var size int64
	if err = tx.QueryRowContext(ctx, `SELECT size FROM catalog_assets WHERE token=?`, asset).Scan(&size); err != nil {
		return nil, err
	}
	if !ok {
		p.Reason = "Portico hasn't measured this track yet, so it plays without gapless or crossfade."
		return p, s.storeAudioPlan(tx, session.ID, p)
	}
	comparisonSource, err := audioComparisonSource(ctx, tx, asset, f)
	if err != nil {
		return nil, err
	}
	measuredSourceFrames := f.DurationFrames
	virtual := false
	var sourceStart float64
	var sourceEnd sql.NullFloat64
	if err = tx.QueryRowContext(ctx, `SELECT l.start_seconds,l.end_seconds FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.token=?`, item, asset).Scan(&sourceStart, &sourceEnd); err != nil {
		return nil, err
	}
	if sourceStart > 0 || sourceEnd.Valid {
		end := f.DurationFrames
		if sourceEnd.Valid {
			end = min(end, int64(math.Round(sourceEnd.Float64*float64(f.SampleRate))))
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
	gain, err := audioGains(ctx, tx, item, asset)
	if err != nil {
		return nil, err
	}
	// Music has its own owner cap. The generic owner bitrate ceiling has
	// already been narrowed into v1 by create; never widen it here.
	cfg := s.DeliveryConfiguration()
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
	p.LoudnessEnvelope, err = audioEnvelope(ctx, tx, asset)
	if err != nil {
		return nil, err
	}
	bakedGain := 0.0
	if v1.AudioServerRender {
		if prefs := s.viewerPreferences(tx, principal, DeliveryContextFrom(ctx)); prefs != nil {
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
		return p, s.storeAudioPlan(tx, session.ID, p)
	}
	if !ownerTranscoding {
		p.Reason = "This device can't decode this track's format, and the server owner doesn't allow converting audio. It plays without gapless or crossfade."
		return p, s.storeAudioPlan(tx, session.ID, p)
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
		return p, s.storeAudioPlan(tx, session.ID, p)
	}
	if (conv.Codec == "opus" || conv.Codec == "aac") && !isLossless(f.Codec) && !isDSD(f.Codec) && sourceBPS > 0 {
		conv.BitrateBPS = min(conv.BitrateBPS, sourceBPS)
	}
	if conv.Codec == "opus" {
		conv.BitrateBPS = min(conv.BitrateBPS, 256000*conv.Channels)
	}
	if isDSD(f.Codec) {
		conv.GainDB = -6
		if prefs := s.viewerPreferences(tx, principal, DeliveryContextFrom(ctx)); prefs != nil {
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
	return p, s.storeAudioPlan(tx, session.ID, p)
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Service) storeAudioPlan(tx *sql.Tx, session string, p *AudioPlan) error {
	raw, err := json.Marshal(storedAudioPlan{AudioPlan: *p, Comparison: p.comparison})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO playback_audio_plans(session_id,plan_json) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET plan_json=excluded.plan_json`, session, string(raw))
	return err
}

// readAudioPlan is a presentation's pinned plan for its current generation.
func readAudioPlan(tx interface {
	QueryRow(string, ...any) *sql.Row
}, id string, generation int) (*AudioPlan, error) {
	var raw string
	err := tx.QueryRow(`SELECT plan_json FROM playback_audio_plans WHERE session_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeStoredAudioPlan(raw, generation)
}

// AudioPlanByGrant is the plan and media session behind a grant, for the audio routes.
func (s *Service) AudioPlanByGrant(ctx context.Context, grant string) (*AudioPlan, string, error) {
	var id string
	var generation int
	if err := s.db.QueryRowContext(ctx, `SELECT id,generation FROM playback_sessions WHERE grant_hash=?`, identity.Digest(grant)).Scan(&id, &generation); err != nil {
		return nil, "", err
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT plan_json FROM playback_audio_plans WHERE session_id=?`, id).Scan(&raw); err != nil {
		return nil, id, err
	}
	var p AudioPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Generation != generation {
		return nil, id, errAudioPlan
	}
	return &p, id, nil
}

// audioGains: tags first (ReplayGain, or R128 converted to the −18 LUFS
// reference), then the server's loudness analysis; album values from tags, or
// from analysis once every track of the album (or book) is measured.
func audioGains(ctx context.Context, tx *sql.Tx, item, asset string) (*AudioGain, error) {
	// audio_tag_evidence is a fact table written synchronously; no readiness
	// wait.
	tags := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT field,value FROM audio_tag_evidence WHERE asset_id=? AND library_id=(SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?))`, asset, item)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		tags[k] = v
	}
	rows.Close()
	g := &AudioGain{Source: "tags"}
	g.TrackDB, g.AlbumDB = replayGain(tags, "track"), replayGain(tags, "album")
	g.TrackPeak = audioTagNumber(tags["replaygain_track_peak"], 0.000001, 64)
	g.AlbumPeak = audioTagNumber(tags["replaygain_album_peak"], 0.000001, 64)
	if g.TrackDB != nil {
		return g, nil
	}
	lufs, peak, ok, err := assetLoudness(ctx, tx, asset)
	if err != nil || !ok {
		if err == nil && g.AlbumDB == nil {
			return nil, nil
		}
		return g, err
	}
	v := -18 - lufs
	g.TrackDB, g.Source = &v, "analysis"
	if peak > 0 {
		g.TrackPeak = &peak
	}
	if g.AlbumDB == nil {
		if a, ap, ok, err := albumLoudness(ctx, tx, item); err != nil {
			return nil, err
		} else if ok {
			av := -18 - a
			g.AlbumDB = &av
			if ap > 0 {
				g.AlbumPeak = &ap
			}
		}
	}
	return g, nil
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

func assetLoudness(ctx context.Context, tx *sql.Tx, asset string) (lufs, peak float64, ok bool, err error) {
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT summary_json FROM analysis_results WHERE asset_id=? AND stage='loudness' AND retired_ms=0 ORDER BY created_ms DESC LIMIT 1`, asset).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	var s struct {
		LUFS *float64 `json:"integratedLUFS"`
		Peak *float64 `json:"truePeakLinear"`
	}
	if json.Unmarshal([]byte(raw), &s) != nil || s.LUFS == nil || math.IsNaN(*s.LUFS) || *s.LUFS < -70 || *s.LUFS > 10 {
		return 0, 0, false, nil
	}
	if s.Peak != nil && *s.Peak > 0 && *s.Peak < 64 {
		peak = *s.Peak
	}
	return *s.LUFS, peak, true, nil
}

// albumLoudness is the duration-weighted energy mean of the album's (or book's)
// tracks, once every one is measured; bounded to 2000 tracks.
func albumLoudness(ctx context.Context, tx *sql.Tx, item string) (float64, float64, bool, error) {
	// Siblings, links, assets and loudness facts are all synchronous facts;
	// the old projection wait is gone.
	rows, err := tx.QueryContext(ctx, `WITH song_siblings(item_id) AS (
   SELECT s2.entity_id FROM catalog_entities source JOIN catalog_songs s1 ON s1.entity_id=source.id JOIN catalog_songs s2 ON s2.album_id=s1.album_id WHERE source.public_id=pid_blob(?) ORDER BY s2.entity_id LIMIT 2001),
 book_siblings(item_id) AS (
   SELECT b2.entity_id FROM catalog_entities source JOIN catalog_book_files b1 ON b1.entity_id=source.id JOIN catalog_book_files b2 ON b2.book_id=b1.book_id WHERE source.public_id=pid_blob(?) ORDER BY b2.entity_id LIMIT 2001),
 siblings(item_id) AS (SELECT item_id FROM song_siblings UNION SELECT item_id FROM book_siblings)
 SELECT MIN(a.token),MAX(a.duration) FROM siblings x JOIN catalog_asset_links ia ON ia.entity_id=x.item_id JOIN catalog_assets a ON a.id=ia.asset_id GROUP BY x.item_id LIMIT 2001`, item, item)
	if err != nil {
		return 0, 0, false, err
	}
	type track struct {
		asset    string
		duration float64
	}
	var tracks []track
	for rows.Next() {
		var t track
		if err = rows.Scan(&t.asset, &t.duration); err != nil {
			rows.Close()
			return 0, 0, false, err
		}
		tracks = append(tracks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, false, err
	}
	if len(tracks) == 0 || len(tracks) > 2000 {
		return 0, 0, false, nil
	}
	var energy, total, peak float64
	for _, t := range tracks {
		l, p, ok, err := assetLoudness(ctx, tx, t.asset)
		if err != nil || !ok || t.duration <= 0 {
			return 0, 0, false, err
		}
		energy += t.duration * math.Pow(10, l/10)
		total += t.duration
		peak = max(peak, p)
	}
	return 10 * math.Log10(energy/total), peak, true, nil
}

// ResolveDecodeGrant resolves a grant for the version 2 audio routes: like ResolveGrant,
// and a prepared (private) presentation's grant too (spec §18.2), whose bytes
// PrefetchCharge bounds.
func (s *Service) ResolveDecodeGrant(grant string) (string, identity.Principal, string, error) {
	return s.ResolveDecodeGrantContext(context.Background(), grant)
}

// ResolveDecodeGrantContext applies the caller's cancellation to initial audio
// grant checks, including a prepared presentation's private grant.
func (s *Service) ResolveDecodeGrantContext(ctx context.Context, grant string) (string, identity.Principal, string, error) {
	return s.resolveGrant(ctx, grant, true)
}

// PrivatePresentation reports whether a media session is a prepared, not yet
// committed next track (spec §18.2).
func (s *Service) PrivatePresentation(ctx context.Context, session string) (bool, error) {
	var private bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_private_presentations WHERE session_id=?)`, session).Scan(&private)
	return private, err
}

// PrefetchCharge counts bytes served to a private presentation against its
// plan's prefetchBytes; false when n more would exceed it (403 prepared_limit).
// A caller that charges before serving gives back what it didn't serve with
// PrefetchRefund, so a failed request never spends the budget (NEW-33).
func (s *Service) PrefetchCharge(session string, n, limit int64) bool {
	if n < 0 || limit < 0 {
		return false
	}
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if s.prefetchRetired[session] {
		return false
	}
	if s.prefetched == nil {
		s.prefetched = map[string]int64{}
	}
	if _, exists := s.prefetched[session]; !exists && len(s.prefetched) >= 4096 {
		return false
	}
	if n > limit-s.prefetched[session] {
		return false
	}
	s.prefetched[session] += n
	return true
}

// PrefetchTake reserves up to want bytes, shared by every overlapping request
// of this private presentation. Exhaustion never resets an active budget.
func (s *Service) PrefetchTake(session string, want, limit int64) int64 {
	if want <= 0 || limit <= 0 {
		return 0
	}
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if s.prefetchRetired[session] {
		return 0
	}
	if s.prefetched == nil {
		s.prefetched = map[string]int64{}
	}
	if _, exists := s.prefetched[session]; !exists && len(s.prefetched) >= 4096 {
		return 0
	}
	n := min(want, max(int64(0), limit-s.prefetched[session]))
	s.prefetched[session] += n
	return n
}

// ForgetPrefetch follows durable stop or commit, never eviction of live grants.
func (s *Service) ForgetPrefetch(session string) {
	// This legacy id-only counter is not an owned occurrence token. In
	// particular an old Stop callback must never select prefetchOwned[id].
	// Generation Stop retires only the records captured by its claim.
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if s.prefetchReaders[session] > 0 {
		if s.prefetchRetired == nil {
			s.prefetchRetired = map[string]bool{}
		}
		s.prefetchRetired[session] = true
		return
	}
	delete(s.prefetched, session)
}

// RetainPrefetch prevents stop/commit cleanup from regranting a budget to a
// writer already in flight. The last writer drops the retired counter.
func (s *Service) RetainPrefetch(session string) func() {
	s.prefetchMu.Lock()
	if s.prefetchReaders == nil {
		s.prefetchReaders = map[string]int{}
	}
	s.prefetchReaders[session]++
	s.prefetchMu.Unlock()
	return func() {
		s.prefetchMu.Lock()
		defer s.prefetchMu.Unlock()
		s.prefetchReaders[session]--
		if s.prefetchReaders[session] <= 0 {
			delete(s.prefetchReaders, session)
			if s.prefetchRetired[session] {
				delete(s.prefetched, session)
				delete(s.prefetchRetired, session)
			}
		}
	}
}

// PrefetchRefund returns n charged bytes that weren't served.
func (s *Service) PrefetchRefund(session string, n int64) {
	if n <= 0 {
		return
	}
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if s.prefetched[session] <= n {
		delete(s.prefetched, session)
		return
	}
	s.prefetched[session] -= n
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
func audioEnvelope(ctx context.Context, tx *sql.Tx, asset string) (*AudioLoudnessEnvelope, error) {
	rows, e := tx.QueryContext(ctx, `SELECT e.edge,e.lufs FROM analysis_audio_envelope e JOIN analysis_results r ON r.id=e.result_id WHERE r.id=(SELECT id FROM analysis_results WHERE asset_id=? AND stage='loudness' AND retired_ms=0 ORDER BY created_ms DESC LIMIT 1) ORDER BY e.edge,e.ordinal`, asset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := &AudioLoudnessEnvelope{IntervalMs: 100, StartLUFS: []float64{}, EndLUFS: []float64{}}
	for rows.Next() {
		var edge string
		var v float64
		if e = rows.Scan(&edge, &v); e != nil {
			return nil, e
		}
		if edge == "start" {
			out.StartLUFS = append(out.StartLUFS, v)
		} else {
			out.EndLUFS = append(out.EndLUFS, v)
		}
	}
	if len(out.StartLUFS) == 0 && len(out.EndLUFS) == 0 {
		return nil, rows.Err()
	}
	return out, rows.Err()
}
