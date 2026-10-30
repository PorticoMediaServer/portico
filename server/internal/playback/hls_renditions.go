package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
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
		r := AudioRenditionPlan{Ordinal: len(out), StreamIndex: a.Index, Action: "copy", Codec: a.Codec, Profile: a.Profile, Channels: a.Channels, BitrateBPS: int(a.BitRate), Language: manifestAudioLanguage(a.Language), Default: a.Index == p.AudioStream, Commentary: a.Commentary, Title: strings.TrimSpace(strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, a.Title))}
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
		if p.VideoAction == "convert" {
			return ""
		}
		level, depth, profile := 8, 8, 0
		if p.VideoAction == "convert" && p.ConversionTarget != nil {
			depth = p.ConversionTarget.BitDepth
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
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subtitles\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,FORCED=%s%s,URI=\"text-%d.m3u8\"\n", manifestQuoted(r.Label), manifestAudioLanguage(r.Language), def, auto, forced, characteristics, i)
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
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,CHANNELS=\"%d\",URI=\"audio-%d.m3u8\"\n", manifestQuoted(r.Label), r.Language, def, auto, max(1, r.Channels), r.Ordinal)
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
			case assets.RangeHDR10, "hdr10_plus":
				dr = "PQ"
			case assets.RangeDolbyVision:
				// Dolby Vision 8.4 sits on an HLG base picture; Apple's
				// authoring specification names its range as HLG (VS-1d).
				dr = "PQ"
				if profile, compatibility := deliveredDolbyVision(p); profile == 8 && compatibility == 4 {
					dr = "HLG"
				}
			case assets.RangeHLG:
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
func (h *HLS) sessionActiveLocked(id string) bool {
	for key := range h.active {
		if producerSession(key) == id {
			return true
		}
	}
	return false
}

var renditionSegmentName = regexp.MustCompile(`^audio-([0-9]{1,2})-([0-9]{6})\.(?:ts|m4s)$`)

// startRendition uses the same process and restart budgets as video. Only an
// explicitly requested track is admitted; all windows stop with their session.
func (h *HLS) startRendition(ctx context.Context, id string, r AudioRenditionPlan, from int) error {
	return h.startRenditionOwned(ctx, id, r, from, nil)
}

type renditionOwner struct {
	generation           int
	request, asset, plan string
	item                 int64
}

// Freeze both occurrence and containing plan before any relocation await.
// Re-entry compares that original snapshot; it cannot borrow a newer generation.
func (h *HLS) renditionOwner(ctx context.Context, id string, r AudioRenditionPlan) (renditionOwner, error) {
	var out renditionOwner
	err := dbwork.WithReadSnapshot(ctx, h.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		if err := tx.QueryRowContext(ctx, `SELECT generation,request_id,item_id,asset_id FROM playback_sessions WHERE id=? AND state NOT IN('stopped','ended','failed') AND expires_at>?`, id, time.Now().UTC().Format(time.RFC3339)).Scan(&out.generation, &out.request, &out.item, &out.asset); err != nil {
			return err
		}
		plan, err := loadDeliveryPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if plan == nil || r.Ordinal < 0 || r.Ordinal >= len(plan.Renditions) || plan.Renditions[r.Ordinal] != r {
			return ErrOccurrenceChanged
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		out.plan = string(raw)
		return nil
	})
	return out, err
}

