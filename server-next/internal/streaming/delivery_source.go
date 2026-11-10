package streaming

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// SourceVideo and SourceAudio are the planner's view of one file. They are read
// from the scanner's stream rows; nothing here opens the file.
type SourceVideo struct {
	Index int    `json:"index"`
	Codec string `json:"codec"`
	mediafacts.StreamDetail
}

type SourceAudio struct {
	Lossless         *bool  `json:"lossless,omitempty"`
	AudioDescription bool   `json:"audioDescription,omitempty"`
	NativeCodec      string `json:"-"`
	Title            string `json:"title,omitempty"`
	Index            int    `json:"index"`
	Ordinal          int    `json:"ordinal"`
	Codec            string `json:"codec"`
	Channels         int    `json:"channels"`
	Layout           string `json:"layout,omitempty"`
	Language         string `json:"language,omitempty"`
	Default          bool   `json:"default,omitempty"`
	Commentary       bool   `json:"commentary,omitempty"`
	mediafacts.StreamDetail
}

// DeliverySource is everything planning knows about one file.
type DeliverySource struct {
	ID        string
	Container string
	Duration  float64
	// BitRate is the whole file's average rate, from size and duration when the
	// container did not state one. It is what a byte-range delivery costs.
	BitRate    int64
	MaxBitRate int64
	Video      *SourceVideo
	Audio      []SourceAudio
	// DetailKnown is false for a file probed before stream detail existed and
	// not refreshed since. Unobserved facts do not reject a route; they are noted.
	DetailKnown bool
	// Remote is a STRM target the server measured and fetches itself. Its
	// original is relayed by the server; any other route reads it through the
	// remote window input.
	Remote bool
}

// audioFamily folds the PCM and DTS variants ffprobe names individually into the
// one name a device declares.
func audioFamily(codec string) string {
	switch {
	case strings.HasPrefix(codec, "pcm_"):
		return "pcm"
	case codec == "dca":
		return "dts"
	case codec == "mp2", codec == "mp1":
		return "mp2"
	}
	return codec
}

func videoFamily(codec string) string {
	switch codec {
	case "h265":
		return "hevc"
	case "mpeg1video":
		return "mpeg2video"
	}
	return codec
}

// legacySource builds a source from the flat fields older callers and tests
// pass. Its detail is unobserved apart from height and colour.
func legacySource(in DeliveryInput) DeliverySource {
	s := DeliverySource{ID: in.SourceID, Container: in.Container, Duration: in.Duration}
	if in.VideoCodec != "" {
		v := &SourceVideo{Codec: videoFamily(in.VideoCodec)}
		v.Height = in.Height
		v.DynamicRange = mediafacts.DynamicRangeOf(in.ColorTransfer, 0)
		s.Video = v
	}
	if in.AudioCodec != "" {
		s.Audio = []SourceAudio{{Index: -1, Codec: audioFamily(in.AudioCodec)}}
	}
	return s
}

// FileFacts is what the caller reads from the catalogue for one file: the
// file's summary row and its video and audio streams in stream order, each with
// its Detail (and observed breadth) filled in.
type FileFacts struct {
	ID                     string
	Container              string
	VideoCodec, AudioCodec string
	Duration               float64
	Size                   int64
	Width, Height          int
	// DetailVersion is the stream detail version the streams were stored at.
	DetailVersion int
	// StreamsCurrent says the stream facts belong to this size and modified time.
	StreamsCurrent bool
	// WavPackLossless is the catalogue's fact for a single-track WavPack file.
	WavPackLossless *bool
	Streams         []mediafacts.Stream
}

// SourceFromFacts builds one file's planning facts from what the catalogue holds.
func SourceFromFacts(f FileFacts) DeliverySource {
	var s DeliverySource
	video, audio := f.VideoCodec, f.AudioCodec
	size, width, height, version := f.Size, f.Width, f.Height, f.DetailVersion
	streamsCurrent, wavpackLossless := f.StreamsCurrent, f.WavPackLossless
	s.ID = f.ID
	s.Container, s.Duration = f.Container, f.Duration
	s.DetailKnown = version >= mediafacts.StreamDetailVersion
	if s.Duration > 0 && size > 0 {
		s.BitRate = int64(float64(size) * 8 / s.Duration)
	}
	for _, st := range f.Streams {
		if st.Type != "video" && st.Type != "audio" {
			continue
		}
		transfer := st.ColorTransfer
		if observed := st.Detail.ObservedInterlaced(); observed != nil {
			st.Detail.Interlaced = *observed
		}
		if st.Type == "audio" && st.Detail.AudioVariant == "" {
			st.Detail.AudioVariant = mediafacts.AudioVariant(st.Codec, st.Detail.Profile, st.Detail.ObjectAudio)
		}
		switch st.Type {
		case "video":
			if s.Video != nil {
				continue
			}
			v := &SourceVideo{Index: st.Index, Codec: videoFamily(st.Codec), StreamDetail: st.Detail}
			if v.Height == 0 {
				v.Height = height
				v.Width = width
			}
			if v.DynamicRange == "" {
				v.DynamicRange = mediafacts.DynamicRangeOf(transfer, 0)
			}
			s.Video = v
		case "audio":
			var lossless *bool
			if streamsCurrent {
				lossless = knownAudioEncodingLossless(st.Codec, st.Detail.Profile, st.Detail.AudioVariant)
				// The catalogue fact belongs to its first audio stream. Without a
				// stored stream index, only a single-track asset proves this binding.
				if st.Codec == "wavpack" && wavpackLossless != nil {
					lossless = copiedBool(wavpackLossless)
				}
			}
			s.Audio = append(
				s.Audio,
				SourceAudio{
					Lossless:         lossless,
					Index:            st.Index,
					Ordinal:          len(s.Audio),
					Codec:            audioFamily(st.Codec),
					NativeCodec:      st.Codec,
					Channels:         st.Channels,
					Layout:           st.ChannelLayout,
					Language:         st.Language,
					Title:            publicAudioText(st.Title, 96),
					Default:          st.Default,
					Commentary:       st.Commentary,
					AudioDescription: st.Detail.AudioDescription,
					StreamDetail:     st.Detail,
				},
			)
		}
	}
	if s.Video != nil && s.Video.MaxBitRate > 0 {
		s.MaxBitRate = s.Video.MaxBitRate
		for _, audio := range s.Audio {
			s.MaxBitRate += audio.BitRate
		}
	}
	// A file whose stream rows are missing (never probed per stream, or a remote
	// descriptor) still plans from the asset row's first-codec summary.
	if s.Video == nil && video != "" {
		v := &SourceVideo{Codec: videoFamily(video)}
		v.Height = height
		v.Width = width
		// The summary carries a codec and geometry, not an observed transfer.
		// effectiveRange owns the planner's fallback; it is not a source fact.
		s.Video = v
	}
	if len(s.Audio) == 0 && audio != "" {
		// Index -1: the stream's position in the file is not known, so the
		// producer takes the first audio track rather than a guessed index.
		s.Audio = []SourceAudio{{Index: -1, Codec: audioFamily(audio)}}
	}
	return s
}
