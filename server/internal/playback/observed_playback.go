package playback

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
)

// ObservedPlayback is a complete sparse snapshot of facts actually observed by
// the playing renderer. It is not device capability, a requested setting, a
// server encode target, or a source-file fact. Zero/absence stays unknown. The
// existing V1 timeline admits it only for its playing device's fresh generation
// and sequence. An old presentation cannot supply a new generation's output.
type ObservedPlayback struct {
	Video                *ObservedVideo       `json:"video,omitempty"`
	Screen               *ObservedScreen      `json:"screen,omitempty"`
	Audio                *ObservedAudioOutput `json:"audio,omitempty"`
	Subtitles            *ObservedSubtitles   `json:"subtitles,omitempty"`
	DeliveredBitrateKbps int                  `json:"deliveredBitrateKbps,omitempty"`
}

// Video describes actual decoded/output frames, not the selected quality cap.
// Screen is separate: available HDR modes, source HDR metadata and decoded
// transfer characteristics do not establish the display's applied HDR mode.
type ObservedVideo struct {
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	FrameRateHz  float64 `json:"frameRateHz,omitempty"`
	BitDepth     int     `json:"bitDepth,omitempty"`
	DynamicRange string  `json:"dynamicRange,omitempty"`
}
type ObservedScreen struct {
	DynamicRange  string  `json:"dynamicRange,omitempty"`
	RefreshRateHz float64 `json:"refreshRateHz,omitempty"`
}
type ObservedAudioOutput struct {
	Handling    string `json:"handling,omitempty"`
	Codec       string `json:"codec,omitempty"`
	ObjectAudio string `json:"objectAudio,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	Layout      string `json:"layout,omitempty"`
	SampleRate  int    `json:"sampleRate,omitempty"`
	BitDepth    int    `json:"bitDepth,omitempty"`
}
type ObservedSubtitles struct {
	Rendering string `json:"rendering"`
}

func observedRange(value string) bool {
	return value == "" || slices.Contains([]string{"sdr", "hdr10", "hdr10plus", "hlg", "dolby_vision"}, value)
}
func observedRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1000
}
func (o *ObservedPlayback) Valid() bool {
	if o == nil {
		return true
	}
	if o.DeliveredBitrateKbps < 0 || o.DeliveredBitrateKbps > 1000000 {
		return false
	}
	if v := o.Video; v != nil {
		if v.Width < 0 || v.Width > 32768 || v.Height < 0 || v.Height > 32768 ||
			!observedRate(v.FrameRateHz) || !observedRange(v.DynamicRange) ||
			(v.BitDepth != 0 && v.BitDepth != 8 && v.BitDepth != 10 && v.BitDepth != 12 && v.BitDepth != 16) {
			return false
		}
	}
	if s := o.Screen; s != nil && (!observedRange(s.DynamicRange) || !observedRate(s.RefreshRateHz)) {
		return false
	}
	if a := o.Audio; a != nil {
		if a.Handling != "" && !slices.Contains([]string{"passthrough", "decoded", "pcm", "mixed"}, a.Handling) {
			return false
		}
		if a.Codec != "" && !slices.Contains([]string{"aac", "ac3", "eac3", "truehd", "dts", "flac", "alac", "pcm", "opus", "mp3", "mp2", "vorbis", "ac4"}, a.Codec) {
			return false
		}
		if a.ObjectAudio != "" && a.ObjectAudio != "atmos" && a.ObjectAudio != "dtsx" {
			return false
		}
		if a.Channels < 0 || a.Channels > 64 || a.SampleRate < 0 || a.SampleRate > 1536000 {
			return false
		}
		if a.Layout != "" && !slices.Contains([]string{"mono", "stereo", "2.0", "5.1", "5.1(side)", "7.1", "7.1(wide)", "7.1(wide-side)"}, a.Layout) {
			return false
		}
		if a.BitDepth != 0 && a.BitDepth != 16 && a.BitDepth != 20 && a.BitDepth != 24 && a.BitDepth != 32 {
			return false
		}
	}
	if s := o.Subtitles; s != nil && !slices.Contains([]string{"drawnByDevice", "externalText", "burnedIn", "off"}, s.Rendering) {
		return false
	}
	return true
}

// StoreObservedPlaybackTx runs only after the same transaction accepted the V1
// report. Its INSERT rechecks exact device, presentation generation and accepted
// sequence, so a racing change/transfer cannot attach an obsolete observation.
// Session and presentation generations are separate: Live buffer generations
// come from the actual channel runtime and need not equal the V1 row generation.
// A supplied snapshot replaces all its blocks: omitted facts are cleared. An
// omitted snapshot makes no new assertion. No private route/URL/model text is
// accepted or persisted by this scalar contract.
func StoreObservedPlaybackTx(ctx context.Context, tx *sql.Tx, session, device string, sessionGeneration, generation int, seq, now int64, o *ObservedPlayback) error {
	if o == nil {
		return nil
	}
	if !o.Valid() || sessionGeneration < 1 || generation < 1 || seq < 0 || now < 0 {
		return errors.New("invalid playback observation")
	}
	video, screen, audio, subtitles := ObservedVideo{}, ObservedScreen{}, ObservedAudioOutput{}, ObservedSubtitles{}
	if o.Video != nil {
		video = *o.Video
	}
	if o.Screen != nil {
		screen = *o.Screen
	}
	if o.Audio != nil {
		audio = *o.Audio
	}
	if o.Subtitles != nil {
		subtitles = *o.Subtitles
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_observed_output(session_id,session_generation,generation,seq,observed_ms,video_known,video_width,video_height,video_fps,video_bit_depth,video_range,screen_known,dynamic_range,refresh_hz,audio_known,audio_handling,audio_codec,object_audio,channels,channel_layout,sample_rate,bit_depth,subtitles_known,subtitle_rendering,delivered_kbps)
 SELECT id,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? FROM playback_v1_sessions s WHERE id=? AND device_id=? AND generation=? AND last_seq=? AND ended_ms=0
 AND ((?=s.generation AND NOT EXISTS(SELECT 1 FROM playback_channel_sessions c WHERE c.session_id=s.id))
 OR EXISTS(SELECT 1 FROM playback_channel_sessions c WHERE c.session_id=s.id AND c.buffer_ordinal=? AND c.ended_ms=0))
 ON CONFLICT(session_id) DO UPDATE SET session_generation=excluded.session_generation,generation=excluded.generation,seq=excluded.seq,observed_ms=excluded.observed_ms,video_known=excluded.video_known,video_width=excluded.video_width,video_height=excluded.video_height,video_fps=excluded.video_fps,video_bit_depth=excluded.video_bit_depth,video_range=excluded.video_range,screen_known=excluded.screen_known,dynamic_range=excluded.dynamic_range,refresh_hz=excluded.refresh_hz,audio_known=excluded.audio_known,audio_handling=excluded.audio_handling,audio_codec=excluded.audio_codec,object_audio=excluded.object_audio,channels=excluded.channels,channel_layout=excluded.channel_layout,sample_rate=excluded.sample_rate,bit_depth=excluded.bit_depth,subtitles_known=excluded.subtitles_known,subtitle_rendering=excluded.subtitle_rendering,delivered_kbps=excluded.delivered_kbps
 WHERE playback_observed_output.session_generation<excluded.session_generation OR (playback_observed_output.session_generation=excluded.session_generation AND (playback_observed_output.generation<excluded.generation OR (playback_observed_output.generation=excluded.generation AND playback_observed_output.seq<excluded.seq)))`,
		sessionGeneration, generation, seq, now, o.Video != nil, video.Width, video.Height, video.FrameRateHz, video.BitDepth, video.DynamicRange, o.Screen != nil, screen.DynamicRange, screen.RefreshRateHz, o.Audio != nil, audio.Handling, audio.Codec, audio.ObjectAudio, audio.Channels, audio.Layout, audio.SampleRate, audio.BitDepth, o.Subtitles != nil, subtitles.Rendering, o.DeliveredBitrateKbps, session, device, sessionGeneration, seq, generation, generation)
	return err
}

