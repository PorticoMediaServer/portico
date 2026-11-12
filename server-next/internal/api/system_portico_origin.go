package api

import (
	"net"
	"strconv"
)

// Published Portico names exist only while their certificate can be served.
// Reuse that list and its actual public port, without taking an address from Host.
func (s *Server) publicPorticoOrigin() string {
	settings, _ := s.access.current()
	if settings.RemoteAccess != "on" {
		return ""
	}
	for _, address := range s.PublishedAddresses() {
		if address.Home || address.Name == "" {
			continue
		}
		host := address.Name
		if address.Port != 443 {
			host = net.JoinHostPort(host, strconv.Itoa(address.Port))
		}
		return "https://" + host
	}
	return ""
}
