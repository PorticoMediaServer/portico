// Package audiofacts measures what a client-side audio decoder needs and can't
// know on its own (spec §18.1 version 2, §18.7): the exact frame count and the
// gapless trim, relative to the codec's raw decoded output, and the codec
// configuration a packet decoder needs, and for MP3, ADTS and Ogg a seek index
// (seekindex.go). Measurement is one confined ffprobe pass
// that decodes with skipping off (every frame of every packet) and reads the
// packets' skip side data, which is how FFmpeg surfaces the LAME header, MP4 edit
// lists and Opus pre-skip; iTunSMPB, which FFmpeg reads only in part (never the
// end padding), is applied here. The package runs nothing itself: the caller
// supplies a confined runner (subtitlevideo.Runtime.MeasureAudio).
package audiofacts

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Facts are one audio asset's measurement.
type Facts struct {
	Container, Codec          string
	Profile                   string
	BitRate                   int64
	SampleRate, Channels      int
	BitDepth                  int
	RawFrames, DurationFrames int64
	StartFrames, EndFrames    int64
	TrimSource                string
	// DecoderConfig is the codec extradata as FFmpeg reports it, base64: AAC's
	// AudioSpecificConfig, Opus's OpusHead, FLAC's 34-byte STREAMINFO body, the
	// ALAC magic cookie. Empty when the codec has none (MP3, PCM).
	DecoderConfig string
	// SeekIndex is the encoded seek index of an MP3, ADTS or Ogg file
	// (EncodeSeekIndex), made with the measurement; nil when there is none.
	SeekIndex []byte
	// HasSeekIndex: a fresh index is stored (Read sets it; the index itself
	// is read only when a client asks for it).
	HasSeekIndex bool
}

var (
	ErrNoAudio      = errors.New("audiofacts: no audio stream")
	ErrInconsistent = errors.New("audiofacts: the measurement is inconsistent")
	ErrTooLong      = errors.New("audiofacts: the output exceeded its budget")
	// ErrSourceChanged: the file on disk no longer matches the scanned asset
	// (size or modified time); the next scan records it and it's measured again.
	ErrSourceChanged = errors.New("audiofacts: the file changed since it was scanned")
)

// Formats FFmpeg may open for a measurement; everything else is refused.
const formatWhitelist = "mov,mp3,flac,ogg,wav,aac,aiff,matroska,asf,ape,wv,tta,w64,caf,amr,dsf,iff,mpc,mpc8"

// Args are ffprobe's arguments for input (a private bridge URL). One pass: the
// audio stream's parameters and extradata, the container name and iTunSMPB, each
// packet's duration and skip side data, and each decoded frame's sample count
// with skipping off (skip_manual). Packet data is never printed.
func Args(input string) []string {
	return []string{"-v", "error", "-threads", "1", "-protocol_whitelist", "http,tcp", "-format_whitelist", formatWhitelist,
		"-probesize", "8388608", "-analyzeduration", "10000000", "-flags2", "+skip_manual", "-select_streams", "a:0",
		"-show_data", "-show_streams", "-show_format", "-show_packets", "-show_frames",
		"-show_entries", "stream=codec_name,profile,bit_rate,sample_rate,channels,duration_ts,time_base,bits_per_raw_sample,bits_per_sample,extradata:stream_tags=iTunSMPB:format=format_name:format_tags=iTunSMPB:packet=duration,size:packet_side_data=skip_samples,discard_padding:frame=nb_samples",
		"-of", "compact", input}
}

// maxLine bounds one output line (a stream line carries the extradata dump).
const maxLine = 1 << 20

