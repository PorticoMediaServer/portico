package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type fileInfoPreparedDocument struct {
	caller readprep.Caller
	ready  *catalog.FileInfoOpeningReady
}
type fileInfoPreparedContextKey struct{}

// An opening preparation has no document yet. Its successful poll resumes the
// original route's composer under fresh admission; it never caches a Detail.
func (d Dependencies) prepareFileInfoDocument(w http.ResponseWriter, r *http.Request, kind string) (*http.Request, *fileInfoPreparedDocument, bool) {
	if d.FileInfoRuntime == nil {
		return r, nil, true
	}
	var itemKind int
	if e := d.DB.QueryRowContext(r.Context(), `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, r.PathValue("id")).Scan(&itemKind); e != nil {
		fileInfoFailure(w, e)
		return r, nil, false
	}
	// Reading documents keep their independent publication/admission protocol.
	if itemKind == 20 || itemKind == 21 {
		return r, nil, true
	}
	if ready, ok := r.Context().Value(fileInfoPreparedContextKey{}).(fileInfoPreparedDocument); ok {
		return r, &ready, true
	}
	ctx, caller, e := d.fileInfoContext(r)
	if e != nil {
		fileInfoFailure(w, e)
		return r, nil, false
	}
	out, e := d.FileInfoRuntime.PrepareOpening(ctx, caller, r.PathValue("id"), kind, r.URL.Query().Get("related"))
	defer out.Release()
	if e != nil {
		fileInfoFailure(w, e)
		return r, nil, false
	}
	if out.Preparing != nil {
		d.writeFileInfoWindow(w, r, out)
		return r, nil, false
	}
	if out.Opening == nil {
		fileInfoFailure(w, readprep.ErrStale)
		return r, nil, false
	}
	return r.WithContext(ctx), &fileInfoPreparedDocument{caller, out.Opening}, true
}

func (d Dependencies) writeFileInfoOpening(w http.ResponseWriter, r *http.Request, out catalog.FileInfoWindowResult) {
	if out.Opening == nil {
		fileInfoFailure(w, readprep.ErrStale)
		return
	}
	ctx, caller, e := d.fileInfoContext(r)
	if e != nil {
		fileInfoFailure(w, e)
		return
	}
	ready := fileInfoPreparedDocument{caller, out.Opening}
	r = r.Clone(context.WithValue(ctx, fileInfoPreparedContextKey{}, ready))
	r.SetPathValue("id", entityid.Encode(out.Opening.Identity.PublicItem[:]))
	q := r.URL.Query()
	q.Del("related")
	if out.Opening.Related != "" {
		q.Set("related", out.Opening.Related)
	}
	r.URL.RawQuery = q.Encode()
	if out.Opening.Kind == "detail" {
		d.detailReadHandler(w, r)
	} else {
		d.itemReadHandler(w, r)
	}
}
