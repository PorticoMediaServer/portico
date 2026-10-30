package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// serveOriginal serves a presentation's original file with Range: through the
// remote-source relay when the source is remote, else from local storage. check
// re-authorizes the grant while bytes flow. decode is true only for the version 2
// audio route, whose grant may be a prepared (private) presentation's.
func (d Dependencies) serveOriginal(w http.ResponseWriter, r *http.Request, grant, aid string, decode bool, check func() error) {
	release, admissionErr := childprocess.AcquirePublication(r.Context())
	if admissionErr != nil {
		failure(w, admissionErr)
		return
	}
	response, close, remote, remoteErr := d.Playback.OpenRemote(r.Context(), grant, r.Method, r.Header.Get("Range"), decode)
	release()
	if remoteErr != nil {
		failure(w, remoteErr)
		return
	}
	if remote {
		defer close()
		reader := &guardedReader{Reader: response.Body, check: newStreamAuthority(check).check}
		body := withRollingDeadline(w)
		defer body.release()
		_ = remotemedia.CopyResponseAs(body, response, reader, response.Header.Get("Content-Type"))
		if reader.readError != nil && r.Context().Err() == nil {
			d.Playback.RemoteReadFailed(grant)
		}
		return
	}
	if d.Storage == nil {
		failure(w, storage.ErrPlaybackSource)
		return
	}
	release, admissionErr = childprocess.AcquirePublication(r.Context())
	if admissionErr != nil {
		failure(w, admissionErr)
		return
	}
	f, e := assets.OpenIsolatedPlayback(r.Context(), d.DB, d.Storage, "", identity.Digest(grant))
	release()
	if e != nil {
		failure(w, e)
		return
	}
	defer f.Close()

	w.Header().Set("Cache-Control", "private, no-store")
	var container, video string
	_ = dbwork.QueryRow(r.Context(), d.DB, `SELECT container,video_codec FROM catalog_assets WHERE token=?`, aid).Scan(&container, &video)
	w.Header().Set("Content-Type", originalContentType(container, video))
	body := withRollingDeadline(w)
	defer body.release()
	http.ServeContent(body, r, "media."+container, f.Modified, &guardedReadSeeker{ReadSeeker: f, check: newStreamAuthority(check).check})
}

// originalContentType names an original file's media type (assets.OriginalContentType).
func originalContentType(container, video string) string {
	return assets.OriginalContentType(container, video)
}