// ReadObservedPlayback reads only this still-active session generation. The
// caller owns ordinary session/viewer authorization and supplies the current
// actual presentation generation (the runtime buffer for Live); it never looks
// up a replacement generation or treats a media URL as an observation key.
func ReadObservedPlayback(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, session string, sessionGeneration, generation int) (*ObservedPlayback, error) {
	if sessionGeneration < 1 || generation < 1 {
		return nil, nil
	}
	var o ObservedPlayback
	var video ObservedVideo
	var screen ObservedScreen
	var audio ObservedAudioOutput
	var subtitles ObservedSubtitles
	var hasVideo, hasScreen, hasAudio, hasSubtitles bool
	err := q.QueryRowContext(ctx, `SELECT o.video_known,o.video_width,o.video_height,o.video_fps,o.video_bit_depth,o.video_range,o.screen_known,o.dynamic_range,o.refresh_hz,o.audio_known,o.audio_handling,o.audio_codec,o.object_audio,o.channels,o.channel_layout,o.sample_rate,o.bit_depth,o.subtitles_known,o.subtitle_rendering,o.delivered_kbps
 FROM playback_observed_output o JOIN playback_v1_sessions s ON s.id=o.session_id AND s.generation=o.session_generation
 WHERE s.id=? AND s.generation=? AND o.generation=? AND o.seq<=s.last_seq AND s.ended_ms=0
 AND ((o.generation=s.generation AND NOT EXISTS(SELECT 1 FROM playback_channel_sessions c WHERE c.session_id=s.id))
 OR EXISTS(SELECT 1 FROM playback_channel_sessions c WHERE c.session_id=s.id AND c.buffer_ordinal=o.generation AND c.ended_ms=0))`, session, sessionGeneration, generation).Scan(
		&hasVideo, &video.Width, &video.Height, &video.FrameRateHz, &video.BitDepth, &video.DynamicRange, &hasScreen, &screen.DynamicRange, &screen.RefreshRateHz, &hasAudio, &audio.Handling, &audio.Codec, &audio.ObjectAudio, &audio.Channels, &audio.Layout, &audio.SampleRate, &audio.BitDepth, &hasSubtitles, &subtitles.Rendering, &o.DeliveredBitrateKbps)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if hasVideo {
		o.Video = &video
	}
	if hasScreen {
		o.Screen = &screen
	}
	if hasAudio {
		o.Audio = &audio
	}
	if hasSubtitles {
		o.Subtitles = &subtitles
	}
	if !o.Valid() {
		return nil, errors.New("invalid stored playback observation")
	}
	return &o, nil
}
