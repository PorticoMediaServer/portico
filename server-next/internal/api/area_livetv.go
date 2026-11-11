package api

import "context"

// liveTVArea is what Live TV and DVR (area 11) keeps on the server. Add this area's fields here and its start-up
// to startLiveTV, in this file, so that building the area never edits server.go.
type liveTVArea struct{}

// startLiveTV builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startLiveTV(ctx context.Context) error {
	return nil
}
