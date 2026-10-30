package decoder

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type LinearEncoding struct {
	Target                               *ConversionTarget
	Deinterlace                          bool
	MaxFrameRate                         float64
	Profile                              string
	Level                                int
	Toolchain                            *ToolchainFacts
	HLSInput, Finite, Video, Audio       bool
	ConvertVideo, ConvertAudio, HDRToSDR bool
	SeekUS, ClipEndUS                    int64
	MaxHeight, VideoBitrate              int
	// Fragmented publishes fragmented MP4 (an init segment and .m4s
	// fragments) instead of MPEG-TS. CopyHEVC marks a copied HEVC picture,
	// whose MP4 sample entry follows its delivered Dolby Vision profile.
	Fragmented, CopyHEVC bool
	// AudioCodec is the selected output codec. CopyDolbyVisionProfile names
	// an untouched copied picture when no conversion target was needed.
	AudioCodec             string
	CopyDolbyVisionProfile int
	// CopyAAC records the selected original AAC-copy/fMP4 packaging case.
	// The muxer needs ADTS normalization without converting the audio.
	CopyAAC bool
}

var outputPath = regexp.MustCompile(`^/output/[a-f0-9]{64}/$`)

func linearOutputEndpoint(input, output string) (string, error) {
	endpoint, e := bridgeEndpoint(input)
	if e != nil {
		return "", e
	}
	u, e := url.Parse(output)
	if e != nil || u.Scheme != "http" || u.Host != endpoint || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !outputPath.MatchString(u.Path) {
		return "", ErrInvalidConfiguration
	}
	return endpoint, nil
}

