package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/webhooks"
)

// startWebhooks runs the owner's outbound webhooks: the dispatcher that sends
// what is queued, and the few events that do not come from a table change.
// Everything here is this server's own; the Portico Account service takes no
// part in it.
func startWebhooks(ctx context.Context, db *sql.DB, ident *identity.Service, backups *backup.Service, updates *administration.Service, console *operations.Store) *webhooks.Store {
	store := webhooks.New(db)
	previousOwnerChanged := ident.OnOwnerChangedTx
	ident.OnOwnerChangedTx = func(ctx context.Context, tx *sql.Tx) error {
		if previousOwnerChanged != nil {
			if err := previousOwnerChanged(ctx, tx); err != nil {
				return err
			}
		}
		return store.RetireOwnerTx(ctx, tx)
	}
	dispatcher := webhooks.NewDispatcher(store)
	dispatcher.Server = func() (string, string) { return ident.ID(), ident.Name() }
	dispatcher.UserAgent = "Portico-Webhooks/" + buildinfo.Info()["version"]
	supervise.Supervise(ctx, "cmd.webhooks", dispatcher.Run)
	if backups != nil {
		backups.OnFinished = func(kind backup.Kind, id string, err error) {
			// The automatic copy taken before a restore is part of the restore,
			// not a backup the owner asked to hear about.
			if kind == backup.KindPreRestore {
				return
			}
			emit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err == nil {
				_ = store.Emit(emit, webhooks.BackupSucceeded, "", map[string]any{"backupId": id, "kind": string(kind)})
				return
			}
			reason := "failed"
			if errors.Is(err, backup.ErrInsufficientDisk) {
				reason = "not_enough_disk_space"
			}
			_ = store.Emit(emit, webhooks.BackupFailed, "", map[string]any{"kind": string(kind), "reason": reason})
		}
	}
	if updates != nil {
		supervise.Supervise(ctx, "cmd.update-notice", func(ctx context.Context) { announceUpdates(ctx, store, updates, console) })
	}
	return store
}

// announceUpdates checks for a newer release a few times a day, but only while
// some webhook wants to hear of one, and announces each version once.
func announceUpdates(ctx context.Context, store *webhooks.Store, updates *administration.Service, console *operations.Store) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(6 * time.Hour)
		subscribed, err := store.Subscribed(ctx)
		if err != nil || !subscribed[webhooks.UpdateAvailable] {
			continue
		}
		channel, feed := "stable", ""
		if console != nil {
			if document, err := console.Settings(ctx, operations.AllowServerScope); err == nil {
				if document.Effective.UpdateChannel != "" {
					channel = document.Effective.UpdateChannel
				}
				feed = document.Effective.UpdateFeedURL
			}
		}
		info := buildinfo.Info()
		current := administration.UpdateBuild{Version: info["version"], BuildID: info["buildId"], SourceDigest: info["sourceDigest"], BuiltAt: info["builtAt"]}
		check, cancel := context.WithTimeout(ctx, time.Minute)
		report := updates.Updates(check, current, channel, feed)
		cancel()
		if !report.UpdateAvailable || report.Latest == nil || report.Latest.Version == "" {
			continue
		}
		announced, err := store.State(ctx, "update.announced")
		if err != nil || announced == report.Latest.Version {
			continue
		}
		if err = store.Emit(ctx, webhooks.UpdateAvailable, "update", map[string]any{"version": report.Latest.Version, "currentVersion": current.Version, "channel": channel, "notesUrl": report.NotesURL}); err != nil {
			log.Printf("Update notice: %v", err)
			continue
		}
		_ = store.SetState(ctx, "update.announced", report.Latest.Version)
	}
}

// announceStarted tells subscribers the server is up and answering.
func announceStarted(ctx context.Context, store *webhooks.Store) {
	if store == nil {
		return
	}
	emit, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = store.Emit(emit, webhooks.ServerStarted, "", map[string]any{"version": buildinfo.Info()["version"]})
}
