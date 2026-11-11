package api

import (
	"context"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/portmap"
	"github.com/PorticoMediaServer/portico/server-next/internal/publicaddr"
	"github.com/PorticoMediaServer/portico/server-next/internal/tlsport"
)

// setupArea is what setup and remote access (area 2) keeps on the server. Add this area's fields here and its start-up
// to startSetup, in this file, so that building the area never edits server.go.
type setupArea struct {
	// portico gets a server linked to a Portico Account its Portico address and certificate,
	// in the background. It contacts nothing until the link's client says the server is
	// linked (portico.Linked, with the link's issue time), so a server with local sign-in
	// never does.
	portico *certclient.Client
	// setupCode is what creating the owner needs. It is made at start while there is no
	// account, and written to the log and to setupFile, never sent to an app. See setup.go.
	setupCode, setupFile string
	// hosted is Portico Services. Only a server choosing or using Portico Accounts asks it
	// anything; tests replace it.
	hosted hosted.Service
	// access is the owner's remote access settings. See remote_access.go.
	access remoteState
	// certs is the certificate for the owner's own address, read from the Keys folder.
	certs *tlsport.Store
	// ports asks the home router to open the public port. It asks nothing until
	// ServeNetwork runs it, which tests never do.
	ports *portmap.Mapper
	// public finds the server's public address from outside services; like ports, it asks
	// nothing until ServeNetwork runs it.
	public *publicaddr.Finder
}

// startSetup builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startSetup(ctx context.Context) error {
	s.portico = certclient.New(certclient.Options{ServerID: s.id, Key: s.identity.key, Keys: filepath.Join(s.dataDir, "Keys"),
		Log: s.log, Now: func() time.Time { return s.now() }})
	s.goBackground(func() { s.portico.Run(ctx) })
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
	s.certs = tlsport.NewStore(filepath.Join(s.dataDir, "Keys"), s.log, func() time.Time { return s.now() })
	// Enhanced waits on the owner's own certificate until it is loaded and valid for the address.
	s.access.ownCertificateUsable = func() bool { return s.certs.Status().State == "ok" }
	s.ports = portmap.New(s.log, nil)
	s.public = publicaddr.New(s.log)
	// When Portico Services is connected, a new public address is reported to it here.
	s.public.OnChange = func(address string) { s.log.Info("this server's public address changed", "address", address) }
	if err := s.loadRemoteAccess(ctx); err != nil {
		return err
	}
	s.networkRoutes()
	s.remoteAccessRoutes()
	s.setupRoutes()
	s.signInKindRoutes()
	return nil
}
