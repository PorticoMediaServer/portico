package main

import "runtime"

// Scratch branch only (server load review): sample blocking and mutex
// contention so /v1/admin/diagnostics/pprof/{block,mutex} have data.
func init() {
	runtime.SetBlockProfileRate(100_000)
	runtime.SetMutexProfileFraction(20)
}
