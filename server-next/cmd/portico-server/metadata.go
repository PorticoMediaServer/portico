package main

import (
	"os"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
)

// useTMDBStandIn points the server's TMDB client at PORTICO_TMDB_ORIGIN when it is set: a
// stand-in, for testing only. TMDB is otherwise asked at its own address, with the owner's key
// from Settings › Metadata or Portico's built-in one.
func useTMDBStandIn(handler *api.Server) {
	if origin := os.Getenv("PORTICO_TMDB_ORIGIN"); origin != "" {
		handler.UseTMDBHost(origin)
	}
}
