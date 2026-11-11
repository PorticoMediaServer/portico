package prepare

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

// A recipe is one word: a step of the video ladder for a film or an episode ("720p-high"), a
// music bitrate for a song or an audiobook ("320", "96"). The original is not a recipe: it
// needs no work.

// Music says a title is listened to, so its copies are made at a music bitrate.
func Music(kind db.ItemKind) bool { return kind == db.KindTrack || kind == db.KindAudiobook }

// Recipes are the smaller copies the server can make of a version, smallest last, up to the
// owner's highest download quality. A step above the version's own picture is not offered.
func Recipes(v db.Version, kind db.ItemKind, highest string) []string {
	var out []string
	if Music(kind) {
		for _, p := range qualityladder.Music() {
			// A lossy source is never made bigger: a copy is offered only below its own rate.
			if p.AudioKbps > 0 && (v.Bitrate == 0 || int64(p.AudioKbps)*1000 < v.Bitrate) {
				out = append(out, p.ID)
			}
		}
		return out
	}
	if v.VideoCodec == "" && v.Height == 0 {
		// A book, a comic or anything else without a picture to make smaller.
		return nil
	}
	limit := 1 << 30
	if p, ok := qualityladder.Find(highest); ok {
		limit = p.VideoBitrateBPS
	}
	for _, p := range qualityladder.Fixed() {
		if p.ID == "2160p" || v.Height > 0 && p.Height > v.Height || p.VideoBitrateBPS > limit || p.SameAsSource(v.Height, v.Bitrate) {
			continue
		}
		out = append(out, p.ID)
	}
	return out
}

// EstimatedSize is about how many bytes a copy of the version at the recipe will take.
func EstimatedSize(v db.Version, recipe string) int64 {
	kbps := 0
	if p, ok := qualityladder.Find(recipe); ok {
		kbps = (p.VideoBitrateBPS + p.MaxAudioBitrateBPS) / 1000
	} else if p, ok := qualityladder.FindMusic(recipe); ok {
		kbps = p.AudioKbps
	}
	// The picture is encoded to a quality with the ladder's rate as its ceiling, so most
	// copies come out smaller than this.
	return v.DurationMS * int64(kbps) / 8
}

// recipeFor turns a recipe word into what the encoder is told, for one part of a version.
// ok is false for a recipe this version cannot be made at.
func recipeFor(word string, kind db.ItemKind, v db.Version, part db.Part) (decoder.PreparedRecipe, string, bool) {
	var video, audio *db.Stream
	for n := range part.Streams {
		s := &part.Streams[n]
		if s.Kind == db.StreamVideo && video == nil {
			video = s
		}
		if s.Kind == db.StreamAudio && (audio == nil || s.Default && !audio.Default) {
			audio = s
		}
	}
	if Music(kind) {
		p, ok := qualityladder.FindMusic(word)
		if !ok || p.AudioKbps == 0 || audio == nil {
			return decoder.PreparedRecipe{}, "", false
		}
		r := decoder.PreparedRecipe{AudioOnly: true, MusicQuality: word, MusicCodec: "aac",
			Source: decoder.PreparedSource{AudioCodec: audio.Codec, AudioSampleRate: audio.SampleRate, AudioBitDepth: audio.BitDepth,
				AudioBitrateBPS: int(audio.Bitrate), AudioChannels: []int{max(audio.Channels, 1)}}}
		// One song of an album kept as one file and a cue sheet is that range of the file.
		if part.StartMS != nil {
			r.MusicStart = float64(*part.StartMS) / 1000
		}
		if part.EndMS != nil {
			r.MusicEnd = float64(*part.EndMS) / 1000
		}
		if _, err := decoder.PreparedMusicFormat(r); err != nil {
			return decoder.PreparedRecipe{}, "", false
		}
		return r, "m4a", true
	}
	p, ok := qualityladder.Find(word)
	// Two episodes in one file need a cut the encoder does not make; their original downloads.
	if !ok || video == nil || part.StartMS != nil || part.EndMS != nil {
		return decoder.PreparedRecipe{}, "", false
	}
	r := decoder.PreparedRecipe{Width: p.Width(), Height: p.Height, VideoKbps: p.VideoBitrateBPS / 1000, AudioKbps: p.MaxAudioBitrateBPS / 1000,
		DropChapters: true, Source: decoder.PreparedSource{BitDepth: video.BitDepth}}
	switch hdr := firstNonEmpty(video.HDR, v.HDR); hdr {
	case "hdr10", "hlg":
		r.Source.DynamicRange = hdr
	case "dolby_vision":
		// The copy keeps the base layer, tone-mapped like any other HDR picture.
		r.Source.DynamicRange, r.Source.DolbyVision = "hdr10", true
	}
	return r, "mp4", true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
