package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

// The owner console's part of starting and stopping, kept here so main.go holds only a call
// for each: the log kept in the data folder for Messages, a failure written into it, and the
// restart the owner asks for.

// errRestart asks main to start the server again once everything has stopped.
var errRestart = errors.New("restart")

// loggedError is a failure run has written to the server's log as well as to stderr.
type loggedError struct{ error }

// openServerLog opens the log kept in the data folder for Messages. Everything logged with the
// logger it answers is kept there as well as written to stderr.
func openServerLog(dataDir string, stderr io.Writer) (*serverlog.Log, *slog.Logger, error) {
	l, err := serverlog.Open(filepath.Join(dataDir, "logs"), stderr)
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(l.Handler())
	slog.SetDefault(log)
	return l, log, nil
}

// closeServerLog writes a failure to start or to keep serving into the log file as well, so
// Messages shows why after the next start, and closes the log. A failure before the log
// opened goes to stderr only.
func closeServerLog(l *serverlog.Log, log *slog.Logger, err *error) {
	if *err != nil && !errors.Is(*err, errRestart) {
		log.Error("portico-server stopped", "error", *err)
		*err = loggedError{*err}
	}
	l.Close()
}

// stopped is what main does with run's answer, and says whether the server stopped on a
// failure. A restart the owner asked for starts the same program with the same arguments, in
// this same process. If that fails (the program file was removed, or replaced by one that
// cannot run here), the server has already stopped cleanly and stays stopped unless a
// service manager starts it again. A failure already written to the log is not written again.
func stopped(log *slog.Logger, err error) bool {
	if errors.Is(err, errRestart) {
		if err = restartSelf(); err != nil {
			err = fmt.Errorf("the server stopped to restart and could not start itself again, so it stays stopped unless a service manager starts it: %w", err)
		}
	}
	if err == nil {
		return false
	}
	if _, ok := err.(loggedError); !ok {
		log.Error("portico-server stopped", "error", err)
	}
	return true
}
