package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var n1EnabledFlag = flag.Bool("n1-component", false, "opt in to test-only N1 component measurement/controls")
var n1CaseFlag = flag.String("n1-component-case", "active/current/present/fixture", "closed N1 component banks/age/bucket/input cell")
var n1CohortFlag = flag.String("n1-component-cohort", "timing", "timing or native-counters; never pool the two")

type n1Case struct {
	Banks  string `json:"banks"`
	Age    string `json:"age"`
	Bucket string `json:"bucket"`
	Input  string `json:"input"`
}

func parseN1Case(value string) (n1Case, error) {
	p := strings.Split(value, "/")
	if len(p) != 4 {
		return n1Case{}, ErrInvalid
	}
	if (p[0] != "active" && p[0] != "dual") || (p[1] != "current" && p[1] != "dirty") || (p[2] != "present" && p[2] != "absent") || (p[3] != "fixture" && p[3] != "escaped") {
		return n1Case{}, ErrInvalid
	}
	return n1Case{p[0], p[1], p[2], p[3]}, nil
}

func n1NS(origin, event time.Time) *int64 {
	if event.IsZero() {
		return nil
	}
	n := event.Sub(origin).Nanoseconds()
	return &n
}

func n1Duration(start, end time.Time) *int64 {
	n := end.Sub(start).Nanoseconds()
	return &n
}

func n1Error(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func n1Join(previous string, err error) string {
	if err == nil {
		return previous
	}
	if previous == "" {
		return err.Error()
	}
	return errors.Join(errors.New(previous), err).Error()
}

type n1Clock struct {
	HoldStartedNS                *int64 `json:"hold_started_ns"`
	PhysicalReturnedNS           *int64 `json:"physical_returned_ns"`
	HoldNS                       *int64 `json:"hold_ns"`
	CommitAttemptedNS            *int64 `json:"commit_attempted_ns"`
	CommitReturnedNS             *int64 `json:"commit_returned_ns"`
	CommitNS                     *int64 `json:"commit_ns"`
	CommitNotAttempted           *bool  `json:"commit_not_attempted"`
	RollbackStartedNS            *int64 `json:"rollback_started_ns"`
	RollbackReturnedNS           *int64 `json:"rollback_returned_ns"`
	RollbackNS                   *int64 `json:"rollback_ns"`
	InvocationStartedNS          *int64 `json:"invocation_started_ns"`
	InvocationReturnedNS         *int64 `json:"invocation_returned_ns"`
	InvocationNS                 *int64 `json:"invocation_ns"`
	TurnSetupStartedNS           *int64 `json:"turn_setup_started_ns"`
	TurnSetupReturnedNS          *int64 `json:"turn_setup_returned_ns"`
	TurnSetupNS                  *int64 `json:"turn_setup_ns"`
	AcquireNS                    *int64 `json:"acquire_ns"`
	AssertNS                     *int64 `json:"assert_ns"`
	BeginNS                      *int64 `json:"begin_ns"`
	PublicationNS                *int64 `json:"publication_ns"`
	ConsumerNS                   *int64 `json:"consumer_ns"`
	PostCommitToPhysicalReturnNS *int64 `json:"post_commit_to_physical_return_ns"`
	XSyncNS                      *int64 `json:"xsync_ns"`
}

type n1Settings struct {
	Actual         *sqliteconn.Settings `json:"actual"`
	DQSDDL         *int                 `json:"dqs_ddl"`
	DQSDML         *int                 `json:"dqs_dml"`
	PolicyVerified *bool                `json:"policy_verified"`
	AssertError    string               `json:"assert_error"`
	ConnectionID   *uint64              `json:"connection_id"`
}

type n1MemorySample struct {
	AtNS               int64             `json:"at_ns"`
	GoHeapAlloc        uint64            `json:"go_heap_alloc_bytes"`
	GoHeapInuse        uint64            `json:"go_heap_inuse_bytes"`
	GoTotalAlloc       uint64            `json:"go_total_alloc_bytes"`
	GoMallocs          uint64            `json:"go_mallocs"`
	GoGCs              uint32            `json:"go_gc_cycles"`
	CurrentRSSBytes    *int64            `json:"current_rss_bytes"`
	ProcessMaxRSSBytes *int64            `json:"process_max_rss_bytes"`
	ProcessUserNS      *int64            `json:"process_user_cpu_ns"`
	ProcessSystemNS    *int64            `json:"process_system_cpu_ns"`
	ProcessUserTicks   *uint64           `json:"process_user_cpu_ticks"`
	ProcessSystemTicks *uint64           `json:"process_system_cpu_ticks"`
	ProcessIO          map[string]uint64 `json:"process_io"`
	Unavailable        map[string]string `json:"unavailable_reasons"`
}

func n1Memory(origin time.Time) *n1MemorySample {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	out := &n1MemorySample{AtNS: time.Since(origin).Nanoseconds(), GoHeapAlloc: m.HeapAlloc, GoHeapInuse: m.HeapInuse, GoTotalAlloc: m.TotalAlloc, GoMallocs: m.Mallocs, GoGCs: m.NumGC, Unavailable: map[string]string{}}
	out.Unavailable["process_cpu_ns"] = "procfs CPU counters are ticks; no observed ticks-per-second conversion"
	if raw, err := os.ReadFile("/proc/self/stat"); err == nil {
		if end := strings.LastIndexByte(string(raw), ')'); end >= 0 {
			fields := strings.Fields(string(raw[end+1:]))
			if len(fields) >= 13 {
				if n, err := strconv.ParseUint(fields[11], 10, 64); err == nil {
					out.ProcessUserTicks = &n
				}
				if n, err := strconv.ParseUint(fields[12], 10, 64); err == nil {
					out.ProcessSystemTicks = &n
				}
			}
		}
	}
	if out.ProcessUserTicks == nil || out.ProcessSystemTicks == nil {
		out.Unavailable["process_cpu_ticks"] = "procfs process CPU counters unavailable"
	}
	if raw, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			p := strings.Fields(line)
			if len(p) == 3 && p[0] == "VmHWM:" && p[2] == "kB" {
				if n, err := strconv.ParseInt(p[1], 10, 64); err == nil {
					n *= 1024
					out.ProcessMaxRSSBytes = &n
				}
			}
		}
	}
	if out.ProcessMaxRSSBytes == nil {
		out.Unavailable["process_max_rss_bytes"] = "procfs process-lifetime VmHWM unavailable"
	}
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 1 {
			if pages, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				n := pages * int64(os.Getpagesize())
				out.CurrentRSSBytes = &n
			}
		}
	}
	if out.CurrentRSSBytes == nil {
		out.Unavailable["current_rss_bytes"] = "procfs resident-page observation unavailable"
	}
	if raw, err := os.ReadFile("/proc/self/io"); err == nil {
		out.ProcessIO = map[string]uint64{}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			p := strings.Fields(line)
			if len(p) != 2 {
				out.Unavailable["process_io"] = "malformed procfs counter"
				continue
			}
			n, err := strconv.ParseUint(p[1], 10, 64)
			if err != nil {
				out.Unavailable[p[0]] = err.Error()
			} else {
				out.ProcessIO[strings.TrimSuffix(p[0], ":")] = n
			}
		}
	} else {
		out.Unavailable["process_io"] = err.Error()
	}
	return out
}

