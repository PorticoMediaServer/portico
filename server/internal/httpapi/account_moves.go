package httpapi

import (
	"net/http"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// MoveCollisionDetails is ApiError.current only for move_collision.
// It is safe identity context for the holder of a valid Waiting proof.
type MoveCollisionDetails struct {
	ExistingUsername string `json:"existingUsername"`
	ServerName       string `json:"serverName"`
}
type moveStatus struct {
	Move *identity.AccountMove `json:"move"`
}
type moveConfirmation struct {
	Move   *identity.AccountMove  `json:"move,omitempty"`
	SignIn *identity.DirectSignIn `json:"signIn,omitempty"`
}
type moveRevision struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}
type porticoMoveRedemption struct {
	Code            string        `json:"code,omitempty"`
	Ticket          string        `json:"ticket,omitempty"`
	PorticoIdentity hosted.Signed `json:"porticoIdentity"`
}

func (d Dependencies) accountMoveRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/sign-in", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.ReadSignInSettings(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/sign-in/move", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.AccountMove(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, moveStatus{out})
	})
	d.httpHandle(mux, "GET /v1/admin/sign-in/move/waiting", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.MoveWaiting(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, moveWaitingList{Items: out.Items, Truncated: out.Truncated})
	})
	d.httpHandle(mux, "POST /v1/admin/sign-in/move/preview", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var q identity.MovePreviewRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if q.To == identity.SignInPortico && d.ownerSwitchWebOrigin(r) == "" {
			failure(w, identity.ErrForbidden)
			return
		}
		out, e := d.Identity.PreviewAccountMove(r.Context(), directBearer(r), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/sign-in/move/confirm", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var q identity.MoveConfirmRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		move, e := d.Identity.AccountMove(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		if move == nil || move.ID != q.MoveID {
			failure(w, identity.ErrProfileChanged)
			return
		}
		if move.To == identity.SignInLocal {
			out, e := d.Identity.ConfirmLocalMove(r.Context(), directBearer(r), q)
			if e != nil {
				failure(w, e)
				return
			}
			if d.Hosted != nil {
				d.Hosted.MembershipChanged()
			}
			writeAuth(w, r, 200, moveConfirmation{SignIn: &out})
			return
		}
		if d.ownerSwitchWebOrigin(r) == "" {
			failure(w, identity.ErrForbidden)
			return
		}
		out, e := d.Identity.ConfirmPorticoMove(r.Context(), directBearer(r), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, moveConfirmation{Move: &out})
	})
	d.httpHandle(mux, "POST /v1/admin/sign-in/move/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var q moveRevision
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.CancelAccountMove(r.Context(), directBearer(r), r.PathValue("id"), q.ExpectedRevision); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/admin/sign-in/move/{id}/finish", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var q moveRevision
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.FinishAccountMove(r.Context(), directBearer(r), r.PathValue("id"), q.ExpectedRevision); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/admin/sign-in/move/codes", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var q identity.MoveCodeRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.MakeMoveCodes(r.Context(), directBearer(r), q)
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/move/password", func(w http.ResponseWriter, r *http.Request) {
		var q identity.MovePasswordRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "move-password", 10); e != nil {
			setupFailure(w, e)
			return
		}
		out, e := d.Identity.ProveMovePassword(r.Context(), q)
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/move/code", func(w http.ResponseWriter, r *http.Request) {
		var q identity.MoveCodePreviewRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "move-code-preview", 20); e != nil {
			setupFailure(w, e)
			return
		}
		out, e := d.Identity.PreviewMoveCode(r.Context(), q.Code)
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/move/local", func(w http.ResponseWriter, r *http.Request) {
		var q identity.LocalMoveRedemption
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "move-code", 20); e != nil {
			setupFailure(w, e)
			return
		}
		out, e := d.Identity.RedeemLocalMove(r.Context(), q)
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/move/portico", func(w http.ResponseWriter, r *http.Request) {
		if e := d.Identity.RequireSignInKind(r.Context(), identity.SignInPortico); e != nil {
			failure(w, e)
			return
		}
		var q porticoMoveRedemption
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if d.Hosted == nil {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "move-link", 20); e != nil {
			setupFailure(w, e)
			return
		}
		who, e := d.Hosted.VerifyIdentity(r.Context(), q.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id"))
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.RedeemPorticoMove(r.Context(), identity.PorticoMoveProof{Code: q.Code, Ticket: q.Ticket, Who: who})
		if e != nil {
			failure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
}

// moveWaitingList is the published target read: bounded to one sheet's 2000
// accounts and explicit about a larger waiting population.
type moveWaitingList struct {
	Items     []identity.MoveWaiting `json:"items"`
	Truncated bool                   `json:"truncated"`
}

func (moveWaitingList) JSONSchema() map[string]any {
	type wire struct {
		Items     []identity.MoveWaiting `json:"items"`
		Truncated bool                   `json:"truncated"`
	}
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	items := shape["properties"].(map[string]any)["items"].(map[string]any)
	items["maxItems"] = 2000
	itemObject := items["items"].(map[string]any)
	item := itemObject["properties"].(map[string]any)
	for key, limit := range map[string]int{"accountId": 128, "username": 64} {
		field := item[key].(map[string]any)
		field["minLength"] = 1
		field["maxLength"] = limit
	}
	// The existing code's own expiry is optional and never nullable: an absent
	// field means this person has no working code, not an unknown date. It is the
	// observed expiry at read time, not a promise the code still redeems.
	item["expiresAt"] = map[string]any{
		"type": "string", "format": "date-time", "minLength": 20, "maxLength": 35,
		"pattern": `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|\+00:00)$`,
	}
	if required, ok := itemObject["required"].([]string); ok {
		kept := make([]string, 0, len(required))
		for _, name := range required {
			if name != "expiresAt" {
				kept = append(kept, name)
			}
		}
		itemObject["required"] = kept
	}
	return shape
}

func registerAccountMoveContracts(registry *apikit.Registry) error {
	owner := func(id, method, path string, status int) apikit.Metadata {
		bodyLimit := int64(0)
		if method != http.MethodGet && method != http.MethodHead {
			bodyLimit = 16384
		}
		return apikit.Metadata{Audience: apikit.Clients, ID: id, Method: method, Path: path, Summary: "Manage the server sign-in move", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: bodyLimit, Status: status, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "revision_mismatch", "sign_in_kind_portico", "sign_in_kind_local", "move_collision", "move_confirmation", "move_code_invalid", "internal"}}
	}
	if e := apikit.RegisterHTTP[struct{}, identity.SignInSettings](registry, owner("get_sign_in_settings", "GET", "/v1/admin/sign-in", 200)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[struct{}, moveStatus](registry, owner("get_sign_in_move", "GET", "/v1/admin/sign-in/move", 200)); e != nil {
		return e
	}
	if e := func() error {
		m := owner("get_sign_in_move_waiting", "GET", "/v1/admin/sign-in/move/waiting", 200)
		// The read joins and orders up to 2000 targets, so it is page-sized.
		m.Cost = apikit.PageSized
		return apikit.RegisterHTTP[struct{}, moveWaitingList](registry, m)
	}(); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[identity.MovePreviewRequest, identity.AccountMove](registry, owner("post_sign_in_move_preview", "POST", "/v1/admin/sign-in/move/preview", 200)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[identity.MoveConfirmRequest, moveConfirmation](registry, owner("post_sign_in_move_confirm", "POST", "/v1/admin/sign-in/move/confirm", 200)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[moveRevision, struct{}](registry, owner("post_sign_in_move_cancel", "POST", "/v1/admin/sign-in/move/{id}/cancel", 204)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[moveRevision, struct{}](registry, owner("post_sign_in_move_finish", "POST", "/v1/admin/sign-in/move/{id}/finish", 204)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[identity.MoveCodeRequest, identity.MoveCodes](registry, owner("post_sign_in_move_codes", "POST", "/v1/admin/sign-in/move/codes", 200)); e != nil {
		return e
	}
	public := func(id, path string) apikit.Metadata {
		m := owner(id, "POST", path, 200)
		m.Access = apikit.Public
		m.Admission = laneAuth
		return m
	}
	if e := apikit.RegisterHTTP[identity.LocalMoveRedemption, identity.DirectSignIn](registry, public("post_auth_move_local", "/v1/auth/move/local")); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[identity.MovePasswordRequest, identity.MoveLinkTicket](registry, public("post_auth_move_password", "/v1/auth/move/password")); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[identity.MoveCodePreviewRequest, identity.MoveCodePreview](registry, public("post_auth_move_code", "/v1/auth/move/code")); e != nil {
		return e
	}
	return apikit.RegisterHTTP[porticoMoveRedemption, identity.DirectSignIn](registry, public("post_auth_move_portico", "/v1/auth/move/portico"))
}
