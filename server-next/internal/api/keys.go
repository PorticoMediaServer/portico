package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Long-lived credentials for tools. An API key is the server's, made by the owner for
// things like Sonarr or Home Assistant. A personal token is any person's own. Both go in
// "Authorization: Bearer" and are limited by their scopes and by what their account may
// do. An app password signs one person in to an app that speaks another server's protocol.
// None of them is ended by a password change or a sign-out; each is revoked on its own.

// Key is a credential made for a tool. Its secret is not part of it.
type Key struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scopes are read, playback, library and admin; admin includes the others.
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	// ExpiresAt is absent for a key that does not expire.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// KeyCreate makes a key.
type KeyCreate struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresAt is optional: more than a minute and at most ten years from now.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// KeyCreated carries the secret, which is shown once and cannot be read again.
type KeyCreated struct {
	Key    Key    `json:"key"`
	Secret string `json:"secret"`
}

type keyKind struct {
	kind  string
	limit int
	// appPassword keys are typed into other apps, so their secret is short enough to type
	// and they carry no scopes of ours.
	appPassword bool
}

var (
	apiKeys        = keyKind{kind: "api_key", limit: 100}
	personalTokens = keyKind{kind: "personal_token", limit: 25}
	appPasswords   = keyKind{kind: "app_password", limit: 25, appPassword: true}
)

func (s *Server) keyRoutes() {
	// Making, listing and ending keys is for a signed-in person: a key never mints another.
	for _, k := range []keyKind{personalTokens, appPasswords} {
		path := "/v1/me/tokens"
		if k.appPassword {
			path = "/v1/me/app-passwords"
		}
		Handle(s, "GET "+path, SignedIn, func(r *http.Request, _ None) (Page[Key], error) { return s.listKeys(r, k) })
		Handle(s, "POST "+path, SignedIn, func(r *http.Request, req KeyCreate) (KeyCreated, error) { return s.createKey(r, k, req) })
		Handle(s, "DELETE "+path+"/{id}", SignedIn, func(r *http.Request, _ None) (None, error) { return s.revokeKey(r, k) })
	}
	Handle(s, "GET /v1/admin/api-keys", Owner, func(r *http.Request, _ None) (Page[Key], error) { return s.listKeys(r, apiKeys) }).Scope = ScopeSession
	Handle(s, "POST /v1/admin/api-keys", Owner, func(r *http.Request, req KeyCreate) (KeyCreated, error) { return s.createKey(r, apiKeys, req) }).Scope = ScopeSession
	Handle(s, "DELETE /v1/admin/api-keys/{id}", Owner, func(r *http.Request, _ None) (None, error) { return s.revokeKey(r, apiKeys) }).Scope = ScopeSession
}

func keyOut(k db.APIKey) Key {
	out := Key{ID: k.PublicID, Name: k.Name, Scopes: k.Scopes, CreatedAt: k.CreatedAt.UTC()}
	if out.Scopes == nil {
		out.Scopes = []string{}
	}
	if !k.LastUsedAt.IsZero() {
		at := k.LastUsedAt.UTC()
		out.LastUsedAt = &at
	}
	if !k.ExpiresAt.IsZero() {
		at := k.ExpiresAt.UTC()
		out.ExpiresAt = &at
	}
	return out
}

// keyOwner is the caller, who must be able to manage their own account to hold its keys.
func keyOwner(r *http.Request, k keyKind) (Caller, error) {
	if k == apiKeys {
		return CallerFrom(r.Context()), nil // the route already required the owner
	}
	return manager(r)
}

func (s *Server) listKeys(r *http.Request, k keyKind) (Page[Key], error) {
	caller, err := keyOwner(r, k)
	if err != nil {
		return Page[Key]{}, err
	}
	keys, err := s.db.APIKeys(r.Context(), caller.UserID, k.kind, s.now())
	out := Page[Key]{Items: make([]Key, 0, len(keys))}
	for _, key := range keys {
		out.Items = append(out.Items, keyOut(key))
	}
	return out, err
}

func (s *Server) createKey(r *http.Request, k keyKind, req KeyCreate) (KeyCreated, error) {
	caller, err := keyOwner(r, k)
	if err != nil {
		return KeyCreated{}, err
	}
	now := s.now()
	if !auth.ValidName(req.Name) {
		return KeyCreated{}, ErrField("name", "Give the key a name of at most 120 characters.")
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return KeyCreated{}, err
	}
	// A repeat answers the key made the first time with a new secret: see db.CreateAPIKey.
	n := db.NewAPIKey{UserID: caller.UserID, Kind: k.kind, Name: req.Name, Limit: k.limit, IdempotencyKey: key,
		RequestHash: requestHash(struct {
			Kind string
			KeyCreate
		}{k.kind, req}), Now: now}
	secret := keyPrefix + auth.NewSecret()
	if k.appPassword {
		if len(req.Scopes) > 0 {
			return KeyCreated{}, ErrField("scopes", "An app password has no scopes.")
		}
		secret = auth.NewCode(4)
	} else {
		if len(req.Scopes) == 0 {
			return KeyCreated{}, ErrField("scopes", "Choose at least one scope: read, playback, library or admin.")
		}
		for _, scope := range req.Scopes {
			if !slices.Contains(GrantableScopes, Scope(scope)) || slices.Contains(n.Scopes, scope) {
				return KeyCreated{}, ErrField("scopes", "The scopes are read, playback, library and admin, each at most once.")
			}
			n.Scopes = append(n.Scopes, scope)
		}
	}
	if req.ExpiresAt != nil {
		if req.ExpiresAt.Before(now.Add(time.Minute)) || req.ExpiresAt.After(now.AddDate(10, 0, 0)) {
			return KeyCreated{}, ErrField("expiresAt", "Choose an expiry more than a minute and at most ten years from now.")
		}
		n.ExpiresAt = *req.ExpiresAt
	}
	n.KeyHash = auth.Hash(secret)
	if k.appPassword {
		n.KeyHash = auth.Hash(auth.CodeKey(secret))
	}
	made, err := s.db.CreateAPIKey(r.Context(), n)
	switch {
	case errors.Is(err, db.ErrKeyLimit):
		return KeyCreated{}, ErrConflict("credential_limit", "There are too many of these already. Revoke one first.")
	case errors.Is(err, db.ErrKeyReused):
		return KeyCreated{}, errKeyReused()
	case errors.Is(err, db.ErrNotFound):
		return KeyCreated{}, ErrConflict("key_revoked", "The key this request made has been revoked since. Create a new one.")
	}
	return KeyCreated{Key: keyOut(made), Secret: secret}, err
}

func (s *Server) revokeKey(r *http.Request, k keyKind) (None, error) {
	caller, err := keyOwner(r, k)
	if err != nil {
		return None{}, err
	}
	return None{}, s.db.RevokeAPIKey(r.Context(), caller.UserID, k.kind, r.PathValue("id"), s.now())
}
