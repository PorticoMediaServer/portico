package decoder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// PreparedRecipe is intentionally not an arbitrary ffmpeg argument surface.
// Profile identity/versioning is owned by preparedmedia, not by a client.
type PreparedRecipe struct {
	// Music fields are used only by the separate audio-only recipe builder.
	MusicQuality, MusicCodec                  string
	MusicStart, MusicEnd                      float64
	AudioOnly, DropChapters, BoundedFragments bool
	Width, Height, VideoKbps, AudioKbps       int
	// VideoCodec is "h264" (empty) or "hevc". An HEVC recipe keeps the
	// source's HDR10 or HLG picture at 10 bits and its surround sound; a
	// recipe with KeepSeventhOne keeps 7.1 (FLAC for 7.1, AAC otherwise),
	// while an older one caps the same source at 5.1 AAC. An H.264 recipe is
	// SDR and stereo, tone-mapping an HDR source.
	VideoCodec string
	// KeepSeventhOne marks the rich recipe version that keeps 7.1. An older
	// rich recipe keeps the same identity and bytes it always had.
	KeepSeventhOne bool
	// Source is what the encode was told about the source it reads. Its zero
	// value is an SDR source with stereo sound.
	Source PreparedSource
}

// PreparedSource is what a prepared encode needs to know of its source beyond
// the recipe: the picture's range and depth (and the Dolby Vision layers to
// leave behind) and each audio track's channel count, in source order.
type PreparedSource struct {
	// WavPack can be lossless or hybrid; only an observed header fact resolves it.
	AudioLossless                                   *bool
	AudioSampleRate, AudioBitDepth, AudioBitrateBPS int
	AudioCodec                                      string
	DynamicRange                                    string
	BitDepth                                        int
	DolbyVision                                     bool
	MasterDisplay                                   string
	MaxCLL                                          string
	AudioChannels                                   []int
}

// PreparedChannels is the channel count a prepared recipe writes for a source
// track: stereo for an ordinary recipe; for a surround recipe mono and stereo
// stay as they are and anything wider becomes 5.1, which every first-party
// player decodes as AAC.
func PreparedChannels(surround bool, source int) int {
	switch {
	case !surround:
		return 2
	case source == 1:
		return 1
	case source <= 2:
		return 2
	default:
		return 6
	}
}

// PreparedChannelsRich is the channel count a rich recipe version writes for
// one source track: the KeepSeventhOne version keeps 7.1, the older one has
// always folded it to 5.1.
func PreparedChannelsRich(seventhOne bool, source int) int {
	if seventhOne && source > 6 {
		return 8
	}
	return PreparedChannels(true, source)
}

// PreparedAudioCodec is the audio codec the KeepSeventhOne rich recipe writes
// for these source tracks: FLAC for a whole programme that carries more than
// six channels on any track, AAC otherwise. One codec covers every track of
// the file so the muxer writes one audio sample entry per track and the
// acceptance check stays exact (VIDEO §3 VS-8).
func PreparedAudioCodec(seventhOne bool, channels []int) string {
	if !seventhOne {
		return "aac"
	}
	for _, c := range channels {
		if c > 6 {
			return "flac"
		}
	}
	return "aac"
}

func preparedAudioKbps(channels int) int {
	switch {
	case channels == 1:
		return 96
	case channels == 2:
		return 192
	}
	return 384
}

func preparedHDR(r PreparedRecipe) bool {
	return r.Source.DynamicRange == "hdr10" || r.Source.DynamicRange == "hlg"
}

// PreparedArgs exposes the same closed, versioned builder to producer tests.
// Execution still requires a reserved bridge or confined descriptor.
func PreparedArgs(input string, r PreparedRecipe) ([]string, error) {
	return preparedArgs(input, r, false)
}