type n1File struct {
	Bytes          *int64 `json:"logical_bytes"`
	AllocatedBytes *int64 `json:"allocated_bytes"`
	Error          string `json:"error"`
}

type n1WAL struct {
	StartedNS  int64              `json:"started_ns"`
	ReturnedNS int64              `json:"returned_ns"`
	Token      *uint64            `json:"gate_token"`
	State      *governor.WALState `json:"state"`
	Error      string             `json:"error"`
	Header     []byte             `json:"header"`
	Files      map[string]n1File  `json:"files"`
}

func n1ReadWAL(origin time.Time, path string) *n1WAL {
	out := &n1WAL{StartedNS: time.Since(origin).Nanoseconds(), Files: map[string]n1File{}}
	state, err := governor.ReadWAL(path)
	if err == nil {
		out.State = &state
	} else {
		out.Error = err.Error()
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		var f n1File
		if info, err := os.Stat(path + suffix); err != nil {
			f.Error = err.Error()
		} else {
			size := info.Size()
			f.Bytes = &size
			if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
				stat := reflect.ValueOf(info.Sys())
				if stat.Kind() == reflect.Pointer && !stat.IsNil() {
					blocks := stat.Elem().FieldByName("Blocks")
					if blocks.IsValid() && blocks.CanInt() {
						allocated := blocks.Int() * 512
						f.AllocatedBytes = &allocated
					}
				}
			}
		}
		out.Files["db"+suffix] = f
	}
	if file, err := os.Open(path + "-wal"); err == nil {
		header := make([]byte, 32)
		if _, err = io.ReadFull(file, header); err == nil {
			out.Header = header
		}
		_ = file.Close()
	}
	out.ReturnedNS = time.Since(origin).Nanoseconds()
	return out
}

