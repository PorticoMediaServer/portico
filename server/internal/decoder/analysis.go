package decoder

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// PreviewVideoFacts selects a closed color conversion for navigation images.
type PreviewVideoFacts struct {
	DynamicRange                                 string
	DolbyVisionProfile, DolbyVisionCompatibility int
}

// AnalysisSpec is a closed recipe. No user filter, filename, protocol or encoder
// argument enters the decoder. All output is streamed through supervised stdout.
type AnalysisSpec struct {
	AudioTruePeakLimit bool
	AudioSourceDepth   int
	AudioGainDB        float64
	PhysicalLock       *os.File
	Kind               string
	StartUS            int64
	IntervalUS         int64
	MaxFrames          int
	MaxDurationUS      int64
	// RegionUS is the length read from StartUS by the segment detection
	// recipes, which measure one stretch of a file rather than all of it.
	RegionUS int64
	// Width/Height select the trickplay frame geometry. Zero keeps the historic
	// 320x180 preview frame. Both must be even, bounded, and chosen by the
	// caller from validated policy, never from media or a client request.
	Width, Height int
	SourceVideo   PreviewVideoFacts
	// Audio conversion (spec §18.1 converted mode): raw decoded frames
	// [AudioStart, AudioStart+AudioFrames) of the source, resampled to AudioRate
	// and AudioChannels, padded or cut to exactly AudioOut frames, encoded as FLAC
	// (AudioBitDepth 16 or 24), Opus or AAC-LC in ADTS (AudioBitrate bits per
	// second).
	AudioStart, AudioFrames, AudioOut int64
	AudioRate, AudioChannels          int
	AudioSourceRate                   int
	AudioPriming                      int64
	AudioBitDepth, AudioBitrate       int
}

