package httpapi

import (
	"net/http"
	httppprof "net/http/pprof"
	"runtime/pprof"
	"strconv"
)

// A field wedge — a stuck stream, an exhausted supervisor pool, a leaked
// goroutine — is currently undiagnosable without attaching a debugger to
// someone's home server. The Go runtime already knows the answer; it just was
// not reachable.
//
// It is mounted behind the same owner authority as every other diagnostic, in
// the admin-heavy lane (capacity 4), so profiling can never itself be the reason
// a viewer waits. The lane's ten-second request budget bounds a CPU profile or a
// trace, which is why the duration is clamped rather than left at the runtime's
// thirty-second default.

// pprofMaxSeconds keeps a sampled profile inside the admin-heavy lane's request
// budget. A profile that outlives its budget returns a truncated body and an
// error, which is worse than a shorter profile that completes.
const pprofMaxSeconds = 8

// pprofRoutes mounts the runtime profiles.
func (d Dependencies) pprofRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/diagnostics/pprof/{profile}", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		name := r.PathValue("profile")
		switch name {
		case "profile", "trace":
			clampProfileSeconds(r)
			if name == "profile" {
				httppprof.Profile(w, r)
				return
			}
			httppprof.Trace(w, r)
		case "cmdline":
			httppprof.Cmdline(w, r)
		case "symbol":
			httppprof.Symbol(w, r)
		default:
			// goroutine, heap, allocs, block, mutex, threadcreate — whatever this
			// runtime actually registered, rather than a list that drifts.
			if pprof.Lookup(name) == nil {
				w.Header().Set("Cache-Control", "no-store")
				write(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "No such runtime profile.", "retryable": false}})
				return
			}
			httppprof.Handler(name).ServeHTTP(w, r)
		}
	})
}

// clampProfileSeconds bounds the sampling window the caller asked for, and
// supplies a short default in place of the runtime's thirty seconds.
func clampProfileSeconds(r *http.Request) {
	query := r.URL.Query()
	seconds, err := strconv.Atoi(query.Get("seconds"))
	if err != nil || seconds < 1 || seconds > pprofMaxSeconds {
		seconds = pprofMaxSeconds
	}
	query.Set("seconds", strconv.Itoa(seconds))
	r.URL.RawQuery = query.Encode()
}
