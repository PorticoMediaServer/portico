package api

import "context"

// readingArea is what reading (area 10) keeps on the server. Add this area's fields here and its start-up
// to startReading, in this file, so that building the area never edits server.go.
type readingArea struct{}

// startReading builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startReading(ctx context.Context) error {
	return nil
}
