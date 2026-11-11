package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/dlna"
)

// DLNA's switch lives in the remote-access settings document, which area 2 fills out with
// the rest of remote access. While it is on, any device on the home networks the owner listed
// browses and plays every original with no sign-in; see internal/dlna for the boundary.

// RemoteAccessSettings is the owner's remote-access document. (So far it holds only the DLNA
// switch.)
type RemoteAccessSettings struct {
	// DLNA lets TVs and consoles at home play without signing in: any device on the home
	// networks listed in the network settings can browse the video and music libraries and
	// play every original file. It ignores profiles, library access, rating limits, viewing
	// hours and stream limits, never converts, and records nothing. Off by default.
	DLNA bool `json:"dlna"`
	revision
}

// RemoteAccessSettingsEdit changes the document; a field left out stays as it is.
type RemoteAccessSettingsEdit struct {
	DLNA *bool `json:"dlna,omitempty"`
}

const remoteAccessSettingsKey = "remote-access"

type storedRemoteAccess struct {
	DLNA bool `json:"dlna"`
}

type remoteAccessState struct {
	atomic.Pointer[storedRemoteAccess]
}

func (s *Server) remoteAccess() storedRemoteAccess {
	if p := s.remoteAccessDoc.Load(); p != nil {
		return *p
	}
	return storedRemoteAccess{}
}

func (s *Server) loadRemoteAccess(ctx context.Context) error {
	raw, _, err := s.db.SettingDocument(ctx, remoteAccessSettingsKey)
	if err != nil || raw == "" {
		return err
	}
	var stored storedRemoteAccess
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return errors.New("the stored remote-access settings are damaged")
	}
	s.remoteAccessDoc.Store(&stored)
	return nil
}

// dlnaHome is the home networks DLNA serves: those the owner listed, while DLNA is on. The
// networks the server guesses before the owner chooses never count.
func (s *Server) dlnaHome() []netip.Prefix {
	if !s.remoteAccess().DLNA {
		return nil
	}
	networks, stored := s.network.current(s.now())
	if stored.Home == nil {
		return nil
	}
	return networks.Home
}

func (s *Server) dlnaRoutes() {
	s.dlna = &dlna.Server{DB: s.db, Log: s.log, ServerID: func() string { return s.id }, Version: s.version, Home: s.dlnaHome,
		Name: func() string {
			name, ok, _ := s.db.Setting(context.Background(), "server.name")
			if !ok {
				name = "Portico"
			}
			return name
		}}
	Handle(s, "GET /v1/admin/settings/remote-access", Owner, func(r *http.Request, _ None) (RemoteAccessSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), remoteAccessSettingsKey)
		return RemoteAccessSettings{s.remoteAccess().DLNA, revision(saved)}, err
	})
	// Opening the library to every device at home is the owner's, signed in.
	Handle(s, "PUT /v1/admin/settings/remote-access", Owner, s.saveRemoteAccess).Scope = ScopeSession
}

func (s *Server) saveRemoteAccess(r *http.Request, req RemoteAccessSettingsEdit) (RemoteAccessSettings, error) {
	match, err := settingsIfMatch(r)
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	next := s.remoteAccess()
	if req.DLNA != nil {
		if _, stored := s.network.current(s.now()); *req.DLNA && stored.Home == nil {
			return RemoteAccessSettings{}, ErrField("dlna", "List your home networks in the network settings first: DLNA answers only there.")
		}
		next.DLNA = *req.DLNA
	}
	raw, _ := json.Marshal(next)
	saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, remoteAccessSettingsKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return RemoteAccessSettings{}, errStale()
	}
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	s.remoteAccessDoc.Store(&next)
	return RemoteAccessSettings{next.DLNA, revision(saved)}, nil
}

// AnnounceDLNA announces the server on the home networks while DLNA is on, for the listener
// at listen, until ctx ends. Nothing is announced when the server listens on loopback only.
func (s *Server) AnnounceDLNA(ctx context.Context, listen string) {
	var stop context.CancelFunc
	done := make(chan struct{})
	close(done)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		on := len(s.dlnaHome()) > 0
		switch {
		case on && stop == nil:
			var announce context.Context
			announce, stop = context.WithCancel(ctx)
			done = make(chan struct{})
			go func() {
				defer close(done)
				dlna.Advertise(announce, dlna.Advertisement{Bind: listen, UUID: dlna.DeviceUUID(s.id), Server: "Portico UPnP/1.0 DLNADOC/1.50",
					Home: func(context.Context) []netip.Prefix { return s.dlnaHome() }})
			}()
		case !on && stop != nil:
			stop()
			<-done
			stop = nil
		}
		select {
		case <-ctx.Done():
			if stop != nil {
				stop()
				<-done
			}
			return
		case <-tick.C:
		}
	}
}
