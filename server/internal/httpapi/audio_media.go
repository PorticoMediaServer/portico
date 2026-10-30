package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// AudioMedia is the media-input runtime's audio work (subtitlevideo.Runtime).
type AudioMedia interface {
	MeasureAudio(ctx context.Context, item, asset string) (audiofacts.Facts, error)
	ConvertAudio(ctx context.Context, item, asset string, spec decoder.AnalysisSpec, out io.Writer) error
}

func writeNoSuchAudio(w http.ResponseWriter) {
	write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "This presentation has no such audio.", "retry": "never"}})
}

func writePreparedLimit(w http.ResponseWriter) {
	write(w, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "prepared_limit", "message": "A prepared track can fetch only its opening bytes until it is committed.", "retry": "never"}})
}

// rangeLength is how many bytes a Range request asks for (the whole file without one).
func rangeLength(header string, size int64) int64 {
	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return size
	}
	a, b, _ := strings.Cut(spec, "-")
	if a == "" {
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil {
			return size
		}
		if n > size {
			return size
		}
		return n
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start >= size {
		return 0
	}
	end := size - 1
	if b != "" {
		if e, err := strconv.ParseInt(b, 10, 64); err == nil && e < end {
			end = e
		}
	}
	if end < start {
		return 0
	}
	return end - start + 1
}

// audioGrant resolves a version 2 audio route's grant: the presentation, its
// plan in the wanted mode, and whether it is a prepared (private) one.
func (d Dependencies) audioGrant(w http.ResponseWriter, r *http.Request, mode string) (grant, aid, item, session string, plan *playback.AudioPlan, private, ok bool) {
	grant = r.PathValue("grant")
	aid, p, item, e := d.Playback.ResolveDecodeGrantContext(r.Context(), grant)
	if errors.Is(e, sql.ErrNoRows) || errors.Is(e, identity.ErrUnauthorized) {
		// A grant is a capability, not a credential: an unknown one is a hidden
		// 404, never a 401 that tells the client to sign in again (NEW-33). An
		// ended one answers presentation_ended through failure (NEW-35).
		writeNoSuchAudio(w)
		return
	}
	if e == nil {
		e = d.itemAccess(r.Context(), p, item)
		if e == nil {
			d.admission.rememberCredential(grant, p)
		}
	}
	if e != nil {
		failure(w, e)
		return
	}
	plan, session, e = d.Playback.AudioPlanByGrant(r.Context(), grant)
	if e != nil || plan == nil || plan.Mode != mode {
		writeNoSuchAudio(w)
		return
	}
	if private, e = d.Playback.PrivatePresentation(r.Context(), session); e != nil {
		failure(w, e)
		return
	}
	return grant, aid, item, session, plan, private, true
}

