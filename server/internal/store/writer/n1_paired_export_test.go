package writer

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/walobserve"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// Exported only into writer's external tests. No executor/connection/frame/grant
// or dispatcher capability crosses this opaque test composition boundary.
type N1PairedComponent struct {
	Governor                       governor.Governor
	Authority                      *governor.Authority
	Snapshots                      *governor.Snapshots
	Browse                         *read.BrowseReader
	Foreground                     *read.ForegroundReader
	View                           read.BrowseInput
	PointView                      read.BrowseInput
	DatabaseID                     string
	RunID, HostEpoch, SourceCommit string
	Measured                       int
	stream                         *n1Stream
	fixture                        *inventoryProgressiveFixture
	trace                          *n1Trace
	summary                        n1SteadySummary
	baseline                       uint64
	baselineConnections            uint64
	baselineCloses                 int
	original, call                 int
	warmup                         int
	stopped                        error
	closed                         bool
	reader                         *n1PairedReaderIdentity
	memoryOpens                    ec5OpenLedger
	native                         *ec2NativeComponent
}

type n1PairedReaderIdentity struct {
	device, inode uint64
	connections   atomic.Uint64
}

func (c *n1PairedReaderIdentity) observe(conn driver.Conn) error {
	getter, ok := conn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		return errors.New("reader native file attestation unavailable")
	}
	device, inode, err := getter.SQLiteMainFileIdentity()
	if err != nil || device != c.device || inode != c.inode {
		return errors.Join(governor.ErrComposition, err)
	}
	c.connections.Add(1)
	return nil
}

func NewN1PairedComponent(t *testing.T, data []byte, warmup int, cohort string) *N1PairedComponent {
	t.Helper()
	return newN1PairedPopulationComponent(t, data, warmup, cohort, attachN1PairedPopulation(t))
}

