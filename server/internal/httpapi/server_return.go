package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
	"reflect"
	"strings"
)

type porticoReturnPrepareRequest struct {
	Origin string `json:"origin"`
	Nonce  string `json:"nonce"`
}
type porticoReturnProof struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type porticoReturnPrepared struct {
	hosted.RegisteredReturnPreparation
	Challenge string             `json:"challenge"`
	ExpiresAt string             `json:"expiresAt"`
	Proof     porticoReturnProof `json:"proof"`
	Context   porticoReturnProof `json:"context"`
}

type registeredSignInPrepareSigner interface {
	SignRegisteredSignInPrepare(context.Context, networking.RegisteredSignInPrepareInput) (networking.RegisteredSignInPrepare, string, string, error)
}

type porticoReturnRequest struct {
	hosted.RegisteredReturnExchange
	RequestID        string                      `json:"requestId,omitempty"`
	SetupToken       string                      `json:"setupToken,omitempty"`
	ExpectedRevision int64                       `json:"expectedRevision,string,omitempty"`
	MoveTicket       string                      `json:"moveTicket,omitempty"`
	MoveCode         string                      `json:"moveCode,omitempty"`
	Setup            *serverreturn.SetupExchange `json:"-"`
	Owner            *serverreturn.OwnerExchange `json:"-"`
}

type normalPorticoReturnRequest porticoReturnRequest

func (q *porticoReturnRequest) UnmarshalJSON(raw []byte) error {
	*q = porticoReturnRequest{}
	fields, err := wirejson.Object(raw, "code", "transactionId", "state", "verifier", "registrationId", "installationId", "serverChallenge", "purpose", "requestId", "setupToken", "expectedRevision", "moveTicket", "moveCode")
	if err != nil {
		return identity.ErrDirectInput
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return identity.ErrDirectInput
		}
	}
	if _, owner := fields["state"]; owner {
		var purpose string
		if json.Unmarshal(fields["purpose"], &purpose) != nil {
			return identity.ErrDirectInput
		}
		if purpose == "setup" {
			var next serverreturn.SetupExchange
			if json.Unmarshal(raw, &next) != nil {
				return identity.ErrDirectInput
			}
			q.Setup = &next
			return nil
		}
		var next serverreturn.OwnerExchange
		if json.Unmarshal(raw, &next) != nil {
			return identity.ErrDirectInput
		}
		q.Owner = &next
		return nil
	}
	if _, hasCode := fields["code"]; !hasCode {
		return identity.ErrDirectInput
	}
	var next normalPorticoReturnRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&next) != nil {
		return identity.ErrDirectInput
	}
	*q = porticoReturnRequest(next)
	return nil
}

func (porticoReturnRequest) JSONSchema() map[string]any {
	normal, err := apikit.Schema(reflect.TypeFor[normalPorticoReturnRequest]())
	if err != nil {
		panic(err)
	}
	normal["additionalProperties"] = false
	return map[string]any{"oneOf": []any{normal, (serverreturn.OwnerExchange{}).JSONSchema(), (serverreturn.SetupExchange{}).JSONSchema()}}
}

func (d Dependencies) serverReturnRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/auth/portico-return/prepare", func(w http.ResponseWriter, r *http.Request) {
		if e := d.Identity.RequireSignInKind(r.Context(), identity.SignInPortico); e != nil {
			failure(w, e)
			return
		}
		var q porticoReturnPrepareRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		u, e := url.Parse(q.Origin)
		nonce, nerr := base64.RawURLEncoding.Strict().DecodeString(q.Nonce)
		installation := r.Header.Get("X-Portico-Installation-Id")
		if e != nil || !networking.RouteOrigin(q.Origin) || u.Scheme != "https" || !networkpolicy.HostsMatch(u.Host, r.Host) || nerr != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != q.Nonce || !identity.ValidInstallationID(installation) {
			failure(w, identity.ErrDirectInput)
			return
		}
		signer, ok := d.RouteIdentity.(challengeSigner)
		contextSigner, contextOK := d.RouteIdentity.(registeredSignInPrepareSigner)
		if d.Hosted == nil || !ok || !contextOK {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e = d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "portico-return-prepare", 30); e != nil {
			setupFailure(w, e)
			return
		}
		prepared, e := d.Hosted.PrepareRegisteredReturn(r.Context(), q.Origin)
		if e != nil {
			failure(w, e)
			return
		}
		challenge, expires, e := d.Hosted.IssueChallenge(r.Context(), installation)
		if e != nil {
			failure(w, e)
			return
		}
		payload, signature, e := signer.SignChallenge(r.Context(), challenge, q.Nonce, installation, expires)
		if e != nil {
			failure(w, e)
			return
		}
		bound, contextPayload, contextSignature, e := contextSigner.SignRegisteredSignInPrepare(r.Context(), networking.RegisteredSignInPrepareInput{ServerID: prepared.ServerID, RegistrationID: prepared.Registration.ID, RegistrationOrigin: prepared.Registration.Origin, HostedOrigin: prepared.HostedOrigin, ClaimOperationID: prepared.ClaimOperationID, Challenge: challenge, Nonce: q.Nonce, InstallationID: installation})
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, porticoReturnPrepared{RegisteredReturnPreparation: prepared, Challenge: challenge, ExpiresAt: bound.ExpiresAt, Proof: porticoReturnProof{payload, signature}, Context: porticoReturnProof{contextPayload, contextSignature}})
	})
	d.httpHandle(mux, "POST /v1/auth/portico-return/exchange", func(w http.ResponseWriter, r *http.Request) {
		var q porticoReturnRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if q.Setup != nil {
			d.completeSetupReturnHTTP(w, r, *q.Setup)
			return
		}
		if q.Owner != nil {
			d.completeOwnerReturnHTTP(w, r, *q.Owner)
			return
		}
		if (q.MoveTicket != "" && q.MoveCode != "") || (q.Purpose != "sign-in" && (q.MoveTicket != "" || q.MoveCode != "")) {
			failure(w, identity.ErrDirectInput)
			return
		}
		if q.Purpose == "setup" && !d.setupRequestAllowed(r) {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if d.Hosted == nil || q.InstallationID != r.Header.Get("X-Portico-Installation-Id") {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "portico-return", 30); e != nil {
			setupFailure(w, e)
			return
		}
		who, e := d.Hosted.ExchangeRegisteredReturn(r.Context(), q.RegisteredReturnExchange)
		if e != nil {
			failure(w, e)
			return
		}
		var out identity.DirectSignIn
		if who.Return.Purpose == "sign-in" {
			if q.MoveTicket != "" || q.MoveCode != "" {
				out, e = d.Identity.RedeemPorticoMove(r.Context(), identity.PorticoMoveProof{Ticket: q.MoveTicket, Code: q.MoveCode, Who: who})
			} else {
				out, e = d.Identity.PorticoSignIn(r.Context(), who)
			}
		} else if who.Return.Purpose == "setup" {
			// Setup is never a registered return: it has its own transport and names no
			// registration (O-163). Its only admission is the setup exchange.
			e = identity.ErrUnauthorized
		} else {
			out, e = d.Identity.CompletePorticoOwnerClaim(r.Context(), directBearer(r), q.ExpectedRevision, who)
		}
		if e != nil {
			failure(w, e)
			return
		}
		if d.Hosted != nil {
			d.Hosted.MembershipChanged()
		}
		writeAuth(w, r, 200, out)
	})
}

