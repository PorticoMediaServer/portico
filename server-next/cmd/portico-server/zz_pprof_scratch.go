package main

// SCRATCH, measurement branch only: never in a pull request.

import (
	"net/http"
	_ "net/http/pprof"
	"os"
)

func init() {
	if addr := os.Getenv("PORTICO_SCRATCH_PPROF"); addr != "" {
		go http.ListenAndServe(addr, nil)
	}
}