func (h *HLS) startRenditionOwned(ctx context.Context, id string, r AudioRenditionPlan, from int, expected *renditionOwner) error {
	if r.Failure != "" {
		return ErrAudioRenditionUnavailable
	}
	if dbwork.Snapshot(ctx) != nil {
		return errOccurrenceClaimed
	}
	intent, err := h.occurrences.intent(id)
	if err != nil {
		return err
	}
	defer intent.release()
	key := renditionKey(id, r.Ordinal)
	owner, err := h.renditionOwner(ctx, id, r)
	if err != nil {
		return err
	}
	if expected != nil && owner != *expected {
		return ErrOccurrenceChanged
	}
	generation, requestID, itemID, assetID := owner.generation, owner.request, owner.item, owner.asset
	if !intent.current() {
		return errOccurrenceClaimed
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.moving[id] != nil || h.memoryFull[id] {
		return ErrSegmentPreparing
	}
	if h.cleaning[id] {
		return ErrSegmentPreparing
	}
	if w := h.windows[key]; w != nil {
		if w.generation != generation {
			return errOccurrenceClaimed
		}
		if w.coversDemand(from) {
			return nil
		}
		if w.relocating {
			h.mu.Unlock()
			select {
			case <-w.done:
			case <-ctx.Done():
				h.mu.Lock()
				return ctx.Err()
			}
			err := h.startRenditionOwned(ctx, id, r, from, &owner)
			h.mu.Lock()
			return err
		}
		if !h.restartAllowedLocked(id) {
			return nil
		}
		w.relocating = true
		w.cancel()
		h.mu.Unlock()
		select {
		case <-w.done:
		case <-ctx.Done():
			h.mu.Lock()
			return ctx.Err()
		}
		err := h.startRenditionOwned(ctx, id, r, from, &owner)
		h.mu.Lock()
		return err
	}
	if h.windows[key] != nil {
		return nil
	}
	if !h.roomToProduce() {
		return ErrConversionSpace
	}
	run, cancel := context.WithCancel(h.ctx)
	w := &hlsWindow{generation: generation, audioConversion: r.Action == "convert", start: from, served: -1, produced: from - 1, cancel: cancel, done: make(chan struct{})}
	owned := &occurrenceRun{generation: generation, requestID: requestID, itemID: itemID, assetID: assetID, cancel: cancel, done: w.done}
	w.run = owned
	if err = intent.install(owned, func() { h.installOccurrenceLocked(key, owned, cancel); h.windows[key] = w }); err != nil {
		cancel()
		return err
	}
	h.noteSessionServedLocked(id)
	h.producers.Add(1)
	supervise.Go("playback.hls.audio-rendition", func() {
		defer h.producers.Done()
		defer func() {
			h.mu.Lock()
			if h.windows[key] == w {
				h.removeOccurrenceLocked(key, owned)
				delete(h.windows, key)
			}
			close(w.done)
			h.mu.Unlock()
			h.occurrences.finished(id, intent.entry, owned)
		}()
		h.produceRendition(h.occurrenceContext(run, id, intent.entry, owned), id, r, from, w, generation)
	})
	return nil
}

func (h *HLS) produceRendition(ctx context.Context, id string, r AudioRenditionPlan, from int, w *hlsWindow, generation int) {
	ctx = withProducerGeneration(ctx, generation)
	failed := func(code string) {
		if code == FailureOutOfSpace && h.retryMemoryOutput(id) {
			return
		}
		// Relocation and shutdown cancel the old window, including its plan
		// load. They must not publish a source failure into the shared plan.
		if ctx.Err() != nil {
			return
		}
		if r.Default {
			h.failedWith(ctx, id, code, "")
		} else {
			h.renditionFailed(ctx, id, generation, r, code)
		}
	}
	plan, err := loadDeliveryPlan(ctx, h.db, id)
	if err != nil {
		failed(FailureSourceUnreadable)
		return
	}
	dir, err := h.createStreamingOutput(ctx, id, plan)
	if err != nil {
		failed(FailureOutOfSpace)
		return
	}
	input, remoteInput, err := h.openWindowInput(ctx, id, plan)
	if err != nil {
		if ctx.Err() == nil {
			failed(FailureSourceUnreadable)
		}
		return
	}
	defer input.Close()
	ctx, releaseInput := input.BorrowContext(ctx)
	defer releaseInput()
	if remoteInput != nil {
		defer remoteInput.Close()
	}
	offset := strconv.Itoa(from * HLSSegmentSeconds)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe"}
	readerOptions, readerURL, residual := input.Options, input.Argument, int64(0)
	if input.InputAt != nil {
		var at int64
		readerOptions, readerURL, at, err = input.InputAt(ctx, int64(from*HLSSegmentSeconds*1000))
		if err != nil {
			failed(FailureConverter)
			return
		}
		residual = int64(from*HLSSegmentSeconds*1000) - at
	} else {
		args = append(args, "-ss", offset)
	}
	args = append(args, readerOptions...)
	args = append(args, "-i", readerURL, "-ss", strconv.FormatFloat(float64(residual)/1000, 'f', 3, 64), "-map", "0:"+strconv.Itoa(r.StreamIndex), "-vn", "-sn", "-dn")
	audioTarget := decoder.ConversionTarget{AudioCodec: r.Codec, AudioChannels: r.Channels, AudioBitrateBPS: r.BitrateBPS, AudioSampleRate: r.SampleRate, AudioChannelLayout: r.ChannelLayout, AudioBitDepth: r.BitDepth, Packaging: renditionPackaging(r)}
	graph, err := decoder.BuildConversion(decoder.ConversionRequest{Target: &audioTarget, Toolchain: decoder.CurrentToolchain(), CopyAudio: r.Action == "copy", ConvertAudio: r.Action == "convert", AudioCodec: r.Codec, AudioChannels: r.Channels, AudioBitrateBPS: r.BitrateBPS, Downmix: r.Downmix == DownmixITULimited, AudioSampleRate: r.SampleRate})
	if err != nil {
		failed(FailureConverter)
		return
	}
	args = append(args, graph.Audio...)
	fragmented := renditionPackaging(r) == "fmp4_hls"
	initWindow := fmt.Sprintf("audio-%d-init-%d.mp4", r.Ordinal, from)
	outputDir := dir
	clock := &convertedFragmentClock{}
	if fragmented || input.InputAt != nil {
		outputDir = filepath.Join(dir, fmt.Sprintf("audio-window-%d-%d", r.Ordinal, from))
		_ = os.RemoveAll(outputDir)
		if err := os.MkdirAll(outputDir, 0700); err != nil {
			failed(FailureOutOfSpace)
			return
		}
		defer os.RemoveAll(outputDir)
	}

	if fragmented {
		outputArgs, _, err := decoder.HLSPackagingArgs("fmp4_hls", initWindow)
		if err != nil {
			failed(FailureConverter)
			return
		}
		args = append(args, outputArgs...)
		args = append(args, "-strict", "-2")
	}
	args = append(args, "-max_muxing_queue_size", "1024", "-f", "hls", "-hls_time", "6", "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file", "-output_ts_offset", offset, "-muxdelay", "0", "-muxpreload", "0", "-start_number", strconv.Itoa(from), "-hls_segment_filename", filepath.Join(outputDir, strings.ReplaceAll(renditionSegmentFile(r, 0), "000000", "%06d")), filepath.Join(outputDir, fmt.Sprintf("audio-%d-window.m3u8", r.Ordinal)))
	run, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	watchDone := make(chan struct{})
	supervise.Go("playback.hls.watch-audio", func() {
		defer close(watchDone)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
			}
			keep := func() bool {
				ownedIO, err := childprocess.OwnedIOContext(ctx)
				if err != nil {
					return false
				}
				if input.Validate != nil {
					if err := input.Validate(ownedIO); err != nil {
						cancel()
						failed(FailureConverter)
						return false
					}
				}
				h.mu.Lock()
				produced := w.produced
				h.mu.Unlock()
				for produced+1 < hlsSegmentCount(plan.Duration) && segmentReady(filepath.Join(dir, renditionSegmentFile(r, produced+1))) {
					produced++
				}
				if input.InputAt != nil && !fragmented {
					for next := produced + 1; next < hlsSegmentCount(plan.Duration) && segmentReady(filepath.Join(outputDir, renditionSegmentFile(r, next))); next++ {
						if err := os.Rename(filepath.Join(outputDir, renditionSegmentFile(r, next)), filepath.Join(dir, renditionSegmentFile(r, next))); err != nil {
							cancel()
							failed(FailureConverter)
							return false
						}
						produced = next
					}
				}
				if fragmented {
					var publishErr error
					produced, publishErr = promoteConvertedFragments(outputDir, dir, initWindow, from, &r, clock)
					if publishErr != nil {
						cancel()
						failed(FailureConverter)
						return false
					}
				}

				h.mu.Lock()
				w.produced = produced
				served := max(w.served, w.start-1)
				h.mu.Unlock()
				limit := max(12, h.configuration().ThrottleBufferSeconds)
				edge := served + (limit+5)/6
				if segmentReady(filepath.Join(dir, renditionSegmentFile(r, edge))) {
					h.mu.Lock()
					w.throttled = true
					h.mu.Unlock()
					cancel()
					return false
				}
				return true
			}()
			if !keep {
				return
			}
		}
	})
	if input.File != nil {
		_, _ = input.File.Seek(0, io.SeekStart)
	}
	tail := &tailWriter{limit: 4096}
	if remoteInput != nil {
		err = remoteInput.Run(run, dir, func(url string) (subtitles.WindowCommand, error) {
			copied := append([]string{}, args...)
			for i := 0; i+1 < len(copied); i++ {
				if copied[i] == "-i" {
					copied[i+1] = url
					break
				}
				if copied[i] == "-protocol_whitelist" {
					copied[i+1] = "http,tcp"
				}
			}
			return subtitles.WindowCommand{Args: copied}, nil
		})
		if err != nil {
			_, _ = tail.Write([]byte(windowConverterDiagnostic(err, "", h.root, h.memoryRoot)))
		}
	} else {
		var progress *vod.ConversionProgressWriter
		if r.Action == "convert" {
			observe, finish := h.observeConversion(ctx, id, generation)
			defer finish()
			args = append([]string{"-progress", "pipe:1", "-stats_period", "1", "-nostats"}, args...)
			progress = vod.NewConversionProgressWriter(observe)
		}
		if input.Validate != nil {
			if err = validateOwnedInput(run, input.Validate); err != nil {
				cancel()
				<-watchDone
				failed(FailureConverter)
				return
			}
		}
		for _, extra := range input.Extra {
			_, _ = extra.Seek(0, io.SeekStart)
		}
		var cmd *exec.Cmd
		if cmd, err = mediaexec.CommandContext(run, mediaexec.Job{Executable: h.binary, Args: args, Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths(), WriteDirs: []string{dir}}); err == nil {
			cmd.WaitDelay = 2 * time.Second
			cmd.Stderr = tail
			if progress != nil {
				cmd.Stdout = progress
			}
			err = childprocess.Run(run, cmd)
		}
	}
	cancel()
	<-watchDone
	if input.Validate != nil {
		if err := validateOwnedInput(ctx, input.Validate); err != nil {
			failed(FailureConverter)
			return
		}
	}
	if err := childprocess.CheckOwnedIO(ctx); err != nil {
		return
	}
	if input.InputAt != nil && !fragmented {
		for next := from; next < hlsSegmentCount(plan.Duration); next++ {
			source := filepath.Join(outputDir, renditionSegmentFile(r, next))
			if !segmentReady(source) {
				continue
			}
			if err := os.Rename(source, filepath.Join(dir, renditionSegmentFile(r, next))); err != nil {
				failed(FailureConverter)
				return
			}
		}
	}
	if fragmented {
		if _, publishErr := promoteConvertedFragments(outputDir, dir, initWindow, from, &r, clock); publishErr != nil {
			failed(FailureConverter)
			return
		}
	}

	h.mu.Lock()
	throttled := w.throttled
	h.mu.Unlock()
	if err != nil && !throttled && ctx.Err() == nil {
		failed(classifyConverterFailure(tail.String()))
	}
}

