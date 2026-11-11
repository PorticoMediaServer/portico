// Command portico-server runs Portico's server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
	"github.com/PorticoMediaServer/portico/server-next/internal/datalock"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata"
	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
	"github.com/PorticoMediaServer/portico/server-next/internal/webapp"
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
		// The same program with the same arguments, in this same process. If that fails (the
		// program file was removed, or replaced by one that cannot run here), the server has
		// already stopped cleanly and stays stopped unless a service manager starts it again.
		if err = restartSelf(); err != nil {
			err = fmt.Errorf("the server stopped to restart and could not start itself again, so it stays stopped unless a service manager starts it: %w", err)
		}
	}
	if err != nil {
		// A failure after the log opened was written there already (see run).
		if _, ok := err.(loggedError); !ok {
			log.Error("portico-server stopped", "error", err)
		}
		os.Exit(1)
	}
}

// loggedError is a failure run has written to the server's log as well as to stderr.
type loggedError struct{ error }

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func run(log *slog.Logger) (err error) {
	data := flag.String("data", env("PORTICO_DATA", "data"), "folder for the database and everything else the server keeps (PORTICO_DATA)")
	listen := flag.String("listen", env("PORTICO_LISTEN", ":32500"), "address to serve the API on (PORTICO_LISTEN)")
	web := flag.String("web", env("PORTICO_WEB_DIR", ""), "folder of the built web app to serve; without it, a folder named web beside this binary (PORTICO_WEB_DIR)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The data folder is used from here on by its full path: the media tools' sandbox gives a
	// conversion only an absolute output folder, so a relative one (the default) made every
	// conversion fail.
	serverData, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	*data = serverData
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	// One server to a data folder, taken before anything in the folder is written.
	release, err := datalock.Hold(serverData, log)
	if err != nil {
		return err
	}
	defer release()
	// Nothing in the data folder is ever played as a library file.
	libraryfile.ServerData = serverData
	// Everything logged from here on is also kept in the data folder for Messages.
	serverLog, err := serverlog.Open(filepath.Join(*data, "logs"), os.Stderr)
	if err != nil {
		return err
	}
	defer serverLog.Close()
	log = slog.New(serverLog.Handler())
	slog.SetDefault(log)
	// From here a failure to start or to keep serving goes to the log file as well, so
	// Messages shows why after the next start. One before this point goes to stderr only.
	defer func() {
		if err != nil && !errors.Is(err, errRestart) {
			log.Error("portico-server stopped", "error", err)
			err = loggedError{err}
		}
	}()
	// Media tools are started through this binary as their helper. That lets a scan keep a
	// few confined workers going, where it would otherwise build a sandbox for every file.
	if _, err := mediaexec.Configure(mediaexec.Options{}); err != nil {
		return err
	}
	// The database, after a restore the owner staged, if there is one: see backup.OpenData.
	// Every sign-in a restore brought back is ended before anything is served.
	d, err := backup.OpenData(ctx, *data, log, time.Now)
	if err != nil {
		return err
	}
	defer d.Close()
	d.StartSearch(filepath.Join(*data, "search.db"), log)
	handler, err := api.New(ctx, d, *data, version, log)
	if err != nil {
		return err
	}
	// rclone, which remote sources run, stops with the server.
	defer handler.RemoteSources().Close()
	// Scans start by themselves from here on: watched folders and rescan intervals.
	go handler.RunSchedules(ctx)
	// No webhook may call Portico's own API.
	if _, port, err := net.SplitHostPort(*listen); err == nil {
		if n, err := strconv.Atoi(port); err == nil {
			handler.Webhooks().ListensOn(n)
		}
	}
	// The media engine plans and runs the streams. Its conversions folder is emptied at start:
	// no stream outlives a restart.
	engine, err := mediaengine.New(ctx, d, mediaengine.Options{Folder: filepath.Join(*data, "transcodes"), Log: log,
		Kept: filepath.Join(*data, "ffmpeg-abilities.json"), Remote: handler.RemoteSources().Open})
	if err != nil {
		return err
	}
	handler.Playback().SetEngine(engine, engine)
	handler.ServeMediaWith(engine.ServeMedia)
	// The web app is served by this same server, at every address that is not the API's, so
	// an install needs no web server in front of it.
	dir := webapp.Find(*web)
	if dir != "" && webapp.Holds(dir, *data) {
		// Everything in the web folder is handed to anyone who can reach the server, signed
		// in or not. The data folder holds the database, the token keys and the setup code.
		log.Error("the web folder is the server's data folder, or holds it, so the web app is NOT served: only the API is answered. "+
			"Give the web app a folder of its own", "web", dir, "data", *data)
		dir = ""
	} else if dir != "" {
		// A library inside the web folder would be handed out unsigned-in the same way, file
		// by file. Libraries change while the server runs, so this is said once, at start.
		if folders, err := d.LibraryFolders(ctx); err == nil {
			for _, folder := range folders {
				if webapp.Holds(dir, folder) || webapp.Holds(folder, dir) {
					log.Warn("the web folder and a library folder overlap: files of the library under the web folder can be fetched by anyone who "+
						"can reach the server, without signing in. Give the web app a folder of its own", "web", dir, "library", folder)
				}
			}
		}
	}
	if dir != "" {
		handler.ServeWebWith(webapp.Handler(dir))
		log.Info("serving the web app", "folder", dir)
	} else if webapp.Find(*web) == "" {
		log.Warn("no web app to serve: only the API is answered. Give its folder with -web, or put it in a folder named web beside this binary")
	}
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
	titleData := &titledata.Updater{DB: d, Dir: filepath.Join(*data, "title-data"), Log: log}
	// A test publication, signed with a throwaway key and served from this machine, for trying
	// the title data before a release is signed. A release never sets these.
	if key := os.Getenv("PORTICO_TITLE_DATA_KEY"); key != "" {
		if titleData.Key, err = titledata.ParseKey(key); err != nil {
			return fmt.Errorf("PORTICO_TITLE_DATA_KEY: %w", err)
		}
		titleData.Address = os.Getenv("PORTICO_TITLE_DATA_ADDRESS")
		log.Warn("title data comes from a test publication", "address", titleData.Address)
	}
	go titleData.Run(ctx)
	// No write timeout: media and the events feed are long replies. Slow request headers are bounded.
	// Messages read the log kept in the data folder.
	handler.UseLog(serverLog)
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
