package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/tlsport"
)

// serveNetwork opens the server's port and starts what the owner's remote access settings
// ask of the network. A failure to open the port or to keep serving is sent on failed. The
// channel returned closes once the announcement and the router's port mapping have stopped,
// which they do when ctx ends.
func serveNetwork(ctx context.Context, srv *http.Server, handler *api.Server, failed chan<- error) <-chan struct{} {
	mapped := make(chan struct{})
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		failed <- err
		close(mapped)
		return mapped
	}
	// The one port answers plain HTTP and, once the owner gives a certificate for their own
	// address, HTTPS too. A renewed certificate is picked up without a restart.
	handler.ListeningOn(ln.Addr())
	certs := handler.Certificates()
	go certs.Run(ctx.Done())
	tlsConfig := &tls.Config{GetCertificate: certs.GetCertificate, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	go func() { failed <- srv.Serve(tlsport.Listen(ln, tlsConfig)) }()
	// Apps at home are told where the server is, and the home router is asked to forward the
	// public port to this one, while the owner's remote access settings want each; both stop
	// when the server stops. A server listening only on this machine cannot be reached from
	// the network, so it announces nothing and the router is never asked.
	port := ln.Addr().(*net.TCPAddr)
	if port.IP.IsLoopback() {
		port.Port = 0
	}
	go func() { handler.ServeNetwork(ctx, port.Port, port.AddrPort().Addr().Unmap()); close(mapped) }()
	return mapped
}
