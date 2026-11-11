package api

import "context"

// consoleArea is what the owner console (area 12): settings documents, Now playing, backups, logs, statistics keeps on the server. Add this area's fields here and its start-up
// to startConsole, in this file, so that building the area never edits server.go.
type consoleArea struct{}

// startConsole builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startConsole(ctx context.Context) error {
	return nil
}