func AnalysisArgs(input string, s AnalysisSpec) ([]string, error) {
	if s.StartUS < 0 || s.MaxDurationUS <= 0 || s.StartUS >= s.MaxDurationUS || s.MaxDurationUS > (1<<53)-1 {
		return nil, ErrInvalidConfiguration
	}
	seekFrames := int64(0)
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1"}
	preview := s.Kind == "image" || s.Kind == "trickplay" || s.Kind == "scenes" || s.Kind == "segment_frames"
	sonicRegion := s.Kind == "sonic_pcm"
	region := s.Kind == "segment_audio" || s.Kind == "segment_frames" || sonicRegion
	if region && (s.RegionUS <= 0 || s.RegionUS > s.MaxDurationUS) {
		return nil, ErrInvalidConfiguration
	}
	if sonicRegion && s.RegionUS > s.MaxDurationUS-s.StartUS {
		return nil, ErrInvalidConfiguration
	}
	if (preview || region) && !sonicRegion {
		// A damaged packet costs a measurement a moment of a region, not
		// the measurement.
		args = append(args, "-max_error_rate", "0.05")
	} else {
		args = append(args, "-xerror")
	}
	tone, hardware, err := analysisPreviewTone(s.SourceVideo, preview)
	if err != nil {
		return nil, err
	}
	if hardware {
		args = append(args, "-init_hw_device", "vulkan=tone", "-filter_hw_device", "tone")
	}
	if s.Kind == "image" || region && s.StartUS > 0 {
		args = append(args, "-ss", fmt.Sprintf("%d.%06d", s.StartUS/1000000, s.StartUS%1000000))
	}
	if audioConversion(s.Kind) {
		if s.AudioSourceRate > 0 && s.AudioSourceRate <= 768000 && s.AudioStart >= s.AudioPriming && s.AudioStart-s.AudioPriming > int64(s.AudioSourceRate)*3 {
			seconds := (s.AudioStart-s.AudioPriming)/int64(s.AudioSourceRate) - 2
			args = append(args, "-ss", fmt.Sprint(seconds))
			seekFrames = seconds*int64(s.AudioSourceRate) + s.AudioPriming
		}
		// Every decoded frame, priming included: the facts' trim is relative to it.
		args = append(args, "-flags2", "+skip_manual")
	}
	args = append(args, "-protocol_whitelist", "http,tcp", "-format_whitelist", "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi,aiff,asf,ape,wv,tta,amr,w64,caf,dsf,iff,mpc,mpc8,mpeg,mpegvideo", "-probesize", "8388608", "-analyzeduration", "10000000", "-i", input, "-map_metadata", "-1", "-map_chapters", "-1", "-threads", "1")
	if region {
		args = append(args, "-t", fmt.Sprintf("%d.%06d", s.RegionUS/1000000, s.RegionUS%1000000))
	}
	switch s.Kind {
	case "sonic_pcm":
		// Sonic is a model input, never a playback conversion or gain claim.
		// Fixed 16 kHz mono float preserves decoded precision before log-mel.
		args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", "aresample=16000:resampler=soxr:precision=28", "-ac", "1", "-ar", "16000", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
	case "segment_audio":
		// The fingerprint's input: the first audio track as 8 kHz mono, padded
		// from the region's start so sample n is always StartUS + n/8000 s.
		args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", "aresample=8000:async=1:first_pts=0", "-ac", "1", "-ar", "8000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	case "segment_frames":
		// One small grey picture a second for the credits reader.
		if s.Width != 0 || s.Height != 0 || s.IntervalUS != 1000000 || s.MaxFrames < 1 || s.MaxFrames > 65536 {
			return nil, ErrInvalidConfiguration
		}
		filter := "fps=1:start_time=0,scale=256:144:force_original_aspect_ratio=decrease:reset_sar=1,pad=256:144:(ow-iw)/2:(oh-ih)/2,setsar=1" + tone + ",format=gray"
		args = append(args, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter, "-frames:v", fmt.Sprint(s.MaxFrames), "-pix_fmt", "gray", "-f", "rawvideo", "pipe:1")
	case "loudness_full":
		// Native rate/layout, double precision; no stereo quantization before measurement.
		args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", "ebur128=metadata=1:peak=true,ametadata=print:file='pipe\\:1'", "-f", "null", "-")
	case "pcm_shared":
		// One decode for every audio analysis (waveform, fingerprint, loudness): the
		// first audio track as 48 kHz stereo, the rate loudness is measured at.
		args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "2", "-af", "aresample=48000:async=1:first_pts=0", "-ar", "48000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	case "audio_flac", "audio_opus", "audio_aac":
		if s.AudioStart < 0 || s.AudioFrames <= 0 || s.AudioOut <= 0 || s.AudioRate < 8000 || s.AudioRate > 768000 || s.AudioChannels < 1 || s.AudioChannels > 8 {
			return nil, ErrInvalidConfiguration
		}
		// asetpts counts samples from the first decoded frame, so the trim is in raw
		// frames whatever timestamps the container gave the priming; again after
		// resampling, whose first output timestamp needn't be 0, so the final cut is
		// exact.
		if math.IsNaN(s.AudioGainDB) || math.IsInf(s.AudioGainDB, 0) || s.AudioGainDB < -60 || s.AudioGainDB > 30 {
			return nil, ErrInvalidConfiguration
		}
		filters := []string{fmt.Sprintf("asetpts=N/SR/TB,atrim=start_sample=%d:end_sample=%d,asetpts=N/SR/TB", s.AudioStart-seekFrames, s.AudioStart-seekFrames+s.AudioFrames)}
		if s.AudioGainDB != 0 {
			filters = append(filters, fmt.Sprintf("volume=%gdB:precision=double", s.AudioGainDB))
		}
		if s.AudioTruePeakLimit {
			// Four-times oversampling bounds inter-sample peaks. Automatic
			// make-up gain is off; lookahead latency is compensated and flushed.
			filters = append(filters, fmt.Sprintf("aresample=%d:resampler=soxr:precision=28:osf=dbl,alimiter=limit=0.8912509381:level=0:latency=1", s.AudioRate*4))
		}
		// Untouched same-rate/depth samples never enter a resampler or dither stage.
		resampleSource := s.AudioSourceRate
		if s.AudioTruePeakLimit {
			resampleSource = s.AudioRate * 4
		}
		filters = append(filters, musicResampleFilters(resampleSource, s.AudioRate, s.AudioSourceDepth, s.AudioBitDepth, s.AudioTruePeakLimit || s.AudioGainDB != 0)...)
		filters = append(filters, fmt.Sprintf("asetpts=N/SR/TB,apad=whole_len=%d,atrim=end_sample=%d", s.AudioOut, s.AudioOut))
		filter := strings.Join(filters, ",")
		args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", filter, "-ac", fmt.Sprint(s.AudioChannels), "-ar", fmt.Sprint(s.AudioRate))
		if s.Kind == "audio_flac" {
			format, depth := "s16", "16"
			if s.AudioBitDepth == 24 || s.AudioBitDepth == 32 {
				format, depth = "s32", fmt.Sprint(s.AudioBitDepth)
			} else if s.AudioBitDepth != 16 {
				return nil, ErrInvalidConfiguration
			}
			args = append(args, "-c:a", "flac", "-sample_fmt", format, "-bits_per_raw_sample", depth, "-strict", "-2", "-f", "flac", "pipe:1")
		} else if s.Kind == "audio_aac" {
			// FFmpeg's own AAC-LC encoder, in ADTS so the stream is readable as it
			// arrives; its 1024-frame delay is the plan's trim (playback.AACPriming).
			if !aacTargetRate(s.AudioRate) || s.AudioChannels > 8 || s.AudioBitrate < 16000 || s.AudioBitrate > 512000 {
				return nil, ErrInvalidConfiguration
			}
			args = append(args, "-c:a", "aac", "-profile:a", "aac_low", "-b:a", fmt.Sprint(s.AudioBitrate), "-f", "adts", "pipe:1")
		} else {
			if s.AudioRate != 48000 || s.AudioBitrate < 6000 || s.AudioBitrate > 512000 {
				return nil, ErrInvalidConfiguration
			}
			args = append(args, "-c:a", "libopus", "-b:a", fmt.Sprint(s.AudioBitrate), "-frame_duration", "20", "-application", "audio", "-f", "ogg", "pipe:1")
		}
	case "image":
		args = append(args, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", "scale=320:180:force_original_aspect_ratio=decrease:reset_sar=1,pad=320:180:(ow-iw)/2:(oh-ih)/2,setsar=1"+tone, "-frames:v", "1", "-c:v", "mjpeg", "-q:v", "3", "-f", "image2pipe", "pipe:1")
	case "trickplay", "scenes":
		if s.IntervalUS < 100000 || s.MaxFrames < 1 || s.MaxFrames > 65536 {
			return nil, ErrInvalidConfiguration
		}
		width, height := s.Width, s.Height
		if width == 0 && height == 0 {
			width, height = 320, 180
		}
		if width < 16 || height < 16 || width > 1920 || height > 1080 || width%2 != 0 || height%2 != 0 {
			return nil, ErrInvalidConfiguration
		}
		scale, pixel := fmt.Sprintf("%d:%d", width, height), "rgb24"
		if s.Kind == "scenes" {
			if s.Width != 0 || s.Height != 0 {
				return nil, ErrInvalidConfiguration
			}
			scale, pixel = "32:18", "gray"
		}
		filter := fmt.Sprintf("fps=1000000/%d:start_time=0,scale=%s:force_original_aspect_ratio=decrease:reset_sar=1,pad=%s:(ow-iw)/2:(oh-ih)/2,setsar=1%s,format=%s", s.IntervalUS, scale, scale, tone, pixel)
		args = append(args, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter, "-pix_fmt", pixel, "-f", "rawvideo", "pipe:1")
	default:
		return nil, ErrInvalidConfiguration
	}
	return args, nil
}

// RunAnalysis waits for actual process retirement even after cancellation. This
// deliberately uses a background key so playback retains its reserved slot.
func RunAnalysis(ctx context.Context, sup *storage.Supervisor, key, executable, input string, res *EndpointReservation, spec AnalysisSpec, consume func(io.Reader) error, libraries ...string) error {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || sup == nil || consume == nil || key == "" || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !res.matches(endpoint) || len(libraries) > 256 {
		if res != nil {
			res.Close()
		}
		return ErrInvalidConfiguration
	}
	for _, path := range libraries {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			res.Close()
			return ErrInvalidConfiguration
		}
	}
	args, err := AnalysisArgs(input, spec)
	if err != nil {
		res.Close()
		return err
	}
	cmd, err := decoderJob{executable: executable, args: args, endpoint: endpoint, libraries: libraries, background: !audioConversion(spec.Kind), hardware: analysisNeedsHardware(spec)}.command()
	if err != nil {
		res.Close()
		return err
	}
	if spec.PhysicalLock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, spec.PhysicalLock)
	}
	if !res.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	retired := make(chan struct{})
	err = sup.RunOwnedCompletion(ctx, analysisRunKey(spec, key), cmd, consume, nil, func() { res.retire(); close(retired) })
	<-retired
	return err
}

