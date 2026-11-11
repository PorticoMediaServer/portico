package api

import (
	"context"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
	"github.com/PorticoMediaServer/portico/server-next/internal/console"
	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/updates"
)

// consoleArea is what the owner console (area 12) keeps on the server: the owner's settings
// documents, Now playing, backups, logs and statistics. Its fields are here and its start-up
// is startConsole, so that building the area never edits server.go.
type consoleArea struct {
	// settings are the owner's settings documents, held in memory. See settings.go.
	settings *settings.Settings
	// totals counts the server's lifetime statistics. See statistics.go.
	totals *console.Totals
	// updates looks for a newer version. See updates.go.
	updates *updates.Checker
	// backups makes and keeps the owner's backups. See backups.go.
	backups *backup.Service
	// restart is closed when the owner asks for a restart. See restore.go.
	restart     chan struct{}
	restartOnce sync.Once
	// serverLog is the server's log, for Messages. See logs.go.
	serverLog *serverlog.Log
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
	s.updates = &updates.Checker{Settings: s.settings, Version: s.version, Now: func() time.Time { return s.now() }}
	// Once a day, whether a newer version is out. Nothing is installed.
	go s.updates.Run(ctx)
	s.updateRoutes()
	s.storageRoutes(s.dataDir)
	s.backups = &backup.Service{DB: s.db, Settings: s.settings, DataDir: s.dataDir, Version: s.version, Log: s.log,
		Now: func() time.Time { return s.now() }}
	// Scheduled backups, at the owner's hour.
	go s.backups.Run(ctx)
	s.backupRoutes()
	s.restart = make(chan struct{})
	s.restoreRoutes()
	s.logRoutes()
	s.playHistoryRoutes()
	s.notificationRoutes()
	s.reportRoutes()
	s.dashboardRoutes()
	// The console's part of the daily tidy-up: notices past the owner's keeping time, crash
	// reports after 30 days and app logs no report names, the activity record after a year.
	s.jobs.Tidy("notices", 24*time.Hour, s.notices().Tidy)
	s.jobs.Tidy("reports", 24*time.Hour, s.TidyReports)
	s.jobs.Tidy("activity record", 24*time.Hour, func(ctx context.Context) error {
		return s.db.RemoveOldActivity(ctx, s.now().AddDate(-1, 0, 0))
	})
	return nil
}
