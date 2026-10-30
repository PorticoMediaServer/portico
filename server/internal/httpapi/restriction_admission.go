package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var errFeatureRestricted = errors.New("This feature is not permitted for this profile.")

func (d Dependencies) featureAllowed(ctx context.Context, p identity.Principal, feature string) error {
	if feature == "" {
		return nil
	}
	return d.Identity.WithProfileRestrictions(ctx, p, func(r identity.ProfileRestrictions) error {
		allowed := false
		switch feature {
		case "downloads":
			allowed = r.AllowDownloads
		case "live_tv":
			allowed = r.AllowLiveTV
		case "dvr":
			allowed = r.AllowDVR
		case "watch_together":
			allowed = r.AllowWatchTogether
		}
		if !allowed {
			return errFeatureRestricted
		}
		return nil
	})
}

func requestFeature(r *http.Request) string {
	path := r.URL.Path
	switch {
	// A profile that has had downloads turned off must still be able to hear
	// so: renewal is how the copies already on its devices are withdrawn, and
	// it refuses each of them by name. The plays it made while it was allowed
	// still count. Neither route hands out anything.
	case path == "/v1/downloads/receipts/revalidate", path == "/v1/downloads/progress":
		return ""
	case strings.HasPrefix(path, "/v1/downloads"), strings.HasSuffix(path, "/download-options"):
		return "downloads"
	case strings.HasPrefix(path, "/v1/groups"):
		return "watch_together"
	case strings.HasPrefix(path, "/v1/dvr"):
		return "dvr"
	case strings.HasPrefix(path, "/v1/channels"), path == "/v1/guide", strings.HasPrefix(path, "/v1/guide/"):
		return "live_tv"
	}
	return ""
}

// Used inside playback, queue and group authority transactions. Pending rating
// classifications are withheld from restricted viewers until catalog publication
// classifies them; a missing projection must never become an adult-content bypass.
func (d Dependencies) itemRestrictionsTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) error {
	return contentaccess.VisibleItemTx(ctx, tx, p, item)
}
