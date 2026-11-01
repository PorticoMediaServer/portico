package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// challengeSigner is the route identity handler's challenge proof
// (networking.RouteIdentityHandler.SignChallenge).
type challengeSigner interface {
	SignChallenge(ctx context.Context, challenge, nonce, installation string, expires time.Time) (string, string, error)
}

func directBearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (d Dependencies) directIdentityRoutes(mux *http.ServeMux) {
	d.accountMoveRoutes(mux)
	d.signInPolicyRoutes(mux)
	d.ownerResetFileRoutes(mux)
	d.serverReturnRoutes(mux)
	// A challenge for a Portico identity assertion: the installation asks this
	// server, has Hosted sign the challenge into its assertion, then signs in
	// with it. The answer is signed with this server's identity key over the
	// client's nonce, so the client knows the challenge is this server's
	// before it asks Hosted for anything (INT M7).
	d.httpHandle(mux, "POST /v1/direct/portico-challenge", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Identity.RequireSignInKind(r.Context(), identity.SignInPortico); err != nil {
			failure(w, err)
			return
		}
		var q struct {
			Nonce string `json:"nonce"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if q.Nonce == "" {
			// A client from before signed challenges (INT M7) sends none: say
			// so, so the app can ask for an update rather than show an error.
			write(w, 400, map[string]any{"error": map[string]any{"code": "client_update_required", "message": "Update the app to sign in to this server.", "retryable": false}})
			return
		}
		signer, ok := d.RouteIdentity.(challengeSigner)
		if d.Hosted == nil || !ok {
			failure(w, identity.ErrUnauthorized)
			return
		}
		installation := r.Header.Get("X-Portico-Installation-Id")
		challenge, expires, e := d.Hosted.IssueChallenge(r.Context(), installation)
		if e != nil {
			failure(w, e)
			return
		}
		payload, signature, e := signer.SignChallenge(r.Context(), challenge, q.Nonce, installation, expires)
		if errors.Is(e, networking.ErrInvalid) {
			failure(w, identity.ErrDirectInput)
			return
		}
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, map[string]any{"challenge": challenge, "expiresAt": expires.UTC().Format(time.RFC3339), "proof": map[string]string{"payload": payload, "signature": signature}})
	})
	d.httpHandle(mux, "POST /v1/direct/sign-in", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Username string `json:"username"`
			Password string `json:"password"`
			// PorticoIdentity replaces username and password for a Portico
			// Account member: a Hosted identity assertion for this server,
			// verified offline. Membership is this server's own decision.
			PorticoIdentity *hosted.Signed `json:"porticoIdentity,omitempty"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		var out identity.DirectSignIn
		var e error
		if q.PorticoIdentity != nil {
			if err := d.Identity.RequireSignInKind(r.Context(), identity.SignInPortico); err != nil {
				failure(w, err)
				return
			}
			if q.Username != "" || q.Password != "" || d.Hosted == nil {
				failure(w, identity.ErrUnauthorized)
				return
			}
			if e = d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "portico", 30); e != nil {
				setupFailure(w, e)
				return
			}
			var who identity.PorticoIdentity
			if who, e = d.Hosted.VerifyIdentity(r.Context(), *q.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id")); e == nil {
				out, e = d.Identity.PorticoSignIn(r.Context(), who)
			}
		} else {
			out, e = d.Identity.DirectLoginFrom(r.Context(), q.Username, q.Password, d.privateSetupPeer(r))
		}
		if e != nil {
			failure(w, e)
			return
		}
		// The remote sign-in policy and the member's access schedule are applied
		// where the session is created (identity.IssueTx, through the request
		// context the router prepares), not here.
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/profiles", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Name string `json:"name"`
			Art  string `json:"art"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.CreateDirectProfile(r.Context(), directBearer(r), q.Name, q.Art)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 201, out)
	})
	d.httpHandle(mux, "PATCH /v1/direct/profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		var q identity.DirectProfileEdit
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.EditDirectProfile(r.Context(), directBearer(r), r.PathValue("id"), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/direct/profiles/order", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Profiles []identity.ProfileOrderEntry `json:"profiles"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.OrderDirectProfiles(r.Context(), directBearer(r), q.Profiles)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/profiles/{id}/select", func(w http.ResponseWriter, r *http.Request) {
		var q identity.DirectSelection
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.SelectDirectProfileFrom(r.Context(), directBearer(r), r.PathValue("id"), q, d.privateSetupPeer(r))
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "GET /v1/direct/profiles/{id}/deletion-preview", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.PreviewDirectProfileDeletion(r.Context(), directBearer(r), r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "DELETE /v1/direct/profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		var q identity.ProfileRetirementRequest
		if err := decode(w, r, &q); err != nil {
			failure(w, err)
			return
		}
		out, err := d.Identity.RetireDirectProfile(r.Context(), directBearer(r), r.PathValue("id"), q)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/direct/profiles/{id}/retirement", func(w http.ResponseWriter, r *http.Request) {
		out, err := d.Identity.DirectProfileRetirement(r.Context(), directBearer(r), r.PathValue("id"), r.URL.Query().Get("lifetime"))
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, out)
	})

	d.httpHandle(mux, "DELETE /v1/direct/trust", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ProfileID      string `json:"profileId"`
			InstallationID string `json:"installationId"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.ForgetRememberedDevices(r.Context(), directBearer(r), q.ProfileID, q.InstallationID); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/direct/password", func(w http.ResponseWriter, r *http.Request) {
		var q identity.DirectPasswordChange
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.ChangeDirectPasswordWithReset(r.Context(), directBearer(r), q.CurrentPassword, q.NewPassword, q.Code, q.TurnOffTwoStep); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "GET /v1/direct/members", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.DirectMembers(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"items": out})
	})
	d.httpHandle(mux, "POST /v1/direct/members", func(w http.ResponseWriter, r *http.Request) {
		var q identity.DirectMemberCreate
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.CreateDirectMember(r.Context(), directBearer(r), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 201, out)
	})
	d.httpHandle(mux, "PATCH /v1/direct/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		var q identity.DirectMemberInput
		if e := decodeLegacyRevision(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.UpdateDirectMember(r.Context(), directBearer(r), r.PathValue("id"), q); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	// A Portico Account owner accepts Hosted's custody of this server's claim
	// (retiring it, its certificates) with its own custody assertion: after an
	// ownership transfer to it, or when custody was released (INT M6, M10).
	d.httpHandle(mux, "POST /v1/direct/ownership/custody", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			PorticoIdentity *hosted.Signed `json:"porticoIdentity"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if d.Hosted == nil || q.PorticoIdentity == nil {
			failure(w, identity.ErrUnauthorized)
			return
		}
		who, e := d.Hosted.VerifyCustody(r.Context(), *q.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id"))
		if e == nil {
			e = d.Identity.AcceptPorticoCustody(r.Context(), directBearer(r), who)
		}
		if e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/direct/ownership", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			AccountID        string                     `json:"accountId"`
			CurrentPassword  string                     `json:"currentPassword"`
			ExpectedRevision int64                      `json:"expectedRevision"`
			NewAccount       *identity.LocalOwnerCreate `json:"newAccount,omitempty"`
			// PorticoIdentity confirms a Portico Account owner, which has no
			// password here: a fresh Hosted identity assertion for that account.
			PorticoIdentity *hosted.Signed `json:"porticoIdentity,omitempty"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		// New local-account creation and existing/Hosted targets are closed,
		// mutually exclusive variants. Never create a member before proof/transfer.
		if q.NewAccount != nil {
			if q.AccountID != "" || q.PorticoIdentity != nil {
				failure(w, identity.ErrDirectInput)
				return
			}
			if e := d.Identity.TransferDirectOwnershipToNewAccount(r.Context(), directBearer(r), *q.NewAccount, q.CurrentPassword, q.ExpectedRevision); e != nil {
				failure(w, e)
				return
			}
			w.WriteHeader(204)
			return
		}
		if q.PorticoIdentity != nil {
			if d.Hosted == nil || q.CurrentPassword != "" {
				failure(w, identity.ErrUnauthorized)
				return
			}
			who, e := d.Hosted.VerifyIdentity(r.Context(), *q.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id"))
			if e == nil {
				e = d.Identity.TransferPorticoOwnership(r.Context(), directBearer(r), q.AccountID, who, q.ExpectedRevision)
			}
			if e != nil {
				failure(w, e)
				return
			}
			w.WriteHeader(204)
			return
		}
		if e := d.Identity.TransferDirectOwnership(r.Context(), directBearer(r), q.AccountID, q.CurrentPassword, q.ExpectedRevision); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
}
