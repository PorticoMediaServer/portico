package api

func (s *Server) registerBrowseRoutes() {
	s.catalogueRoutes()
	s.itemFileRoutes()
}
