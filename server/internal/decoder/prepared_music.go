package decoder

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const preparedInputFormats = "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi,aiff,asf,ape,wv,tta,amr,w64,caf,dsf,iff,mpc,mpc8"

// PreparedMusicOutput describes the first audio track actually written by a
// versioned music recipe. It is shared by admission and completed-media checks.
type PreparedMusicOutput struct {
	Codec, Container                  string
	Rate, Depth, Channels, BitrateBPS int
}

func PreparedMusicFormat(r PreparedRecipe) (PreparedMusicOutput, error) {
	var o PreparedMusicOutput
	s := r.Source
	if !r.AudioOnly || s.AudioSampleRate < 8000 || s.AudioSampleRate > 2822400 || len(s.AudioChannels) == 0 || s.AudioChannels[0] < 1 || s.AudioChannels[0] > 8 {
		return o, ErrInvalidConfiguration
	}
	o = PreparedMusicOutput{Codec: r.MusicCodec, Rate: s.AudioSampleRate, Depth: s.AudioBitDepth, Channels: s.AudioChannels[0]}
	switch r.MusicQuality {
	case "original", "cd-quality":
		if o.Codec != "flac" || r.MusicQuality == "original" && strings.HasPrefix(s.AudioCodec, "pcm_f") {
			return o, ErrInvalidConfiguration
		}
		o.Container = "flac"
		if strings.HasPrefix(s.AudioCodec, "dsd_") {
			o.Rate, o.Depth = 176400, 24
		}
		if o.Rate > 655350 {
			return o, ErrInvalidConfiguration
		}
		if o.Depth <= 0 {
			o.Depth = 16
		}
		if o.Depth > 16 && o.Depth <= 24 {
			o.Depth = 24
		}
		if o.Depth > 24 {
			o.Depth = 32
		}
		if o.Depth < 16 {
			o.Depth = 16
		} // FLAC's storage width; no new source precision.
		if r.MusicQuality == "cd-quality" {
			o.Depth = 16
			target := 48000
			if s.AudioSampleRate%11025 == 0 {
				target = 44100
			}
			o.Rate = min(o.Rate, target)
		}
	case "320", "96":
		o.Depth = 0
		o.BitrateBPS, _ = strconv.Atoi(r.MusicQuality)
		o.BitrateBPS *= 1000
		// A compatibility conversion must not assign a higher bitrate to a lossy
		// source. The source-equivalent case normally retains the file unchanged.
		lossless := preparedLossless(s.AudioCodec)
		if s.AudioCodec == "wavpack" && s.AudioLossless != nil {
			lossless = *s.AudioLossless
		}
		if !lossless && !strings.HasPrefix(s.AudioCodec, "dsd_") && s.AudioBitrateBPS > 0 {
			o.BitrateBPS = min(o.BitrateBPS, s.AudioBitrateBPS)
		}
		switch o.Codec {
		case "opus":
			o.Container, o.Rate = "ogg", 48000
			o.BitrateBPS = min(o.BitrateBPS, 256000*o.Channels)
			if o.BitrateBPS < 6000 {
				return o, ErrInvalidConfiguration
			}
		case "aac":
			o.Container = "m4a"
			o.Rate = 0
			for _, n := range []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000} {
				sameFamily := n%11025 == 0
				if n <= s.AudioSampleRate && sameFamily == (s.AudioSampleRate%11025 == 0) {
					o.Rate = n
					break
				}
			}
			if o.Rate == 0 || o.BitrateBPS < 16000 {
				return o, ErrInvalidConfiguration
			}
		default:
			return o, ErrInvalidConfiguration
		}
	default:
		return o, ErrInvalidConfiguration
	}
	if math.IsNaN(r.MusicStart) || math.IsInf(r.MusicStart, 0) || math.IsNaN(r.MusicEnd) || math.IsInf(r.MusicEnd, 0) || r.MusicStart < 0 || r.MusicEnd < 0 || r.MusicEnd > 0 && r.MusicEnd <= r.MusicStart {
		return o, ErrInvalidConfiguration
	}
	return o, nil
}

