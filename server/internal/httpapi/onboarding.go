package httpapi

import (
	"context"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strconv"
	"time"
)

// Recovery and sign-in use the same owner-configured home networks as playback.
func (d Dependencies) privateSetupPeer(r *http.Request) bool { return !d.requestIsRemote(r) }
func (d Dependencies) signInPeer(r *http.Request) string     { return d.setupPeerSubject(r) }

func (d Dependencies) setupPeerSubject(r *http.Request) string {
	return clientAddress(r, d.trustedProxyPrefixes(r.Context()))
}
func setupFailure(w http.ResponseWriter, e error) {
	var protocol *identity.SetupProtocolError
	if errors.As(e, &protocol) {
		status := 400
		retryable := false
		if protocol.Code == "rate_limited" {
			status = 429
			retryable = true
		}
		if protocol.Code == "authorization_pending" || protocol.Code == "slow_down" {
			retryable = true
		}
		if protocol.Interval > 0 {
			w.Header().Set("Retry-After", fmtSetupInterval(protocol.Interval))
		}
		write(w, status, map[string]any{"error": map[string]any{"code": protocol.Code, "message": protocol.Code, "retryable": retryable}})
		return
	}
	failure(w, e)
}
func fmtSetupInterval(n int) string {
	if n < 5 {
		n = 5
	}
	return strconv.Itoa(n)
}
func (d Dependencies) onboardingRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/setup/resume", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !d.setupRequestAllowed(r) {
			failure(w, identity.ErrUnauthorized)
			return
		}
		var q struct {
			RequestID  string `json:"requestId"`
			SetupToken string `json:"setupToken"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "resume", 20); e != nil {
			setupFailure(w, e)
			return
		}
		out, e := d.Identity.ResumeSetup(r.Context(), q.RequestID, q.SetupToken, d.privateSetupPeer(r))
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "GET /v1/setup/state", d.serveSetupState)
	d.httpHandle(mux, "POST /v1/quick-connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if e := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), "quick-create", 10); e != nil {
			setupFailure(w, e)
			return
		}
		var q identity.QuickStart
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.StartQuick(ctx, q, d.privateSetupPeer(r))
		if e != nil {
			setupFailure(w, e)
			return
		}
		// A code for this server is approved on this server (its /device page, or
		// any app signed in to it), never on the Portico Account site.
		if origin := d.quickApprovalOrigin(r); origin != "" {
			out.VerificationURI = origin + "/device"
			out.VerificationURIComplete = origin + "/device#code=" + out.UserCode
		}
		write(w, 201, out)
	})
	d.httpHandle(mux, "POST /v1/quick-connect/review", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		for _, subject := range []string{"ip:" + d.setupPeerSubject(r), "account:" + p.AccountID} {
			if e = d.Identity.SetupLimit(r.Context(), subject, "quick-review", 10); e != nil {
				setupFailure(w, e)
				return
			}
		}
		var q struct {
			UserCode string `json:"userCode"`
		}
		if e = decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.ReviewQuick(r.Context(), q.UserCode)
		if e != nil {
			setupFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/quick-connect/decision", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		for _, subject := range []string{"ip:" + d.setupPeerSubject(r), "account:" + p.AccountID} {
			if e = d.Identity.SetupLimit(r.Context(), subject, "quick-decision", 10); e != nil {
				setupFailure(w, e)
				return
			}
		}
		var q struct {
			RequestID string `json:"requestId"`
			UserCode  string `json:"userCode"`
			Decision  string `json:"decision"`
		}
		if e = decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e = d.Identity.DecideQuick(r.Context(), p, q.RequestID, q.UserCode, q.Decision); e != nil {
			setupFailure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	for _, action := range []string{"token", "cancel"} {
		d.httpHandle(mux, "POST /v1/quick-connect/"+action, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var q struct {
				DeviceCode string `json:"deviceCode"`
			}
			if e := decode(w, r, &q); e != nil {
				failure(w, e)
				return
			}
			if action == "cancel" {
				if e := d.Identity.CancelQuick(ctx, q.DeviceCode); e != nil {
					setupFailure(w, e)
					return
				}
				w.WriteHeader(204)
				return
			}
			out, e := d.Identity.PollQuick(ctx, q.DeviceCode)
			if e != nil {
				setupFailure(w, e)
				return
			}
			writeAuth(w, r, 200, out)
		})
	}
}

// quickApprovalOrigin is the origin the new device reached this server on. The
// answer goes only to that device, so it cannot redirect anyone else.
func (d Dependencies) quickApprovalOrigin(r *http.Request) string {
	return networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context()))
}
