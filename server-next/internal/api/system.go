package api

import (
	"net/http"
)

// System is what an app reads before sign-in to learn what this server is and can do.
type System struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// APIMajor is the API version in the path that this server speaks.
	APIMajor int `json:"apiMajor"`
	// Capabilities names optional features this server has. An app checks here before using one.
	Capabilities []string `json:"capabilities"`
	// SetupRequired is true until the owner account exists.
	SetupRequired bool `json:"setupRequired"`
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
			e := refusal(http.StatusServiceUnavailable, "not_ready", "The server is not ready yet.", RetrySame)
			e.RetryAfterMs = 1000
			return Health{}, e
		}
		return Health{Status: "ok"}, nil
	})
	Handle(s, "GET /v1/system", Public, func(r *http.Request, _ None) (System, error) {
		name, ok, err := s.db.Setting(r.Context(), "server.name")
		if err != nil {
			return System{}, err
		}
		if !ok {
			name = "Portico"
		}
		hasUsers, err := s.db.HasUsers(r.Context())
		if err != nil {
			return System{}, err
		}
		return System{ID: s.id, Name: name, Version: s.version, APIMajor: APIMajor, Capabilities: []string{},
			SetupRequired: !hasUsers, SignInMethods: []string{"password"}}, nil
	})
}
