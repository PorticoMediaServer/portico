package api

import "context"

// togetherArea is what playing together and other devices (area 8) keeps on the server. Add this area's fields here and its start-up
// to startTogether, in this file, so that building the area never edits server.go.
type togetherArea struct{}

// startTogether builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startTogether(ctx context.Context) error {
	return nil
}
