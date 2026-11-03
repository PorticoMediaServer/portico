package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Scratch branch only (server load review): the statement profile is on from
// start; SIGUSR2 empties it and SIGUSR1 writes it beside the data directory.
func init() {
	if len(os.Args) > 1 {
		return // helper modes
	}
	dbwork.ProfileStatements(true)
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for s := range signals {
			if s == syscall.SIGUSR2 {
				dbwork.ProfileStatements(true)
				dbwork.ScratchResetFlushes()
				continue
			}
			out, _ := json.MarshalIndent(dbwork.StatementProfile(), "", " ")
			_ = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("PORTICO_DATA_DIR")), "statement-profile-"+strconv.Itoa(os.Getpid())+".json"), out, 0o600)
			nested, _ := json.MarshalIndent(dbwork.ScratchNestedStacks(), "", " ")
			_ = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("PORTICO_DATA_DIR")), "nested-"+strconv.Itoa(os.Getpid())+".json"), nested, 0o600)
			flushes, _ := json.MarshalIndent(dbwork.ScratchFlushes(), "", " ")
			_ = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("PORTICO_DATA_DIR")), "cache-flushes-"+strconv.Itoa(os.Getpid())+".json"), flushes, 0o600)
		}
	}()
}
