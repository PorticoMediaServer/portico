package httpapi

import (
	"context"
	"math"
	"net/http"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// ConcurrencyDiagnostics is the whole admission and database picture in one
// document. Every number here is one a capacity decision was made from, so the
// same numbers have to be visible when that decision is revisited: a lane
// capacity is only defensible next to its rejection count and queue wait, and a
// pool size is only defensible next to WaitCount and WaitDuration.
type ConcurrencyDiagnostics struct {
	Pool dbwork.PoolStats `json:"pool"`
	// Snapshots is read-snapshot admission: how many composite reads hold a
	// pooled connection right now, how many may, and how long callers waited for
	// a permit. It is the number that says whether composite reads are starving
	// the writer.
	Snapshots dbwork.SnapshotStats `json:"readSnapshots"`
	WriteGate dbwork.GateStats     `json:"writeGate"`
	// WriterAdmission is the writer's bounded ticket pool: how full it is,
	// what users displaced, and what it refused (each a writer_capacity).
	WriterAdmission dbwork.AdmissionStats `json:"writerAdmission"`
	// WritePlans is every committed write measured against its work budget,
	// with the recent overruns; UngatedWrites counts changes that bypassed
	// the gate. Both say where the writer's time went when a hold was long.
	WritePlans    dbwork.PlanStats       `json:"writePlans"`
	UngatedWrites dbwork.UngatedStats    `json:"ungatedWrites"`
	Pressure      dbwork.PressureStats   `json:"pressure"`
	Health        dbwork.HealthReport    `json:"health"`
	Lanes         []LaneDiagnostics      `json:"httpLanes"`
	JobLanes      []operations.LaneState `json:"jobLanes"`
	Search        SearchDiagnostics      `json:"search"`
	// Reads is the process-wide statement picture: how much work callers asked
	// for, next to the pool and gate numbers that say how long they waited.
	Reads dbwork.ReadStats `json:"reads"`
	// Publications is the number of gated write transactions committed since the
	// process started: one per batch, which is the granularity a read-model cache
	// or a reconciler wants when it asks "has anything been published since?"
	Publications uint64 `json:"publications"`
	// Authorization is the bounded authority caches. `authorityEpoch` is the
	// fence they hang on: it moves whenever a security-fence transaction commits
	// or any statement writes a table that decides authority, and every entry
	// made before that stops matching.
	Authorization AuthorizationCacheDiagnostics `json:"authorizationCaches"`
	// RouteCosts is statements, transactions and pooled-connection acquisitions
	// per request, per route. A route whose statement count is linear in library
	// size shows here long before it shows in a percentile.
	RouteCosts []RouteCost `json:"routeCosts"`
	// Process, WAL, Helpers and Integrity are the triage half: what the runtime,
	// the write-ahead log, the media helper pool and the database's own
	// structure look like when something is wrong that the percentiles do not
	// explain.
	// Conversions is the host-capacity bound on simultaneous media conversions:
	// how many are running, how many this machine will run at once, and how many
	// starts have been turned away. The third number is how an owner learns
	// their hardware is the limit rather than their network.
	Conversions playback.ConversionCapacity `json:"conversions"`
	Process     ProcessDiagnostics          `json:"process"`
	WAL         dbwork.WALStats             `json:"wal"`
	Helpers     storage.SupervisorStats     `json:"storageHelpers"`
	Integrity   IntegrityDiagnostics        `json:"integrity"`
}

// AuthorizationCacheDiagnostics reports the authority caches and their fence.
type AuthorizationCacheDiagnostics struct {
	Principals          PrincipalCacheStats `json:"principals"`
	LibraryGrants       int                 `json:"libraryGrants"`
	GrantCapacity       int                 `json:"libraryGrantCapacity"`
	AuthorityGeneration uint64              `json:"authorityGeneration"`
}

// IntegrityDiagnostics is what the server knows about its database's structural
// health. ForeignSchemaObjects counts objects this build does not define — a
// state directory written by a different or newer Portico — and is the first
// thing to look at when the server behaves in a way the code cannot explain.
type IntegrityDiagnostics struct {
	dbwork.IntegrityReport
	ForeignSchemaObjects int `json:"foreignSchemaObjects"`
}

// ProcessDiagnostics is the triage view a field incident needs: whether
// goroutines are leaking, where the heap is against the limit the garbage
// collector is working to, and whether anything has panicked. Without these
// three a hung server and a leaking one look identical from outside.
type ProcessDiagnostics struct {
	Goroutines     int    `json:"goroutines"`
	HeapAllocBytes uint64 `json:"heapAllocBytes"`
	HeapInUseBytes uint64 `json:"heapInUseBytes"`
	HeapSysBytes   uint64 `json:"heapSysBytes"`
	NextGCBytes    uint64 `json:"nextGCBytes"`
	GCCycles       uint32 `json:"gcCycles"`
	// MemoryLimitBytes is the soft limit the collector is working to. Null
	// means Go's unbounded sentinel or a finite limit beyond the shared
	// safe-integer bound; MemoryLimitBytesExact then carries that finite value
	// as a decimal string so it is never rounded as a JSON number.
	MemoryLimitBytes      *int64          `json:"memoryLimitBytes"`
	MemoryLimitBytesExact string          `json:"memoryLimitBytesExact,omitempty"`
	ContainedPanics       uint64          `json:"containedPanics"`
	LoopRestarts          uint64          `json:"supervisedLoopRestarts"`
	MaxProcs              int             `json:"maxProcs"`
	DatabaseCalls         uint64          `json:"databaseCalls"`
	OpenFileSoftLimit     uint64          `json:"openFileSoftLimit"`
	OpenFileHardLimit     uint64          `json:"openFileHardLimit"`
	ProcessCPU            operations.Fact `json:"processCpu"`
	CapacityPolicy        string          `json:"capacityPolicy"`
}

// diagnosticsSafeInteger is the largest integer both generated contract copies
// decode exactly; larger JSON numbers have already lost precision in
// JavaScript before validation.
const diagnosticsSafeInteger = int64(9007199254740991)

// memoryLimitBytes projects Go's soft memory limit onto the wire. MaxInt64 is
// Go's unbounded sentinel, so it is null. A finite limit inside the shared
// safe-integer bound stays the exact integer; a larger finite limit is null
// plus its exact decimal string rather than an unsafe JSON number.
func memoryLimitBytes(limit int64) *int64 {
	if limit < 0 || limit > diagnosticsSafeInteger {
		return nil
	}
	value := limit
	return &value
}

func memoryLimitBytesExact(limit int64) string {
	if limit <= diagnosticsSafeInteger || limit == math.MaxInt64 {
		return ""
	}
	return strconv.FormatInt(limit, 10)
}

type processDiagnosticsShape ProcessDiagnostics

// JSONSchema keeps the published integer bounded to the shared safe range and
// declares the optional exact decimal companion. Unbounded and unrepresentable
// limits are represented by the required nullable integer, never by a wide
// JSON number.
func (ProcessDiagnostics) JSONSchema() map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[processDiagnosticsShape]())
	if err != nil {
		panic(err)
	}
	properties := shape["properties"].(map[string]any)
	properties["memoryLimitBytes"] = map[string]any{"type": "integer", "nullable": true, "minimum": 0, "maximum": diagnosticsSafeInteger}
	properties["memoryLimitBytesExact"] = map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$"}
	return shape
}

