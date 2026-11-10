//go:build crashpoints

package db

import (
	"os"
	"strconv"
	"sync/atomic"
	"syscall"

	"modernc.org/sqlite"
)

// Built with -tags crashpoints and run with PORTICO_CRASH_AT=N, the process kills itself
// with SIGKILL at the Nth change it is about to make to a database: a row inserted, updated
// or deleted, or a commit, counted from the start. The crash tool runs the same sequence of
// writes for N = 1, 2, 3 and so on, so every point at which a write can die is tried once.
// No product build includes this file.
var crashAt, crashPoints atomic.Int64

func init() {
	n, _ := strconv.ParseInt(os.Getenv("PORTICO_CRASH_AT"), 10, 64)
	if n <= 0 {
		return
	}
	crashAt.Store(n)
	sqlite.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, _ string) error {
		if h, ok := c.(sqlite.HookRegisterer); ok {
			h.RegisterPreUpdateHook(func(sqlite.SQLitePreUpdateData) { crashPoint() })
			h.RegisterCommitHook(func() int32 { crashPoint(); return 0 })
		}
		return nil
	})
}

func crashPoint() {
	if crashPoints.Add(1) == crashAt.Load() {
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {} // the signal is on its way; nothing more may be written
	}
}
