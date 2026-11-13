package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/PorticoMediaServer/portico/server-next/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// What the packaged program needs at its start: the names the release packages give its
// settings, the questions it answers without serving, and a data folder when given none.

var (
	showVersion = flag.Bool("version", false, "print the version and stop")
	check       = flag.Bool("health-check", false, "ask the server running at the listening address whether it is ready, and stop: exit status 0 when it is")
)

// packagedStart answers --version and --health-check, after which the program is done, and
// otherwise fills in the data folder when none was given.
func packagedStart(listen string, data *string) (done bool, err error) {
	if *showVersion {
		return true, json.NewEncoder(os.Stdout).Encode(buildinfo.Info())
	}
	if *check {
		// The check says only what it found. It is not the server, and "the server stopped"
		// in a container's health log would be wrong about a server that is still starting.
		if err := healthCheck(listen); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true, nil
	}
	if *data == "" {
		folder, err := defaultData()
		if err != nil {
			return false, err
		}
		*data = folder
	}
	return false, nil
}

// packaged reads a setting that the release packages name one way and this server named
// another. The packaged name wins where both are set and differ, and the log says so: a
// server that started on an empty data folder in the wrong place would look like a lost one.
func packaged(log *slog.Logger, packagedName, name, fallback string) string {
	v, other := os.Getenv(packagedName), os.Getenv(name)
	if v == "" {
		return env(name, fallback)
	}
	if other != "" && other != v {
		log.Warn("two settings name the same thing and differ; the first is used", "used", packagedName, "value", v, "ignored", name, "ignoredValue", other)
	}
	return v
}

// answerService answers the Windows installer's service arguments, or runs as the service
// itself, and then ends the program. Anywhere else it returns at once.
func answerService() {
	handled, err := serviceCommand()
	if !handled {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// watchService stops the server when it is the worker of a Windows service and its standard
// input closes: that is how the service tells it to stop, and it also happens if the service
// process dies.
func watchService(stop func()) {
	if os.Getenv(supervise.WorkerEnvironment) == "1" {
		supervise.WatchParent(stop, os.Stdin)
	}
}

// logStderr is where the log goes besides its files. A package whose service manager keeps no
// output says so (PORTICO_LOG_STDERR=off): the files are then the only log.
func logStderr() io.Writer {
	if os.Getenv("PORTICO_LOG_STDERR") == "off" {
		return io.Discard
	}
	return os.Stderr
}
