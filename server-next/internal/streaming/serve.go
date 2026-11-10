package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// ServeMedia answers one request for a stream's bytes: the original file by
// byte range, or a playlist or segment of a conversion. It does one in-memory
// lookup of the grant and no database work. A grant that is unknown, stopped or
// failed, and a file name the stream does not have, are the same hidden 404.
func (s *Streams) ServeMedia(w http.ResponseWriter, r *http.Request, grant, file string) {
	st, err := s.grant(grant)
	if err != nil {
		mediaError(w, err)
		return
	}
	// check ends a transfer already under way when its stream stops.
	check := func() error { _, e := s.grant(grant); return e }
	if st.mode == "direct" {
		if file != "original" && file != "" {
			mediaError(w, ErrHLSArtifactNotFound)
			return
		}
		s.serveOriginal(w, r, st, check)
		return
	}
	f, err := s.hls.OpenFile(r.Context(), st.id, file)
	if err != nil {
		mediaError(w, err)
		return
	}
	defer f.Close()
	if strings.HasSuffix(file, ".m3u8") {
		serveManifest(w, r, f, file, check)
		return
	}
	info, err := f.Stat()
	if err != nil {
		mediaError(w, err)
		return
	}
	switch filepath.Ext(file) {
	case ".mp4":
		w.Header().Set("Content-Type", "video/mp4")
	case ".m4s":
		w.Header().Set("Content-Type", "video/iso.segment")
	default:
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeContent(w, r, file, info.ModTime(), &guardedReader{ReadSeeker: f, check: check})
}

func (s *Streams) serveOriginal(w http.ResponseWriter, r *http.Request, st *stream, check func() error) {
	body, _, modified, err := st.source.Open(r.Context())
	if err != nil {
		mediaError(w, err)
		return
	}
	defer body.Close()
	video := ""
	if st.plan.VideoAction != "none" {
		video = st.plan.SourceVideoCodec
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", mediafacts.OriginalContentType(st.plan.SourceContainer, video))
	http.ServeContent(w, r, "media."+st.plan.SourceContainer, modified, &guardedReader{ReadSeeker: body, check: check})
}

// guardedReader stops handing out bytes once the stream they belong to is over.
type guardedReader struct {
	io.ReadSeeker
	check func() error
}

func (g *guardedReader) Read(p []byte) (int, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	return g.ReadSeeker.Read(p)
}

const maxHLSManifestBytes = 1 << 20
const hlsStartAtSourceOrigin = "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES"

// Portico converts on-demand files from source time zero. EVENT describes how
// the playlist grows; it does not tell a player to start at the source origin.
// Keep this preference in the root playlist, including after finalization, so
// AVFoundation does not interpret an unfinished conversion as a live-edge start.
// Resume and subsequent seeks still require the client's actual seek ack.
func sourceOriginManifest(raw []byte, root bool) ([]byte, error) {
	if len(raw) > maxHLSManifestBytes || !bytes.HasPrefix(raw, []byte("#EXTM3U\n")) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("invalid generated HLS manifest")
	}
	if !root {
		return raw, nil
	}
	starts, event, master, vod, ended := 0, false, false, false, false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#EXT-X-START:") {
			starts++
			if line != hlsStartAtSourceOrigin {
				return nil, errors.New("conflicting generated HLS start preference")
			}
		}
		event = event || line == "#EXT-X-PLAYLIST-TYPE:EVENT"
		vod = vod || line == "#EXT-X-PLAYLIST-TYPE:VOD"
		ended = ended || line == "#EXT-X-ENDLIST"
		master = master || strings.HasPrefix(line, "#EXT-X-STREAM-INF:")
	}
	if starts > 1 || (vod && !ended) || (vod && event) || (!event && !master && !vod) {
		return nil, errors.New("unexpected generated HLS root playlist")
	}
	if starts == 1 {
		return raw, nil
	}
	out := make([]byte, 0, len(raw)+len(hlsStartAtSourceOrigin)+1)
	out = append(out, "#EXTM3U\n"...)
	out = append(out, hlsStartAtSourceOrigin...)
	out = append(out, '\n')
	out = append(out, raw[len("#EXTM3U\n"):]...)
	return out, nil
}

func serveManifest(w http.ResponseWriter, r *http.Request, f *os.File, name string, check func() error) {
	raw, err := io.ReadAll(io.LimitReader(f, maxHLSManifestBytes+1))
	if err == nil {
		raw, err = sourceOriginManifest(raw, name == "master.m3u8")
	}
	if authErr := check(); authErr != nil {
		mediaError(w, authErr)
		return
	}
	if err != nil {
		w.Header().Set("Retry-After", "1")
		writeMediaError(w, http.StatusServiceUnavailable, "stream_not_ready", "Stream manifest is not ready.", true)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// ServeContent computes length/ranges from the served representation, not the
	// smaller file on disk. The producer's atomic playlist publication is untouched.
	// A growing playlist can be atomically replaced more than once in the same
	// second. Last-Modified's second precision must not turn a new version into304.
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(raw))
}

// mediaError answers a media request that cannot be served, in the API's one
// error format. What is over, unknown or not this stream's is a 404 that says
// nothing more; what is not ready yet is retryable.
func mediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSegmentPreparing), errors.Is(err, ErrHLSArtifactUnavailable):
		w.Header().Set("Retry-After", "1")
		writeMediaError(w, http.StatusServiceUnavailable, "segment_preparing", ErrSegmentPreparing.Error(), true)
	case errors.Is(err, ErrConversionSpace):
		w.Header().Set("Retry-After", "5")
		writeMediaError(w, http.StatusServiceUnavailable, "conversion_storage_full", ErrConversionSpace.Error(), true)
	case errors.Is(err, ErrAudioRenditionUnavailable):
		writeMediaError(w, http.StatusUnprocessableEntity, "audio_rendition_unavailable", err.Error(), false)
	case errors.Is(err, ErrGrantEnded), errors.Is(err, ErrHLSArtifactNotFound), errors.Is(err, ErrSourceUnavailable),
		errors.Is(err, ErrSourceChanged), errors.Is(err, os.ErrNotExist):
		writeMediaError(w, http.StatusNotFound, "not_found", ErrGrantEnded.Error(), false)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The player went away; nobody reads this.
		writeMediaError(w, 499, "request_cancelled", "The request was cancelled.", true)
	default:
		writeMediaError(w, http.StatusInternalServerError, "media_conversion_failed", errMediaConversionFailed.Error(), false)
	}
}

func writeMediaError(w http.ResponseWriter, status int, code, message string, retry bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retry": retry}})
}