// Parse reads Args' output. It streams: memory is O(1) in the file's length.
func Parse(r io.Reader) (Facts, error) {
	var f Facts
	var itun string
	var packets, frames, packetBytes int64
	var firstSkip, lastDiscard int64
	var sawStream bool
	var durationTicks int64
	var streamTimeBase string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		line := sc.Text()
		kind, rest, _ := strings.Cut(line, "|")
		fields := compactFields(rest)
		switch kind {
		case "packet":
			packets++
			if n, e := strconv.ParseInt(fields["size"], 10, 64); e == nil && n > 0 && n < 1<<30 {
				packetBytes += n
			}
			discard := int64(0)
			if v, ok := fields["side_datum/skip_samples:skip_samples"]; ok && packets == 1 {
				firstSkip, _ = strconv.ParseInt(v, 10, 64)
			}
			if v, ok := fields["side_datum/skip_samples:discard_padding"]; ok {
				discard, _ = strconv.ParseInt(v, 10, 64)
			}
			if discard < 0 || discard > 1<<20 {
				return f, ErrInconsistent
			}
			lastDiscard += discard
		case "frame":
			n, err := strconv.ParseInt(fields["nb_samples"], 10, 64)
			if err != nil || n < 0 || n > 1<<20 {
				return f, ErrInconsistent
			}
			frames += n
		case "stream":
			if sawStream {
				continue
			}
			sawStream = true
			f.Codec = fields["codec_name"]
			switch f.Codec {
			case "musepack7":
				f.Codec = "mpc7"
			case "musepack8":
				f.Codec = "mpc8"
			}
			durationTicks, _ = strconv.ParseInt(fields["duration_ts"], 10, 64)
			streamTimeBase = fields["time_base"]
			f.Profile = fields["profile"]
			f.BitRate, _ = strconv.ParseInt(fields["bit_rate"], 10, 64)
			f.SampleRate, _ = strconv.Atoi(fields["sample_rate"])
			f.Channels, _ = strconv.Atoi(fields["channels"])
			if n, err := strconv.Atoi(fields["bits_per_raw_sample"]); err == nil && n > 0 && n <= 64 {
				f.BitDepth = n
			} else if n, err := strconv.Atoi(fields["bits_per_sample"]); err == nil && n > 0 && n <= 64 {
				f.BitDepth = n
			}
			if data := extradata(fields["extradata"]); len(data) > 0 {
				f.DecoderConfig = base64.StdEncoding.EncodeToString(data)
			}
			if v := fields["tag:iTunSMPB"]; v != "" {
				itun = v
			}
		case "format":
			f.Container = containerOf(fields["format_name"], f.Codec)
			if v := fields["tag:iTunSMPB"]; v != "" {
				itun = v
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return f, ErrTooLong
		}
		return f, err
	}
	if !sawStream || f.Codec == "" || f.SampleRate <= 0 || f.Channels <= 0 {
		return f, ErrNoAudio
	}
	if f.Container == "" {
		f.Container = containerOf("", f.Codec)
	}
	f.RawFrames = frames
	if frames <= 0 || firstSkip < 0 || lastDiscard < 0 {
		return f, ErrInconsistent
	}
	f.StartFrames, f.EndFrames = firstSkip, lastDiscard
	f.DurationFrames = frames - firstSkip - lastDiscard
	f.TrimSource = sourceOf(f)
	if f.Codec == "mpc7" || f.Codec == "mpc8" {
		f.TrimSource = "mpc-header-pending"
	}
	// MP4's sample table describes the audible end even when the final AAC
	// packet contains unused samples and no iTunSMPB tag was written.
	if f.Container == "mp4" && f.Codec == "aac" && durationTicks > 0 && durationTicks < 1<<45 {
		numText, denText, ok := strings.Cut(streamTimeBase, "/")
		num, e1 := strconv.ParseInt(numText, 10, 64)
		den, e2 := strconv.ParseInt(denText, 10, 64)
		if ok && e1 == nil && e2 == nil && num > 0 && den > 0 {
			duration := int64(math.Round(float64(durationTicks) * float64(num) * float64(f.SampleRate) / float64(den)))
			padding := frames - firstSkip - duration
			if duration > 0 && padding >= lastDiscard && padding <= 4096 {
				f.DurationFrames = duration
				f.EndFrames = padding
			}
		}
	}

	// iTunSMPB (Apple's encoders, and most AAC/ALAC in MP4): delay, padding and the
	// exact total. It wins when it fits the decoded stream; FFmpeg applies its
	// delay (as an edit list) but never its end padding.
	if delay, total, ok := parseITunSMPB(itun); ok {
		if delay+total <= frames {
			f.StartFrames, f.DurationFrames, f.EndFrames, f.TrimSource = delay, total, frames-delay-total, "itunsmpb"
		} else {
			f.TrimSource = "measured"
		}
	}
	if f.DurationFrames <= 0 || f.StartFrames+f.DurationFrames+f.EndFrames != f.RawFrames {
		return f, ErrInconsistent
	}
	if packetBytes > 0 {
		f.BitRate = int64(float64(packetBytes) * 8 * float64(f.SampleRate) / float64(f.DurationFrames))
	}
	if f.BitRate < 0 || f.BitRate > 1<<40 {
		f.BitRate = 0
	}
	return f, nil
}

