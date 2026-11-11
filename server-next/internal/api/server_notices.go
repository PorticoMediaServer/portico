package api

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/notices"
)

// Server notices: when the cause of an alert starts, whoever the owner chose for server notices
// (the owner, or the owner and administrators) gets one notice. The causes are the Dashboard's
// alerts, read by the same function, so a notice and the Dashboard never disagree about what is
// wrong. A cause is told once it has lasted noticeAfter, and not again until it has been well
// for wellAfter: a source on a poor link, found unreachable at one look and fine at the next,
// is one problem to the owner and one notice, and a single timeout is none.

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
	// toldAlertsKey is the settings row holding the causes seen and told, so a cause that was
	// told before a restart is not told again after it.
	toldAlertsKey = "console.told_alerts"
	// alertCheckEvery is how often the causes are looked at: each look is one small read of the
	// remote sources and two calls on the data folder's disk.
	alertCheckEvery = time.Minute
	// noticeAfter is how long a cause lasts before it is told; wellAfter is how long it must be
	// gone before it is forgotten, and told again if it comes back.
	noticeAfter = 5 * time.Minute
	wellAfter   = 30 * time.Minute
	// storageMargin is how many points above the warning line free space must be for low
	// storage to count as gone, so free space sitting at the line is one notice.
	storageMargin = 2
)

// seenAlert is one cause as the notices remember it: when it was first and last seen, and
// whether it has been told.
type seenAlert struct {
	Since time.Time `json:"since"`
	Seen  time.Time `json:"seen"`
	Told  bool      `json:"told,omitempty"`
}

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

// tellNewAlerts notes the causes that last now, sends a notice for each that has lasted
// noticeAfter and is not yet told, and forgets each that has been gone for wellAfter.
func (s *Server) tellNewAlerts(ctx context.Context) error {
	sources, err := s.db.RemoteSources(ctx)
	if err != nil {
		return err
	}
	seen := map[string]seenAlert{}
	if raw, ok, err := s.db.Setting(ctx, toldAlertsKey); err != nil {
		return err
	} else if ok {
		// A row that cannot be read is taken as nothing seen: at worst a notice is sent again.
		_ = json.Unmarshal([]byte(raw), &seen)
	}
	before := maps.Clone(seen)
	now := s.now()
	// Libraries are not read: their alerts send no notice.
	for _, a := range s.alerts(nil, sources) {
		if !noticedAlerts[a.Code] {
			continue
		}
		key := alertKey(a)
		e, ok := seen[key]
		if !ok {
			e.Since = now
		}
		e.Seen = now
		if !e.Told && now.Sub(e.Since) >= noticeAfter {
			// The notice is sent before the row records it, so a stop between the two sends it
			// again rather than never.
			if err := s.notices().Send(ctx, notices.Server, a.Code, a.Params, a.Action.Target); err != nil {
				return err
			}
			e.Told = true
		}
		seen[key] = e
	}
	stillLow := s.storageNearLine()
	for key, e := range seen {
		switch {
		case e.Seen.Equal(now):
		// Whether a newer version is out is not known after a start until the first check, so a
		// version told before the restart is kept (and not told again) meanwhile.
		case strings.HasPrefix(key, "update_available:") && s.updates.State().CheckedAt == nil,
			strings.HasPrefix(key, "storage_nearly_full:") && stillLow:
			e.Seen = now
			seen[key] = e
		case now.Sub(e.Seen) >= wellAfter:
			delete(seen, key)
		}
	}
	if maps.Equal(before, seen) {
		return nil
	}
	raw, _ := json.Marshal(seen)
	return s.db.SaveSetting(ctx, toldAlertsKey, string(raw), now)
}

// storageNearLine says whether free space on the data folder's disk is under the warning line
// plus storageMargin.
func (s *Server) storageNearLine() bool {
	total, _ := diskspace.Total(s.dataDir)
	free, _ := diskspace.Free(s.dataDir)
	return total > 0 && free*100/total < int64(storageDocument.Get(s.settings).WarnPercent+storageMargin)
}