// audioConversion is whether a kind is one of the playback audio conversions
// (spec §18.1 converted mode) rather than background analysis.
func audioConversion(kind string) bool {
	return kind == "audio_flac" || kind == "audio_opus" || kind == "audio_aac"
}

func analysisRunKey(spec AnalysisSpec, key string) string {
	if audioConversion(spec.Kind) {
		return "playback:audio:" + key
	}
	return "analysis:" + key
}

func analysisNeedsHardware(s AnalysisSpec) bool {
	return (s.Kind == "image" || s.Kind == "trickplay" || s.Kind == "scenes" || s.Kind == "segment_frames") && s.SourceVideo.DynamicRange == "dolby_vision" && DolbyVisionBaseRange(s.SourceVideo.DolbyVisionProfile, s.SourceVideo.DolbyVisionCompatibility) == ""
}
func analysisPreviewTone(video PreviewVideoFacts, preview bool) (string, bool, error) {
	if !preview {
		return "", false, nil
	}
	dynamic := video.DynamicRange
	if dynamic == "dolby_vision" {
		dynamic = DolbyVisionBaseRange(video.DolbyVisionProfile, video.DolbyVisionCompatibility)
		if dynamic == "" {
			tools := CurrentToolchain()
			if tools == nil || !tools.Filters["libplacebo"] || !tools.VulkanAvailable {
				return "", false, ErrInvalidConfiguration
			}
			return ",format=yuv420p10le,hwupload,libplacebo=tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:format=yuv420p,hwdownload,format=yuv420p", true, nil
		}
	}
	switch dynamic {
	case "", "sdr":
		return "", false, nil
	case "hdr10", "hlg":
		return "," + SoftwareToneMap(DefaultToneMapAlgorithm), false, nil
	default:
		return "", false, ErrInvalidConfiguration
	}
}
