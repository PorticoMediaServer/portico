package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// catalogCommand schedules durable source enumeration while the server is
// stopped. It never deletes a serving image, runs workers, opens a listener or
// creates a new state directory. The next normal server start does the work in
// its ordinary bounded transactions. A second process must never bypass the
// process-local writer gate, so use exactly the startup instance lock.
func catalogCommand(ctx context.Context, args []string, out io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "catalog" {
		return false, nil
	}
	if len(args) < 3 || args[1] != "rebuild" {
		return true, errors.New("usage: portico-server catalog rebuild <domain> [--data-dir <directory>]")
	}
	domain, err := strconv.Atoi(args[2])
	valid := false
	for _, d := range compactcatalog.RebuildDomains() {
		valid = valid || d == domain
	}
	if err != nil || !valid {
		return true, fmt.Errorf("unknown catalogue domain %q; registered domains: %v", args[2], compactcatalog.RebuildDomains())
	}
	flags := flag.NewFlagSet("catalog rebuild", flag.ContinueOnError)
	flags.SetOutput(out)
	data := flags.String("data-dir", "", "server data directory (defaults to normal startup location)")
	if err = flags.Parse(args[3:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, errors.New("unexpected catalog rebuild arguments")
	}
	var layout storagepaths.Layout
	if *data == "" {
		layout, err = startupLayout()
		if err != nil {
			return true, err
		}
	} else {
		if !filepath.IsAbs(*data) {
			return true, errors.New("data directory must be absolute")
		}
		layout = storagepaths.At(filepath.Clean(*data))
	}
	dbPath := storagepaths.Path(layout.Data, "server.sqlite")
	info, err := os.Stat(dbPath)
	if err != nil {
		return true, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return true, errors.New("catalog rebuild requires an existing server database")
	}
	lock, err := acquireInstanceLock(storagepaths.Path(layout.Data, "server.lock"))
	if err != nil {
		return true, err
	}
	defer lock.Close()
	db, err := persistence.Open(dbPath)
	if err != nil {
		return true, err
	}
	defer db.Close()
	if err = compactcatalog.NewWorker(db).Rebuild(ctx, domain); err != nil {
		return true, err
	}
	_, err = fmt.Fprintf(out, "Catalogue domain %d queued for rebuild; start the server to run its bounded worker.\n", domain)
	return true, err
}