func (d Dependencies) completeOwnerReturnHTTP(w http.ResponseWriter, r *http.Request, q serverreturn.OwnerExchange) {
	installation := r.Header.Get("X-Portico-Installation-Id")
	authorization := r.Header.Get("Authorization")
	if d.Hosted == nil || d.Networking == nil || len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(authorization, "Bearer ") || strings.ContainsAny(strings.TrimPrefix(authorization, "Bearer "), " ,\r\n") || len(r.Cookies()) != 0 || len(r.Header.Values("X-Portico-Installation-Id")) != 1 || q.InstallationID != installation {
		failure(w, identity.ErrUnauthorized)
		return
	}
	origin := d.ownerSwitchWebOrigin(r)
	if origin == "" {
		failure(w, identity.ErrUnauthorized)
		return
	}
	if err := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "owner-return", 30); err != nil {
		setupFailure(w, err)
		return
	}
	out, err := d.Hosted.ExchangeOwnerCompletion(r.Context(), d.Networking, strings.TrimPrefix(authorization, "Bearer "), origin, q)
	if err != nil {
		failure(w, err)
		return
	}
	d.Hosted.MembershipChanged()
	writeAuth(w, r, http.StatusOK, out)
}
func (d Dependencies) completeSetupReturnHTTP(w http.ResponseWriter, r *http.Request, q serverreturn.SetupExchange) {
	installation := r.Header.Get("X-Portico-Installation-Id")
	authorization := r.Header.Get("Authorization")
	if d.Hosted == nil || d.Networking == nil || len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(authorization, "Bearer ") || strings.ContainsAny(strings.TrimPrefix(authorization, "Bearer "), " ,\r\n") || len(r.Cookies()) != 0 || len(r.Header.Values("X-Portico-Installation-Id")) != 1 || q.InstallationID != installation {
		failure(w, identity.ErrUnauthorized)
		return
	}
	origin := d.ownerSwitchWebOrigin(r)
	if origin == "" {
		failure(w, identity.ErrUnauthorized)
		return
	}
	if err := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "setup-return", 30); err != nil {
		setupFailure(w, err)
		return
	}
	out, err := d.Hosted.ExchangeSetupCompletion(r.Context(), d.Networking, strings.TrimPrefix(authorization, "Bearer "), origin, q)
	if err != nil {
		failure(w, err)
		return
	}
	d.Hosted.MembershipChanged()
	writeAuth(w, r, http.StatusOK, out)
}
func registerServerReturnHTTPContracts(registry *apikit.Registry) error {
	if e := apikit.RegisterHTTP[porticoReturnPrepareRequest, porticoReturnPrepared](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_auth_portico_return_prepare", Method: "POST", Path: "/v1/auth/portico-return/prepare", Summary: "Prepare a return from an existing verified server origin", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 2048, Status: 200, Headers: []string{"X-Portico-Installation-Id"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "rate_limited", "sign_in_kind_local", "portico_services_unavailable", "internal"}}); e != nil {
		return e
	}
	return apikit.RegisterHTTP[porticoReturnRequest, identity.DirectSignIn](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_auth_portico_return_exchange", Method: "POST", Path: "/v1/auth/portico-return/exchange", Summary: "Complete a registered Portico Account return", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 16384, Status: 200, Headers: []string{"X-Portico-Installation-Id"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "rate_limited", "sign_in_kind_portico", "sign_in_kind_local", "access_refused", "account_move_waiting", "move_collision", "move_code_invalid", "portico_services_unavailable", "internal"}})
}
