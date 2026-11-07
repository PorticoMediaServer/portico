package writer

import (
	"database/sql/driver"
	"errors"
	"flag"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var ec5MemoryEnabled = flag.Bool("ec5-memory", false, "opt-in process memory timeline; separate instrumented component method")

// Existing QL/N1 checkpoints, not a second process-memory implementation.
type ComponentMemorySample struct {
	Method                  string
	StartedNS, ReturnedNS   int64
	Process                 *n1MemorySample
	Usage                   *[4]governor.Amounts
	NativeCurrent           any
	NativeUnavailableReason string
}

func EC5MemoryRequested() bool                   { return *ec5MemoryEnabled }
func (c *N1PairedComponent) MemoryEnabled() bool { return *ec5MemoryEnabled }
func (c *N1PairedComponent) SampleMemory() ComponentMemorySample {
	out := ComponentMemorySample{Method: "ec5-component-memory-v1", StartedNS: time.Since(c.stream.origin).Nanoseconds(), NativeUnavailableReason: "writer cache/schema/statement CURRENT bytes are in owner before/after-turn snapshots; reader/CP idle polling unsafe/unavailable; no aggregate native peak"}
	out.Process = n1Memory(c.stream.origin)
	if c.Authority != nil {
		usage := c.Authority.Usage()
		out.Usage = &usage
	}
	out.ReturnedNS = time.Since(c.stream.origin).Nanoseconds()
	return out
}
func (c *N1PairedComponent) memoryCheckpoint(phase string) error {
	if !*ec5MemoryEnabled {
		return nil
	}
	return c.stream.append("memory_checkpoint", map[string]any{"phase": phase, "memory": c.SampleMemory(), "qualified_profile_ready": false})
}

type ec5OpenObservation struct {
	ID                                            uint64
	StartedNS, ReturnedNS, ElapsedNS              int64
	Error                                         string
	CacheCurrent, SchemaCurrent, StatementCurrent *int64
	CounterStartedNS, CounterReturnedNS           int64
	Unavailable                                   map[string]string
}
type ec5OpenLedger struct {
	mu       sync.Mutex
	used     int
	records  [16]ec5OpenObservation
	overflow bool
}

// Called synchronously on the native owner connection immediately after its
// actual Connect returns. No connection/statement/pointer escapes this callback.
func (l *ec5OpenLedger) observe(origin time.Time, conn driver.Conn, started, returned time.Time, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used == len(l.records) {
		l.overflow = true
		return
	}
	r := &l.records[l.used]
	l.used++
	*r = ec5OpenObservation{ID: uint64(l.used), StartedNS: started.Sub(origin).Nanoseconds(), ReturnedNS: returned.Sub(origin).Nanoseconds(), ElapsedNS: returned.Sub(started).Nanoseconds(), Error: n1Error(err), Unavailable: map[string]string{"usable_open": "native Connect only; caller policy/assert/Capture and complete usable-open interval unavailable"}}
	r.CounterStartedNS = time.Since(origin).Nanoseconds()
	if native, ok := conn.(sqlite.DBStatus); ok && err == nil {
		for _, q := range []struct {
			name   string
			op     sqlite.DBStatusOp
			target **int64
		}{{"cache", sqlite.DBStatusCacheUsed, &r.CacheCurrent}, {"schema", sqlite.DBStatusSchemaUsed, &r.SchemaCurrent}, {"statement", sqlite.DBStatusStmtUsed, &r.StatementCurrent}} {
			current, _, e := native.Status(q.op, false)
			if e != nil {
				r.Unavailable[q.name] = e.Error()
			} else {
				v := int64(current)
				*q.target = &v
			}
		}
	} else {
		r.Unavailable["native_memory"] = "no observed native Status capability on failed/unavailable connection"
	}
	r.CounterReturnedNS = time.Since(origin).Nanoseconds()
}
func (c *N1PairedComponent) finishMemory() error {
	if !*ec5MemoryEnabled {
		return nil
	}
	if err := c.memoryCheckpoint("after_joined_fixture_close"); err != nil {
		return err
	}
	c.memoryOpens.mu.Lock()
	defer c.memoryOpens.mu.Unlock()
	err := c.stream.append("memory_lifecycle", map[string]any{"method": "ec5-component-memory-v1", "reader_native_opens": c.memoryOpens.records[:c.memoryOpens.used], "reader_open_overflow": c.memoryOpens.overflow, "writer_native_open": nil, "writer_native_open_unavailable_reason": "initial physical opening precedes existing decorator; Setup/constructor elapsed is not native Connect", "reader_idle_native_close": nil, "reader_idle_close_unavailable_reason": "Snapshot owner Close is observed separately; idle native close hook unavailable", "complete_lifecycle": false, "native_peak": nil, "native_peak_unavailable_reason": "current DBStatus samples are not an all-connection live peak"})
	if c.memoryOpens.overflow {
		err = errors.Join(err, errors.New("EC5 native-open ledger overflow; incomplete"))
	}
	return err
}

func (c *N1PairedComponent) verifyMemoryCgroup() error {
	if !*ec5MemoryEnabled {
		if os.Getenv("PORTICO_EC5_MEMORY") != "" {
			return errors.New("EC5 companion flag required")
		}
		return nil
	}
	if os.Getenv("PORTICO_EC5_MEMORY") != "1" {
		return errors.New("EC5 explicit flag requires companion env")
	}
	host := qual.CaptureHost() // QL's existing raw hierarchy/namespace evidence
	if err := c.stream.append("memory_host", map[string]any{"method": "ec5-component-memory-v1", "host": host, "qualification": false}); err != nil {
		return err
	}
	if len(host.Cgroups) == 0 || filepath.Clean("/"+host.Cgroups[0].Path) != filepath.Clean(os.Getenv("PORTICO_EC5_CGROUP")) {
		return errors.New("EC5 actual cgroup differs from owned capped leaf")
	}
	effective := uint64(2000000000)
	for i, cg := range host.Cgroups {
		for _, name := range []string{"memory.max", "memory.swap.max"} {
			raw, ok := cg.Files[filepath.ToSlash(filepath.Join(cg.Path, name))]
			if !ok {
				if i == 0 || cg.Path != host.MountPoint {
					return errors.New("EC5 leaf cap unavailable")
				}
				continue
			}
			value := strings.TrimSpace(raw)
			if value == "max" {
				if i == 0 {
					return errors.New("EC5 leaf unbounded")
				}
				continue
			}
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			if i == 0 && name == "memory.max" && (n > 2000000000 || n <= 2000000000-uint64(os.Getpagesize())) {
				return errors.New("EC5 leaf not page-rounded assigned 2e9B")
			}
			if name == "memory.max" && n < effective {
				effective = n
			}
			if i == 0 && name == "memory.swap.max" && n != 0 {
				return errors.New("EC5 swap must be zero")
			}
		}
	}
	if effective > 2000000000 || effective <= 2000000000-uint64(os.Getpagesize()) {
		return errors.New("EC5 effective budget is not the requested page-rounded 2e9B")
	}
	return nil
}