func newN1PairedPopulationComponent(t *testing.T, data []byte, warmup int, cohort string, population *QualificationPopulationManifest, native ...bool) *N1PairedComponent {
	t.Helper()
	if *ec5MemoryEnabled && cohort != "native-counters" {
		t.Fatal("EC5 native-memory instrumentation requires separate native-counters cohort")
	}
	cfg, err := decodeFirstLightConfig(data)
	if err != nil || cfg.Fixtures > 64 || warmup < 1 || warmup > 64 || cfg.Fixtures+warmup > inventoryProgressiveLimit || (cohort != "timing" && cohort != "native-counters") {
		t.Fatal("invalid bounded paired config", err)
	}
	file, err := openFirstLightOutput(cfg.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	origin := time.Now()
	c := &N1PairedComponent{RunID: cfg.RunID, HostEpoch: cfg.HostEpoch, SourceCommit: cfg.SourceCommit, Measured: cfg.Fixtures, warmup: warmup}
	c.DatabaseID = fmt.Sprintf("%s/%d/%d/db", cfg.RunID, os.Getpid(), origin.UnixNano())
	c.stream = &n1Stream{file: file, origin: origin, base: map[string]any{"schema": "portico-n1-component-v1", "component_layout": "n1-paired-foreground-v1", "label": "N1 PAIRED COMPONENT DIRECTIONAL", "qualification": false, "run_id": cfg.RunID, "process_epoch": c.DatabaseID, "qualified_profile_ready": false}}
	if err := c.stream.append("composition_start", map[string]any{"declared_source_commit": cfg.SourceCommit, "host_epoch": cfg.HostEpoch, "database_id": c.DatabaseID, "requested_measured_originals": cfg.Fixtures, "requested_warmup_originals": warmup, "cohort": cohort}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !c.closed {
			c.Close(errors.New("fixture exited before explicit joined final"))
		}
	})
	if err := c.verifyMemoryCgroup(); err != nil {
		t.Fatal(err)
	}
	if err := c.memoryCheckpoint("before_fixture"); err != nil {
		t.Fatal(err)
	}
	c.trace = newN1Trace(origin, cohort == "native-counters")
	var w *executor
	var path string
	var original *inventoryAcceptCommand
	if population != nil {
		w, path, _ = testExecutor(t, c.trace.decorate)
		original = seedInventoryFreshPopulation(t, w, true)
	} else {
		w, path, original = inventoryFreshSeedFixture(t, c.trace.decorate)
	}
	w.now = time.Now // before ANY measured BEGIN; no clock option exists here
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var existingLibraries []string
	if population != nil {
		existingLibraries = []string{"qualification-movies"}
	}
	foregroundSource := readfixture.ForegroundOn(t, path, conn, existingLibraries...) // exact existing seed journal; no second writer
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	gc := readfixture.LiveConfig(time.Now)
	var owner *inventoryMemoryFixture
	if len(native) > 1 {
		t.Fatal("one compiled native composition")
	}
	if len(native) == 1 && native[0] {
		owner, c.native = newEC2NativeOwner(t, w, path, original, c.trace, gc)
		w = owner.executor
	} else {
		registry := nativeInventoryFixtureRegistry(t, w, inventoryCommandDefinitions(), "scan_fresh_test.go", "../internal/readfixture/source.go")
		owner = newInventoryMemoryFixture(t, w, registry, original, inventoryMemoryBudget{preparationBytes: 4 << 20, reservedBytes: 4 << 20}, gc)
	}
	registry := owner.registry
	c.fixture = &inventoryProgressiveFixture{owner: owner, base: original, limit: warmup + cfg.Fixtures, ready: true, sharedForeground: true}
	c.fixture.sharedMaintenance = c.native != nil
	c.fixture.snapshot, err = c.fixture.currentSnapshot(t)
	if err != nil {
		t.Fatal(err)
	}
	c.Governor, c.Authority = owner.governor, owner.authority
	c.trace.useRegistry(registry)
	c.baseline = c.trace.conn.id
	c.baselineConnections, c.baselineCloses = c.trace.connections, c.trace.closeCount
	raw, _ := json.Marshal(registry.Manifest())
	digest := sha256.Sum256(raw)
	facts := &n1Attempt{}
	n1ReadFixtureFacts(t, t.Context(), w, facts, original.request.scope.libraryKey)
	c.summary = n1SteadySummary{FixtureSetup: &n1FixtureSetup{facts.AgeState, facts.StoredProfiles, facts.Cardinalities}, RequestedWarmup: warmup, RequestedMeasured: cfg.Fixtures, DatabaseID: c.DatabaseID, RegistrySHA256: hex.EncodeToString(digest[:]), Turns: map[string]*n1TurnCensus{}, WarmupOriginals: []ReceiptReference{}}
	for _, role := range []string{"fresh_begin", "resolve", "ack"} {
		c.summary.Turns[role] = &n1TurnCensus{}
	}
	openedDatabases := 1
	c.summary.OpenedDatabases = &openedDatabases
	readerConfig := readfixture.ReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), path, readerConfig, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := db.Conn(t.Context())
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	readRegistry := nativeFixtureRegistry(t, rc, readerConfig, read.HarnessDefinitions(), "../internal/readfixture/source.go", "../read/foreground.go")
	if err := errors.Join(rc.Close(), db.Close()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	device, inode, err := stateownership.SingleLinkedFileIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	getter, ok := c.trace.conn.native.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		t.Fatal("writer native file attestation unavailable")
	}
	writerDevice, writerInode, identityErr := getter.SQLiteMainFileIdentity()
	if identityErr != nil || writerDevice != device || writerInode != inode {
		t.Fatal("writer/path native file binding mismatch", identityErr)
	}
	connector, err := sqliteconn.Connector(path, readerConfig, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	c.reader = &n1PairedReaderIdentity{device: device, inode: inode}
	readerConnector := &faultConnector{Connector: connector, observeNative: c.reader.observe}
	if *ec5MemoryEnabled {
		readerConnector.connectObserved = func(conn driver.Conn, start, end time.Time, err error) {
			c.memoryOpens.observe(origin, conn, start, end, err)
		}
	}
	c.Snapshots, err = governor.NewSnapshots(readerConnector, readerConfig, c.Governor, c.Authority, readRegistry)
	if err != nil {
		t.Fatal(err)
	}
	c.Browse, err = read.NewBrowseReader(c.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	c.Foreground, err = read.NewForegroundReader(c.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	c.View = read.BrowseInput{Database: c.DatabaseID, TokenHash: "token", Library: 1, Kind: 1, Limit: 40}
	c.PointView = c.View // existing detailed playback/personal fixture remains library1
	if population != nil {
		c.View.Library = 7
	} // browse traverses the actual canonical1M parent
	if c.native != nil {
		c.attachEC2WC(t, population)
		n1ReadFixtureFacts(t, t.Context(), w, facts, original.request.scope.libraryKey)
		c.summary.FixtureSetup = &n1FixtureSetup{facts.AgeState, facts.StoredProfiles, facts.Cardinalities}
		c.baseline, c.baselineConnections, c.baselineCloses = c.trace.conn.id, c.trace.connections, c.trace.closeCount
	}
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Open(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash := sha256.New()
	_, readErr := io.Copy(binaryHash, binary)
	if err := errors.Join(readErr, binary.Close()); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for _, name := range []string{"n1_paired_export_test.go", "n1_paired_population_test.go", "ec2_native_component_test.go", "ec2_native_bindings_external_test.go", "wc_component_fixture_test.go", "canonical_n1_binding_test.go", "ec3_write_waits_external_test.go", "ec5_memory_export_test.go", "ec5_memory_external_test.go", "ec2_sustained_external_test.go", "../qual/workload/ec2.go", "transaction_test.go", "n1_paired_external_test.go", "n1_component_steady_test.go", "n1_progressive_fixture_test.go", "inventory_memory_test.go", "governor_turn_test.go", "scan_fresh_test.go", "../internal/readfixture/source.go", "../internal/readfixture/config.go", "../qual/workload/foreground.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw)
		hashes[name] = hex.EncodeToString(hash[:])
	}
	host, hostErr := os.Hostname()
	bootData, bootErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
	var observedHost *string
	if hostErr == nil {
		observedHost = &host
	}
	var bootID *string
	if bootErr == nil {
		observed := strings.TrimSpace(string(bootData))
		bootID = &observed
	}
	if err := c.stream.append("start", map[string]any{"declared_source_commit": cfg.SourceCommit, "source_hashes": hashes, "binary_sha256": hex.EncodeToString(binaryHash.Sum(nil)), "host_epoch": cfg.HostEpoch, "observed_hostname": observedHost, "hostname_error": n1Error(hostErr), "observed_boot_id": bootID, "boot_error": n1Error(bootErr), "manifest_scope": "local scheduler manifest is exact component-config SHA256; external QL manifest remains external", "database_id": c.DatabaseID, "accepting_writer_baseline": map[string]any{"connection_id": c.baseline, "observed_connections": c.baselineConnections, "observed_native_closes": c.baselineCloses, "scope": "measured lifetime starts after proven setup/reopen; full earlier trace retained"}, "native_device": writerDevice, "native_inode": writerInode, "schema_sha256": schema.SHA256, "read_fixture_journal_sha256": readfixture.ForegroundHash(t), "foreground_fixture_temporal_origin": foregroundSource.TemporalOrigin(), "foreground_time_scope": "existing synthetic seed offsets rebased once before construction using time.Now; no token renewal or runtime authority", "reader_policy": readerConfig, "foreground_fixture_profile": gc.Foreground, "foreground_partition": gc.Partitions[governor.Foreground], "foreground_partition_scope": "unchanged RD2 contribution; native binding additionally funds existing WC units", "max_grants_per_class": gc.MaxGrantsPerClass, "n1_request": owner.request, "n1_work": map[string]Work{"limit": owner.recipe.limit, "setup": owner.recipe.setup, "terminal": owner.recipe.terminal}, "warmup_originals": warmup, "measured_originals": cfg.Fixtures, "cohort": cohort, "clock": "time.Now real monotonic", "canonical_population": population, "foreground_library_by_route": map[string]int64{"browse": c.View.Library, "detail": c.PointView.Library, "playback": c.PointView.Library, "continue": c.PointView.Library}, "scope": "same DB; N1 library7; canonical browse7 when attached, existing point/personal overlay1; kind2 excluded only in attached seed", "profile_scope": "existing RD2 LIVE config and N1 class amounts/Work; no production qualification", "memory_scope": "process heap/RSS/allocation snapshots include concurrent readers and observers; not isolated N1 LIVE peaks", "cache_population": map[string]string{"n1": "warm persistent after actual warmup", "foreground": "first use; no read warmup"}, "reader_window_connection_id": nil, "reader_window_connection_id_unavailable_reason": "public SnapshotResult has no per-window physical instance ID; actual native file checked on every connect"}); err != nil {
		t.Fatal(err)
	}
	if *ec5MemoryEnabled {
		if err := c.stream.append("memory_fixture", map[string]any{"method": "ec5-component-memory-v1", "observed_database_cardinalities": facts.Cardinalities, "library_scope": "actual canonical parent when supplied, plus existing foreground point/personal overlay1; table counts are database-wide", "canonical_population": population, "scale_fixture_available": population != nil, "per_library_item_counts": nil, "per_library_item_counts_unavailable_reason": "existing owned observer exposes database table counts, not a per-library/scale constructor", "sustained_schedule_available": true, "sustained_native_missing_bindings": c.missingEC2Bindings(), "whole_server_composition_complete": false, "assigned_budget_bytes": int64(2000000000), "profile_scope": "assigned M=2e9 cgroup; not 2GB physical/M=1.6e9 profile; no owner allocations changed"}); err != nil {
			t.Fatal(err)
		}
		if err := c.memoryCheckpoint("after_fixture_before_warmup"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < warmup; i++ {
		if err := c.RunOriginal(t, t.Context(), time.Now(), true, cohort); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func (c *N1PairedComponent) RunOriginal(t *testing.T, ctx context.Context, ready time.Time, warmup bool, cohort string) error {
	return c.runOriginal(t, ctx, ready, warmup, cohort, false)
}

// RunEC2Original adds typed read-only WAL brackets outside each actual owner
// turn. Failed probes remain explicit; no native evidence is reconstructed.
func (c *N1PairedComponent) RunEC2Original(t *testing.T, ctx context.Context, ready time.Time, cohort string) error {
	return c.runOriginal(t, ctx, ready, false, cohort, true)
}
func (c *N1PairedComponent) runOriginal(t *testing.T, ctx context.Context, ready time.Time, warmup bool, cohort string, telemetry bool) error {
	if c.stopped != nil {
		return c.stopped
	}
	c.original++
	population, census := "measured", &c.summary.Measured
	if warmup {
		population, census = "warmup", &c.summary.Warmup
	}
	census.Offered++
	if err := c.stream.append("original_offer", map[string]any{"original_ordinal": c.original, "scheduled_at": ready, "population": population}); err != nil {
		return err
	}
	command, err := c.fixture.NextOriginal()
	if err != nil {
		c.stopped = err
		return err
	}
	for _, role := range []string{"fresh_begin", "resolve", "ack"} {
		var before *walobserve.WALSnapshot
		if telemetry {
			var err error
			before, err = c.ObserveEC2WAL(ctx)
			if err != nil {
				raw, _ := json.Marshal(map[string]any{"original_reference": ReceiptReference{command.identity, command.digest}, "turn_role": role, "physical_attempt_id": nil, "before": before, "after": nil, "before_error": err.Error(), "after_error": nil})
				c.stopped = errors.Join(err, c.EmitEC2("wal_turn", raw))
				return c.stopped
			}
		}
		c.call++
		c.summary.Turns[role].Started++
		record := runN1SteadyTurn(t, ctx, c.stream, c.fixture, c.trace, &c.summary, c.original, c.call, population, role, cohort, c.baseline, n1SteadyControls{clock: time.Now, initialConnections: &c.baselineConnections, initialCloses: &c.baselineCloses})
		if telemetry {
			// Always collect after a returned physical call, even on command failure or
			// cancellation. These finite file probes own no native rows/transaction.
			after, observeErr := c.ObserveEC2WAL(context.WithoutCancel(ctx))
			raw, _ := json.Marshal(map[string]any{"original_reference": record.Reference, "turn_role": role, "physical_attempt_id": record.ID, "physical_attempt_ordinal": record.PhysicalAttemptOrdinal, "before": before, "after": after, "before_error": "", "after_error": n1Error(observeErr), "header_sync_ns": nil, "header_sync_unavailable_reason": "typed native WAL IO has no isolated header-sync/xSync interval"})
			if err := errors.Join(observeErr, c.EmitEC2("wal_turn", raw)); err != nil {
				c.stopped = err
				return err
			}
		}
		if !record.Complete {
			c.stopped = errors.New("incomplete N1 " + role + ": " + record.Error)
			return c.stopped
		}
	}
	census.Complete++
	if warmup {
		c.summary.WarmupOriginals = append(c.summary.WarmupOriginals, ReceiptReference{command.identity, command.digest})
	}
	return nil
}

// ObserveEC2WAL binds advisory telemetry to this actual owner path/gate. It
// neither publishes admission nor opens a second database or native connection.
func (c *N1PairedComponent) ObserveEC2WAL(ctx context.Context) (*walobserve.WALSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed || c.fixture == nil {
		return nil, errors.New("paired owner unavailable")
	}
	return walobserve.Observe(c.fixture.owner.path, c.fixture.owner.wal)
}
func (c *N1PairedComponent) EmitEC2(kind string, raw json.RawMessage) error {
	return c.stream.append("ec2_"+kind, map[string]any{"record": raw, "qualified_profile_ready": false})
}
func (c *N1PairedComponent) EmitRead(id string, scheduled time.Time, raw json.RawMessage) error {
	return c.stream.append("foreground", map[string]any{"call_id": id, "scheduled_at": scheduled, "observation": raw})
}
func (c *N1PairedComponent) EmitScheduler(raw json.RawMessage) error {
	return c.stream.append("scheduler", map[string]any{"record": raw})
}
func (c *N1PairedComponent) Close(cause error) error {
	if c.closed {
		return errors.New("paired component already closed")
	}
	c.closed = true
	checkpointErr := c.memoryCheckpoint("before_fixture_close")
	var readerErr, writerErr error
	var readerReturned *bool
	if c.Snapshots != nil {
		readerErr = c.Snapshots.Close()
		returned := true
		readerReturned = &returned
	}
	var stats any
	var nativeJoined *bool
	var usage *[4]governor.Amounts
	var live any
	if c.Authority != nil {
		observed := c.Authority.Usage()
		usage = &observed
		live = c.Authority.ForegroundUsage()
	}
	if c.fixture != nil {
		w := c.fixture.owner.executor
		if c.native != nil {
			writerErr = c.closeEC2Native()
		} else {
			writerErr = w.close()
		}
		if c.Authority != nil {
			observed := c.Authority.Usage()
			usage = &observed
		}
		actualStats := w.db.Stats()
		stats = actualStats
		w.physicalFaults.mu.Lock()
		failed := w.physicalFaults.failed
		w.physicalFaults.mu.Unlock()
		joined := writerErr == nil && w.closed && !failed && actualStats.OpenConnections == 0 && actualStats.InUse == 0 && c.trace.connections == c.trace.closed
		nativeJoined = &joined
		c.summary.PoolStats = &actualStats
		c.summary.Usage = usage
		c.summary.NativeJoined = nativeJoined
		opened, closed := c.trace.connections, c.trace.closed
		c.summary.ConnectionsOpened = &opened
		c.summary.ConnectionsClosed = &closed
		c.summary.NativeCloses = c.trace.closes[:c.trace.closeCount]
		c.summary.NativeCloseOverflow = c.trace.closeOverflow
		c.summary.Complete = joined && !c.trace.closeOverflow && c.stopped == nil && c.summary.Warmup.Complete == c.warmup && c.summary.Measured.Complete == c.Measured
	}
	var readerConnections *uint64
	if c.reader != nil {
		n := c.reader.connections.Load()
		readerConnections = &n
	}
	complete := cause == nil && c.stopped == nil && c.summary.Complete && readerReturned != nil && readerErr == nil && usage != nil && *usage == ([4]governor.Amounts{}) && c.Authority.ForegroundUsage().Live == 0 && c.Authority.ForegroundUsage().Waiters == 0
	memoryErr := c.finishMemory()
	if memoryErr != nil || checkpointErr != nil {
		complete = false
	}
	finalErr := errors.Join(cause, c.stopped, readerErr, writerErr, memoryErr, checkpointErr)
	if !complete && finalErr == nil {
		finalErr = errors.New("paired component incomplete; no successful whole-cell claim")
	}
	err := c.stream.append("final", map[string]any{"complete": complete, "n1": c.summary, "read_pool_close_returned": readerReturned, "read_pool_close_error": n1Error(readerErr), "writer_close_error": n1Error(writerErr), "authority_usage": usage, "live_usage": live, "writer_pool_stats": stats, "writer_native_joined": nativeJoined, "reader_attested_connections": readerConnections, "reader_idle_close_observation": nil, "reader_idle_close_unavailable_reason": "Snapshots.Close owner return is observed; no separate native idle Close hook", "error": n1Error(finalErr), "qualified_profile_ready": false})
	return errors.Join(finalErr, err, c.stream.file.Close())
}
