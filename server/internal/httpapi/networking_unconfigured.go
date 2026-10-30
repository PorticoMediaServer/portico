package httpapi

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// A Direct Sign-In server can have no Hosted claim controller at all. Its
// network-settings reads are still documents, not missing routes, so clients
// can render their unconfigured state without treating it as a transport fault.
func (d Dependencies) unconfiguredNetworkingRoutes(mux *http.ServeMux) {
	for _, path := range []string{"/v1/networking/claim", "/v1/networking/certificate", "/v1/networking/remote"} {
		d.httpHandle(mux, "GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if _, err := d.owner(r); err != nil {
				administrationFailure(w, err)
				return
			}
			switch r.URL.Path {
			case "/v1/networking/claim":
				write(w, 200, networking.ClaimStatus{State: "unconfigured", Identity: networking.ClaimIdentityExpected{ServerID: d.Identity.ID(), LocalGeneration: 0}, Actions: []string{}})
			case "/v1/networking/certificate":
				write(w, 200, networking.CertificateStatus{State: "unconfigured", Environment: "production", Reachability: "probe_required", Config: networking.CertificateConfig{PublicPort: 443, Revision: 1}})
			case "/v1/networking/remote":
				write(w, 200, networking.RemoteStatus{State: "unconfigured", Generation: 1, Config: networking.RemoteConfig{Revision: 1, PCP: true, NATPMP: true, UPnP: true, PublicPort: 32500}, Topology: networking.Topology{LAN: []string{}, Public: []string{}}, Candidates: []networking.RemoteCandidate{}, Mappings: []networking.MappingSummary{}})
			}
		})
	}
}