func sourceOf(f Facts) string {
	switch {
	case strings.HasPrefix(f.Codec, "pcm_"):
		return "none"
	case f.Codec == "flac":
		return "flac"
	case f.Codec == "opus":
		return "opus-preskip"
	case f.Codec == "mp3" && f.StartFrames > 0:
		return "lame"
	case f.Container == "mp4" && f.StartFrames > 0:
		return "mp4-editlist"
	case f.StartFrames == 0 && f.EndFrames == 0:
		return "none"
	}
	return "measured"
}

var itunPattern = regexp.MustCompile(`^\s*[0-9A-Fa-f]{8} ([0-9A-Fa-f]{8}) ([0-9A-Fa-f]{8}) ([0-9A-Fa-f]{16})`)

// parseITunSMPB reads " 00000000 DDDDDDDD PPPPPPPP TTTTTTTTTTTTTTTT …": the
// encoder delay, the end padding and the exact total, in frames.
func parseITunSMPB(v string) (delay, total int64, ok bool) {
	m := itunPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	d, e1 := strconv.ParseInt(m[1], 16, 64)
	t, e2 := strconv.ParseInt(m[3], 16, 64)
	if e1 != nil || e2 != nil || d < 0 || t <= 0 {
		return 0, 0, false
	}
	return d, t, true
}

// containerOf names the container the way the plan does (spec §18.1).
func containerOf(format, codec string) string {
	first, _, _ := strings.Cut(format, ",")
	switch {
	case strings.Contains(format, "mp4") || first == "mov":
		return "mp4"
	case first == "aac":
		return "adts"
	case first == "matroska":
		return "mka"
	case first == "":
		switch codec {
		case "mp3":
			return "mp3"
		case "flac":
			return "flac"
		}
		return ""
	}
	return first
}

// compactFields splits ffprobe's compact line ("k=v|k=v"), unescaping \\, \n, \|.
func compactFields(s string) map[string]string {
	out := map[string]string{}
	var key, cur strings.Builder
	inValue := false
	flush := func() {
		if key.Len() > 0 {
			out[key.String()] = cur.String()
		}
		key.Reset()
		cur.Reset()
		inValue = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			default:
				c = s[i]
			}
			if inValue {
				cur.WriteByte(c)
			} else {
				key.WriteByte(c)
			}
		case c == '|':
			flush()
		case c == '=' && !inValue:
			inValue = true
		default:
			if inValue {
				cur.WriteByte(c)
			} else {
				key.WriteByte(c)
			}
		}
	}
	flush()
	return out
}

// extradata reads ffprobe's hex dump ("00000000: 1210 56e5  ..V.").
func extradata(dump string) []byte {
	var out []byte
	for _, line := range strings.Split(dump, "\n") {
		_, rest, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		// The hex columns end at a double space before the ASCII rendering.
		hexPart := rest
		if i := strings.Index(rest, "  "); i >= 0 {
			hexPart = rest[:i]
		}
		b, err := hex.DecodeString(strings.ReplaceAll(hexPart, " ", ""))
		if err != nil {
			return nil
		}
		out = append(out, b...)
	}
	return out
}

func (f Facts) String() string {
	return fmt.Sprintf("%s/%s %d Hz %d ch: %d frames (raw %d, trim %d+%d, %s)", f.Container, f.Codec, f.SampleRate, f.Channels, f.DurationFrames, f.RawFrames, f.StartFrames, f.EndFrames, f.TrimSource)
}
