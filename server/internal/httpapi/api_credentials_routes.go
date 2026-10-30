package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Routes for API keys (the owner's) and personal access tokens (any member's).
// Every one of them is session-only: a key or token cannot list, make or end
// keys and tokens, its own included.

// ScopeDocument is one scope a key or token can be given.
type ScopeDocument struct {
	ID string `json:"id"`
}

// CredentialOptions tells a console what it may offer.
type CredentialOptions struct {
	Scopes []ScopeDocument `json:"scopes"`
	// MaximumKeys and MaximumTokens are how many may be in use at once.
	MaximumKeys   int `json:"maximumKeys"`
	MaximumTokens int `json:"maximumTokens"`
	// TokensAllowed is false when the owner has turned personal access tokens
	// off for members. The owner and administrators may still make them.
	TokensAllowed bool `json:"tokensAllowed"`
	// RequestsPerMinute is the rate one key or token is held to.
	RequestsPerMinute int `json:"requestsPerMinute"`
	// QueryParameter is the query parameter a key or token may travel in on a
	// GET, for an address that works on its own (artwork, a stream, a
	// download). Anywhere else it goes in the Authorization header.
	QueryParameter string `json:"queryParameter"`
}

func credentialRouteError(err error) error {
	var fields *access.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fields):
		out := &apierror.Error{Code: "invalid_request"}
		for _, name := range fields.Fields {
			out.Fields = append(out.Fields, apierror.Field{Path: name, Code: "invalid", Message: "Check this value."})
		}
		return out
	case errors.Is(err, access.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "idempotency_key_reused"}
	case errors.Is(err, access.ErrCapacity):
		return &apierror.Error{Code: "credential_limit"}
	case errors.Is(err, access.ErrAccessTokensOff):
		return &apierror.Error{Code: "access_tokens_off"}
	case errors.Is(err, access.ErrCredentialMember):
		return &apierror.Error{Code: "credential_unavailable"}
	case errors.Is(err, identity.ErrCredentialInput), errors.Is(err, access.ErrInvalid):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrNotVisible),
		errors.Is(err, identity.ErrPasswordChangeRequired):
		return foundationAuthError(err)
	default:
		return &apierror.Error{Code: "internal", Cause: err}
	}
}

// sessionAuthority re-proves the caller's own live session inside the store's
// transaction, which is all a member needs to manage their own tokens.
func (d Dependencies) sessionAuthority(p identity.Principal) access.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		if d.Identity == nil {
			return identity.ErrUnauthorized
		}
		_, err := d.Identity.SessionFamilyTx(ctx, tx, p)
		return err
	}
}

func (d Dependencies) credentialOptions(ctx context.Context, p identity.Principal) (CredentialOptions, error) {
	out := CredentialOptions{Scopes: []ScopeDocument{}, MaximumKeys: access.MaxAPIKeys, MaximumTokens: access.MaxAccessTokens, TokensAllowed: true, RequestsPerMinute: d.apiRequestsPerMinute(ctx), QueryParameter: QueryCredentialParameter}
	for _, scope := range access.CredentialScopes {
		out.Scopes = append(out.Scopes, ScopeDocument{ID: scope})
	}
	out.TokensAllowed = false
	if e := d.DB.QueryRowContext(ctx, `SELECT access_tokens=1 AND disabled=0 FROM direct_memberships WHERE account_id=?`, p.AccountID).Scan(&out.TokensAllowed); e != nil {
		return out, e
	}
	return out, nil
}

