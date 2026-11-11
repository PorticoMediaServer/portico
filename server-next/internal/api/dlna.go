package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/dlna"
)

// DLNA's switch lives in the remote-access settings document, which area 2 fills out with
// the rest of remote access. While it is on, any device on the home networks the owner listed
// browses and plays every original with no sign-in; see internal/dlna for the boundary.

// RemoteAccessSettings is the owner's remote-access document. (So far it holds only the DLNA
// switch.)
type RemoteAccessSettings struct {
	// DLNA shares every original file with any device on your home networks, with no sign-in.
	// (The home networks are those listed in the network settings. It ignores profiles,
	// library access, rating limits, viewing hours and stream limits, never converts, and
	// records nothing. Off by default.)
	DLNA bool `json:"dlna"`
	// DLNANames are further names DLNA answers to, besides addresses and the server's own and
	// home-network names: for a container whose host name is not the machine's, or the owner's
	// own domain on a proxy. Up to 20.
	DLNANames []string `json:"dlnaNames"`
	// Warning is set while DLNA is on and the home networks take in the whole internet: the app
	// shows it when the switch is turned on and for as long as it is true. Nothing is refused.
	Warning string `json:"warning,omitempty"`
	revision
}

// RemoteAccessSettingsEdit changes the document; a field left out stays as it is.
type RemoteAccessSettingsEdit struct {
	DLNA      *bool     `json:"dlna,omitempty"`
	DLNANames *[]string `json:"dlnaNames,omitempty"`
}

const remoteAccessSettingsKey = "remote-access"

type storedRemoteAccess struct {
	DLNA      bool     `json:"dlna"`
	DLNANames []string `json:"dlnaNames,omitempty"`
}

type remoteAccessState struct {
	atomic.Pointer[storedRemoteAccess]
}

func (s *Server) remoteAccess() storedRemoteAccess {
	if p := s.remote.Load(); p != nil {
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
	s.remote.Store(&stored)
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

// coversEverything says a home list takes in the whole internet (0.0.0.0/0 or ::/0). With DLNA
// on that shares every original with anyone who can reach the server. The owner is warned, not
// stopped (the owner's principle: steer people away from a mistake, never block it).
func coversEverything(home []netip.Prefix) bool {
	for _, p := range home {
		if p.Bits() == 0 {
			return true
		}
	}
	return false
}

func (s *Server) dlnaRoutes() {
	s.dlna = &dlna.Server{DB: s.db, Log: s.log, ServerID: func() string { return s.id }, Version: s.version, Home: s.dlnaHome,
		// From home by the server's own rule, and never as this machine's loopback or a proxy the
		// owner listed: a proxy that passes on no visitor's address would make every visitor
		// look like the proxy itself, and an owner who put a plain proxy in front has not chosen
		// to publish the library. Players never connect from either.
		FromHome: func(r *http.Request) bool {
			n := s.networks()
			from, ok := n.Client(r)
			return ok && n.FromHome(r) && !from.IsLoopback() && !(auth.Networks{Home: n.Proxies}).AtHome(from)
		},
		// When area 2 adds the owner's own address for the server, its name joins these.
		Names: func() []string { return s.remoteAccess().DLNANames },
		Name: func() string {
			name, ok, _ := s.db.Setting(context.Background(), "server.name")
			if !ok {
				name = "Portico"
			}
			return name
		}}
	Handle(s, "GET /v1/admin/settings/remote-access", Owner, func(r *http.Request, _ None) (RemoteAccessSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), remoteAccessSettingsKey)
		cur := s.remoteAccess()
		return RemoteAccessSettings{cur.DLNA, nonNil(cur.DLNANames), s.dlnaWarning(cur.DLNA), revision(saved)}, err
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
		_, stored := s.network.current(s.now())
		if *req.DLNA && stored.Home == nil {
			return RemoteAccessSettings{}, ErrField("dlna", "List your home networks in the network settings first: DLNA answers only there.")
		}
		next.DLNA = *req.DLNA
	}
	if req.DLNANames != nil {
		names, err := checkNames(*req.DLNANames)
		if err != nil {
			return RemoteAccessSettings{}, err
		}
		next.DLNANames = names
	}
	raw, _ := json.Marshal(next)
	saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, remoteAccessSettingsKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return RemoteAccessSettings{}, errStale()
	}
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	s.remote.Store(&next)
	return RemoteAccessSettings{next.DLNA, nonNil(next.DLNANames), s.dlnaWarning(next.DLNA), revision(saved)}, nil
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

func nonNil(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// checkNames reads the names the owner typed: host names only, in lower case, at most 20.
func checkNames(typed []string) ([]string, error) {
	if len(typed) > 20 {
		return nil, ErrField("dlnaNames", "List at most 20 names.")
	}
	out := []string{}
	for _, name := range typed {
		name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
		if name == "" || len(name) > 253 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "" ||
			strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
			return nil, ErrField("dlnaNames", "Enter names such as media.example.com, without http:// or a port.")
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// dlnaWarning is the warning for the owner while DLNA is on with a home list that takes in the
// whole internet, and "" otherwise.
func (s *Server) dlnaWarning(on bool) string {
	if networks, stored := s.network.current(s.now()); on && stored.Home != nil && coversEverything(networks.Home) {
		return "Your home networks include the whole internet, so DLNA shares every original file with anyone on the internet " +
			"who can reach this server, with no sign-in."
	}
	return ""
}