func (d Dependencies) audioDecodeRoutes(mux *http.ServeMux) {
	// Direct: the original file with Range (spec §18.1). Before commit a prepared
	// presentation's grant serves at most prefetchBytes in total.
	serveDirect := func(w http.ResponseWriter, r *http.Request) {
		grant, aid, _, session, plan, private, ok := d.audioGrant(w, r, "direct")
		if !ok {
			return
		}
		reader, err := d.Playback.RetainAudioOccurrence(r.Context(), grant, session, plan.Generation)
		if err != nil {
			failure(w, err)
			return
		}
		defer reader.Close()
		r = r.WithContext(reader.Context())
		// A prepared track's budget is reserved for the range asked, then settled
		// to the body bytes actually sent: a failed or short response gives the
		// rest back (NEW-33).
		if private && r.Method != http.MethodHead {
			reserved := rangeLength(r.Header.Get("Range"), plan.Bytes)
			if !reader.Charge(reserved, plan.PrefetchBytes) {
				writePreparedLimit(w)
				return
			}
			counted := &servedBytes{ResponseWriter: w}
			defer func() { reader.Refund(reserved - counted.served()) }()
			w = counted
		}
		d.serveOriginal(w, r, grant, aid, true, func() error {
			_, p, item, e := d.Playback.ResolveDecodeGrantContext(r.Context(), grant)
			if e == nil {
				e = d.itemAccess(r.Context(), p, item)
				if e == nil {
					d.admission.rememberCredential(grant, p)
				}
			}
			return e
		})
	}
	d.httpHandle(mux, "GET /v1/media/{grant}/audio", serveDirect)
	d.httpHandle(mux, "HEAD /v1/media/{grant}/audio", serveDirect)
	// A direct MP3, ADTS or Ogg plan's seek index (spec §18.1): byte offsets at
	// packet or page boundaries and the raw frame each starts at, made once per
	// file with its facts. The client reads the original from the nearest entry
	// with a Range request; nothing is converted.
	d.httpHandle(mux, "GET /v1/media/{grant}/audio-index", func(w http.ResponseWriter, r *http.Request) {
		_, aid, _, _, plan, _, ok := d.audioGrant(w, r, "direct")
		if !ok {
			return
		}
		if plan.SeekIndex == nil {
			writeNoSuchAudio(w)
			return
		}
		index, found, err := audiofacts.ReadSeekIndex(r.Context(), d.DB, aid)
		if err != nil {
			failure(w, err)
			return
		}
		if !found {
			// The file changed since the plan was made: the index went with it.
			writeNoSuchAudio(w)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		write(w, http.StatusOK, SeekIndexContract{Version: 1, Offsets: index.Offsets, Frames: index.Frames})
	})
	// Converted: FLAC, Ogg Opus or ADTS AAC, exact (spec §18.1). ?fromFrame=N starts at
	// output frame N; before commit only from frame 0, within prefetchBytes.
	d.httpHandle(mux, "GET /v1/media/{grant}/audio-converted", func(w http.ResponseWriter, r *http.Request) {
		grant, aid, item, session, plan, private, ok := d.audioGrant(w, r, "converted")
		if !ok {
			return
		}
		reader, err := d.Playback.RetainAudioOccurrence(r.Context(), grant, session, plan.Generation)
		if err != nil {
			failure(w, err)
			return
		}
		defer reader.Close()
		r = r.WithContext(reader.Context())
		c := plan.Convert
		if d.AudioMedia == nil || c == nil {
			write(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "conversion_unavailable", "message": "Audio conversion isn't available on this server.", "retry": "never"}})
			return
		}
		from := int64(0)
		if v := r.URL.Query().Get("fromFrame"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 || n >= plan.DurationFrames {
				write(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_request", "message": "fromFrame is outside the track.", "field": "fromFrame", "retry": "never"}})
				return
			}
			from = n
		}
		if private && from != 0 {
			writePreparedLimit(w)
			return
		}
		skip := int64(math.Round(float64(from) * float64(c.SourceRate) / float64(c.Rate)))
		out := plan.DurationFrames - from
		spec := decoder.AnalysisSpec{Kind: "audio_flac", MaxDurationUS: max(1, c.SourceFrames*1_000_000/int64(max(1, c.SourceRate))+1_000_000),
			AudioTruePeakLimit: c.TruePeakLimit, AudioSourceDepth: c.SourceBitDepth, AudioGainDB: c.GainDB, AudioSourceRate: c.SourceRate, AudioPriming: c.SourceStart, AudioStart: c.SourceStart + skip, AudioFrames: max(1, c.SourceFrames-skip), AudioOut: out, AudioRate: c.Rate, AudioChannels: c.Channels, AudioBitDepth: c.BitDepth}
		start, end := int64(0), int64(0)
		w.Header().Set("Content-Type", "audio/flac")
		if c.Codec == "opus" {
			spec.Kind, spec.AudioBitrate = "audio_opus", c.BitrateBPS
			total := (playback.OpusPreSkip + out + 959) / 960 * 960
			start, end = playback.OpusPreSkip, total-playback.OpusPreSkip-out
			w.Header().Set("Content-Type", "audio/ogg")
		}
		if c.Codec == "aac" {
			spec.Kind, spec.AudioBitrate = "audio_aac", c.BitrateBPS
			total := (playback.AACPriming + out + 1023) / 1024 * 1024
			start, end = playback.AACPriming, total-playback.AACPriming-out
			w.Header().Set("Content-Type", "audio/aac")
		}
		w.Header().Set("X-Audio-Trim-Start", strconv.FormatInt(start, 10))
		w.Header().Set("X-Audio-Trim-End", strconv.FormatInt(end, 10))
		w.Header().Set("X-Audio-Frames", strconv.FormatInt(out, 10))
		w.Header().Set("Cache-Control", "private, no-store")
		body := withRollingDeadline(w)
		defer body.release()
		authority := newStreamAuthority(func() error {
			_, principal, currentItem, e := d.Playback.ResolveDecodeGrantContext(r.Context(), grant)
			if e == nil {
				e = d.itemAccess(r.Context(), principal, currentItem)
			}
			return e
		})
		var sink io.Writer = &authorityWriter{w: body, ctx: r.Context(), check: authority.check}
		if private {
			sink = &budgetWriter{w: body, ctx: r.Context(), check: authority.check, take: func(n int64) int64 { return reader.Take(n, plan.PrefetchBytes) }, refund: func(n int64) { reader.Refund(n) }}
		}
		if err := d.AudioMedia.ConvertAudio(r.Context(), item, aid, spec, sink); err != nil && r.Context().Err() == nil {
			// Headers are gone; the stream just ends early and the client falls back (§18.3).
			fmt.Fprint(io.Discard, err)
		}
	})
}

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

// servedBytes counts the body bytes of a successful (2xx) response, so a
// prepared track is charged only for what it received; an error body counts
// nothing.
type servedBytes struct {
	http.ResponseWriter
	status int
	n      int64
}

func (s *servedBytes) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *servedBytes) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	if s.status >= 200 && s.status < 300 {
		s.n += int64(n)
	}
	return n, err
}

func (s *servedBytes) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *servedBytes) served() int64 { return s.n }