// LinearProbe is a bounded stream probe. Every HLS descendant has already been
// rewritten by the private gateway; protocol/file restrictions are not relaxed.
func LinearProbe(ctx context.Context, supervisor *storage.Supervisor, key, executable, input string, hls bool, reservation *EndpointReservation, physicalLock *os.File, libraries ...string) ([]byte, error) {
	defer reservation.Close()
	endpoint, e := bridgeEndpoint(input)
	if e != nil || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !reservation.matches(endpoint) {
		return nil, ErrInvalidConfiguration
	}
	protocols, formats := "http,tcp", "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi"
	if hls {
		protocols, formats = "http,tcp,crypto", "hls,mpegts,mov,aac,mp3"
	}
	args := []string{"-v", "error", "-threads", "2", "-protocol_whitelist", protocols, "-format_whitelist", formats, "-rw_timeout", "15000000", "-probesize", "8388608", "-analyzeduration", "5000000", "-show_format", "-show_streams", "-of", "json"}
	if hls {
		args = append(args, "-allowed_extensions", "ALL")
	}
	args = append(args, input)
	for _, file := range libraries {
		if !filepath.IsAbs(file) || filepath.Clean(file) != file {
			return nil, ErrInvalidConfiguration
		}
	}
	cmd, e := confinedCommand(executable, args, endpoint, libraries...)
	if e != nil {
		return nil, e
	}
	if physicalLock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, physicalLock)
	}
	return runOwned(ctx, supervisor, key, cmd, endpoint, reservation)
}
func linearHLSArgs(input, output string, p LinearEncoding) ([]string, bool, error) {
	if !p.Video && !p.Audio || p.SeekUS < 0 || p.ClipEndUS < 0 || p.Finite && p.ClipEndUS <= p.SeekUS || p.MaxHeight < 0 || p.MaxHeight > 4320 || p.VideoBitrate < 0 || p.VideoBitrate > 200000000 || p.HDRToSDR && !p.ConvertVideo {
		return nil, false, ErrInvalidConfiguration
	}
	protocols, formats := "http,tcp", "mpegts"
	if p.HLSInput {
		protocols, formats = "http,tcp,crypto", "hls,mpegts,mov,aac,mp3"
	} else if p.Finite {
		formats = "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "2", "-protocol_whitelist", protocols, "-format_whitelist", formats, "-rw_timeout", "15000000", "-probesize", "8388608", "-analyzeduration", "5000000", "-copyts", "-start_at_zero"}
	if p.HLSInput {
		args = append(args, "-allowed_extensions", "ALL")
	}
	seconds := func(n int64) string { return strconv.FormatFloat(float64(n)/1e6, 'f', 6, 64) }
	if p.Finite {
		args = append(args, "-readrate", "1", "-readrate_initial_burst", "12", "-ss", seconds(p.SeekUS))
	}
	// Copy graphs may strip or rewrite Dolby Vision packets. They need the
	// same target/toolchain checks as a graph that converts picture or sound.
	graph, err := BuildConversion(ConversionRequest{Target: p.Target, ConvertVideo: p.ConvertVideo, ConvertAudio: p.ConvertAudio, CopyVideo: p.Video && !p.ConvertVideo, CopyAudio: p.Audio && !p.ConvertAudio, ToneMap: p.HDRToSDR, Deinterlace: p.Deinterlace, MaxHeight: p.MaxHeight, MaxFrameRate: p.MaxFrameRate, VideoBitrateBPS: p.VideoBitrate, KeyframeSeconds: 4, Toolchain: p.Toolchain})
	if err != nil {
		return nil, false, err
	}
	hardware := HardwareStagesRequireDevice(graph.Stages)
	args = append(args, graph.Input...)
	args = append(args, "-i", input)
	// With copyts/start_at_zero, the stop coordinate is on the preserved source
	// clock, not the duration since this worker's mid-programme join.
	if p.Finite {
		args = append(args, "-t", seconds(p.ClipEndUS))
	}
	if p.Video {
		args = append(args, "-map", "0:v:0")
	}
	if p.Audio {
		args = append(args, "-map", "0:a:0")
	}
	args = append(args, graph.Video...)
	if p.ConvertVideo {
		args = append(args, "-force_key_frames", "expr:if(isnan(prev_forced_t),1,gte(t,prev_forced_t+4))")
		if p.Profile != "" {
			args = append(args, "-profile:v", p.Profile)
		}
		if p.Target == nil && p.Level > 0 {
			args = append(args, "-level:v", strconv.FormatFloat(float64(p.Level)/10, 'f', 1, 64))
		}

	}
	args = append(args, graph.Audio...)
	if p.Fragmented {
		if p.Audio && p.CopyAAC && !p.ConvertAudio {
			args = append(args, "-bsf:a", "aac_adtstoasc")
		}
		videoCodec, copiedAudioCodec, profile := "", "", 0
		if p.Video {
			if p.Target != nil {
				videoCodec = p.Target.VideoCodec
				if !p.ConvertVideo {
					profile, _ = p.Target.DeliveredDolbyVision()
				}
			} else if p.CopyHEVC && !p.ConvertVideo {
				videoCodec, profile = "hevc", p.CopyDolbyVisionProfile
			}
		}
		if p.Audio && !p.ConvertAudio {
			copiedAudioCodec = p.AudioCodec
			if p.Target != nil {
				copiedAudioCodec = p.Target.AudioCodec
			}
		}
		args = append(args, HLSMuxArgs(true, videoCodec, copiedAudioCodec, profile)...)
		args = append(args, "-sn", "-dn", "-avoid_negative_ts", "disabled", "-muxdelay", "0", "-f", "hls", "-hls_time", "4", "-hls_list_size", "12", "-hls_flags", "independent_segments+program_date_time", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4", "-method", "PUT", "-rw_timeout", "15000000", "-hls_segment_filename", output+"segment-%09d.m4s", output+"index.m3u8")
		return args, hardware, nil
	}
	args = append(args, "-sn", "-dn", "-avoid_negative_ts", "disabled", "-muxdelay", "0", "-f", "hls", "-hls_time", "4", "-hls_list_size", "12", "-hls_flags", "independent_segments+program_date_time", "-hls_segment_options", "mpegts_copyts=1", "-method", "PUT", "-rw_timeout", "15000000", "-hls_segment_filename", output+"segment-%09d.ts", output+"index.m3u8")
	return args, hardware, nil
}

// RunLinearHLS retains the process, private endpoints and optional physical tuner
// lock until actual child exit. PUT output contains only complete muxed media.
func RunLinearHLS(ctx context.Context, supervisor *storage.Supervisor, key, executable, input, output string, p LinearEncoding, reservation *EndpointReservation, physicalLock *os.File, libraries ...string) error {
	defer reservation.Close()
	endpoint, e := linearOutputEndpoint(input, output)
	if e != nil || supervisor == nil || key == "" || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !reservation.matches(endpoint) {
		return ErrInvalidConfiguration
	}
	for _, file := range libraries {
		if !filepath.IsAbs(file) || filepath.Clean(file) != file {
			return ErrInvalidConfiguration
		}
	}
	p.Toolchain = CurrentToolchain()
	args, hardware, e := linearHLSArgs(input, output, p)
	if e != nil {
		return e
	}
	// Only this job's reserved output path may be written through the sandbox.
	written, e := url.Parse(output)
	if e != nil {
		return ErrInvalidConfiguration
	}
	cmd, e := confinedOutputCommand(executable, args, endpoint, written.Path, hardware, libraries...)
	if e != nil {
		return e
	}
	if physicalLock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, physicalLock)
	}
	if !reservation.claim(endpoint, cmd) {
		return ErrReservationInUse
	}
	retired := make(chan struct{})
	e = supervisor.RunOwnedLinear(ctx, "playback:linear:"+key, cmd, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e }, nil, func() { reservation.retire(); close(retired) })
	<-retired
	return e
}
