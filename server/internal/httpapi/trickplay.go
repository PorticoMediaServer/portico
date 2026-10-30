package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// Chapter thumbnails and trickplay tiles are item-scoped, immutable previews of
// catalogued media, exactly like item artwork. They are therefore published on
// the item path under the same library-visibility authorization as
// GET /v1/items/{id}/art/{kind}, not under a playback grant: a grant belongs to
// one prepared or HLS session, is reissued on renewal and dies with the
// session, so grant-scoped preview URLs could not be cached by a player, could
// not be shown before a session exists, and would break mid-stream on renewal.
func (d Dependencies) trickplayRoutes(mux *http.ServeMux) {

	d.httpHandle(mux, "GET /v1/items/{id}/trickplay/{set}/thumbnails.vtt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if d.Analysis == nil {
			analysisFailure(w, mediaanalysis.ErrUnsupported)
			return
		}
		if r.URL.RawQuery != "" {
			analysisFailure(w, mediaanalysis.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		a, e := d.analysisAccess(r)
		if e != nil {
			analysisFailure(w, e)
			return
		}
		out, e := d.Analysis.Thumbnails(ctx, a, r.PathValue("set"))
		if e != nil {
			analysisFailure(w, e)
			return
		}
		after, e := d.analysisAccess(r)
		if e != nil {
			analysisFailure(w, e)
			return
		}
		if a.ViewerFence != after.ViewerFence || a.LibraryID != after.LibraryID {
			failure(w, identity.ErrNotVisible)
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(out)))
		w.Header().Set("Cache-Control", "private, max-age=300")
		_, _ = w.Write([]byte(out))
	})
	d.httpHandle(mux, "GET /v1/items/{id}/trickplay", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if d.Analysis == nil {
			analysisFailure(w, mediaanalysis.ErrUnsupported)
			return
		}
		if r.URL.RawQuery != "" {
			analysisFailure(w, mediaanalysis.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		a, e := d.analysisAccess(r)
		if e != nil {
			analysisFailure(w, e)
			return
		}
		out, e := d.Analysis.Trickplay(ctx, a)
		if e != nil {
			analysisFailure(w, e)
			return
		}
		after, e := d.analysisAccess(r)
		if e != nil {
			analysisFailure(w, e)
			return
		}
		if a.ViewerFence != after.ViewerFence || a.LibraryID != after.LibraryID {
			failure(w, identity.ErrNotVisible)
			return
		}
		write(w, 200, out)
	})
}

func (d Dependencies) trickplayImage(w http.ResponseWriter, r *http.Request, read func(context.Context, mediaanalysis.Access) (mediaanalysis.Image, error)) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if d.Analysis == nil {
		analysisFailure(w, mediaanalysis.ErrUnsupported)
		return
	}
	if r.URL.RawQuery != "" {
		analysisFailure(w, mediaanalysis.ErrInput)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	a, e := d.analysisAccess(r)
	if e != nil {
		analysisFailure(w, e)
		return
	}
	out, e := read(ctx, a)
	if e != nil {
		analysisFailure(w, e)
		return
	}
	after, e := d.analysisAccess(r)
	if e != nil {
		analysisFailure(w, e)
		return
	}
	if a.ViewerFence != after.ViewerFence || a.LibraryID != after.LibraryID {
		failure(w, identity.ErrNotVisible)
		return
	}
	w.Header().Set("Content-Type", out.MIME)

	// Preview bytes are immutable for their digest; the viewer fence is
	// rechecked on every request, so a private cache is safe.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+out.Digest+`"`)
	if previewETagMatches(r.Header.Get("If-None-Match"), `"`+out.Digest+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(out.Data)))
	_, _ = w.Write(out.Data)
}
func (d Dependencies) registerTrickplayImageRoutes(registry *apikit.Registry) error {
	if e := registerSubtitleRaw[struct{}, SubtitleBytesContract](registry, "GET /v1/items/{id}/chapters/{chapter}/image", func(w http.ResponseWriter, r *http.Request) {
		chapter := r.PathValue("chapter")
		d.trickplayImage(w, r, func(ctx context.Context, a mediaanalysis.Access) (mediaanalysis.Image, error) {
			return d.Analysis.ChapterImage(ctx, a, chapter)
		})
	}); e != nil {
		return e
	}
	if e := registerSubtitleRaw[struct{}, SubtitleBytesContract](registry, "GET /v1/items/{id}/trickplay/{set}/tiles/{tile}.jpg", func(w http.ResponseWriter, r *http.Request) {
		set, name := r.PathValue("set"), r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, ok := strings.CutSuffix(name, ".jpg")
		index, e := strconv.Atoi(raw)
		if !ok || e != nil || raw != strconv.Itoa(index) {
			analysisFailure(w, mediaanalysis.ErrInput)
			return
		}
		d.trickplayImage(w, r, func(ctx context.Context, a mediaanalysis.Access) (mediaanalysis.Image, error) {
			return d.Analysis.Tile(ctx, a, set, index)
		})
	}); e != nil {
		return e
	}
	return nil
}

func previewETagMatches(header, etag string) bool {
	if len(header) > 1024 {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}
