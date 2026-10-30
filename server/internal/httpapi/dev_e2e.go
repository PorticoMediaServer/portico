//go:build devtrust && !release

package httpapi

import (
	"net/http"
	"net/netip"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The development e2e owner, the server-side counterpart of Hosted's
// /dev/e2e/session: on a development server with a throwaway state directory
// (identity.Service.DevE2EUsername), a browser or simulator on this machine
// signs in as the seeded owner without a password. The answer is exactly a
// direct sign-in's, so the client stores and refreshes the session through its
// real path; no credential is typed, printed or read. It answers only a
// loopback peer, and only in devtrust builds (a test checks release builds).
//
//	GET  /v1/dev/e2e          {"username": "e2e-owner"}, or 404
//	POST /v1/dev/e2e/sign-in  {"username": "e2e-owner"} → the direct sign-in answer
func init() {
	devContracts = append(devContracts, func(registry *apikit.Registry) error {
		if err := apikit.RegisterHTTP[struct{}, struct {
			Username string `json:"username"`
		}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dev_e2e", Method: "GET", Path: "/v1/dev/e2e", Summary: "Read the development test owner", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, Status: 200, Errors: []string{"not_found", "internal"}}); err != nil {
			return err
		}
		return apikit.RegisterHTTP[struct {
			Username string `json:"username"`
		}, identity.DirectSignIn](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_dev_e2e_sign_in", Method: "POST", Path: "/v1/dev/e2e/sign-in", Summary: "Sign in as the development test owner", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "not_found", "unauthorized", "internal"}})
	})
	routeLanes["GET /v1/dev/e2e"] = laneAuth
	routeLanes["POST /v1/dev/e2e/sign-in"] = laneAuth
	devRoutes = append(devRoutes, func(d Dependencies, mux *http.ServeMux) {
		d.httpHandle(mux, "GET /v1/dev/e2e", func(w http.ResponseWriter, r *http.Request) {
			name, ok := d.Identity.DevE2EUsername()
			if !ok || !devE2ELoopbackPeer(r) {
				devE2ENotFound(w)
				return
			}
			write(w, 200, map[string]string{"username": name})
		})
		d.httpHandle(mux, "POST /v1/dev/e2e/sign-in", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := d.Identity.DevE2EUsername(); !ok || !devE2ELoopbackPeer(r) {
				devE2ENotFound(w)
				return
			}
			var q struct {
				Username string `json:"username"`
			}
			if e := decode(w, r, &q); e != nil {
				failure(w, e)
				return
			}
			out, e := d.Identity.DevE2ESignIn(r.Context(), q.Username)
			if e != nil {
				failure(w, e)
				return
			}
			writeAuth(w, r, 200, out)
		})
	})
}

// Development sign-in trusts only the socket peer, never forwarded addresses.
func devE2ELoopbackPeer(r *http.Request) bool {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	return err == nil && peer.Addr().Unmap().IsLoopback()
}

func devE2ENotFound(w http.ResponseWriter) {
	write(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "Not found.", "retryable": false}})
}