func (h *HLS) renditionFile(ctx context.Context, id, name string, p *DeliveryPlan) (string, error) {
	path := filepath.Join(h.outputDir(id), name)
	parts := renditionSegmentName.FindStringSubmatch(name)
	if parts == nil {
		if isRenditionInit(name) {
			var ordinal int
			if _, err := fmt.Sscanf(name, "audio-%d-init.mp4", &ordinal); err != nil || ordinal < 0 || ordinal >= len(p.Renditions) || renditionPackaging(p.Renditions[ordinal]) != "fmp4_hls" {
				return "", errors.New("unknown stream artifact")
			}
			if !segmentReady(path) {
				if err := h.startRendition(ctx, id, p.Renditions[ordinal], 0); err != nil {
					return "", err
				}
				return "", ErrSegmentPreparing
			}
		}
		return path, nil
	}
	ordinal, _ := strconv.Atoi(parts[1])
	index, _ := strconv.Atoi(parts[2])
	if ordinal >= len(p.Renditions) || index >= hlsSegmentCount(p.Duration) || name != renditionSegmentFile(p.Renditions[ordinal], index) {
		return "", errors.New("unknown stream artifact")
	}
	key := renditionKey(id, ordinal)
	if segmentReady(path) {
		h.noteRenditionServed(id, key, index)
		return path, nil
	}
	if err := h.startRendition(ctx, id, p.Renditions[ordinal], index); err != nil {
		return "", err
	}
	return h.awaitSegment(ctx, key, index, func(ctx context.Context) (string, error) {
		wait, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			var failure string
			if e := dbwork.QueryRow(ctx, h.db, `SELECT COALESCE(json_extract(renditions_json,?), '') FROM playback_delivery_plans WHERE session_id=?`, fmt.Sprintf("$[%d].failure", ordinal), id).Scan(&failure); e != nil {
				return "", e
			}
			if failure != "" {
				return "", ErrAudioRenditionUnavailable
			}
			current := filepath.Join(h.outputDir(id), name)
			if current != path {
				path = current
				if err := h.startRendition(ctx, id, p.Renditions[ordinal], index); err != nil {
					return "", err
				}
			}
			if segmentReady(path) {
				h.noteRenditionServed(id, key, index)
				return path, nil
			}
			select {
			case <-wait.Done():
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "", ErrSegmentPreparing
			case <-tick.C:
			}
		}
	})
}

func hevcSampleEntry(p *DeliveryPlan) string {
	profile := 0
	if p != nil && p.OutputVideoCodec == "hevc" && p.VideoAction == "copy" {
		profile, _ = deliveredDolbyVision(p)
	}
	return decoder.HEVCSampleEntry(profile)
}

var ErrAudioRenditionUnavailable = errors.New("This audio track is unavailable for this playback. Choose another audio track.")

func (h *HLS) renditionFailed(ctx context.Context, id string, generation int, r AudioRenditionPlan, code string) {
	if failureDuringClaim(ctx) {
		return
	}
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_delivery_plans SET renditions_json=json_set(renditions_json,?,?) WHERE session_id=? AND json_extract(renditions_json,?)=? AND EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=?)`, fmt.Sprintf("$[%d].failure", r.Ordinal), code, id, fmt.Sprintf("$[%d].streamIndex", r.Ordinal), r.StreamIndex, id, generation)
}

func (h *HLS) noteRenditionServed(id, key string, index int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.noteSessionServedLocked(id)
	if w := h.windows[key]; w != nil {
		w.served = max(w.served, index)
	}
}
