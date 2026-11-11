// Command portico-server runs Portico's server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata"
)

// version is set by the release build.
var version = "dev"

func main() {
	// The sandbox around ffmpeg and ffprobe starts this same binary again as its helper.
	// That must be answered before anything else, flags included.
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(log)
	if errors.Is(err, errRestart) {
		// The same program with the same arguments, in this same process.
		err = restartSelf()
	}
	if err != nil {
		log.Error("portico-server stopped", "error", err)
		os.Exit(1)
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func run(log *slog.Logger) error {
	data := flag.String("data", env("PORTICO_DATA", "data"), "folder for the database and everything else the server keeps (PORTICO_DATA)")
	listen := flag.String("listen", env("PORTICO_LISTEN", ":32500"), "address to serve the API on (PORTICO_LISTEN)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	// Nothing in the data folder is ever played as a library file.
	serverData, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	libraryfile.ServerData = serverData
	// Media tools are started through this binary as their helper. That lets a scan keep a
	// few confined workers going, where it would otherwise build a sandbox for every file.
	if _, err := mediaexec.Configure(mediaexec.Options{}); err != nil {
		return err
	}
	// A restore the owner asked for is made now, before the database opens. If the restored
	// database will not open, what was in use is put back.
	restored, undo, err := backup.ApplyPending(*data)
	if err != nil {
		log.Error("the restore could not be made; the server is as it was", "error", err)
	}
	d, err := db.Open(ctx, filepath.Join(*data, "main.db"))
	if err != nil && undo != nil {
		log.Error("the restored database would not open, so the server goes back to what it had", "backup", restored, "error", err)
		if err := undo(); err != nil {
			return err
		}
		restored = ""
		d, err = db.Open(ctx, filepath.Join(*data, "main.db"))
	}
	if err != nil {
		return err
	}
	defer d.Close()
	d.StartSearch(filepath.Join(*data, "search.db"), log)
	if restored != "" {
		// Everyone signs in again: the restored sign-ins are from before the backup.
		if err := d.EndEverySignIn(ctx, time.Now()); err != nil {
			return err
		}
		log.Info("the server was restored from a backup; everyone signs in again", "backup", restored)
	}
	handler, err := api.New(ctx, d, *data, version, log)
	if err != nil {
		return err
	}
	// rclone, which remote sources run, stops with the server.
	defer handler.RemoteSources().Close()
	// Scans start by themselves from here on: watched folders and rescan intervals.
	go handler.RunSchedules(ctx)
	// The media engine plans and runs the streams. Its conversions folder is emptied at start:
	// no stream outlives a restart.
	engine, err := mediaengine.New(ctx, d, mediaengine.Options{Folder: filepath.Join(*data, "transcodes"), Log: log, Remote: handler.RemoteSources().Open})
	if err != nil {
		return err
	}
	handler.Playback().SetEngine(engine, engine)
	handler.ServeMediaWith(engine.ServeMedia)
	go engine.Run(ctx)
	go handler.Totals().Run(ctx)
	// The list of changes behind the events feed is kept short. A reader further behind than
	// what is kept is told to read everything again.
	go func() {
		tick := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, err := d.PruneChanges(ctx); err != nil && ctx.Err() == nil {
					log.Error("pruning the list of changes failed", "error", err)
				}
			}
		}
	}()
	// Moods and similar titles: the published title data file, checked weekly. Nothing is
	// downloaded until a release is signed, or while the owner has it switched off.
	go (&titledata.Updater{DB: d, Dir: filepath.Join(*data, "title-data"), Log: log}).Run(ctx)
	// No write timeout: media and the events feed are long replies. Slow request headers are bounded.
	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	log.Info("portico-server is serving", "listen", *listen, "data", *data, "version", version)

	restart := false
	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	case <-handler.RestartRequested():
		restart = true
		log.Info("portico-server is restarting, as the owner asked")
	}
	// Let requests in flight finish, then close the database.
	wait, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(wait); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Jobs in hand stop and are put back to waiting, so the next start does not take them for
	// jobs that were cut short.
	handler.Jobs().Wait(wait)
	// Where everyone had got to is saved before the database closes, and every converter stopped.
	handler.Playback().Save(wait, db.User)
	// Today's statistics so far are added to the day's totals.
	handler.Totals().Flush(wait)
	if err := engine.Shutdown(wait); err != nil {
		log.Error("stopping the streams failed", "error", err)
	}
	log.Info("portico-server stopped cleanly")
	if restart {
		return errRestart
	}
	return nil
}

// errRestart asks main to start the server again once everything has stopped.
var errRestart = errors.New("restart")
