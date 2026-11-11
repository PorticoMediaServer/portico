package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/compat"
	"github.com/PorticoMediaServer/portico/server-next/internal/dlna"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
	"github.com/PorticoMediaServer/portico/server-next/internal/webhooks"
)

// toolsArea is what outside tools (area 13): webhooks, keys for tools, the Jellyfin-compatible routes, the assistant connection, DLNA keeps on the server. Add this area's fields here and its start-up
// to startTools, in this file, so that building the area never edits server.go.
type toolsArea struct {
	// webhooks sends the owner’s webhooks. See webhooks.go.
	webhooks *webhooks.Service
	// integrations are the owner's switches for outside tools. See integrations.go.
	integrations integrationState
	// compat and compatKey serve the Jellyfin-compatible routes. See compat.go.
	compat    compatState
	compatKey []byte
	// remoteAccessDoc is the remote-access settings, and dlna the DLNA server they switch. See dlna.go.
	remoteAccessDoc remoteAccessState
	dlna            *dlna.Server
}

// startTools builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startTools(ctx context.Context) error {
	if err := s.loadIntegrations(ctx); err != nil {
		return err
	}
	compatKey, err := s.db.SettingOrCreate(ctx, "compat.token_key", auth.NewKey)
	if err != nil {
		return err
	}
	s.compatKey = []byte(compatKey)
	s.integrationRoutes()
	s.mcpRoutes()
	if err := s.loadRemoteAccess(ctx); err != nil {
		return err
	}
	s.dlnaRoutes()
	box, err := secrets.Open(s.dataDir)
	if err != nil {
		return err
	}
	s.webhooks = webhooks.New(s.db, box, s.log, func() time.Time { return s.now() }, func() (string, string) {
		name, ok, _ := s.db.Setting(context.WithoutCancel(ctx), "server.name")
		if !ok {
			name = "Portico"
		}
		return s.id, name
	})
	s.webhookRoutes()
	s.startWebhooks(ctx)
	return nil
}

// serveTools answers the addresses outside tools have outside the ordinary routes, and says
// whether it did. The Jellyfin-compatible routes live outside /v1, whatever their capitals.
func (s *Server) serveTools(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		return false
	}
	op, values, ok := compat.Match(r.Method, r.URL.Path)
	if !ok || !s.integrations.get().CompatibleRoutes {
		return false
	}
	if op == "" {
		s.writeError(w, r, refusal(http.StatusMethodNotAllowed, "method_not_allowed", "This address does not take that method.", false))
		return true
	}
	s.serveCompat(w, r, op, values)
	return true
}