func preparedArgs(input string, r PreparedRecipe, validate bool) ([]string, error) {
	args := []string{"-v", "error", "-xerror", "-nostdin", "-threads", "2", "-protocol_whitelist", "http,tcp", "-format_whitelist", preparedInputFormats, "-probesize", "8388608", "-analyzeduration", "10000000", "-i", input}
	if validate {
		return append(args, "-map", "0:v?", "-map", "0:a?", "-sn", "-dn", "-f", "null", "-"), nil
	}
	if r.MusicQuality != "" {
		return preparedMusicArgs(args, r)
	}
	hevc := r.VideoCodec == "hevc"
	if r.VideoCodec != "" && r.VideoCodec != "h264" && !hevc || hevc && r.AudioOnly {
		return nil, ErrInvalidConfiguration
	}
	if hevc && r.AudioKbps != 384 || !hevc && r.AudioKbps != 192 && r.AudioKbps != 128 && r.AudioKbps != 96 {
		return nil, ErrInvalidConfiguration
	}
	if r.Source.DynamicRange != "" && r.Source.DynamicRange != "sdr" && !preparedHDR(r) || len(r.Source.AudioChannels) > 16 {
		return nil, ErrInvalidConfiguration
	}
	if r.Source.MasterDisplay != "" && !masteringDisplayPattern.MatchString(r.Source.MasterDisplay) || r.Source.MaxCLL != "" && !maxCLLPattern.MatchString(r.Source.MaxCLL) {
		return nil, ErrInvalidConfiguration
	}
	if r.AudioOnly {
		args = append(args, "-map", "0:a", "-vn")
	} else {
		if !validPreparedVideo(r) {
			return nil, ErrInvalidConfiguration
		}
		scale := fmt.Sprintf("scale=w='max(2,trunc(min(min(iw*sar,%d),%d*dar)/2)*2)':h='max(2,trunc(min(min(ih,%d),%d/dar)/2)*2)',setsar=1", r.Width, r.Height, r.Height, r.Width)
		rate := []string{"-fpsmax", "60", "-fps_mode", "vfr", "-maxrate", strconv.Itoa(r.VideoKbps) + "k", "-bufsize", strconv.Itoa(r.VideoKbps*2) + "k", "-threads", "2"}
		if !hevc {
			// An HDR source becomes a standard picture here: scaled in its own
			// range, then tone-mapped on the processor, never written flat.
			if preparedHDR(r) {
				scale += "," + SoftwareToneMap("hable")
			}
			args = append(args, "-map", "0:v:0", "-map", "0:a?", "-vf", scale, "-c:v", "libx264", "-preset", "medium", "-pix_fmt", "yuv420p", "-profile:v", "high", "-level:v", strconv.FormatFloat(float64(H264OutputLevel(r.Width, r.Height, 60))/10, 'f', 1, 64), "-crf", "21")
			args = append(args, rate...)
			if preparedHDR(r) {
				args = append(args, "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv")
			}
		} else {
			// HEVC keeps the source's depth (ten bits for HDR) and its range,
			// with HDR10's mastering description carried in the stream.
			deep := preparedHDR(r) || r.Source.BitDepth >= 10
			pixel, profile := "yuv420p", "main"
			if deep {
				pixel, profile = "yuv420p10le", "main10"
			}
			params := "pools=2:frame-threads=1:repeat-headers=1:log-level=error"
			color := []string{}
			if preparedHDR(r) {
				transfer := "smpte2084"
				if r.Source.DynamicRange == "hlg" {
					transfer = "arib-std-b67"
				}
				color = []string{"-color_primaries", "bt2020", "-color_trc", transfer, "-colorspace", "bt2020nc", "-color_range", "tv"}
				if r.Source.DynamicRange == "hdr10" {
					params += ":hdr10=1"
					if r.Source.MasterDisplay != "" {
						params += ":master-display=" + r.Source.MasterDisplay
					}
					if r.Source.MaxCLL != "" {
						params += ":max-cll=" + r.Source.MaxCLL
					}
				}
			}
			args = append(args, "-map", "0:v:0", "-map", "0:a?", "-vf", scale, "-c:v", "libx265", "-preset", "fast", "-pix_fmt", pixel, "-profile:v", profile, "-tag:v", "hvc1", "-crf", "22", "-x265-params", params)
			args = append(args, rate...)
			args = append(args, color...)
			if r.Source.DolbyVision {
				// Only the base layer is kept; no Dolby Vision unit may survive
				// into a stream that is not described as Dolby Vision.
				args = append(args, "-bsf:v", "filter_units=remove_types=62|63")
			}
		}
	}
	if r.DropChapters {
		// Chapter positions are not copied into a derivative with a new clock.
		args = append(args, "-map_chapters", "-1")
	}
	if r.BoundedFragments {
		// A pipe cannot seek back to repair an unbounded MP4 fragment. Flush
		// at one-second intervals, including audio with no video keyframes.
		args = append(args, "-frag_duration", "1000000")
	}
	seventhOne := hevc && r.KeepSeventhOne
	audioCodec := PreparedAudioCodec(seventhOne, r.Source.AudioChannels)
	args = append(args, "-sn", "-dn", "-map_metadata", "-1", "-c:a", audioCodec)
	if hevc && len(r.Source.AudioChannels) > 0 {
		for i, channels := range r.Source.AudioChannels {
			out := PreparedChannelsRich(seventhOne, channels)
			args = append(args, "-ac:a:"+strconv.Itoa(i), strconv.Itoa(out))
			if audioCodec != "flac" {
				args = append(args, "-b:a:"+strconv.Itoa(i), strconv.Itoa(preparedAudioKbps(out))+"k")
			}
		}
	} else {
		args = append(args, "-b:a", strconv.Itoa(min(r.AudioKbps, 192))+"k", "-ac", "2")
	}
	return append(args, "-ar", "48000", "-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1"), nil
}
func preparedCommand(executable, input string, res *EndpointReservation, recipe PreparedRecipe, validate bool, libraries ...string) (*exec.Cmd, string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || len(libraries) > 256 {
		return nil, "", ErrInvalidConfiguration
	}
	for _, p := range libraries {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, "", ErrInvalidConfiguration
		}
	}
	endpoint, e := bridgeEndpoint(input)
	if e != nil {
		return nil, "", e
	}
	if res == nil || !res.matches(endpoint) {
		return nil, "", ErrInvalidConfiguration
	}
	args, e := preparedArgs(input, recipe, validate)
	if e != nil {
		return nil, "", e
	}
	cmd, e := decoderJob{executable: executable, args: args, endpoint: endpoint, libraries: libraries, background: true}.command()
	return cmd, endpoint, e
}

