package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// RemoteOutsideCheck is the last explicit owner check. A problem is an unknown outcome,
// never a false reachable value. Completed checks carry Services' own time and results.
type RemoteOutsideCheck struct {
	CheckedAt *time.Time                   `json:"checkedAt,omitempty"`
	Addresses []hosted.ReachabilityAddress `json:"addresses,omitempty"`
	Problem   string                       `json:"problem,omitempty"`
}

func (s *Server) checkOutside(ctx context.Context) {
	kind, err := s.signInKind(ctx)
	v, _ := s.access.current()
	if err == nil && (kind != "portico" || v.RemoteAccess != "on") {
		s.access.mu.Lock()
		s.access.outside = nil
		s.access.mu.Unlock()
		return
	}
	out := &RemoteOutsideCheck{Problem: "Remote access could not be checked. Try Check now again later."}
	if err == nil {
		link, linked, failed := s.db.Link(ctx)
		if failed == nil && linked && !link.Unlinked {
			if result, failed := s.link.client.Reachable(ctx, time.Unix(link.IssuedAt, 0)); failed == nil {
				out = &RemoteOutsideCheck{CheckedAt: &result.CheckedAt, Addresses: result.Addresses}
			}
		}
	}
	s.access.mu.Lock()
	s.access.outside = out
	s.access.mu.Unlock()
}

// Reading the page never checks or contacts Services. Do not show a former Portico
// check after a switch to local sign-in or while remote access is off.
func (s *Server) outsideFact(ctx context.Context) *RemoteOutsideCheck {
	kind, err := s.signInKind(ctx)
	v, _ := s.access.current()
	if err != nil || kind != "portico" || v.RemoteAccess != "on" {
		return nil
	}
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.outside
}
