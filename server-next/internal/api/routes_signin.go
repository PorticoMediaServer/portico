package api

func (s *Server) registerSignInRoutes() {
	s.systemRoutes()
	s.networkRoutes()
	s.signInSettingsRoutes()
	s.identityRoutes()
	s.authRoutes()
	s.ownerResetRoutes()
	s.profileRoutes()
	s.preferenceRoutes()
	s.deviceRoutes()
	s.memberRoutes()
	s.invitationRoutes()
	s.keyRoutes()
}
