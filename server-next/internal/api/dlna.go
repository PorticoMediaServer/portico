package api

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/dlna"
)

// DLNA's switch and its extra names are fields of the remote access settings (see
// RemoteAccessSettings in remote_access.go). While it is on, any device on the home networks the
// owner listed browses and plays every original with no sign-in; see internal/dlna for the
// boundary.

// dlnaHome is the home networks DLNA serves: those the owner listed, while DLNA is on. The
// networks the server guesses before the owner chooses never count.
func (s *Server) dlnaHome() []netip.Prefix {
	if v, _ := s.access.current(); !v.DLNA {
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
		// From home by the server's own rule, and never from this machine's loopback, where a
		// proxy on the same machine that passes on no visitor's address would make the whole
		// internet look like this machine. A proxy the owner listed that passes on no address is
		// served (the owner's ruling, 11 November), and noted as the network settings note it, so
		// the remote-access settings name it in their warning for a day after.
		FromHome: func(r *http.Request) bool {
			n := s.networks()
			from, ok := n.Client(r)
			if !ok || from.IsLoopback() {
				return false
			}
			silent := (auth.Networks{Home: n.Proxies}).AtHome(from)
			if silent && n.AtHome(from) {
				s.noteSilentProxy(peerAddr(r))
				return true
			}
			return n.FromHome(r)
		},
		// When area 2 adds the owner's own address for the server, its name joins these.
		Names: func() []string { v, _ := s.access.current(); return v.DLNANames },
		Name:  s.serverName}
}

// checkDLNA checks the DLNA fields of a remote access document about to be saved, against
// the one in force. Turning DLNA on needs the owner's own home list, since DLNA answers only
// there; and opening the library to every device at home is for the owner signed in, never a key.
func (s *Server) checkDLNA(r *http.Request, v *RemoteAccessSettings, before RemoteAccessSettings) error {
	names, err := checkNames(v.DLNANames)
	if err != nil {
		return err
	}
	v.DLNANames = names
	if v.DLNA == before.DLNA && slices.Equal(v.DLNANames, nonNil(before.DLNANames)) {
		return nil
	}
	if CallerFrom(r.Context()).Key {
		return refusal(http.StatusForbidden, "session_required", "This needs a signed-in person, not a key.", false)
	}
	if _, stored := s.network.current(s.now()); v.DLNA && !before.DLNA && stored.Home == nil {
		return ErrField("dlna", "List your home networks in the network settings first: DLNA answers only there.")
	}
	return nil
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
// whole internet or a trusted proxy has passed DLNA requests on with no visitor's address in the
// last day (each such proxy named), and "" otherwise.
func (s *Server) dlnaWarning(on bool) string {
	var warnings []string
	if networks, stored := s.network.current(s.now()); on && stored.Home != nil && coversEverything(networks.Home) {
		warnings = append(warnings, "Your home networks include the whole internet, so DLNA shares every original file with anyone on the "+
			"internet who can reach this server, with no sign-in.")
	}
	for _, proxy := range s.silentProxies() {
		if on {
			warnings = append(warnings, "The trusted proxy at "+proxy+" is passing DLNA requests on without saying who sent them, so "+
				"anyone who can reach that proxy can read every original file, with no sign-in.")
		}
	}
	return strings.Join(warnings, " ")
}

// silentProxies are the trusted proxies seen passing on no visitor's address in the last day,
// from the record the network settings warn from.
func (s *Server) silentProxies() []string {
	now := s.now()
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	var out []string
	for proxy, at := range s.network.silent {
		if now.Sub(at) <= silentProxyFor {
			out = append(out, proxy)
		}
	}
	slices.Sort(out)
	return out
}
