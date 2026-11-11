package api

import "context"

// toolsArea is what outside tools (area 13): webhooks, keys for tools, the Jellyfin-compatible routes, the assistant connection, DLNA keeps on the server. Add this area's fields here and its start-up
// to startTools, in this file, so that building the area never edits server.go.
type toolsArea struct{}

// startTools builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startTools(ctx context.Context) error {
	return nil
}
