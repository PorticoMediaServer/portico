package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type IntegrationStatus struct {
	Enabled  bool     `json:"enabled"`
	Profiles []string `json:"profiles"`
	Players  bool     `json:"players"`
}

func (d Dependencies) integrationEnabled(ctx context.Context) (bool, error) {
	if d.DB == nil {
		return false, nil
	}
	var enabled bool
	err := dbwork.QueryRow(ctx, d.DB, `SELECT COALESCE("integrations.compatibilityEnabled",0) FROM server_settings WHERE singleton=1`).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return enabled, err
}
func (d Dependencies) requireIntegrations(ctx context.Context) error {
	enabled, err := d.integrationEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return &apierror.Error{Code: "not_found"}
	}
	return nil
}

type IntegrationLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
}
type IntegrationToken struct {
	Token     string `json:"token"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	AccountID string `json:"accountId"`
}
type IntegrationTokenRequest struct {
	Name string `json:"name"`
}

func registerIntegrationCredentials(registry *apikit.Registry, d Dependencies) error {
	base := []string{"unauthorized", "not_permitted", "insufficient_scope", "session_required", "invalid_request", "not_found", "credential_limit", "internal"}
	meta := func(id, method, path, summary string, access apikit.Access, scope apikit.Scope, status, body int) apikit.Metadata {
		return apikit.Metadata{Audience: apikit.Integrations, Admission: laneSecurityFence, ID: id, Method: method, Path: path, Summary: summary, Access: access, Requires: scope, Lane: apikit.Security, Cost: apikit.Constant, Status: status, BodyLimit: int64(body), Errors: base}
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, IntegrationStatus]{Metadata: meta("get_integration_compatibility", "GET", "/v1/integrations/compatibility", "Read integration compatibility capabilities", apikit.Owner, apikit.ScopeAdmin, 200, 0), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (IntegrationStatus, error) {
		enabled, err := d.integrationEnabled(ctx)
		return IntegrationStatus{Enabled: enabled, Profiles: []string{"automation", "catalogue", "statistics-polling", "native-controls"}, Players: false}, err
	}}); err != nil {
		return err
	}
	list := meta("list_integration_app_passwords", "GET", "/v1/integrations/app-passwords", "List named integration app passwords", apikit.Owner, apikit.ScopeSession, 200, 0)
	list.Query = []string{"cursor"}
	if err := apikit.Register(registry, apikit.Route[struct{}, access.AppPasswordPage]{Metadata: list, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.AppPasswordPage, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.AppPasswordPage{}, foundationAuthError(err)
		}
		out, err := d.Access.Access.AppPasswords(ctx, d.integrationCredentialOwner(p), r.URL.Query().Get("cursor"))
		return out, credentialRouteError(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[access.AppPasswordRequest, access.IssuedAppPassword]{Metadata: meta("create_integration_app_password", "POST", "/v1/integrations/app-passwords", "Create an integration app password for one account and profile", apikit.Owner, apikit.ScopeSession, 201, 4096), Handler: func(ctx context.Context, r *http.Request, q access.AppPasswordRequest) (access.IssuedAppPassword, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.IssuedAppPassword{}, foundationAuthError(err)
		}
		out, err := d.Access.Access.CreateAppPassword(ctx, d.integrationCredentialOwner(p), d.Identity, q)
		return out, credentialRouteError(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, access.AppPassword]{Metadata: meta("revoke_integration_app_password", "DELETE", "/v1/integrations/app-passwords/{id}", "Revoke an app password and every token issued from it", apikit.Owner, apikit.ScopeSession, 200, 0), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.AppPassword, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.AppPassword{}, foundationAuthError(err)
		}
		out, err := d.Access.Access.RevokeAppPassword(ctx, d.integrationCredentialOwner(p), d.Identity, r.PathValue("id"))
		return out, credentialRouteError(err)
	}}); err != nil {
		return err
	}
	for _, kind := range []struct{ id, path, kind string }{{"reveal_api_key", "/v1/admin/api-keys/{id}/secret", identity.CredentialKindKey}, {"reveal_integration_app_password", "/v1/integrations/app-passwords/{id}/secret", identity.CredentialKindAppPassword}} {
		if err := apikit.Register(registry, apikit.Route[struct{}, access.CredentialSecret]{Metadata: meta(kind.id, "GET", kind.path, "Reveal this credential to the signed-in owner", apikit.Owner, apikit.ScopeSession, 200, 0), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.CredentialSecret, error) {
			p, err := d.foundationCaller(r)
			if err != nil {
				return access.CredentialSecret{}, foundationAuthError(err)
			}
			out, err := d.Access.Access.RevealCredential(ctx, d.integrationCredentialOwner(p), r.PathValue("id"), kind.kind)
			return out, credentialRouteError(err)
		}}); err != nil {
			return err
		}
	}
	if err := apikit.Register(registry, apikit.Route[IntegrationLogin, IntegrationToken]{Metadata: meta("exchange_integration_app_password", "POST", "/v1/integrations/authenticate", "Exchange a dedicated app password for a scoped integration token", apikit.Public, apikit.ScopeSession, 201, 4096), Handler: func(ctx context.Context, r *http.Request, q IntegrationLogin) (IntegrationToken, error) {
		if err := d.requireIntegrations(ctx); err != nil {
			return IntegrationToken{}, err
		}
		if d.credentials == nil || d.Access.Access == nil || d.Identity == nil {
			return IntegrationToken{}, &apierror.Error{Code: "unauthorized"}
		}
		digest := identity.Digest(q.Password)
		_, err := d.credentials.resolve(ctx, digest, clientAddress(r, d.trustedProxyPrefixes(ctx)), func(ctx context.Context) (access.ResolvedCredential, error) {
			credential, e := d.Access.Access.ResolveCredential(ctx, digest)
			if e == nil && credential.Kind != identity.CredentialKindAppPassword {
				e = identity.ErrUnauthorized
			}
			return credential, e
		})
		if err != nil {
			return IntegrationToken{}, credentialRouteError(err)
		}
		issued, err := d.Access.Access.ExchangeAppPassword(ctx, d.Identity, q.Username, q.Password, q.Name)
		return IntegrationToken{Token: issued.Secret, UserID: issued.Credential.Member.ProfileID, AccountID: issued.Credential.Member.AccountID, Username: issued.Credential.Member.Username}, credentialRouteError(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[IntegrationTokenRequest, access.IssuedCredential]{Metadata: meta("mint_integration_key", "POST", "/v1/integrations/keys", "Create a key within this app password grant", apikit.Device, apikit.ScopeRead, 201, 4096), Handler: func(ctx context.Context, r *http.Request, q IntegrationTokenRequest) (access.IssuedCredential, error) {
		if err := d.requireIntegrations(ctx); err != nil {
			return access.IssuedCredential{}, err
		}
		c, ok := identity.APICredentialFrom(ctx)
		if !ok || (c.Kind != identity.CredentialKindKey && c.Kind != identity.CredentialKindIntegrationSession) {
			return access.IssuedCredential{}, &apierror.Error{Code: "not_permitted"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.IssuedCredential{}, foundationAuthError(err)
		}
		out, err := d.Access.Access.MintIntegrationToken(ctx, d.Identity, p, c.ID, "", q.Name)
		return out, credentialRouteError(err)
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, access.IntegrationKeyPage]{Metadata: meta("list_integration_keys", "GET", "/v1/integrations/keys", "Read keys within the caller's integration grant", apikit.Device, apikit.ScopeRead, 200, 0), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.IntegrationKeyPage, error) {
		if err := d.requireIntegrations(ctx); err != nil {
			return access.IntegrationKeyPage{}, err
		}
		c, ok := identity.APICredentialFrom(ctx)
		p, err := d.foundationCaller(r)
		if err != nil {
			return access.IntegrationKeyPage{}, foundationAuthError(err)
		}
		var auth access.Authorize
		if !ok {
			// A signed-in owner has the same explicit recovery authority as the
			// owner-only per-key reveal route. No foreign administrator flag grants it.
			if p.Role != identity.TierOwner {
				return access.IntegrationKeyPage{}, &apierror.Error{Code: "not_permitted"}
			}
			auth = d.integrationCredentialOwner(p)
		} else {
			auth = func(ctx context.Context, tx *sql.Tx) error {
				_, err := d.Identity.SessionFamilyTx(ctx, tx, p)
				return err
			}
		}
		out, err := d.Access.Access.IntegrationKeys(ctx, auth, p, c)
		return out, credentialRouteError(err)
	}})
}

func (d Dependencies) integrationCredentialOwner(p identity.Principal) access.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		if _, err := d.tierAuthorityTx(ctx, tx, p, identity.TierOwner); err != nil {
			return err
		}
		return d.Identity.RequireCredentialManagementTx(ctx, tx, p)
	}
}
