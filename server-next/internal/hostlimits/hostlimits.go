// Package hostlimits reads and, where the OS allows it, raises the per-process
// resource ceilings this server actually runs into.
//
// At two hundred streaming viewers the file-descriptor count per stream is a
// client socket, a media file or helper socketpair and the helper's own
// descriptors — call it three or four — on top of the SQLite pool, its WAL and
// shm, the scanner, and every stat the converter does. Docker's default soft
// NOFILE is 1024 and macOS often starts at 256. Crossing that limit does not
// crash: `accept` returns EMFILE and net/http backs off for a second, so the
// server degrades into "everything is mysteriously broken" rather than failing
// in a way anyone can read. Raising the soft limit to the hard one costs nothing
// and needs no privilege.
package hostlimits

import (
	"log"
	"os"
	"runtime/debug"
)

// automaticMemoryLimit leaves room for memory outside the Go runtime, including
// SQLite's allocator, the binary, helper processes and the operating system.
// The reserve is a planning allowance, not a hard RSS guarantee. Go's limit is
// soft and can be exceeded by a working set that cannot be collected.
func automaticMemoryLimit(memory int64) int64 {
	if memory <= 0 {
		return 0
	}
	soft := memory - max(memory/4, int64(128<<20))
	if soft < 64<<20 {
		// A guessed limit below the minimum working allowance would keep the
		// collector busy without establishing that the server fits this host.
		return 0
	}
	return soft
}

// OpenFiles reports the current soft and hard descriptor limits. On a platform
// with no such limit both are zero, which is the honest answer rather than a
// fabricated ceiling.
func OpenFiles() (soft, hard uint64) { return openFileLimits() }

// EffectiveMemoryBytes reports the smaller discoverable physical-memory and
// process cgroup ceiling. It sizes bounded resources and the default Go runtime
// memory limit; zero means neither source could be measured.
func EffectiveMemoryBytes() int64 {
	physical, _ := physicalMemoryBytes()
	cgroup, _ := cgroupMemoryLimit()
	return effectiveMemoryBytes(physical, cgroup)
}

func effectiveMemoryBytes(physical, cgroup uint64) int64 {
	if physical == 0 || cgroup > 0 && cgroup < physical {
		physical = cgroup
	}
	// Untrusted OS text must never wrap a signed budget negative.
	if physical > 1<<63-1 {
		return 0
	}
	return int64(physical)
}

// Apply raises the descriptor limit and, where a memory ceiling is discoverable,
// gives the garbage collector one to work to. It logs what it did, because both
// numbers are things an owner reading a support bundle needs to see, and it
// never fails: a server that cannot raise its own limits still runs.
func Apply() {
	if soft, hard, raised := raiseOpenFiles(); raised {
		log.Printf("Open file limit raised to %d (hard limit %d)", soft, hard)
	} else if soft > 0 && soft < 4096 {
		// Worth saying out loud: at this ceiling a busy evening degrades into
		// mysterious failures rather than an honest refusal.
		log.Printf("Open file limit is %d, which is low for a server carrying many streams; the hard limit is %d", soft, hard)
	}
	if os.Getenv("GOMEMLIMIT") != "" {
		// The runtime already parsed this owner override at process startup,
		// including "off". Do not silently replace it with our default.
		return
	}
	memory := EffectiveMemoryBytes()
	soft := automaticMemoryLimit(memory)
	if soft == 0 || soft >= debug.SetMemoryLimit(-1) {
		// Unknown/tiny hosts retain the runtime default. An embedding caller's
		// smaller programmatic limit also remains in force.
		return
	}
	debug.SetMemoryLimit(soft)
	log.Printf("Go runtime memory limit set to %d MiB, from effective memory of %d MiB", soft>>20, memory>>20)
}
