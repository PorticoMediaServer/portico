package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

// Media URLs are unguessable, playback-family-bound capabilities. The runtime
// rechecks the current lease and selected/retained content for every media read;
// neither browser HLS nor AVPlayer needs to expose the controller proof in URLs.
func (d Dependencies) linearMediaRoutes(mux *http.ServeMux) {
	if d.PlaybackRuntime == nil || d.PlaybackRuntime.Linear == nil {
		return
	}
	runtime := d.PlaybackRuntime.Linear
	// v1 channel sessions' media is under /v1 (spec §18.6).
	serve := func(w http.ResponseWriter, r *http.Request) {
		id, generation, token, name := r.PathValue("playbackId"), r.PathValue("generation"), r.PathValue("token"), r.PathValue("name")
		if name != "logo" {
			runtime.ServeMedia(w, r, id, generation, token, name)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if d.Metadata == nil || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		p, selection, err := runtime.MediaGrant(r.Context(), id, generation, token)
		if err != nil || selection.LogoItemID == "" || d.itemAccess(r.Context(), p, selection.LogoItemID) != nil {
			http.NotFound(w, r)
			return
		}
		file, mime, err := d.Metadata.Artwork(r.Context(), selection.LogoItemID, "poster")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Size() > 16<<20 {
			http.NotFound(w, r)
			return
		}
		authorize := func() bool {
			current, s, err := runtime.MediaGrant(r.Context(), id, generation, token)
			return err == nil && s.LogoItemID == selection.LogoItemID && d.itemAccess(r.Context(), current, s.LogoItemID) == nil
		}
		if !authorize() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		if r.Method == http.MethodHead {
			return
		}
		chunk := make([]byte, 64<<10)
		for {
			if !authorize() {
				return
			}
			n, err := file.Read(chunk)
			if n > 0 {
				if !authorize() {
					return
				}
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
				if _, e := w.Write(chunk[:n]); e != nil {
					return
				}
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
		}
	}
	d.httpHandle(mux, "GET /v1/media/linear/{playbackId}/{generation}/{token}/{name}", serve)
}