// processDiagnostics samples the runtime. ReadMemStats stops the world briefly,
// which is acceptable on an owner-only diagnostic and is why this is not on any
// other path.
func processDiagnostics() ProcessDiagnostics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	soft, hard := hostlimits.OpenFiles()
	limit := debug.SetMemoryLimit(-1)
	return ProcessDiagnostics{
		Goroutines:            runtime.NumGoroutine(),
		HeapAllocBytes:        mem.HeapAlloc,
		HeapInUseBytes:        mem.HeapInuse,
		HeapSysBytes:          mem.HeapSys,
		NextGCBytes:           mem.NextGC,
		GCCycles:              mem.NumGC,
		MemoryLimitBytes:      memoryLimitBytes(limit),
		MemoryLimitBytesExact: memoryLimitBytesExact(limit),
		ContainedPanics:       supervise.Panics(),
		LoopRestarts:          supervise.Restarts(),
		MaxProcs:              runtime.GOMAXPROCS(0),
		DatabaseCalls:         dbwork.DatabaseCalls(),
		OpenFileSoftLimit:     soft,
		OpenFileHardLimit:     hard,
		ProcessCPU:            operations.ProcessCPU(),
		CapacityPolicy:        "Resource pressure is advisory; explicit owner caps govern admission.",
	}
}

