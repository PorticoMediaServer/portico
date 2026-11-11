package api

import "github.com/PorticoMediaServer/portico/server-next/internal/remote"

func (s *Server) registerLibraryRoutes() error {
	var err error
	if s.remote, err = remote.NewRegistry(s.db, s.dataDir); err != nil {
		return err
	}
	s.remoteSourceRoutes()
	return nil
}
