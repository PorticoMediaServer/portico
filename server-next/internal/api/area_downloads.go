package api

import "context"

// downloadsArea is what downloads and offline (area 9) keeps on the server. Add this area's fields here and its start-up
// to startDownloads, in this file, so that building the area never edits server.go.
type downloadsArea struct{}

// startDownloads builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startDownloads(ctx context.Context) error {
	return nil
}
