package api

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/notices"
)

// Server notices: when the cause of an alert starts, whoever the owner chose for server notices
// (the owner, or the owner and administrators) gets one notice. The causes are the Dashboard's
// alerts, read by the same function, so a notice and the Dashboard never disagree about what is
// wrong. A notice is sent once when its cause starts and not again while it lasts; when the
// cause ends it is forgotten, so it is told again if it comes back.

// noticedAlerts are the alerts that also send a notice, with the notice kind the same as the
// alert's code. A library folder that cannot be read stays on the Dashboard only: the owner
// chose notices for a remote source, and a sleeping drive would otherwise send one each time.
var noticedAlerts = map[string]bool{
	"storage_nearly_full": true,
	"backup_failed":       true,
	"remote_unreachable":  true,
	"update_available":    true,
}

const (
	// toldAlertsKey is the settings row holding the alerts already told, so a cause that was
	// told before a restart is not told again after it.
	toldAlertsKey = "console.told_alerts"
	// alertCheckEvery is how often the causes are looked at: each look is one small read of the
	// remote sources and two calls on the data folder's disk.
	alertCheckEvery = time.Minute
)

// alertKey names one occurrence of an alert's cause: its code and what it is about. A new
// version is named by its number, so a later version is told although the last is not installed.
func alertKey(a DashboardAlert) string {
	if a.Code == "update_available" {
		return a.Code + ":" + a.Params["latest"]
	}
	return a.Code + ":" + a.Action.Target
}

// runServerNotices looks at the causes once a minute until ctx ends.
func (s *Server) runServerNotices(ctx context.Context) {
	ticker := time.NewTicker(alertCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.tellNewAlerts(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("server notices could not be sent; they are tried again in a minute", "error", err)
			}
		}
	}
}

// tellNewAlerts sends a notice for each alert whose cause has started since the last look, and
// forgets each one whose cause has ended.
func (s *Server) tellNewAlerts(ctx context.Context) error {
	sources, err := s.db.RemoteSources(ctx)
	if err != nil {
		return err
	}
	told := map[string]bool{}
	if raw, ok, err := s.db.Setting(ctx, toldAlertsKey); err != nil {
		return err
	} else if ok {
		var keys []string
		// A row that cannot be read is taken as nothing told: at worst a notice is sent again.
		_ = json.Unmarshal([]byte(raw), &keys)
		for _, k := range keys {
			told[k] = true
		}
	}
	// Libraries are not read: their alerts send no notice.
	now := map[string]bool{}
	for _, a := range s.alerts(nil, sources) {
		if !noticedAlerts[a.Code] {
			continue
		}
		key := alertKey(a)
		if !told[key] {
			// The notice is sent before the row records it, so a stop between the two sends it
			// again rather than never.
			if err := s.notices().Send(ctx, notices.Server, a.Code, a.Params, a.Action.Target); err != nil {
				return err
			}
		}
		now[key] = true
	}
	// Whether a newer version is out is not known after a start until the first check, so a
	// version told before the restart is not forgotten (and told again) meanwhile.
	if s.updates.State().CheckedAt == nil {
		for k := range told {
			if strings.HasPrefix(k, "update_available:") {
				now[k] = true
			}
		}
	}
	if maps.Equal(told, now) {
		return nil
	}
	raw, _ := json.Marshal(slices.Sorted(maps.Keys(now)))
	return s.db.SaveSetting(ctx, toldAlertsKey, string(raw), s.now())
}
