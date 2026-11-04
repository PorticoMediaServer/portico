package main

import (
	"context"
	"database/sql"
	"os"
	"runtime/pprof"
)

var cpuProfiling bool
var cpuProfileFile *os.File

func startCPUProfile(path string) {
	if path == "" {
		return
	}
	f, e := os.Create(path)
	must(e)
	must(pprof.StartCPUProfile(f))
	cpuProfileFile = f
	cpuProfiling = true
}
func stopCPUProfile() {
	if cpuProfileFile != nil {
		pprof.StopCPUProfile()
		cpuProfileFile.Close()
	}
}
func profiledExec(db interface {
	Exec(string, ...any) (sql.Result, error)
}, tag, q string, a ...any) {
	if !cpuProfiling {
		exec(db, q, a...)
		return
	}
	pprof.Do(context.Background(), pprof.Labels("native_sql", tag), func(context.Context) { exec(db, q, a...) })
}
