package main

import (
	"os"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
)

// useTMDBFromEnv gives the server TMDB when a read access token is set. Until the owner can
// enter provider keys in Settings, PORTICO_TMDB_TOKEN is the way; PORTICO_TMDB_ORIGIN points it
// at a stand-in for testing.
func useTMDBFromEnv(handler *api.Server) {
	if token := os.Getenv("PORTICO_TMDB_TOKEN"); token != "" {
		handler.UseProvider(tmdb.New(token, env("PORTICO_TMDB_ORIGIN", tmdb.Origin)))
	}
}