// runPrepared holds the caller's inherited custody descriptor until the actual
// child retires, including cancellation and parent-death recovery. Background
// work does not consume the supervisor's reserved interactive playback slot.
func runPrepared(ctx context.Context, s *storage.Supervisor, key string, cmd *exec.Cmd, endpoint string, res *EndpointReservation, custody *os.File, consume func(io.Reader) error) error {
	if s == nil || key == "" || custody == nil || cmd == nil {
		return ErrInvalidConfiguration
	}
	release, err := admitPreparedConversion(ctx)
	if err != nil {
		return err
	}
	defer release()
	cmd.ExtraFiles = append(cmd.ExtraFiles, custody)
	if !res.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	retired := make(chan struct{})
	e := s.RunOwnedCompletion(ctx, "optimization:"+key, cmd, consume, nil, func() { res.retire(); close(retired) })
	<-retired
	return e
}
func RunPreparedProbe(ctx context.Context, s *storage.Supervisor, key, executable, input string, res *EndpointReservation, custody *os.File, libraries ...string) ([]byte, error) {
	cmd, e := probeFormatsCommand(executable, input, res, preparedInputFormats, libraries...)
	if e != nil {
		return nil, e
	}
	endpoint, e := bridgeEndpoint(input)
	if e != nil {
		return nil, e
	}
	var b []byte
	e = runPrepared(ctx, s, key, cmd, endpoint, res, custody, func(r io.Reader) error {
		var err error
		b, err = io.ReadAll(io.LimitReader(r, MaxProbeOutputBytes+1))
		if err == nil && len(b) > MaxProbeOutputBytes {
			err = ErrProbeOutput
		}
		return err
	})
	return b, e
}
func RunPreparedEncode(ctx context.Context, s *storage.Supervisor, key, executable, input string, res *EndpointReservation, custody *os.File, recipe PreparedRecipe, out io.Writer, libraries ...string) error {
	cmd, endpoint, e := preparedCommand(executable, input, res, recipe, false, libraries...)
	if e != nil {
		return e
	}
	return runPrepared(ctx, s, key, cmd, endpoint, res, custody, func(r io.Reader) error { _, e := io.Copy(out, r); return e })
}
func RunPreparedValidate(ctx context.Context, s *storage.Supervisor, key, executable, input string, res *EndpointReservation, custody *os.File, libraries ...string) error {
	cmd, endpoint, e := preparedCommand(executable, input, res, PreparedRecipe{}, true, libraries...)
	if e != nil {
		return e
	}
	return runPrepared(ctx, s, key, cmd, endpoint, res, custody, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
}

func validPreparedVideo(r PreparedRecipe) bool {
	for _, p := range qualityladder.Fixed() {
		if r.Width == p.Width() && r.Height == p.Height && r.VideoKbps == p.VideoBitrateBPS/1000 {
			return true
		}
	}
	return false
}
