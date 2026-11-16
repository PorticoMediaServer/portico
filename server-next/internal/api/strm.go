package api

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/strm"
	"net/netip"
)

// STRMSources shares the same address policy between scans and playback.
func (s *Server) STRMSources() *strm.Client {
	if s.strm == nil {
		s.strm = strm.New(func() []netip.Addr {
			var out []netip.Addr
			if s.public != nil {
				if ip, err := netip.ParseAddr(s.public.Answer().Address); err == nil {
					out = append(out, ip)
				}
			}
			return out
		})
	}
	return s.strm
}