// SearchDiagnostics reports the per-viewer search governor.
type SearchDiagnostics struct {
	Active         int    `json:"active"`
	PerViewerLimit int    `json:"perViewerLimit"`
	GlobalLimit    int    `json:"globalLimit"`
	Rejected       uint64 `json:"rejected"`
}

func (d Dependencies) concurrencyDiagnostics(ctx context.Context) ConcurrencyDiagnostics {
	out := ConcurrencyDiagnostics{
		Process:         processDiagnostics(),
		WAL:             dbwork.WAL(ctx, d.DB),
		Pool:            dbwork.Pool(d.DB),
		Snapshots:       dbwork.Snapshots(),
		WriteGate:       dbwork.WriteGate().Stats(),
		WriterAdmission: dbwork.WriteGate().Admission(),
		WritePlans:      dbwork.Plans(),
		UngatedWrites:   dbwork.Ungated(),
		Pressure:        dbwork.Pressure(),
		Lanes:           []LaneDiagnostics{},
		JobLanes:        []operations.LaneState{},
		Search:          SearchDiagnostics{PerViewerLimit: maxSearchesPerViewer, GlobalLimit: maxSearchesGlobal},
		Reads:           dbwork.Reads(),
		Publications:    dbwork.Publications(),
		Authorization: AuthorizationCacheDiagnostics{
			Principals:          d.principals.stats(),
			LibraryGrants:       d.access.size(),
			GrantCapacity:       accessCacheEntries,
			AuthorityGeneration: dbwork.AuthorityGeneration(),
		},
		RouteCosts: RouteCosts(),
	}
	if d.admission != nil {
		out.Lanes = d.admission.diagnostics()
		d.admission.searchMu.Lock()
		out.Search.Active = d.admission.searchTotal
		d.admission.searchMu.Unlock()
		out.Search.Rejected = d.admission.searchDenied.Load()
	}
	if d.Watchdog != nil {
		out.Health = d.Watchdog.Report()
	}
	if d.Storage != nil {
		out.Helpers = d.Storage.Supervisor.Stats()
	}
	if d.Playback != nil {
		out.Conversions = d.Playback.Conversions()
	}
	out.Integrity = IntegrityDiagnostics{IntegrityReport: dbwork.LastIntegrity(), ForeignSchemaObjects: dbwork.ForeignSchemaObjects()}
	return out
}

func (d Dependencies) concurrencyRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/diagnostics/concurrency", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		out := d.concurrencyDiagnostics(r.Context())
		if d.Scheduler != nil {
			out.JobLanes = d.Scheduler.LaneStates(r.Context())
		}
		write(w, 200, out)
	})
	// The database's structural health, checked now rather than whenever the
	// scheduled pass last ran. It is the heavy admin lane because quick_check
	// and foreign_key_check both read the whole database.
	d.httpHandle(mux, "POST /v1/admin/diagnostics/integrity", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		write(w, 200, IntegrityDiagnostics{IntegrityReport: dbwork.Integrity(r.Context(), d.DB), ForeignSchemaObjects: dbwork.ForeignSchemaObjects()})
	})
	// Repair is a separate route from the check, and a POST, because it deletes
	// rows. It removes orphans only from children the schema declares ON DELETE
	// CASCADE — where the schema has already said what removing the parent
	// means — and reports the rest for a person to decide about. Nothing here
	// runs on a timer: a repair nobody asked for is a repair nobody reviews.
	d.httpHandle(mux, "POST /v1/admin/diagnostics/integrity/repair", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		report, err := persistence.RepairCascadeOrphans(r.Context(), d.DB)
		if err != nil {
			// A partial repair is still a result worth returning: it says what was
			// removed before it stopped, which is what an owner needs to decide
			// whether to run it again.
			write(w, 200, map[string]any{"repair": report, "incomplete": true, "error": "The repair could not be completed."})
			return
		}
		write(w, 200, map[string]any{"repair": report, "incomplete": false})
	})
	// Readiness is deliberately unauthenticated and carries no internals: a
	// supervisor has to be able to ask "are you up?" of a server that is still
	// opening its database. The shared writer probe is the same one /health/ready
	// uses, so a healthy read pool cannot hide a blocked writer.
	d.httpHandle(mux, "GET /v1/readiness", func(w http.ResponseWriter, r *http.Request) {
		result := d.readiness(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		if !result.Ready {
			w.Header().Set("Retry-After", "2")
			write(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "status": result.Status})
			return
		}
		write(w, http.StatusOK, map[string]any{"ready": true, "status": result.Status})
	})
}
