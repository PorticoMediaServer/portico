package downloads

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

func musicDownloadQuality(ctx context.Context, tx *sql.Tx, p identity.Principal) (string, error) {
	values, _, _, e := operations.EffectivePreferences(tx, p.Viewer, playback.DeliveryContextFrom(ctx).DeviceClass)
	if e != nil {
		return "", e
	}
	return values.Text("music.qualityDownloads"), nil
}
func canonicalMusicQuality(q string) bool {
	for _, id := range qualityladder.MusicIDs() {
		if q == id {
			return true
		}
	}
	return false
}

// videoDownloadQuality is the profile's saved Download quality for a title, one
// value per device class. A value the ladder no longer makes falls back to the
// untouched file rather than asking for a quality the server cannot prepare.
func videoDownloadQuality(ctx context.Context, tx *sql.Tx, p identity.Principal) (string, error) {
	values, _, _, e := operations.EffectivePreferences(tx, p.Viewer, playback.DeliveryContextFrom(ctx).DeviceClass)
	if e != nil {
		return "", e
	}
	q := values.Text("quality.downloads")
	for _, id := range qualityladder.IDs() {
		if q == id {
			return q, nil
		}
	}
	return QualityOriginal, nil
}

func requestedDownloadQuality(ctx context.Context, tx *sql.Tx, p identity.Principal, q string, s source) (string, error) {
	if readingKind(s.Kind) {
		if q != "" && q != QualityOriginal {
			return "", ErrInput
		}
		return QualityOriginal, nil
	}
	if q != "" {
		return q, nil
	}
	if s.Kind == "song" {
		return musicDownloadQuality(ctx, tx, p)
	}
	// An audiobook file keeps the untouched file: the video ladder says nothing
	// about audio, and it has no approved audio-default consumer of its own.
	if audioKind(s.Kind) {
		return QualityOriginal, nil
	}
	return videoDownloadQuality(ctx, tx, p)
}

func musicSourceEquivalent(q string, s source) bool {
	if s.Kind != "song" || s.Cue {
		return false
	}
	switch q {
	case "original":
		return true
	case "cd-quality":
		known := s.AudioLossless
		if known == nil {
			known = assets.KnownLosslessAudio(s.AudioCodec)
		}
		if known == nil {
			return false
		}
		if !*known {
			return s.AudioBitrateBPS > 0
		}
		rate := 48000
		if s.AudioRate%11025 == 0 {
			rate = 44100
		}
		return s.AudioRate > 0 && s.AudioRate <= rate && s.AudioDepth > 0 && s.AudioDepth <= 16
	case "320", "96":
		limit, _ := strconv.Atoi(q)
		known := s.AudioLossless
		if known == nil {
			known = assets.KnownLosslessAudio(s.AudioCodec)
		}
		return known != nil && !*known && s.AudioBitrateBPS > 0 && s.AudioBitrateBPS <= limit*1000
	}
	return false
}

func musicTarget(id string, s source) (playback.DeliverySource, bool) {
	q, c, ok := preparedmedia.MusicProfile(id)
	if !ok {
		return playback.DeliverySource{}, false
	}
	r := decoder.PreparedRecipe{AudioOnly: true, MusicQuality: q, MusicCodec: c, Source: decoder.PreparedSource{AudioLossless: s.AudioLossless, AudioCodec: s.AudioCodec, AudioSampleRate: s.AudioRate, AudioBitDepth: s.AudioDepth, AudioBitrateBPS: s.AudioBitrateBPS, AudioChannels: []int{s.AudioChannels}}}
	o, e := decoder.PreparedMusicFormat(r)
	if e != nil {
		return playback.DeliverySource{}, false
	}
	container := o.Container
	if container == "m4a" {
		container = "mp4"
	}
	return playback.DeliverySource{Container: container, Audio: []playback.SourceAudio{{Codec: o.Codec, Channels: o.Channels, StreamDetail: assets.StreamDetail{SampleRate: o.Rate, BitDepth: o.Depth, BitRate: int64(o.BitrateBPS)}}}}, true
}
func musicRecipeFor(device playback.CapabilityDocument, q string, s source) string {
	if !canonicalMusicQuality(q) {
		return ""
	}
	if !s.Cue && musicSourceEquivalent(q, s) {
		return ""
	}
	ids := []string{}
	switch q {
	case "original":
		if s.Cue {
			ids = []string{"music-original-flac-v1"}
		}
	case "cd-quality":
		ids = []string{"music-cd-quality-v1"}
	case "320", "96":
		ids = []string{"music-opus-" + q + "-v1", "music-aac-" + q + "-v1"}
	}
	for _, id := range ids {
		target, ok := musicTarget(id, s)
		if ok && playback.DownloadPlayable(target, device) {
			return id
		}
	}
	return ""
}

func (s *Service) musicOptions(ctx context.Context, tx *sql.Tx, p identity.Principal, found source, base QualityOption, versions []preparedmedia.Version, live map[string][2]string, owner, transcoding bool) []QualityOption {
	device := playback.CapabilityDocumentTx(ctx, tx, p)
	out := []QualityOption{}
	for _, rung := range qualityladder.Music() {
		q := rung.ID
		option := base
		option.Quality, option.Label = q, qualityLabel(q)
		option.TargetHeight, option.MaxVideoBitrateBPS = 0, 0
		option.MaxAudioBitrateBPS = rung.AudioKbps * 1000
		option.DataGBPerHour = rung.DataMBPerHour / 1000
		option.EffectiveQuality = q
		if musicSourceEquivalent(q, found) {
			option.EffectiveQuality = "original"
			option.SameAsOriginal = q != "original"
			out = append(out, decorate(option, live))
			continue
		}
		if q == "original" && !found.Cue {
			out = append(out, decorate(option, live))
			continue
		}
		option.Kind = "optimized"
		option.PreparedProfileID = musicRecipeFor(device, q, found)
		option.Bytes, option.Estimated = estimateBytes(q, option.PreparedProfileID, found)
		version := selectableVersionForSource(versions, option.PreparedProfileID, found.Asset)
		switch {
		case option.PreparedProfileID == "":
			option.Available = false
			option.Reason = ReasonNotOptimized
		case !base.Available:
		case version != nil:
			option.Available = true
			option.Bytes = version.Size
			option.Estimated = false
			option.PreparedVersionID = version.ID
		case !transcoding:
			option.Available = false
			option.Reason = playback.RungReasonTranscodeOff
		case owner && preparedmedia.Producible(option.PreparedProfileID):
			option.Available = true
			option.RequiresPrepare = true
		default:
			option.Available = false
			option.Reason = ReasonNotOptimized
		}
		out = append(out, decorate(option, live))
	}
	return out
}

// An extracted original cue track occupies prepared storage just like any
// other derivative; the public quality name must not bypass that budget.
func storedQualityBudget(quality, recipe string) string {
	if quality == QualityOriginal && recipe != "" {
		return "cd-quality"
	}
	return quality
}