type n1Attempt struct {
	Ordinal                int                        `json:"attempt_ordinal"`
	ID                     string                     `json:"attempt_id"`
	Case                   n1Case                     `json:"case"`
	Cohort                 string                     `json:"cohort"`
	Phase                  string                     `json:"reached_phase"`
	Reference              *ReceiptReference          `json:"original_reference"`
	DatabaseID             string                     `json:"database_id"`
	DatabasePath           string                     `json:"database_path"`
	PhysicalAttemptOrdinal *int                       `json:"physical_attempt_ordinal"`
	StoredProfiles         []n1StoredProfile          `json:"stored_search_profiles"`
	AgeState               []int64                    `json:"age_input_published_pending"`
	InputLengths           map[string]int             `json:"input_lengths"`
	BoundRequest           *governor.Request          `json:"bound_synthetic_request"`
	MemoryPartition        map[string]uint64          `json:"synthetic_memory_partition"`
	WorkLimit              *Work                      `json:"synthetic_work_limit"`
	Cardinalities          map[string]int64           `json:"fixture_cardinalities"`
	RequiredBytes          *uint64                    `json:"inventory_fresh_required_bytes"`
	RegistrySHA256         string                     `json:"captured_registry_manifest_sha256"`
	Clock                  n1Clock                    `json:"owner_clock"`
	Settings               n1Settings                 `json:"settings"`
	Outcome                string                     `json:"physical_outcome"`
	Error                  string                     `json:"error"`
	Refusal                *bool                      `json:"refusal"`
	Fenced                 *bool                      `json:"frame_fenced"`
	Ambiguous              *bool                      `json:"commit_ambiguous"`
	RowsClosed             *bool                      `json:"rows_closed"`
	ConnectionReturned     *bool                      `json:"connection_returned"`
	Retired                *bool                      `json:"custody_retired"`
	NativeJoined           *bool                      `json:"native_joined"`
	PoolStats              *sql.DBStats               `json:"pool_stats"`
	Usage                  *[4]governor.Amounts       `json:"authority_usage"`
	Result                 *InventoryResult           `json:"component_result"`
	Units                  []n1Unit                   `json:"units"`
	LedgerOverflow         bool                       `json:"unit_ledger_overflow"`
	WALBefore              *n1WAL                     `json:"wal_before"`
	WALAfter               *n1WAL                     `json:"wal_after"`
	Memory                 map[string]*n1MemorySample `json:"memory_checkpoints"`
	NativeBefore           *n1NativeSnapshot          `json:"native_before"`
	NativeAfter            *n1NativeSnapshot          `json:"native_after"`
	Continuity             n1ConnectionContinuity     `json:"connection_continuity"`
	NativeCloses           []n1NativeClose            `json:"native_closes"`
	NativeCloseOverflow    bool                       `json:"native_close_overflow"`
	Complete               bool                       `json:"complete"`
	Unavailable            map[string]string          `json:"unavailable_reasons"`
}

type n1StoredProfile struct {
	ID                                      int64
	BankKey, Recipe, Engine, Unicode, State string
}

var n1Missing = map[string]string{
	"examined_rejected_rows":  "driver exposes returned/changed rows, not all examined/rejected rows",
	"native_memory_peak":      "DBStatus memory is current; high=0 is not a peak",
	"whole_input_live_peak":   "checkpoint heap/RSS/allocations do not identify complete overlapping live ownership",
	"consumer":                "pure component; production publication, consumer and recovery not exercised",
	"gate_token":              "inventoryMemoryFixtureTurn owns its WALGate; it does not export its token",
	"xsync":                   "existing native WALIO observes writes, not xSync counts or duration",
	"device_io_peak":          "process IO and file allocation are not per-device requests, queue depth, class or peak core demand",
	"canonical_encoded_bytes": "existing canonical digest API does not return encoded byte count",
	"acquire_assert_begin":    "turn_setup is enclosing fixture call; exact acquire/assert phases not exposed",
	"whole_legal_min_max":     "fixture and escaped-evidence sample are a partial input matrix",
	"cache_population":        "explicit first-command population; setup touches OS cache; neither OS-cold, warmed steady state nor sustained reset",
	"owner_intervals":         "null means phase not reached or private fixture did not return its owner view; no timing reconstructed",
	"settings_actual":         "null until an actual verified Settings observation is available from the private turn",
	"rows_changed_returning":  "native driver rows expose RETURNING rows, not a separate changed-row count",
}

