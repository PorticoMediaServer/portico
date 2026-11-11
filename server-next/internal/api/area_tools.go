package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
	"github.com/PorticoMediaServer/portico/server-next/internal/webhooks"
)

// toolsArea is what outside tools (area 13): webhooks, keys for tools, the Jellyfin-compatible routes, the assistant connection, DLNA keeps on the server. Add this area's fields here and its start-up
// to startTools, in this file, so that building the area never edits server.go.
type toolsArea struct {
	// webhooks sends the owner’s webhooks. See webhooks.go.
	webhooks *webhooks.Service
}

// startTools builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startTools(ctx context.Context) error {
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