func registerCredentialRoutes(registry *apikit.Registry, d Dependencies) error {
	store := func() (*access.Store, error) {
		if d.Access.Access == nil || d.Identity == nil {
			return nil, &apierror.Error{Code: "administration_unavailable"}
		}
		return d.Access.Access, nil
	}
	listErrors := []string{"unauthorized", "not_permitted", "session_required", "invalid_request", "administration_unavailable", "internal"}
	makeErrors := []string{"unauthorized", "not_permitted", "session_required", "invalid_request", "idempotency_key_reused", "credential_limit", "credential_unavailable", "access_tokens_off", "administration_unavailable", "internal"}
	endErrors := []string{"unauthorized", "not_permitted", "session_required", "invalid_request", "not_found", "administration_unavailable", "internal"}

	err := apikit.Register(registry, apikit.Route[struct{}, CredentialOptions]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "get_api_access_options", Method: "GET", Path: "/v1/me/api-access", Summary: "Read the scopes and limits for API keys and personal access tokens",
		Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "session_required", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (CredentialOptions, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return CredentialOptions{}, foundationAuthError(err)
		}
		out, err := d.credentialOptions(ctx, p)
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}

	// ---- the owner's API keys
	err = apikit.Register(registry, apikit.Route[struct{}, access.CredentialPage]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneAdmin, ID: "list_api_keys", Method: "GET", Path: "/v1/admin/api-keys", Summary: "List this server's API keys",
		Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(25, 100), Errors: listErrors,
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.CredentialPage, error) {
		s, err := store()
		if err != nil {
			return access.CredentialPage{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.CredentialPage{}, foundationAuthError(err)
		}
		out, err := s.Credentials(ctx, d.accessAuthority(p, identity.TierOwner), access.CredentialQuery{Kind: identity.CredentialKindKey, Cursor: r.URL.Query().Get("cursor"), Limit: r.URL.Query().Get("limit")})
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[access.CredentialRequest, access.IssuedCredential]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneSecurityFence, ID: "create_api_key", Method: "POST", Path: "/v1/admin/api-keys", Summary: "Create an API key; the owner may reveal it later",
		Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 201, Errors: makeErrors,
	}, Handler: func(ctx context.Context, r *http.Request, body access.CredentialRequest) (access.IssuedCredential, error) {
		s, err := store()
		if err != nil {
			return access.IssuedCredential{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.IssuedCredential{}, foundationAuthError(err)
		}
		out, err := s.CreateCredential(ctx, d.accessAuthority(p, identity.TierOwner), d.Identity, p, identity.CredentialKindKey, body)
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, access.Credential]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneSecurityFence, ID: "revoke_api_key", Method: "DELETE", Path: "/v1/admin/api-keys/{id}", Summary: "Revoke an API key",
		Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: endErrors,
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.Credential, error) {
		s, err := store()
		if err != nil {
			return access.Credential{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.Credential{}, foundationAuthError(err)
		}
		out, err := s.RevokeCredential(ctx, d.accessAuthority(p, identity.TierOwner), d.Identity, identity.CredentialKindKey, r.PathValue("id"), "")
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}

	// ---- a member's own tokens
	err = apikit.Register(registry, apikit.Route[struct{}, access.CredentialPage]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "list_access_tokens", Method: "GET", Path: "/v1/me/access-tokens", Summary: "List your personal access tokens",
		Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(25, 100), Errors: listErrors,
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.CredentialPage, error) {
		s, err := store()
		if err != nil {
			return access.CredentialPage{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.CredentialPage{}, foundationAuthError(err)
		}
		out, err := s.Credentials(ctx, d.sessionAuthority(p), access.CredentialQuery{Kind: identity.CredentialKindToken, AccountID: p.AccountID, Cursor: r.URL.Query().Get("cursor"), Limit: r.URL.Query().Get("limit")})
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[access.CredentialRequest, access.IssuedCredential]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneSecurityFence, ID: "create_access_token", Method: "POST", Path: "/v1/me/access-tokens", Summary: "Create a personal access token; its secret is shown once",
		Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 201, Errors: makeErrors,
	}, Handler: func(ctx context.Context, r *http.Request, body access.CredentialRequest) (access.IssuedCredential, error) {
		s, err := store()
		if err != nil {
			return access.IssuedCredential{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.IssuedCredential{}, foundationAuthError(err)
		}
		options, err := d.credentialOptions(ctx, p)
		if err != nil {
			return access.IssuedCredential{}, credentialRouteError(err)
		}
		if !options.TokensAllowed {
			return access.IssuedCredential{}, &apierror.Error{Code: "access_tokens_off"}
		}
		out, err := s.CreateCredential(ctx, d.sessionAuthority(p), d.Identity, p, identity.CredentialKindToken, body)
		return out, credentialRouteError(err)
	}})
	if err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, access.Credential]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneSecurityFence, ID: "revoke_access_token", Method: "DELETE", Path: "/v1/me/access-tokens/{id}", Summary: "Revoke one of your personal access tokens",
		Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: endErrors,
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.Credential, error) {
		s, err := store()
		if err != nil {
			return access.Credential{}, err
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.Credential{}, foundationAuthError(err)
		}
		out, err := s.RevokeCredential(ctx, d.sessionAuthority(p), d.Identity, identity.CredentialKindToken, r.PathValue("id"), p.AccountID)
		return out, credentialRouteError(err)
	}})
}
