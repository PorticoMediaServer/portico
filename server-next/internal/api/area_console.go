package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/console"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// consoleArea is what the owner console (area 12) keeps on the server: the owner's settings
// documents, Now playing, backups, logs and statistics. Its fields are here and its start-up
// is startConsole, so that building the area never edits server.go.
type consoleArea struct {
	// settings are the owner's settings documents, held in memory. See settings.go.
	settings *settings.Settings
	// totals counts the server's lifetime statistics. See statistics.go.
	totals *console.Totals
}

// Totals are the server's lifetime statistics: the server runs them, and flushes them as it stops.
func (s *Server) Totals() *console.Totals { return s.totals }

// startConsole builds this area's services and registers its routes. New calls it first of
// the newer areas: the settings documents it opens are what the others read.
func (s *Server) startConsole(ctx context.Context) error {
	var damaged []error
	var err error
	if s.settings, damaged, err = settings.Open(ctx, s.db); err != nil {
		return err
	}
	for _, err := range damaged {
		s.log.Warn("a settings document could not be read and is at its defaults until the owner saves it", "error", err)
	}
	s.settingsRoutes()
	s.activityRoutes()
	s.totals = &console.Totals{DB: s.db, Log: s.log, Now: func() time.Time { return s.now() }}
	s.playback.CountInto(s.totals)
	s.statisticsRoutes()
	return nil
}
