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
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
)

// version is set by the release build.
var version = "dev"

func main() {
	// The sandbox around ffmpeg starts this same binary again as its helper.
	// That must be answered before anything else, flags included.
	if handled, err := decoder.RunHelper(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
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
	d, err := db.Open(ctx, filepath.Join(*data, "main.db"))
	if err != nil {
		return err
	}
	defer d.Close()
	handler, err := api.New(ctx, d, *data, version, log)
	if err != nil {
		return err
	}
	// Until the owner can enter provider keys in Settings, a TMDB read access token can be
	// given here. PORTICO_TMDB_ORIGIN points it at a stand-in for testing.
	if token := os.Getenv("PORTICO_TMDB_TOKEN"); token != "" {
		handler.UseProvider(tmdb.New(token, env("PORTICO_TMDB_ORIGIN", tmdb.Origin)))
	}
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
	// No write timeout: media and the events feed are long replies. Slow request headers are bounded.
	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	log.Info("portico-server is serving", "listen", *listen, "data", *data, "version", version)

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	// Let requests in flight finish, then close the database.
	wait, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(wait); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Where everyone had got to is saved before the database closes.
	handler.Playback().Save(wait, db.User)
	log.Info("portico-server stopped cleanly")
	return nil
}
