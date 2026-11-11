package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// System is what an app reads before sign-in to learn what this server is and can do.
type System struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Version is told only to a signed-in caller: a stranger has no use for it but to look
	// up what is wrong with that release.
	Version string `json:"version,omitempty"`
	// APIMajor is the API version in the path that this server speaks.
	APIMajor int `json:"apiMajor"`
	// Capabilities names optional features this server has. An app checks here before using one.
	Capabilities []string `json:"capabilities"`
	// SetupRequired is true until the owner account exists.
	SetupRequired bool `json:"setupRequired"`
	// SignInMethods lists the ways to sign in, in the order to offer them.
	SignInMethods []string `json:"signInMethods"`
	// SignIn is the server's kind of sign-in: "portico" when people sign in with a Portico
	// Account, "local" when they sign in to this server directly. Apps read it before showing
	// any sign-in form.
	SignIn string `json:"signIn"`
	// AtHome says the caller is on one of the owner's home networks, as this server sees it.
	// An app must never take it as proof that it is at home: an impostor server can say true.
	AtHome bool `json:"atHome"`
}

// Health is the answer of the health checks.
type Health struct {
	Status string `json:"status"`
}

func (s *Server) systemRoutes() {
	Handle(s, "GET /v1/health/live", Public, func(r *http.Request, _ None) (Health, error) {
		return Health{Status: "ok"}, nil
	})
	Handle(s, "GET /v1/health/ready", Public, func(r *http.Request, _ None) (Health, error) {
		if err := s.db.Ping(r.Context()); err != nil {
			s.log.Error("readiness check failed", "error", err)
			e := refusal(http.StatusServiceUnavailable, "not_ready", "The server is not ready yet.", true)
			e.RetryAfterMs = 1000
			return Health{}, e
		}
		return Health{Status: "ok"}, nil
	})
	Handle(s, "GET /v1/system", Public, func(r *http.Request, _ None) (System, error) {
		name, err := s.serverName(r)
		if err != nil {
			return System{}, err
		}
		hasUsers, err := s.db.HasUsers(r.Context())
		if err != nil {
			return System{}, err
		}
		kind, err := s.signInKind(r.Context())
		if err != nil {
			return System{}, err
		}
		methods := []string{"password"}
		if kind == "portico" {
			methods = []string{"portico"}
		}
		out := System{ID: s.id, Name: name, APIMajor: APIMajor, Capabilities: []string{},
			SetupRequired: !hasUsers, SignInMethods: methods, SignIn: kind, AtHome: s.networks().FromHome(r)}
		if _, err := s.admit(r, &Route{Access: SignedIn, Scope: ScopeRead}); err == nil {
			out.Version = s.version
		}
		return out, nil
	})
}

// serverName is the name the owner gave this server, or "Portico" until they do.
func (s *Server) serverName(r *http.Request) (string, error) {
	name, ok, err := s.db.Setting(r.Context(), db.ServerNameKey)
	if err == nil && !ok {
		name = "Portico"
	}
	return name, err
}
