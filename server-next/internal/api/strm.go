package api

import "github.com/PorticoMediaServer/portico/server-next/internal/strm"

// STRMSources is the one reader of `.strm` targets, shared by scans and playback.
func (s *Server) STRMSources() *strm.Client {
	if s.strm == nil {
		s.strm = strm.New()
	}
	return s.strm
}
