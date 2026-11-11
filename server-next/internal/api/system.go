package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
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
	// Warnings are for the person setting the server up, and are sent only until the owner
	// account exists: today, that this request arrived from the server's network gateway, so
	// the server cannot tell home from away as it is set up. The codes are those of the
	// owner's network settings, where the same warning is shown afterwards with the
	// gateway's address, which is left out here.
	Warnings []Warning `json:"warnings,omitempty"`
	// SignIn is the server's kind of sign-in: "portico" when people sign in with a Portico
	// Account, "local" when they sign in to this server directly. Apps read it before showing
	// any sign-in form.
	SignIn string `json:"signIn"`
	// AtHome says the caller is on one of the owner's home networks, as this server sees it.
	// An app must never take it as proof that it is at home: an impostor server can say true.
	AtHome bool `json:"atHome"`
	// Move is "to_local" or "to_portico" while a switch of the kind of sign-in still has people
	// waiting, so their apps offer the right step before sign-in; absent otherwise.
	Move string `json:"move,omitempty"`
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
		out := System{ID: s.id, Name: s.serverName(), APIMajor: APIMajor, Capabilities: []string{},
			SetupRequired: !hasUsers, SignInMethods: methods, SignIn: kind, AtHome: s.fromHome(r)}
		if move, moving, err := s.db.CurrentMove(r.Context()); err != nil {
			return System{}, err
		} else if moving {
			out.Move = "to_" + move.To
		}

		if _, err := s.admit(r, &Route{Access: SignedIn, Scope: ScopeRead}); err == nil {
			out.Version = s.version
		}
		// Setup works from any network. This only tells whoever is doing it what to expect.
		if gateway, from := s.fromGateway(r); from && !hasUsers {
			if w, ok := gatewayWarning(s.networks(), gateway, s.networks().Home, false); ok {
				out.Warnings = []Warning{w}
			}
		}
		return out, nil
	})
}

// serverName is the name the owner gave this server in Settings › General.
func (s *Server) serverName() string { return settings.General.Get(s.settings).Name }
