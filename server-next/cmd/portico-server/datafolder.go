package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// defaultData is where the server keeps everything when it was given no data folder: a place
// that is the same whatever folder it was started in. A service started by launchd starts in
// "/", and one started by Windows in System32.
func defaultData() (string, error) {
	home, _ := os.UserHomeDir()
	_, docker := os.Stat("/.dockerenv")
	return dataFolder(runtime.GOOS, os.Getenv, home, docker == nil)
}

// dataFolder is defaultData for a system and environment given to it. The release packages
// say what they are with PORTICO_CONTAINER=1 and PORTICO_SERVICE=1.
func dataFolder(goos string, env func(string) string, home string, docker bool) (string, error) {
	service := env("PORTICO_SERVICE") == "1"
	switch {
	case docker || env("PORTICO_CONTAINER") == "1":
		return "/config", nil
	case goos == "windows":
		base := env("LOCALAPPDATA")
		if service {
			if base = env("ProgramData"); base == "" {
				base = `C:\ProgramData`
			}
		}
		if base == "" {
			return "", errors.New("no data folder was given and LOCALAPPDATA is not set")
		}
		return base + `\Portico`, nil
	case service:
		return "/var/lib/portico", nil
	case home == "":
		return "", errors.New("no data folder was given and this user has no home folder")
	case goos == "darwin":
		return filepath.Join(home, "Library", "Application Support", "Portico"), nil
	}
	return filepath.Join(home, ".local", "share", "portico"), nil
}

// oldServerData refuses a data folder that holds the database of the server before this one
// and none of this one's. This server cannot read that database, and starting empty beside
// it would look as if the libraries and accounts were lost.
func oldServerData(data string) error {
	if _, err := os.Stat(filepath.Join(data, "main.db")); err == nil {
		return nil
	}
	for _, old := range []string{filepath.Join("Database", "server.sqlite"), "server.sqlite"} {
		if _, err := os.Stat(filepath.Join(data, old)); err == nil {
			return errors.New("the data folder " + data + " holds the data of an earlier Portico server, which this version cannot read: " +
				"choose another data folder (-data, or PORTICO_DATA_DIR), or move that one away")
		}
	}
	return nil
}
