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
	// SetupCodeFile is the file on the server's own disk that holds the setup code, told
	// while setup is required so the setup page can say where to look. It is a place, not
	// the code: reading it takes access to the server's disk.
	SetupCodeFile string `json:"setupCodeFile,omitempty"`
	// SignInMethods lists the ways to sign in, in the order to offer them.
	SignInMethods []string `json:"signInMethods"`
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
		out := System{ID: s.id, Name: s.serverName(), APIMajor: APIMajor, Capabilities: []string{},
			SetupRequired: !hasUsers, SignInMethods: []string{"password"}}
		if !hasUsers {
			out.SetupCodeFile = s.setupFile
		}
		if _, err := s.admit(r, &Route{Access: SignedIn, Scope: ScopeRead}); err == nil {
			out.Version = s.version
		}
		return out, nil
	})
}

// serverName is the name the owner gave this server in Settings › General.
func (s *Server) serverName() string { return settings.General.Get(s.settings).Name }