func preparedLossless(codec string) bool {
	return strings.HasPrefix(codec, "pcm_") || codec == "flac" || codec == "alac" || codec == "ape" || codec == "tta" || codec == "truehd" || codec == "mlp"
}

func preparedMusicArgs(args []string, r PreparedRecipe) ([]string, error) {
	o, e := PreparedMusicFormat(r)
	if e != nil {
		return nil, e
	}
	args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "0", "-map_chapters", "-1")
	filters := []string{}
	if r.MusicStart != 0 || r.MusicEnd != 0 {
		trim := fmt.Sprintf("atrim=start_sample=%d", int64(math.Round(r.MusicStart*float64(r.Source.AudioSampleRate))))
		if r.MusicEnd > 0 {
			trim += fmt.Sprintf(":end_sample=%d", int64(math.Round(r.MusicEnd*float64(r.Source.AudioSampleRate))))
		}
		filters = append(filters, trim, "asetpts=N/SR/TB")
		args = append(args, "-metadata", "CUESHEET=")
	}
	if strings.HasPrefix(r.Source.AudioCodec, "dsd_") {
		filters = append(filters, "volume=-6dB:precision=double")
	}
	filters = append(filters, musicResampleFilters(r.Source.AudioSampleRate, o.Rate, r.Source.AudioBitDepth, o.Depth, strings.HasPrefix(r.Source.AudioCodec, "dsd_"))...)
	if len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}
	args = append(args, "-ac", strconv.Itoa(o.Channels), "-ar", strconv.Itoa(o.Rate))
	switch o.Codec {
	case "flac":
		sf := "s32"
		if o.Depth <= 16 {
			sf = "s16"
		}
		args = append(args, "-c:a", "flac", "-sample_fmt", sf, "-bits_per_raw_sample", strconv.Itoa(o.Depth), "-strict", "-2", "-f", "flac")
	case "opus":
		args = append(args, "-c:a", "libopus", "-b:a", strconv.Itoa(o.BitrateBPS), "-application", "audio", "-f", "ogg")
	case "aac":
		args = append(args, "-c:a", "aac", "-profile:a", "aac_low", "-b:a", strconv.Itoa(o.BitrateBPS), "-frag_duration", "1000000", "-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-f", "mp4")
	}
	return append(args, "pipe:1"), nil
}

// soxr handles rate conversion at double precision. The final equal-rate SWR
// conversion quantizes to the requested used bits and adds triangular dither;
// output_sample_bits is an SWR option, not a soxr quantizer option.
func musicResampleFilters(sourceRate, rate, sourceDepth, depth int, processed bool) []string {
	out := []string{}
	// Zero is an unspecified legacy input rate, not evidence of a rate change.
	// Still establish the output rate before the final output-frame trim. Only
	// measured changes add dither; measured music recipes supply the actual rate.
	rateChanged := sourceRate > 0 && sourceRate != rate
	if sourceRate == 0 || rateChanged {
		out = append(out, fmt.Sprintf("aresample=%d:resampler=soxr:precision=28:osf=dbl", rate))
	}
	if depth > 0 && (rateChanged || processed || sourceDepth > depth) {
		format := "s32"
		if depth <= 16 {
			format = "s16"
		}
		out = append(out, fmt.Sprintf("aresample=%d:resampler=swr:osf=%s:output_sample_bits=%d:dither_method=triangular", rate, format, depth))
		if depth > 16 && depth < 32 {
			// SWR adds TPDF at the requested width but keeps the low storage
			// bits in s32. FLAC discards those bits; round explicitly first so
			// that discard cannot introduce a half-LSB DC bias.
			scale := int64(1) << (depth - 1)
			out = append(out, "aformat=sample_fmts=dbl", fmt.Sprintf("aeval=round(val(ch)*%d)/%d:c=same", scale, scale))
		}
	}
	return out
}
