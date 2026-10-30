package dbwork

import (
	"log"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// An idle server should be idle. About ten background loops polled the database
// every 250-300 milliseconds whether or not there was anything to do, which is
// most of the remaining idle cost and means a disk that can never spin down.
// "Idle" is not an opinion, though, so it is counted: every database call that
// goes through this package increments this, and the concurrency diagnostic
// publishes it, so the claim is checked by subtraction over a minute rather than
// asserted.
//
// It counts calls made through dbwork, which is every write in the tree and the
// reads that go through the helpers. The health watchdog probes the handle
// directly and is deliberately not counted: it is the one thing that should
// still be talking to the database on an idle server.
var databaseCalls atomic.Uint64

// DatabaseCalls reports how many calls this package has made.
func DatabaseCalls() uint64 { return databaseCalls.Load() }

// PORTICO_DBWORK_TRACE=1 additionally attributes those calls to the code that
// made them, and prints the busiest sites every thirty seconds. It is how a loop
// that polls when it has nothing to do is found by name rather than by reading
// ten files and guessing. Off by default and free when off: one atomic add.
var (
	traceCalls  = os.Getenv("PORTICO_DBWORK_TRACE") == "1"
	callSitesMu sync.Mutex
	callSites   = map[string]uint64{}
	commitSites = map[string]uint64{}
	traceOnce   sync.Once
)

func countCall() {
	databaseCalls.Add(1)
	if traceCalls {
		recordCallSite()
	}
}

// noteCommitSite attributes a commit that actually changed rows, which is what
// wakes every background loop. A loop that writes on every pass keeps the whole
// server awake, and this is how it is found by name.
func noteCommitSite() {
	if !traceCalls {
		return
	}
	traceOnce.Do(startCallTrace)
	site := callerSite(4)
	callSitesMu.Lock()
	commitSites[site]++
	callSitesMu.Unlock()
}

func recordCallSite() {
	traceOnce.Do(startCallTrace)
	site := callerSite(4)
	callSitesMu.Lock()
	callSites[site]++
	callSitesMu.Unlock()
}

// callerSite names the first frame outside this package.
func callerSite(skip int) string {
	var frames [8]uintptr
	depth := runtime.Callers(skip, frames[:])
	iterator := runtime.CallersFrames(frames[:depth])
	for {
		frame, more := iterator.Next()
		if frame.Function != "" && !strings.Contains(frame.Function, "/internal/dbwork.") {
			return shortFunction(frame.Function)
		}
		if !more {
			return "unknown"
		}
	}
}

func shortFunction(name string) string {
	if index := strings.LastIndex(name, "/"); index >= 0 {
		return name[index+1:]
	}
	return name
}

func startCallTrace() {
	supervise.Go("dbwork.call-trace", func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		previous := map[string]uint64{}
		previousCommits := map[string]uint64{}
		for range ticker.C {
			callSitesMu.Lock()
			type entry struct {
				name  string
				delta uint64
			}
			var entries, commits []entry
			for name, total := range callSites {
				if delta := total - previous[name]; delta > 0 {
					entries = append(entries, entry{name, delta})
				}
				previous[name] = total
			}
			for name, total := range commitSites {
				if delta := total - previousCommits[name]; delta > 0 {
					commits = append(commits, entry{name, delta})
				}
				previousCommits[name] = total
			}
			callSitesMu.Unlock()
			sort.Slice(entries, func(i, j int) bool { return entries[i].delta > entries[j].delta })
			for index, e := range entries {
				if index >= 12 {
					break
				}
				log.Printf("dbwork trace: %6d calls in 30s  %s", e.delta, e.name)
			}
			sort.Slice(commits, func(i, j int) bool { return commits[i].delta > commits[j].delta })
			for index, e := range commits {
				if index >= 8 {
					break
				}
				log.Printf("dbwork trace: %6d CHANGING commits in 30s  %s", e.delta, e.name)
			}
		}
	})
}
