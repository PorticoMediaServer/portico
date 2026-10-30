package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func registerIntegrationSubtitleValidation(registry *apikit.Registry, d Dependencies) error {
	return apikit.RegisterRaw[struct{}, subtitles.ValidationReceipt](registry, apikit.RawRoute[struct{}, subtitles.ValidationReceipt]{Metadata: apikit.Metadata{ID: "validate_integration_subtitles", Method: "POST", Path: "/v1/integrations/items/{id}/subtitles/refresh", Summary: "Reread exact subtitle sidecars using stored media evidence", Audience: apikit.Integrations, Access: apikit.Owner, Requires: apikit.ScopeLibrary, Admission: laneDefault, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 4096, Status: 200, Errors: subtitleRouteErrors()}, Serve: func(w http.ResponseWriter, r *http.Request) {
		if d.Subtitles == nil {
			subtitleFailure(w, subtitles.ErrUnavailable)
			return
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			subtitleFailure(w, err)
			return
		}
		var body struct{}
		if err = subtitleDecode(w, r, &body); err != nil {
			subtitleFailure(w, err)
			return
		}
		out, err := d.Subtitles.ValidateItem(r.Context(), p, r.PathValue("id"))
		if err != nil {
			subtitleFailure(w, err)
			return
		}
		write(w, 200, out)
	}})
}
