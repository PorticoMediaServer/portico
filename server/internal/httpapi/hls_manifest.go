package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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

type guardedManifest struct {
	*bytes.Reader
	check func() error
}

func (m *guardedManifest) Read(p []byte) (int, error) {
	if err := m.check(); err != nil {
		return 0, err
	}
	return m.Reader.Read(p)
}

func serveHLSManifest(w http.ResponseWriter, r *http.Request, f *os.File, check func() error) {
	raw, err := io.ReadAll(io.LimitReader(&guardedFile{File: f, check: check}, maxHLSManifestBytes+1))
	if err != nil {
		if authErr := check(); authErr != nil {
			failure(w, authErr)
		} else {
			invalidHLSManifest(w)
		}
		return
	}
	raw, err = sourceOriginManifest(raw, r.PathValue("file") == "master.m3u8")
	if err != nil {
		invalidHLSManifest(w)
		return
	}
	if err = check(); err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// ServeContent computes length/ranges from the served representation, not the
	// smaller file on disk. The producer's atomic playlist publication is untouched.
	// A growing playlist can be atomically replaced more than once in the same
	// second. Last-Modified's second precision must not turn a new version into304.
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, r.PathValue("file"), time.Time{}, &guardedManifest{Reader: bytes.NewReader(raw), check: check})
}

func invalidHLSManifest(w http.ResponseWriter) {
	write(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "stream_manifest_invalid", "message": "The playback manifest could not be read.", "retryable": false}})
}
