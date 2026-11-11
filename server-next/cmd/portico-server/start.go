package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
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
		fmt.Println(version)
		return true, nil
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
