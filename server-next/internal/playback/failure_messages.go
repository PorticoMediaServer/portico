package playback

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func routeName(route string) string {
	switch route {
	case RouteOriginal:
		return "Original"
	case RouteRepackaged:
		return "Repackaged"
	case RouteConverted:
		return "Converted"
	}
	return "Playback"
}

// Only catalogue and device names go into these lines: never failure details, file paths,
// media addresses or player grants. Collapse whitespace so a name cannot add another line.
func messageName(name string) string { return strings.Join(strings.Fields(name), " ") }

func (s *Service) logRememberedFailure(before Session, next string, until time.Time) {
	message := fmt.Sprintf("%s failed for %q on %s. ", routeName(before.Plan.Route), messageName(before.Item.Title), messageName(before.Device.Name))
	if next != "" {
		message += fmt.Sprintf("%s until %s.", routeName(next), until.Format("Jan 2, 2006 at 15:04 MST"))
	} else {
		message += fmt.Sprintf("This way is avoided until %s.", until.Format("Jan 2, 2006 at 15:04 MST"))
	}
	s.log.Info(message, "log_kind", "playback")
}

func (s *Service) retryRememberedFailure(ctx context.Context, device db.SessionDevice, r resolved) {
	retried, err := s.db.RetryRouteFailure(ctx, device.ID, r.version.FileID, r.plan.Player, r.plan.Route, s.now())
	if err != nil {
		s.log.Error("Could not record a playback retry.", "log_kind", "playback")
	} else if retried {
		s.log.Info(fmt.Sprintf("Trying %s again for %q on %s.", routeName(r.plan.Route), messageName(r.item.Title), messageName(device.Name)), "log_kind", "playback")
	}
}
