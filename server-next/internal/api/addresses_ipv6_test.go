package api

import (
	"crypto/tls"
	"net/netip"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/portmap"
)

func TestIPv6ManualOpeningPublishesItsListenerAndStableName(t *testing.T) {
	s := newTestServer(t)
	client := netip.MustParseAddr("2605:a144:2327:3137::1")
	var clientMu sync.Mutex
	s.ports6 = portmap.NewIPv6(s.networkLog, func() portmap.Router {
		clientMu.Lock()
		defer clientMu.Unlock()
		return portmap.Router{Client: client}
	})
	s.access.mu.Lock()
	s.access.listenPort = 32500
	s.access.mu.Unlock()
	s.SetPorticoZone("ptc-aaaaaaaaaaaaaaaaaaaa.portico.direct", func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil })
	v, _ := s.access.current()
	v.IPv6, v.RemoteAccess, v.PortMapping, v.PublicPort = true, "on", "off", 42000
	s.access.set(v, true)
	find := func() *PublishedAddress {
		for _, a := range s.PublishedAddresses() {
			if ip, _ := netip.ParseAddr(a.Address); ip.Is6() {
				return &a
			}
		}
		return nil
	}
	a := find()
	if a == nil || a.Address != client.String() || a.Port != 32500 || a.Name != "2605-a144-2327-3137--1.ptc-aaaaaaaaaaaaaaaaaaaa.portico.direct" {
		t.Fatal("IPv6 used the translated IPv4 port, lost its address or lost its certificate name", a)
	}
	v.IPv6 = false
	s.access.set(v, true)
	if a := find(); a != nil {
		t.Fatal("IPv6 Off still publishes it", a)
	}
	v.IPv6, v.RemoteAccess = true, "off"
	s.access.set(v, true)
	if a := find(); a != nil {
		t.Fatal("Remote access Off still publishes IPv6", a)
	}
	v.RemoteAccess = "on"
	s.access.set(v, true)
	clientMu.Lock()
	client = netip.MustParseAddr("fd00::1")
	clientMu.Unlock()
	if a := find(); a != nil {
		t.Fatal("a private IPv6 address was published as public", a)
	}
	clientMu.Lock()
	client = netip.MustParseAddr("2605:a144:2327:3137::1")
	clientMu.Unlock()
	s.access.mu.Lock()
	s.access.networkStarted = true
	s.access.listenIP = netip.MustParseAddr("0.0.0.0")
	s.access.mu.Unlock()
	if a := find(); a != nil || s.ipv6OpeningNote(v) != ipv6ListenerNote {
		t.Fatal("an IPv4-only listener advertised an IPv6 route", a)
	}
}
