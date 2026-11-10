package streaming

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// A planned music stream is what PlanAudio decided: the device's own audio
// engine decodes the original file, or an exact FLAC, Opus or AAC conversion,
// with the server's trim and gain. It has three addresses under its grant:
//
//	/v1/media/{grant}/audio            the original, by byte range (direct plans)
//	/v1/media/{grant}/audio-index      the file's seek index (direct MP3, ADTS, Ogg)
//	/v1/media/{grant}/audio-converted  the conversion, from ?fromFrame=N

// SeekIndexContract is GET /v1/media/{grant}/audio-index: Offsets[i] is a
// byte offset at a packet (MP3, ADTS) or page (Ogg) boundary and Frames[i] the
// raw frame decoding from there starts at, counted as the plan's trim counts
// (from the first audio packet; Vorbis: the page granule before it). Entries
// rise in both, a few seconds apart, at most 4096.
type SeekIndexContract struct {
	Version int     `json:"version"`
	Offsets []int64 `json:"offsets"`
	Frames  []int64 `json:"frames"`
}

func (st *stream) audioURL() string {
	if st.audio.Mode == "converted" {
		return "/v1/media/" + st.grant + "/audio-converted"
	}
	return "/v1/media/" + st.grant + "/audio"
}

func (s *Streams) serveAudio(w http.ResponseWriter, r *http.Request, st *stream, file string, check func() error) {
	plan := st.audio
	switch {
	case file == "audio" && plan.Mode == "direct":
		body, _, modified, err := st.source.Open(r.Context())
		if err != nil {
			mediaError(w, err)
			return
		}
		defer body.Close()
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", mediafacts.OriginalContentType(plan.Container, ""))
		http.ServeContent(w, r, "media."+plan.Container, modified, &guardedReader{ReadSeeker: body, check: check})
	case file == "audio-index" && plan.Mode == "direct" && plan.SeekIndex != nil && st.seekIndex != nil:
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SeekIndexContract{Version: 1, Offsets: st.seekIndex.Offsets, Frames: st.seekIndex.Frames})
	case file == "audio-converted" && plan.Mode == "converted" && plan.Convert != nil:
		s.serveConvertedAudio(w, r, st, check)
	default:
		mediaError(w, ErrHLSArtifactNotFound)
	}
}

// serveConvertedAudio streams the plan's conversion, exact to the frame.
// ?fromFrame=N starts at output frame N, which is how a seek is made.
func (s *Streams) serveConvertedAudio(w http.ResponseWriter, r *http.Request, st *stream, check func() error) {
	plan, c := st.audio, st.audio.Convert
	if s.hls.binary == "" {
		mediaError(w, ErrNoConverter)
		return
	}
	from := int64(0)
	if v := r.URL.Query().Get("fromFrame"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n >= plan.DurationFrames {
			writeMediaError(w, http.StatusBadRequest, "invalid_request", "fromFrame is outside the track.", false)
			return
		}
		from = n
	}
	skip := int64(math.Round(float64(from) * float64(c.SourceRate) / float64(c.Rate)))
	out := plan.DurationFrames - from
	spec := decoder.AnalysisSpec{
		Kind: "audio_flac", MaxDurationUS: max(1, c.SourceFrames*1_000_000/int64(max(1, c.SourceRate))+1_000_000),
		AudioTruePeakLimit: c.TruePeakLimit, AudioSourceDepth: c.SourceBitDepth, AudioGainDB: c.GainDB, AudioSourceRate: c.SourceRate,
		AudioPriming: c.SourceStart, AudioStart: c.SourceStart + skip, AudioFrames: max(1, c.SourceFrames-skip), AudioOut: out,
		AudioRate: c.Rate, AudioChannels: c.Channels, AudioBitDepth: c.BitDepth,
	}
	start, end, contentType := int64(0), int64(0), "audio/flac"
	if c.Codec == "opus" {
		spec.Kind, spec.AudioBitrate = "audio_opus", c.BitrateBPS
		total := (OpusPreSkip + out + 959) / 960 * 960
		start, end, contentType = OpusPreSkip, total-OpusPreSkip-out, "audio/ogg"
	}
	if c.Codec == "aac" {
		spec.Kind, spec.AudioBitrate = "audio_aac", c.BitrateBPS
		total := (AACPriming + out + 1023) / 1024 * 1024
		start, end, contentType = AACPriming, total-AACPriming-out, "audio/aac"
	}
	body, size, _, err := st.source.Open(r.Context())
	if err != nil {
		mediaError(w, err)
		return
	}
	defer body.Close()
	source, ok := body.(io.ReaderAt)
	if !ok {
		mediaError(w, errors.New("this kind of source cannot be converted yet"))
		return
	}
	bridge, err := inputbridge.Open(r.Context(), source, size)
	if err != nil {
		mediaError(w, err)
		return
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		mediaError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Audio-Trim-Start", strconv.FormatInt(start, 10))
	w.Header().Set("X-Audio-Trim-End", strconv.FormatInt(end, 10))
	w.Header().Set("X-Audio-Frames", strconv.FormatInt(out, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	sink := &guardedWriter{w: w, check: check}
	// Headers are sent with the first bytes. If the conversion fails after that
	// the stream just ends early, and the player falls back.
	err = decoder.RunAnalysis(r.Context(), s.processes, "audio:"+st.id+":"+strconv.FormatInt(from, 10), s.hls.binary, bridge.URL(), reservation, spec,
		func(converted io.Reader) error { _, e := io.Copy(sink, converted); return e })
	if err != nil && !sink.wrote {
		mediaError(w, err)
	}
}

// guardedWriter stops a conversion once the stream it belongs to is over.
type guardedWriter struct {
	w     io.Writer
	check func() error
	wrote bool
}

func (g *guardedWriter) Write(p []byte) (int, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	g.wrote = true
	return g.w.Write(p)
}
