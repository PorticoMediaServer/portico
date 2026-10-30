package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
)

// httpHandle retains a handler's HTTP and access behavior while requiring its
// wire types, credential scope and admission class on the shared registry.
func (d Dependencies) httpHandle(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	d.httpHandler(mux, pattern, handler)
}
func (d Dependencies) httpHandler(mux *http.ServeMux, pattern string, handler http.Handler) {
	handler = d.meteredMedia(pattern, handler)
	mountPattern := pattern
	if pattern == "GET /v1/metadata/{kind}/{id}/preview" {
		// A literal final /preview crosses the bulk preview-query route's fixed
		// prefix. Dispatch this existing contract through an all-wildcard tail;
		// the canonical bulk path is then strictly more specific to ServeMux.
		mountPattern = muxPattern("GET", "/v1/metadata/{kind}/{id}/preview")
		preview := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("metadataAction") != "preview" {
				http.NotFound(w, r)
				return
			}
			preview.ServeHTTP(w, r)
		})
	}
	if d.httpContractsPrivate {
		mux.Handle(mountPattern, handler)
		return
	}
	registry := d.registry
	if registry == nil {
		var err error
		registry, err = FoundationRegistry(d)
		if err != nil {
			panic(err)
		}
	}
	if err := registry.MountHTTPAt(mux, pattern, mountPattern, handler); err != nil {
		panic(err)
	}
}

func registerHTTPContracts(registry *apikit.Registry) error {
	if err := registerReadingHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerLibrariesHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerBrowseHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerMusicMixesHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerMetadataHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerPersonalHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerLiveHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerPlaybackHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerAdministrationHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerSegmentHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerSetupHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerIdentityHTTPContracts(registry); err != nil {
		return err
	}
	for _, register := range devContracts {
		if err := register(registry); err != nil {
			return err
		}
	}
	return nil
}

// httpRegistrar lets a service bind handlers through the same typed registry.
type httpRegistrar struct {
	d   Dependencies
	mux *http.ServeMux
}

func (r httpRegistrar) Handle(pattern string, h http.Handler) { r.d.httpHandler(r.mux, pattern, h) }
func (r httpRegistrar) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	r.d.httpHandle(r.mux, pattern, h)
}