type n1Stream struct {
	file     *os.File
	origin   time.Time
	base     map[string]any
	sequence uint64
	err      error
}

func (s *n1Stream) append(kind string, fields map[string]any) error {
	if s.err != nil {
		return s.err
	}
	s.sequence++
	record := make(map[string]any, len(s.base)+len(fields)+3)
	for k, v := range s.base {
		record[k] = v
	}
	for k, v := range fields {
		record[k] = v
	}
	record["kind"], record["sequence"], record["at_ns"] = kind, s.sequence, time.Since(s.origin).Nanoseconds()
	var raw []byte
	raw, s.err = json.Marshal(record)
	if len(raw) > 2<<20 {
		s.err = errors.New("N1 component record exceeds bounded 2 MiB wire")
	}
	if s.err == nil {
		_, s.err = s.file.Write(append(raw, '\n'))
	}
	if s.err == nil {
		s.err = s.file.Sync()
	} // ALWAYS outside custody
	return s.err
}

func n1SeedCase(t *testing.T, e *executor, c *inventoryAcceptCommand, cell n1Case) *inventoryAcceptCommand {
	t.Helper()
	for _, q := range []struct {
		enabled bool
		sql     string
	}{
		{cell.Banks == "dual", `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) SELECT 2,'raw-building',recipe_version,engine_version,unicode_fingerprint,'building',1000 FROM search_generations WHERE id=1`},
		{cell.Banks == "dual", `UPDATE search_revision SET building_generation_id=2 WHERE singleton=1`},
		{cell.Age == "dirty", `UPDATE libraries SET age_published_revision=81,age_pending_operations=2 WHERE id='fresh-library'`},
		{cell.Bucket == "absent", `DELETE FROM catalog_age_item_counts WHERE block_unrated=0`},
	} {
		if q.enabled {
			if _, err := e.db.ExecContext(t.Context(), q.sql); err != nil {
				t.Fatal(err)
			}
		}
	}
	if cell.Input == "escaped" {
		r := c.request
		r.files = append([]inventoryFile(nil), r.files...)
		f := &r.files[0]
		f.objectIdentity, f.changeToken = strings.Repeat("&", 8192), strings.Repeat("\x00", 4096)
		f.size, f.modifiedNS = math.MaxInt64, math.MinInt64
		evidence := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation, Object: objectidentity.StableObjectIdentity(f.objectIdentity, r.source.rootIdentity), Size: f.size, ModifiedNS: f.modifiedNS, ChangeToken: f.changeToken}
		var err error
		f.revision, err = evidence.Revision()
		if err != nil {
			t.Fatal(err)
		}
		c, err = newInventoryAcceptCommand(c.identity, r)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// One original, one fresh component on the existing W fixture. No public
// issuer, dispatcher, opening constructor, grant or recipe is reproduced here.
func runN1Attempt(t *testing.T, ctx context.Context, stream *n1Stream, ordinal int, cell n1Case, cohort, failQuery string, observed **n1Attempt, options ...n1AttemptOptions) (record *n1Attempt) {
	t.Helper()
	population := "first-use-reopen"
	if len(options) > 1 {
		t.Fatal("multiple N1 population options")
	}
	if len(options) == 1 {
		population = options[0].population
	}
	if err := validateN1Population(population, 0); err != nil {
		t.Fatal(err)
	}
	record = &n1Attempt{Ordinal: ordinal, ID: fmt.Sprintf("%s/%d", stream.base["process_epoch"], ordinal), Case: cell, Cohort: cohort, Phase: "fixture_setup", Outcome: "unobserved", Memory: map[string]*n1MemorySample{}, Unavailable: n1Missing}
	record.Continuity = n1ConnectionContinuity{Population: population, WarmupOriginals: []ReceiptReference{}, UnavailableReason: errN1ProgressiveFixture.Error(), DeliberateErrorControl: failQuery != ""}
	*observed = record // also survives a helper's Fatal/Goexit before return
	record.Unavailable = make(map[string]string, len(n1Missing)+1)
	for key, reason := range n1Missing {
		record.Unavailable[key] = reason
	}
	var e *executor
	var turn *commandTurn
	var authority *governor.Authority
	var path string
	trace := newN1Trace(stream.origin, cohort == "native-counters")
	trace.failQuery = failQuery
	// Registered first, so canonical fixture cleanup joins/closes its pool before
	// emission. Defers below snapshot pre-close physical facts, even on Goexit.
	t.Cleanup(func() {
		if e != nil {
			stats := e.db.Stats()
			record.PoolStats = &stats
			joined := e.closed && stats.OpenConnections == 0 && trace.connections == trace.closed
			e.physicalFaults.mu.Lock()
			joined = joined && !e.physicalFaults.failed
			e.physicalFaults.mu.Unlock()
			record.NativeJoined = &joined
		}
		record.Memory["after_fixture_close"] = n1Memory(stream.origin)
		record.NativeCloses, record.NativeCloseOverflow = trace.closes[:trace.closeCount], trace.closeOverflow
		if t.Failed() && record.Error == "" {
			record.Error = "test fixture failed during " + record.Phase + "; see native units/test log"
		}
		record.Complete = record.Error == "" && record.Outcome == "committed" && record.NativeJoined != nil && *record.NativeJoined && record.Clock.HoldNS != nil && record.Settings.Actual != nil && !record.LedgerOverflow && !record.NativeCloseOverflow
		if err := stream.append("attempt", map[string]any{"attempt_id": record.ID, "attempt_ordinal": ordinal, "attempt": record}); err != nil {
			t.Error(err)
		}
	})
	defer func() {
		if turn != nil {
			rowsErr := turn.closeRows()
			rowsClosed := rowsErr == nil
			record.RowsClosed = &rowsClosed
			if !turn.transaction.done {
				_, err := turn.transaction.rollback(rowsErr)
				if err != nil {
					record.Error = n1Join(record.Error, err)
				}
			}
			tx := turn.transaction
			record.Clock.HoldStartedNS, record.Clock.PhysicalReturnedNS = n1NS(stream.origin, tx.start), n1NS(stream.origin, tx.physicalReturnedAt)
			if !tx.start.IsZero() && !tx.physicalReturnedAt.IsZero() {
				n := tx.physicalReturnedAt.Sub(tx.start).Nanoseconds()
				record.Clock.HoldNS = &n
			}
			record.Clock.CommitAttemptedNS, record.Clock.CommitReturnedNS = n1NS(stream.origin, tx.commitStartedAt), n1NS(stream.origin, tx.commitReturnedAt)
			if !tx.commitStartedAt.IsZero() && !tx.commitReturnedAt.IsZero() {
				n := tx.commitReturnedAt.Sub(tx.commitStartedAt).Nanoseconds()
				record.Clock.CommitNS = &n
			}
			if !tx.commitReturnedAt.IsZero() && !tx.physicalReturnedAt.IsZero() {
				record.Clock.PostCommitToPhysicalReturnNS = n1Duration(tx.commitReturnedAt, tx.physicalReturnedAt)
			}
			notAttempted := !tx.commitAttempted
			record.Clock.CommitNotAttempted = &notAttempted
			verified := tx.policyVerified
			record.Settings.PolicyVerified = &verified
			if verified {
				actual := tx.settings
				record.Settings.Actual = &actual
				record.Settings.DQSDDL, record.Settings.DQSDML = &actual.DQSDDL, &actual.DQSDML
			}
			if c, ok := tx.custody.(*governedTurn); ok {
				request, limit := c.expected, tx.workLimit
				record.BoundRequest, record.WorkLimit = &request, &limit
				record.MemoryPartition = map[string]uint64{"preparation_bytes": c.inventoryMemory.preparationBytes, "nonborrowable_reserve_bytes": c.inventoryMemory.reservedBytes}
			}
			if !tx.start.IsZero() {
				n := 1
				record.PhysicalAttemptOrdinal = &n
			}
			record.ConnectionReturned, record.Retired = &tx.connectionReturned, &tx.retirementComplete
			switch tx.result.state {
			case committed:
				record.Outcome = "committed"
			case rolledBack:
				record.Outcome = "rolled_back"
			case commitUnknown:
				record.Outcome = "unknown"
			}
			if tx.finishErr != nil {
				record.Error = n1Join(record.Error, tx.finishErr)
			}
		}
		trace.armed = false
		record.Continuity.observeAfter(trace)
		if population == "setup-retained-first-use" && record.Continuity.Same != nil && !*record.Continuity.Same {
			record.Error = n1Join(record.Error, errors.New("physical connection lifetime interrupted in retained-setup population"))
		}
		record.Units, record.LedgerOverflow = trace.units[:trace.used], trace.overflow
		for i := range record.Units {
			u := &record.Units[i]
			if u.Error != "" {
				switch u.UnitID {
				case "PRAGMA foreign_keys", "PRAGMA ignore_check_constraints", "PRAGMA defer_foreign_keys", "SELECT sqlite_version()", "PRAGMA journal_mode", "PRAGMA synchronous", "PRAGMA wal_autocheckpoint", "PRAGMA cache_size", "PRAGMA busy_timeout", "PRAGMA mmap_size", "PRAGMA query_only", "PRAGMA temp_store":
					record.Settings.AssertError = u.Error
				}
			}
			if u.Phase == "rollback" {
				record.Clock.RollbackStartedNS, record.Clock.RollbackReturnedNS, record.Clock.RollbackNS = u.StartedNS, u.ReturnedNS, u.ElapsedNS
			}
			if u.UnitID == "BEGIN IMMEDIATE" {
				record.Clock.BeginNS = u.ElapsedNS
			}
		}
		if e != nil {
			fenced, ambiguous := e.fenced, e.ambiguous
			record.Fenced, record.Ambiguous = &fenced, &ambiguous
			record.WALAfter = n1ReadWAL(stream.origin, path)
		}
		if trace.conn != nil {
			record.Settings.ConnectionID = &trace.conn.id
		}
		if trace.conn != nil && !trace.conn.closed {
			record.NativeAfter = &n1NativeSnapshot{}
			trace.conn.snapshot(record.NativeAfter, false)
		} else {
			record.Unavailable["native_after"] = "connection unavailable or already physically closed before external getter"
		}
		if authority != nil {
			usage := authority.Usage()
			record.Usage = &usage
		}
		record.Memory["after_physical_return"] = n1Memory(stream.origin)
	}()
	record.Memory["before_fixture_setup"] = n1Memory(stream.origin)
	var command *inventoryAcceptCommand
	var registry *stmt.Registry
	if population == "setup-retained-first-use" {
		e, path, command, registry = inventoryFreshFixture(t, trace.decorate)
	} else {
		e, path, command, registry = inventoryFreshFixture(t)
	}
	record.DatabaseID, record.DatabasePath = record.ID+"/db", path
	manifest, err := json.Marshal(registry.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifest)
	record.RegistrySHA256 = hex.EncodeToString(manifestHash[:])
	command = n1SeedCase(t, e, command, cell)
	record.AgeState = make([]int64, 3)
	if err := e.db.QueryRowContext(ctx, `SELECT age_input_revision,age_published_revision,age_pending_operations FROM libraries WHERE id='fresh-library'`).Scan(&record.AgeState[0], &record.AgeState[1], &record.AgeState[2]); err != nil {
		t.Fatal(err)
	}
	profiles, err := e.db.QueryContext(ctx, `SELECT id,bank_key,recipe_version,engine_version,unicode_fingerprint,state FROM search_generations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for profiles.Next() {
		var p n1StoredProfile
		if err := profiles.Scan(&p.ID, &p.BankKey, &p.Recipe, &p.Engine, &p.Unicode, &p.State); err != nil {
			_ = profiles.Close()
			t.Fatal(err)
		}
		record.StoredProfiles = append(record.StoredProfiles, p)
	}
	if err := errors.Join(profiles.Err(), profiles.Close()); err != nil {
		t.Fatal(err)
	}
	e.now = time.Now // real owner clock; existing synthetic Work is NOT widened
	request, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	record.Reference = &request.Receipt
	record.InputLengths = map[string]int{}
	for i, text := range inventoryRequestTexts(&command.request) {
		record.InputLengths[fmt.Sprintf("text_%02d", i)] = len(*text)
	}
	record.InputLengths["root_bytes"] = len(command.request.source.resolvedRoot)
	required, err := inventoryFreshRequiredBytes(command)
	if err != nil {
		t.Fatal(err)
	}
	record.RequiredBytes = &required
	record.Cardinalities = map[string]int64{}
	for _, table := range []string{"libraries", "library_sources", "inventory_runs", "catalog_entities", "catalog_assets", "search_generations", "catalog_age_item_counts"} {
		var n int64
		if err := e.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		record.Cardinalities[table] = n
	}
	if population == "first-use-reopen" {
		if err := e.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, e.config)
		if err != nil {
			t.Fatal(err)
		}
		e.openPool(connector, trace.decorate) // existing TEST-ONLY decoration seam
	}
	if err := e.db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	record.WALBefore = n1ReadWAL(stream.origin, path)
	record.NativeBefore = &n1NativeSnapshot{}
	trace.conn.snapshot(record.NativeBefore, false)
	id, connections, closes := trace.conn.id, trace.connections, trace.closeCount
	record.Continuity.BeforeID, record.Continuity.ConnectionsBefore = &id, &connections
	record.Continuity.NativeClosesBefore = &closes
	record.Phase = "prepared"
	if err := stream.append("attempt_start", map[string]any{"attempt_id": record.ID, "attempt_ordinal": ordinal, "database_id": record.DatabaseID, "database_path": path, "original_reference": record.Reference, "case": cell, "cohort": cohort, "fixture_cardinalities": record.Cardinalities, "input_lengths": record.InputLengths, "cache_state": population, "declared_warmup": 0}); err != nil {
		t.Fatal(err)
	}
	record.Memory["before_turn"] = n1Memory(stream.origin)
	if err := ctx.Err(); err != nil {
		record.Error = err.Error()
		return record
	}
	trace.armed = true
	record.Phase = "turn_setup"
	setup := time.Now()
	record.Clock.TurnSetupStartedNS = n1NS(stream.origin, setup)
	turn, authority = inventoryMemoryFixtureTurn(t, e, registry, command, inventoryMemoryBudget{preparationBytes: 4 << 20, reservedBytes: 4 << 20})
	returned := time.Now()
	record.Clock.TurnSetupReturnedNS = n1NS(stream.origin, returned)
	record.Clock.TurnSetupNS = n1Duration(setup, returned)
	record.Phase = "effects"
	invoke := time.Now()
	record.Clock.InvocationStartedNS = n1NS(stream.origin, invoke)
	out, err := applyInventoryAccept(ctx, turn, command)
	returned = time.Now()
	record.Clock.InvocationReturnedNS = n1NS(stream.origin, returned)
	record.Clock.InvocationNS = n1Duration(invoke, returned)
	record.Result = &out // zero public Outcome stays zero; no Applied/Evidence mint
	if err == nil {
		err = turn.closeRows()
	}
	if err == nil {
		record.Phase = "commit"
		_, err = turn.transaction.commit()
	} else {
		record.Phase = "rollback"
		_, err = turn.transaction.rollback(err)
	}
	if err != nil {
		record.Error = err.Error()
		var refusal *Refusal
		refused := errors.As(err, &refusal) || errors.Is(err, ErrQuantum)
		record.Refusal = &refused
	}
	record.Phase = "physical_return"
	return record
}

func TestN1ComponentMeasurement(t *testing.T) {
	if !*n1EnabledFlag {
		if os.Getenv("PORTICO_WRITER_N1_COMPONENT") != "" {
			t.Fatal("explicit component opt-in requires -n1-component")
		}
		t.Skip("opt-in test-only N1 target requires -n1-component")
	}
	if os.Getenv("PORTICO_WRITER_N1_COMPONENT") == "" {
		t.Fatal("missing PORTICO_WRITER_N1_COMPONENT=1 for explicit component opt-in")
	}
	if os.Getenv("PORTICO_WRITER_N1_COMPONENT") != "1" {
		t.Fatal("invalid component opt-in")
	}
	configFile, err := os.Open(os.Getenv("PORTICO_WRITER_N1_COMPONENT_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	data, configReadErr := io.ReadAll(io.LimitReader(configFile, 16385))
	if err := errors.Join(configReadErr, configFile.Close()); err != nil {
		t.Fatal(err)
	}
	config, err := decodeFirstLightConfig(data) // syntax only; NEW component wire
	if err != nil || config.Fixtures > 64 {
		t.Fatal("component config: 1..64 requested fixtures", err)
	}
	cell, err := parseN1Case(*n1CaseFlag)
	if err != nil || (*n1CohortFlag != "timing" && *n1CohortFlag != "native-counters") {
		t.Fatal("invalid closed component case/cohort", err)
	}
	if err := validateN1Population(*n1PopulationFlag, *n1WarmupFlag); err != nil {
		t.Fatal(err)
	}
	file, err := openFirstLightOutput(config.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	origin := time.Now()
	configHash := sha256.Sum256(data)
	stream := &n1Stream{file: file, origin: origin, base: map[string]any{"schema": "portico-n1-component-v1", "label": "N1 COMPONENT DIRECTIONAL", "qualification": false, "run_id": config.RunID, "process_epoch": fmt.Sprintf("%s/%d/%d", config.RunID, os.Getpid(), origin.UnixNano())}}
	sources := map[string]string{}
	for _, path := range []string{"scan_fresh_test.go", "inventory_memory_test.go", "governor_turn_test.go", "schema_fixture_test.go", "transaction_test.go", "n1_component_population_test.go", "scan_fresh.go", "scan_fresh_observation.go", "transaction.go", "n1_component_target_test.go", "n1_component_native_test.go", "testdata/scan-inventory-fresh.json"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("component source pin", err)
		}
		hash := sha256.Sum256(raw)
		sources[path] = hex.EncodeToString(hash[:])
	}
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Open(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, binary)
	closeErr := binary.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	host, hostErr := os.Hostname()
	boot, bootErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
	sqlHashes := map[stmt.ID]string{}
	for _, d := range scanInventoryFreshDefinitions() {
		h := sha256.Sum256([]byte(d.SQL))
		sqlHashes[d.ID] = hex.EncodeToString(h[:])
	}
	start := map[string]any{"origin_utc": origin.UTC(), "pid": os.Getpid(), "observed_host": host, "host_error": n1Error(hostErr), "host_epoch": config.HostEpoch, "boot_id": strings.TrimSpace(string(boot)), "boot_error": n1Error(bootErr), "declared_source_commit": config.SourceCommit, "binary_sha256": hex.EncodeToString(hash.Sum(nil)), "config_sha256": hex.EncodeToString(configHash[:]), "schema_sha256": schema.SHA256, "source_hashes": sources, "sql_sha256_by_id": sqlHashes, "requested_fixtures": config.Fixtures, "population": *n1PopulationFlag, "declared_warmup": *n1WarmupFlag, "steady_supported": false, "steady_unavailable_reason": errN1ProgressiveFixture.Error(), "case": cell, "cohort": *n1CohortFlag, "clock": "time.Now with monotonic time.Sub before JSON", "synthetic_memory_partition": map[string]uint64{"preparation_bytes": 4 << 20, "nonborrowable_reserve_bytes": 4 << 20}, "synthetic_work_source": "inventoryMemoryFixtureTurn; unchanged test recipe, NOT production profile", "db_status_methods": n1StatusOps, "native_counter_scope": "same physical connection; cumulative/current, reset=false; no native memory peak", "ledger_layout_bytes": reflect.TypeFor[n1Unit]().Size() * n1UnitLimit, "unavailable_reasons": n1Missing}
	if err := stream.append("start", start); err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	offered, started, physical, complete, failed, unknown, refused, fenced, joined := 0, 0, 0, 0, 0, 0, 0, 0, 0
	stopReason := ""
	defer func() {
		fields := map[string]any{"requested_fixtures": config.Fixtures, "offered": offered, "started": started, "physically_returned": physical, "native_joined": joined, "complete_attempts": complete, "error_attempts": failed, "unknown_attempts": unknown, "known_refusals": refused, "known_fenced": fenced, "complete": offered == config.Fixtures && complete == offered && stream.err == nil && !t.Failed() && ctx.Err() == nil, "stop_reason": n1Join(stopReason, ctx.Err()), "case": cell, "cohort": *n1CohortFlag, "population": *n1PopulationFlag, "declared_warmup": *n1WarmupFlag, "observed_warmup": 0, "steady_supported": false, "consumer": "not exercised", "unavailable_dimension_count": len(n1Missing), "unavailable_reasons": n1Missing}
		if err := stream.append("final", fields); err != nil {
			t.Error(err)
		}
	}()
	for ordinal := 1; ordinal <= config.Fixtures && ctx.Err() == nil; ordinal++ {
		var record *n1Attempt
		offered++
		ok := t.Run(fmt.Sprintf("attempt_%03d", ordinal), func(t *testing.T) {
			record = runN1Attempt(t, ctx, stream, ordinal, cell, *n1CohortFlag, "", &record, n1AttemptOptions{population: *n1PopulationFlag})
		})
		if record != nil && record.Clock.TurnSetupStartedNS != nil {
			started++
		}
		if record != nil && record.Clock.PhysicalReturnedNS != nil {
			physical++
		}
		if record != nil && record.Refusal != nil && *record.Refusal {
			refused++
		}
		if record != nil && record.Fenced != nil && *record.Fenced {
			fenced++
		}
		if record != nil && record.NativeJoined != nil && *record.NativeJoined {
			joined++
		}
		if record != nil && record.Complete {
			complete++
		} else {
			failed++
		}
		if record == nil || record.Outcome == "unknown" || record.Outcome == "unobserved" {
			unknown++
		}
		if !ok || record == nil || !record.Complete || stream.err != nil {
			stopReason = "incomplete attempt; see original error, units and test log"
			t.Error("N1 component incomplete; retain raw prefix")
			break
		}
	}
}
