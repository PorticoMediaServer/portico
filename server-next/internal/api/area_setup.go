package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// setupArea is what setup and remote access (area 2) keeps on the server. Add this area's fields here and its start-up
// to startSetup, in this file, so that building the area never edits server.go.
type setupArea struct {
	// setupCode is what creating the owner needs. It is made at start while there is no
	// account, and written to the log and to setupFile, never sent to an app. See setup.go.
	setupCode, setupFile string
	// hosted is Portico Services. Only a server choosing or using Portico Accounts asks it
	// anything; tests replace it.
	hosted hosted.Service
	// access is the owner's remote access settings. See remote_access.go.
	access remoteState
}

// startSetup builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startSetup(ctx context.Context) error {
	s.hosted = hosted.NotConnected{}
	if err := s.loadSetupCode(ctx, s.dataDir); err != nil {
		return err
	}
	if err := s.resetRemoteAccess(ctx, s.dataDir); err != nil {
		return err
	}
	if err := s.loadNetworks(ctx); err != nil {
		return err
	}
	if err := s.loadRemoteAccess(ctx); err != nil {
		return err
	}
	s.networkRoutes()
	s.remoteAccessRoutes()
	s.setupRoutes()
	return nil
}
