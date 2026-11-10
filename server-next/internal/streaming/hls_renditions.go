package streaming

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// AudioRenditionPlan describes independently produced audio on the source clock.
// A published but unrequested rendition owns no process or generated segments.
type AudioRenditionPlan struct {
	Failure     string `json:"failure,omitempty"`
	Ordinal     int    `json:"ordinal"`
	StreamIndex int    `json:"streamIndex"`
	Action      string `json:"action"`
	Codec       string `json:"codec"`
	Profile     string `json:"profile,omitempty"`
	// Packaging is derived from the containing delivery plan, including when
	// that plan is loaded. It is not another persisted or public decision.
	Packaging     string `json:"-"`
	Channels      int    `json:"channels"`
	BitrateBPS    int    `json:"bitrateBps"`
	Downmix       string `json:"downmix,omitempty"`
	SampleRate    int    `json:"sampleRate,omitempty"`
	BitDepth      int    `json:"bitDepth,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	Language      string `json:"language"`
	Label         string `json:"label"`
	Title         string `json:"title,omitempty"`
	Default       bool   `json:"default"`
	Commentary    bool   `json:"commentary,omitempty"`
}

func planAudioRenditions(p DeliveryPlan, source DeliverySource, client ClientProfile, target QualityTarget, convert bool) []AudioRenditionPlan {
	if p.Mode != "hls" || p.VideoAction == "none" || len(source.Audio) < 2 {
		return nil
	}
	kind := TransportHLSTS
	if p.OutputContainer == "fmp4_hls" {
		kind = TransportHLSFMP4
	}
	transport := transportNamed(client.Transports, kind)
	if transport == nil {
		transport = &ClientTransport{Transport: TransportHLSTS, Audio: []string{"aac"}}
	}
	out := []AudioRenditionPlan{}
	// Keep the chosen track even when it falls beyond the bounded offer window.
	tracks := source.Audio[:min(16, len(source.Audio))]
	if p.AudioStream >= 0 {
		found := false
		for _, a := range tracks {
			found = found || a.Index == p.AudioStream
		}
		if !found {
			tracks = append([]SourceAudio{}, tracks...)
			for _, a := range source.Audio {
				if a.Index == p.AudioStream {
					tracks[len(tracks)-1] = a
					break
				}
			}
		}
	}
	for _, a := range tracks {
		r := AudioRenditionPlan{
			Ordinal:     len(out),
			StreamIndex: a.Index,
			Action:      "copy",
			Codec:       a.Codec,
			Profile:     a.Profile,
			Channels:    a.Channels,
			BitrateBPS:  int(a.BitRate),
			Language:    manifestAudioLanguage(a.Language),
			Default:     a.Index == p.AudioStream,
			Commentary:  a.Commentary,
			Title: strings.TrimSpace(strings.Map(func(r rune) rune {
				if r < 32 || r == 127 {
					return ' '
				}
				return r
			}, a.Title)),
		}
		r.Packaging = "mpegts_hls"
		if kind == TransportHLSFMP4 {
			r.Packaging = "fmp4_hls"
		}
		if len(audioRejections(&a, client, *transport)) > 0 || !serverCopyAudio(kind, a.Codec) || bitrateOver(a.BitRate, target.MaxAudioBitrateBPS) {
			if !convert {
				continue
			}
			t := audioOutputFor(&a, client, target, transport, currentConversionOutputs())
			if t.NoRate {
				continue
			}
			r.Action, r.Codec, r.Channels, r.BitrateBPS, r.SampleRate = "convert", t.Codec, t.Channels, t.Bitrate, t.SampleRate
			if t.Downmix {
				r.Downmix = DownmixITULimited
			}
		}
		if r.Action == "copy" || r.Channels == a.Channels && (a.Layout == "mono" || a.Layout == "stereo" || a.Layout == "5.1" || a.Layout == "5.1(side)" || a.Layout == "7.1") {
			r.ChannelLayout = a.Layout
		}
		if r.Codec == "flac" {
			r.BitDepth = flacDepth(&a, client.audioCodec(r.Codec))
		}
		r.Label = fmt.Sprintf("%s · %s · track %d", r.Language, strings.ToUpper(r.Codec), a.Ordinal+1)
		if a.Title != "" {
			r.Label = a.Title + " · " + r.Label
		}
		if a.Commentary {
			r.Label += " · Commentary"
		}
		// The API label is also the HLS NAME identity used for track selection.
		r.Label = manifestQuoted(r.Label)
		out = append(out, r)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func audioCodecString(codec, action, profile string) string {
	switch codec {
	case "aac":
		if action == "convert" {
			return "mp4a.40.2"
		}
		switch strings.ToLower(profile) {
		case "lc":
			return "mp4a.40.2"
		case "he-aac":
			return "mp4a.40.5"
		case "he-aacv2":
			return "mp4a.40.29"
		}
		return ""
	case "ac3":
		return "ac-3"
	case "eac3":
		return "ec-3"
	case "mp3":
		return "mp4a.40.34"
	case "opus":
		return "opus"
	case "flac":
		return "fLaC"
	case "dts":
		// DTS variants and muxers differ: even core DTS may be carried as
		// mp4a with an ES object type rather than a dtsc sample entry. Until
		// the exact output init is measured, let the player inspect it.
		return ""
	case "truehd":
		return "mlpa"
	}
	return ""
}

func videoCodecString(p *DeliveryPlan) string {
	switch p.OutputVideoCodec {
	case "h264":
		profile, level := 100, 52
		if p.VideoAction == "convert" && p.ConversionTarget != nil {
			switch p.ConversionTarget.VideoProfile {
			case "main":
				profile = 77
			case "baseline":
				profile = 66
			}
		}
		if p.VideoAction == "copy" && p.Trace != nil && p.Trace.Video != nil {
			v := p.Trace.Video
			if v.Profile == "" || v.Level <= 0 {
				return ""
			}
			if v.Level > 0 {
				level = v.Level
			}
			switch strings.ToLower(v.Profile) {
			case "high":
				profile = 100
			case "baseline", "constrained baseline":
				profile = 66
			case "main":
				profile = 77
			case "high 10":
				profile = 110
			default:
				return ""
			}
		} else {
			w, h := convertedDimensions(*p)
			rate := 0.0
			if p.Trace != nil && p.Trace.Video != nil {
				rate = p.Trace.Video.FrameRate
			}
			if p.MaxFrameRate > 0 {
				rate = min(rate, p.MaxFrameRate)
			}
			level = decoder.H264OutputLevel(w, h, rate)
			if p.ConversionTarget != nil && p.ConversionTarget.VideoLevel > 0 {
				level = p.ConversionTarget.VideoLevel
			}
		}
		// Main/Baseline (and other source profiles) carry constraint flags
		// that vary by encoder. The source facts and target do not capture
		// those bytes; leave CODECS out rather than inventing zero flags.
		if profile != 100 {
			return ""
		}
		return fmt.Sprintf("avc1.%02x00%02x", profile, level)
	case "hevc":
		if entry := hevcSampleEntry(p); entry == "dvh1" || entry == "dvhe" {
			if p.Trace.Video.DolbyVisionLevel <= 0 {
				return ""
			}
			profile, _ := deliveredDolbyVision(p)
			return fmt.Sprintf("%s.%02d.%02d", entry, profile, p.Trace.Video.DolbyVisionLevel)
		}
		level := 120
		if p.VideoAction == "convert" {
			w, h := convertedDimensions(*p)
			rate := 0.0
			if p.Trace != nil && p.Trace.Video != nil {
				rate = p.Trace.Video.FrameRate
			}
			if p.MaxFrameRate > 0 {
				rate = min(rate, p.MaxFrameRate)
			}
			level = decoder.HEVCOutputLevel(w, h, rate)
			if p.ConversionTarget != nil && p.ConversionTarget.VideoLevel > 0 {
				level = p.ConversionTarget.VideoLevel
			}
		}
		main10 := false
		if p.VideoAction == "convert" && p.ConversionTarget != nil {
			main10 = p.ConversionTarget.BitDepth == 10
		}
		if p.VideoAction == "copy" && p.Trace != nil && p.Trace.Video != nil {
			v := p.Trace.Video
			main10 = v.BitDepth > 8
			if v.Level > 0 {
				level = v.Level
			}
		}
		if main10 {
			return fmt.Sprintf("hvc1.2.4.L%d.B0", level)
		}
		return fmt.Sprintf("hvc1.1.6.L%d.B0", level)
	case "av1":
		level, depth, profile := 8, 8, 0
		if p.VideoAction == "convert" {
			// The level the encoder will write follows from the picture's size and
			// cadence, as for H.264 and HEVC above.
			w, h := convertedDimensions(*p)
			rate := 0.0
			if p.Trace != nil && p.Trace.Video != nil {
				rate = p.Trace.Video.FrameRate
			}
			if p.MaxFrameRate > 0 {
				rate = min(rate, p.MaxFrameRate)
			}
			level = decoder.AV1OutputLevel(w, h, rate)
			if p.ConversionTarget != nil && p.ConversionTarget.BitDepth == 10 {
				depth = 10
			}
		}
		if p.VideoAction == "copy" && p.Trace != nil && p.Trace.Video != nil {
			v := p.Trace.Video
			if v.Level >= 0 && v.Level <= 31 {
				level = v.Level
			}
			if v.BitDepth == 10 || v.BitDepth == 12 {
				depth = v.BitDepth
			}
			switch strings.ToLower(v.Profile) {
			case "high":
				profile = 1
			case "professional":
				profile = 2
			}
		}
		return fmt.Sprintf("av01.%d.%02dM.%02d", profile, level, depth)
	case "vp9":
		if p.VideoAction != "copy" || p.Trace == nil || p.Trace.Video == nil {
			return ""
		}
		v := p.Trace.Video
		profile := -1
		switch strings.ToLower(strings.TrimSpace(v.Profile)) {
		case "profile 0":
			profile = 0
		case "profile 1":
			profile = 1
		case "profile 2":
			profile = 2
		case "profile 3":
			profile = 3
		}
		if profile < 0 || (profile < 2 && v.BitDepth != 8) || (profile >= 2 && v.BitDepth != 10 && v.BitDepth != 12) {
			return ""
		}
		switch v.Level {
		case 10, 11, 20, 21, 30, 31, 40, 41, 50, 51, 52, 60, 61, 62:
			return fmt.Sprintf("vp09.%02d.%02d.%02d", profile, v.Level, v.BitDepth)
		}
		return ""
	}
	return ""
}

func manifestQuoted(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func manifestTextRenditions(p *DeliveryPlan) []TextRenditionPlan {
	if !p.AirPlay {
		return p.TextRenditions
	}
	selected := []TextRenditionPlan{}
	for _, track := range p.TextRenditions {
		if track.Selected {
			selected = append(selected, track)
		}
	}
	return selected
}

func hlsMasterManifest(p *DeliveryPlan) []byte {
	var b strings.Builder
	version := 4
	if p.OutputContainer == "fmp4_hls" {
		version = 7
	}
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-INDEPENDENT-SEGMENTS\n", version)
	for i, r := range manifestTextRenditions(p) {
		def, auto, forced := "NO", "NO", "NO"
		if r.Selected {
			def, auto = "YES", "YES"
		}
		if r.Forced {
			forced = "YES"
		}
		characteristics := ""
		if r.HearingImpaired {
			characteristics = ",CHARACTERISTICS=\"public.accessibility.transcribes-spoken-dialog,public.accessibility.describes-music-and-sound\""
		}
		fmt.Fprintf(
			&b,
			"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subtitles\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,FORCED=%s%s,URI=\"text-%d.m3u8\"\n",
			manifestQuoted(r.Label),
			manifestAudioLanguage(r.Language),
			def,
			auto,
			forced,
			characteristics,
			i,
		)
	}
	videoCodec := videoCodecString(p)
	codecs := []string{}
	completeCodecs := p.VideoAction == "none" || videoCodec != ""
	if p.VideoAction != "none" {
		codecs = append(codecs, videoCodec)
	}
	audioRate := p.AudioBitrateBPS
	for _, r := range p.Renditions {
		def, auto := "NO", "YES"
		if r.Default {
			def = "YES"
		}
		if r.Commentary {
			auto = "NO"
		}
		fmt.Fprintf(
			&b,
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,CHANNELS=\"%d\",URI=\"audio-%d.m3u8\"\n",
			manifestQuoted(r.Label),
			r.Language,
			def,
			auto,
			max(1, r.Channels),
			r.Ordinal,
		)
		c := audioCodecString(r.Codec, r.Action, r.Profile)
		if c == "" {
			completeCodecs = false
		}
		if c != "" && !containsString(codecs, c) {
			codecs = append(codecs, c)
		}
		audioRate = max(audioRate, r.BitrateBPS)
	}
	if len(p.Renditions) == 0 && p.AudioAction != "none" {
		profile := ""
		if p.Trace != nil && p.Trace.Audio != nil {
			profile = p.Trace.Audio.Profile
		}
		c := audioCodecString(p.OutputAudioCodec, p.AudioAction, profile)
		if c == "" {
			completeCodecs = false
		}
		if c != "" {
			codecs = append(codecs, c)
		}
	}
	videoRate := p.VideoBitrateBPS
	if videoRate == 0 && p.Trace != nil {
		videoRate = int(p.Trace.BitRate)
	}
	if videoRate <= 0 {
		videoRate = 8_000_000
	}
	peak := videoRate + max(audioRate, 192000)
	average := peak
	if p.VideoAction == "convert" && p.ConversionTarget != nil && p.ConversionTarget.RateControl == "quality" && p.ConversionTarget.VideoBitrateBPS == 0 {
		peak = 200_000_000 + max(audioRate, 192000)
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d", peak, average)
	if completeCodecs && len(codecs) > 0 {
		fmt.Fprintf(&b, ",CODECS=\"%s\"", strings.Join(codecs, ","))
	}
	profile, compatibility := deliveredDolbyVision(p)
	if hevcSampleEntry(p) == "dvh1" && profile == 8 {
		brand := ""
		switch compatibility {
		case 1:
			brand = "db1p"
		case 4:
			brand = "db4h"
		}
		if brand != "" && videoCodecString(p) != "" {
			fmt.Fprintf(&b, ",SUPPLEMENTAL-CODECS=\"%s/%s\"", videoCodecString(p), brand)
		}
	}
	if len(p.Renditions) > 0 {
		b.WriteString(",AUDIO=\"audio\"")
	}
	if len(manifestTextRenditions(p)) > 0 {
		b.WriteString(",SUBTITLES=\"subtitles\"")
	}
	if p.Trace != nil && p.Trace.Video != nil {
		v := p.Trace.Video
		w, h := v.Width, v.Height
		if p.VideoAction == "convert" {
			w, h = convertedDimensions(*p)
		}
		if w > 0 && h > 0 {
			fmt.Fprintf(&b, ",RESOLUTION=%dx%d", w, h)
		}
		rate := v.FrameRate
		if p.MaxFrameRate > 0 {
			rate = min(rate, p.MaxFrameRate)
		}
		if rate > 0 {
			fmt.Fprintf(&b, ",FRAME-RATE=%.3f", rate)
		}
		dr := "SDR"
		if p.VideoAction == "convert" && p.ConversionTarget != nil {
			switch p.ConversionTarget.DynamicRange {
			case "hdr10":
				dr = "PQ"
			case "hlg":
				dr = "HLG"
			}
		}
		if !p.ToneMap && p.VideoAction == "copy" {
			dynamicRange := v.OutputRange
			if dynamicRange == "" {
				dynamicRange = v.DynamicRange
			}
			switch dynamicRange {
			case mediafacts.RangeHDR10, "hdr10_plus":
				dr = "PQ"
			case mediafacts.RangeDolbyVision:
				// Dolby Vision 8.4 sits on an HLG base picture; Apple's
				// authoring specification names its range as HLG (VS-1d).
				dr = "PQ"
				if profile, compatibility := deliveredDolbyVision(p); profile == 8 && compatibility == 4 {
					dr = "HLG"
				}
			case mediafacts.RangeHLG:
				dr = "HLG"
			}
		}
		fmt.Fprintf(&b, ",VIDEO-RANGE=%s", dr)
	}
	if p.VideoAction != "none" && p.ConversionTarget != nil && (p.Trace == nil || p.Trace.Video == nil) {
		dr := "SDR"
		switch p.ConversionTarget.DynamicRange {
		case "hdr10":
			dr = "PQ"
		case "hlg":
			dr = "HLG"
		}
		fmt.Fprintf(&b, ",VIDEO-RANGE=%s", dr)
		if p.TargetWidth > 0 && p.TargetHeight > 0 {
			fmt.Fprintf(&b, ",RESOLUTION=%dx%d", p.TargetWidth, p.TargetHeight)
		}
	}
	b.WriteString("\nvideo.m3u8\n")
	return []byte(b.String())
}

func writeNamedManifest(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(data) {
		return nil
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func publishRenditionManifests(dir string, p *DeliveryPlan, video []byte) error {
	if p == nil || (len(p.Renditions) == 0 && len(p.TextRenditions) == 0 && p.OutputContainer != "fmp4_hls") {
		return writeManifest(dir, video)
	}
	if err := writeNamedManifest(dir, "video.m3u8", video); err != nil {
		return err
	}
	for _, r := range p.Renditions {
		media := strings.ReplaceAll(string(hlsTimelineManifest(p.Duration)), "segment-", fmt.Sprintf("audio-%d-", r.Ordinal))
		if renditionPackaging(r) == "fmp4_hls" {
			media = strings.ReplaceAll(string(fragmentedManifest([]byte(media))), "init.mp4", renditionInitFile(r.Ordinal))
		}
		if err := writeNamedManifest(dir, fmt.Sprintf("audio-%d.m3u8", r.Ordinal), []byte(media)); err != nil {
			return err
		}
	}
	if err := publishTextManifests(dir, p); err != nil {
		return err
	}
	return writeManifest(dir, hlsMasterManifest(p))
}

func renditionKey(id string, ordinal int) string { return id + "#a" + strconv.Itoa(ordinal) }
func producerSession(key string) string          { id, _, _ := strings.Cut(key, "#a"); return id }

var renditionSegmentName = regexp.MustCompile(`^audio-([0-9]{1,2})-([0-9]{6})\.(?:ts|m4s)$`)

func renditionArtifactDeclared(name string, p *DeliveryPlan) bool {
	if p == nil {
		return false
	}
	for ordinal, r := range p.Renditions {
		if r.Ordinal != ordinal {
			continue
		}
		if name == fmt.Sprintf("audio-%d.m3u8", r.Ordinal) || renditionPackaging(r) == "fmp4_hls" && name == renditionInitFile(r.Ordinal) {
			return true
		}
	}
	parts := renditionSegmentName.FindStringSubmatch(name)
	if parts == nil {
		return false
	}
	ordinal, _ := strconv.Atoi(parts[1])
	index, _ := strconv.Atoi(parts[2])
	return ordinal < len(p.Renditions) && index < hlsSegmentCount(p.Duration) && name == renditionSegmentFile(p.Renditions[ordinal], index)
}

func hevcSampleEntry(p *DeliveryPlan) string {
	profile := 0
	if p != nil && p.OutputVideoCodec == "hevc" && p.VideoAction == "copy" {
		profile, _ = deliveredDolbyVision(p)
	}
	return decoder.HEVCSampleEntry(profile)
}

var ErrAudioRenditionUnavailable = errors.New("This audio track is unavailable for this playback. Choose another audio track.")
