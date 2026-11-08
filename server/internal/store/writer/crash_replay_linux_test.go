package writer

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// Linux process-crash controls, not power-loss or latency qualification. All
// databases, killed-process templates and spill live below the parent's TempDir.
// No production hook, startup adapter, command, permission or forecast is added.
var crashClock = time.Date(2100, 11, 5, 10, 30, 0, 0, time.UTC)

type crashCase struct{ Domain, Operation, Boundary, CommitFault string }

type crashWitness struct {
	ScanIssuer *scanIssuerPreparedCrashSource
	Reclaim    *reclaimCrashSource
	Effective  *effectiveCrashOriginal
	Passive    *passiveCrashProof
	Opening    *scanOpeningCrashProof
	Progress   *progressCrashSource
	Security   *securityCrashSource

	Version          int
	SchemaSHA        string
	Engine           string
	Case             crashCase
	Path             string
	Reference        ReceiptReference
	Saved            EffectiveStageBeginResult
	InventoryJournal *inventoryCrashJournal // raw pre-COMMIT observation, never a writer result
	FreshIdentity    *freshCrashIdentity
	N1Driver         *n1DriverCrashSource
	ScanRun          *ScanRunReference
	ScanSaved        *ScanRunSnapshot
	ScanClockMS      int64
	Unknown          bool

	HistoryState map[string][32]byte
}

func crashPoint(t *testing.T, witness *crashWitness, boundary string) {
	t.Helper()
	if witness.Case.Boundary != boundary {
		return
	}
	pipe := os.NewFile(3, "crash-boundary")
	if err := json.NewEncoder(pipe).Encode(witness); err != nil {
		t.Fatal(err)
	}
	// Only the parent kills the process. A missing hook, early ordinary exit,
	// timeout or wrong signal fails instead of counting as a crash case.
	<-t.Context().Done()
	t.Fatal("parent failed to SIGKILL the boundary process")
}

type crashConn struct {
	writerDriverConn
	t        *testing.T
	witness  *crashWitness
	commit   *atomic.Bool
	afterSQL func(string)
}

func (c *crashConn) before(q string) {
	switch q {
	case "BEGIN IMMEDIATE":
		crashPoint(c.t, c.witness, "before_begin")
	case "COMMIT":
		crashPoint(c.t, c.witness, "before_commit")
	case "PRAGMA wal_checkpoint(PASSIVE)":
		crashPoint(c.t, c.witness, "before_checkpoint")
	}
}

func (c *crashConn) after(q string) {
	if c.afterSQL != nil {
		c.afterSQL(q)
	}
	switch {
	case q == "BEGIN IMMEDIATE":
		crashPoint(c.t, c.witness, "after_begin")
	case q == "COMMIT":
		if c.commit != nil {
			c.commit.Store(true)
		}
		crashPoint(c.t, c.witness, "after_commit")
	case strings.HasPrefix(q, "INSERT INTO catalog_credit_images("):
		crashPoint(c.t, c.witness, "stage_image")
	case strings.HasPrefix(q, "UPDATE metadata_provider_operations SET credit_image_id="):
		crashPoint(c.t, c.witness, "stage_attach")
	case strings.HasPrefix(q, "INSERT INTO enrichment_command_receipts("):
		crashPoint(c.t, c.witness, "stage_receipt")
	case strings.Contains(q, "FROM enrichment_command_receipts") || strings.Contains(q, "FROM scan_command_receipts"):
		crashPoint(c.t, c.witness, "resolve_receipt")
	case strings.HasPrefix(q, "UPDATE metadata_provider_operations SET acknowledged_command_sequence=") || strings.HasPrefix(q, "UPDATE inventory_runs SET acknowledged_command_sequence="):
		crashPoint(c.t, c.witness, "ack_floor")
	case q == "PRAGMA wal_checkpoint(PASSIVE)":
		crashPoint(c.t, c.witness, "after_checkpoint")
	}
}

var errCrashCommitReply = errors.New("test-only lost COMMIT reply")

func (c *crashConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.before(q)
	if q == "COMMIT" && c.witness.Case.CommitFault == "before" {
		return nil, errCrashCommitReply
	}
	out, err := c.writerDriverConn.ExecContext(ctx, q, args)
	if err == nil {
		c.after(q)
		if q == "COMMIT" && c.witness.Case.CommitFault == "after" {
			return nil, errCrashCommitReply
		}
	}
	return out, err
}

func (c *crashConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.before(q)
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	return &crashRows{Rows: rows, after: func() { c.after(q) }}, nil
}

// Preserve contextual prepared execution too: statement-turn implementations
// may prepare rather than use the native connection's direct Query/Exec path.
func (c *crashConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	statement, err := c.writerDriverConn.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &crashStmt{Stmt: statement, conn: c, query: q}, nil
}

type crashStmt struct {
	driver.Stmt
	conn  *crashConn
	query string
}

func (s *crashStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.conn.before(s.query)
	out, err := s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	if err == nil {
		s.conn.after(s.query)
	}
	return out, err
}

func (s *crashStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.conn.before(s.query)
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &crashRows{Rows: rows, after: func() { s.conn.after(s.query) }}, nil
}

type crashRows struct {
	driver.Rows
	after  func()
	closed bool
}

func (r *crashRows) Close() error {
	err := r.Rows.Close()
	if !r.closed {
		r.closed = true
		if err == nil {
			r.after()
		}
	}
	return err
}

func installCrashDriver(t *testing.T, e *executor, path string, witness *crashWitness) {
	installCrashDriverAt(t, e, path, witness, crashClock, nil)
}

func installCrashDriverAt(t *testing.T, e *executor, path string, witness *crashWitness, clock time.Time, afterSQL func(string)) {
	t.Helper()
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, e.config)
	if err != nil {
		t.Fatal(err)
	}
	var committed atomic.Bool
	e.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &crashConn{writerDriverConn: conn.(writerDriverConn), t: t, witness: witness, commit: &committed, afterSQL: afterSQL}
	})
	e.now = func() time.Time {
		if committed.Load() && e.db.Stats().InUse == 0 {
			crashPoint(t, witness, "physical_return")
		}
		return clock
	}
	primeEffectiveFixturePool(t, e)
}

// The child sets up genuine source fixtures and calls the merged facade. It
// sends the exact original reference and file to its parent before stopping.
func TestCrashReplayChild(t *testing.T) {
	encoded := os.Getenv("PORTICO_CRASH_CASE")
	if encoded == "" {
		return
	}
	witness := crashWitness{Version: 1, SchemaSHA: schema.SHA256, Engine: schema.NativeSQLiteVersion}
	if err := json.Unmarshal([]byte(encoded), &witness.Case); err != nil {
		t.Fatal(err)
	}
	if witness.Case.Domain == "scan_issuer" {
		crashScanIssuerFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "scan_issuer_ack" {
		crashScanAckFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "wc_security" {
		crashSecurityFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "wc_progress" {
		crashProgressFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "passive_native" {
		crashPassiveFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "scan_opening" {
		crashScanOpeningFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "effective_steps" {
		crashEffectiveStepsFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "wc_reclaim" {
		crashReclaimFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "inventory" {
		crashInventoryChild(t, &witness)
		return
	}
	if witness.Case.Domain == "inventory_fresh_component" {
		crashFreshInventoryChild(t, &witness)
		return
	}
	if witness.Case.Domain == "wc_n1_driver" {
		crashN1DriverFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "wc_n1_history" {
		crashN1HistoryFixture(t, &witness)
		return
	}
	if witness.Case.Domain == "inventory_native" {
		crashInventoryNativeFixture(t, &witness)
		return
	}
	if witness.Case.Domain != "effective" {
		t.Fatal("unsupported crash journal domain", witness.Case.Domain)
	}
	f := newEffectiveFirstLightFixture(t, func() time.Time { return crashClock })
	witness.Path, witness.Reference = f.path, effectiveReference(f.command).Receipt
	request := effectiveReference(f.command)
	if witness.Case.Operation != "stage" {
		result, err := f.writer.StageEffective(t.Context(), f.disclosure, f.command)
		if err != nil || result.Outcome.State != Applied || result.Outcome.Replayed {
			t.Fatal(result, err)
		}
		witness.Saved = result.Value
	}
	if witness.Case.Operation == "checkpoint" {
		crashCheckpointChild(t, f, &witness)
		return
	}
	installCrashDriver(t, f.executor, f.path, &witness)
	var err error
	var result EffectiveStageResult
	var ack AckResult
	switch witness.Case.Operation {
	case "stage":
		result, err = f.writer.StageEffective(t.Context(), f.disclosure, f.command)
	case "resolve":
		result, err = f.writer.ResolveEffective(t.Context(), f.disclosure, request)
	case "ack":
		ack, err = f.writer.AckEffective(t.Context(), f.disclosure, request)
	default:
		t.Fatal("unknown crash operation", witness.Case)
	}
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(result, EffectiveStageResult{}) || !reflect.DeepEqual(ack, AckResult{}) || !f.executor.ambiguous || !f.writer.effective.fenced {
			t.Fatal("uncertain COMMIT reported success or escaped quarantine", result, ack, err)
		}
		witness.Unknown = true
		crashPoint(t, &witness, "uncertain_return")
	} else if err != nil {
		t.Fatal(err)
	}
	if witness.Case.Operation != "ack" {
		witness.Saved = result.Value
	}
	crashPoint(t, &witness, "api_return")
	t.Fatal("requested boundary was never reached", witness.Case)
}

func crashInventoryChild(t *testing.T, witness *crashWitness) {
	t.Helper()
	w, requests, e, _ := inventoryFacadeFixture(t)
	e.now = func() time.Time { return crashClock }
	w = bindCrashInventory(t, e, w.effective.path, *w.effective.inventoryPlan)
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	witness.Reference, witness.Path = q.Receipt, w.effective.path
	installCrashDriver(t, e, witness.Path, witness)
	var err error
	var result InventoryResult
	var ack AckResult
	switch witness.Case.Operation {
	case "resolve":
		result, err = w.ResolveScan(t.Context(), cap, q)
	case "ack":
		ack, err = w.AckScan(t.Context(), cap, q)
	default:
		t.Fatal("unsupported Inventory crash operation", witness.Case)
	}
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(result, InventoryResult{}) || !reflect.DeepEqual(ack, AckResult{}) || !e.ambiguous || !w.effective.fenced {
			t.Fatal("Inventory uncertain COMMIT reported success", result, ack, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	}
	if err != nil {
		t.Fatal(err)
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("Inventory boundary was never reached", witness.Case)
}

type crashCheckpointConnector struct {
	driver.Connector
	t       *testing.T
	witness *crashWitness
	wrap    func(writerDriverConn) writerDriverConn
}

func (c crashCheckpointConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	var wrapped writerDriverConn = &crashConn{writerDriverConn: conn.(writerDriverConn), t: c.t, witness: c.witness}
	if c.wrap != nil {
		wrapped = c.wrap(wrapped)
	}
	return wrapped, nil
}

func crashCheckpointChild(t *testing.T, f *effectiveFirstLightFixture, witness *crashWitness) {
	t.Helper()
	// Keep the writer pool resident: closing the last connection would perform
	// a clean-close checkpoint before the actual governed PASSIVE step.
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A separate test composition funds a checkpoint reader. The writer-only
	// first-light profile has no reader partition and is not a CP profile.
	g, authority, wal := crashGovernor(t, f.executor, f.writer.effective.plan)
	state, err := governor.ReadWAL(f.path)
	if err != nil || wal.Publish(wal.Observation(), state, crashClock) != nil {
		t.Fatal("checkpoint preflight", state, err)
	}
	cp, err := governor.NewCheckpointer(crashCheckpointConnector{Connector: connector, t: t, witness: witness}, cfg, g, authority,
		governor.CheckpointPolicy{DatabasePath: f.path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	r := governor.Request{Key: governor.JobKey{Domain: 1, ID: "crash-passive"}, Owner: 1, Class: governor.Recovery,
		Budget: governor.UnitBudget{RawRows: 1, VMOps: 10000, Pages: 32768}, MaxHold: 25 * time.Millisecond, Deadline: crashClock.Add(time.Second)}
	r.Need[governor.Reader], r.Need[governor.MemoryBytes], r.Need[governor.CPUMilli], r.Need[governor.IOBytes] = 1, 64<<10, 100, 64<<20
	out, err := cp.Step(t.Context(), r)
	if err != nil || out.Refusal != nil || !out.PhysicalSafe || !out.DatabaseMatched || out.Frames == 0 || out.Backfilled != out.Frames {
		t.Fatal(out, err)
	}
	crashPoint(t, witness, "checkpoint_return")
	t.Fatal("checkpoint boundary was never reached", witness.Case)
}

func runCrashChild(t *testing.T, cell crashCase, beforeKill ...func(crashWitness)) crashWitness {
	t.Helper()
	if len(beforeKill) > 1 {
		t.Fatal("unsupported crash observer count")
	}
	scratch := t.TempDir()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	encoded, err := json.Marshal(cell)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), crashChildGuard)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashReplayChild$", "-test.count=1", "-test.timeout="+crashChildTimeout.String())
	cmd.Env = append(os.Environ(), "PORTICO_CRASH_CASE="+string(encoded), "TMPDIR="+scratch, "GOTMPDIR="+scratch)
	cmd.ExtraFiles = []*os.File{writer}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	type delivery struct {
		value crashWitness
		err   error
	}
	ready := make(chan delivery, 1)
	go func() {
		var d delivery
		d.err = json.NewDecoder(reader).Decode(&d.value)
		ready <- d
	}()
	var got delivery
	select {
	case got = <-ready:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("boundary timeout: %v\n%s", cell, output.String())
	}
	if got.err == nil && len(beforeKill) == 1 {
		relative, pathErr := filepath.Rel(scratch, got.value.Path)
		if pathErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) ||
			got.value.Case != cell || got.value.Version != 1 || got.value.SchemaSHA != schema.SHA256 || got.value.Engine != schema.NativeSQLiteVersion {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("untrusted pre-kill witness", pathErr)
		}
		beforeKill[0](got.value)
	}
	killErr := cmd.Process.Kill()
	err = cmd.Wait()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if got.err != nil || killErr != nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL || got.value.Case != cell ||
		got.value.Version != 1 || got.value.SchemaSHA != schema.SHA256 || got.value.Engine != schema.NativeSQLiteVersion {
		t.Fatalf("not a witnessed SIGKILL: %v, %v, %v, %v\n%s", cell, got.err, killErr, err, output.String())
	}
	relative, err := filepath.Rel(scratch, got.value.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		t.Fatal("child database escaped owned scratch", got.value.Path, err)
	}
	return got.value
}

func reopenCrashExecutor(t *testing.T, path string) *executor {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := &executor{config: cfg, now: func() time.Time { return crashClock }}
	e.openPool(connector, nil)
	t.Cleanup(func() {
		if err := e.close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("integrity_check", integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign_key_check", rows.Err())
	}
	return e
}

func TestCrashReplayEffective(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	template := newEffectiveFirstLightFixture(t, func() time.Time { return crashClock })
	if joined, err := closeEffectiveFirstLightFixture(template); err != nil || !joined {
		t.Fatal("fresh fixture did not physically join before crash cases", joined, err)
	}
	common := []string{"before_begin", "after_begin", "before_commit", "after_commit", "physical_return", "api_return"}
	var cells []crashCase
	for _, operation := range []string{"stage", "resolve", "ack"} {
		boundaries := append([]string(nil), common...)
		switch operation {
		case "stage":
			boundaries = append(boundaries, "stage_image", "stage_attach", "stage_receipt")
		case "resolve":
			boundaries = append(boundaries, "resolve_receipt")
		case "ack":
			boundaries = append(boundaries, "resolve_receipt", "ack_floor")
		}
		for _, boundary := range boundaries {
			cells = append(cells, crashCase{"effective", operation, boundary, ""})
		}
		for _, fault := range []string{"before", "after"} {
			cells = append(cells, crashCase{"effective", operation, "uncertain_return", fault})
		}
	}
	for _, boundary := range []string{"before_checkpoint", "after_checkpoint", "checkpoint_return"} {
		cells = append(cells, crashCase{"effective", "checkpoint", boundary, ""})
	}
	for _, cell := range cells {
		t.Run(fmt.Sprint(cell.Operation, "/", cell.Boundary, "/", cell.CommitFault), func(t *testing.T) {
			witness := runCrashChild(t, cell)
			verifyEffectiveCrash(t, template.command, witness)
		})
	}
}

func crashGovernor(t *testing.T, e *executor, p effectivePlan) (governor.Governor, *governor.Authority, *governor.WALGate) {
	t.Helper()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = p.need
		partitions[i][governor.Reader] = 2
	}
	g, authority, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	return g, authority, wal
}

func bindCrashInventory(t *testing.T, e *executor, path string, inventory effectivePlan) *Writer {
	t.Helper()
	p := effectiveFixturePlan()
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	defs = append(defs, inventoryHistoryDefinitions()...)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "inventory_dispatch_test.go", "inventory_resolution_test.go", "scan_receipts_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.registration, inventory.registration = golden.Receipt(), golden.Receipt()
	g, authority, wal := crashGovernor(t, e, p)
	r, err := newEffectiveRuntime(e, path, g, authority, wal, 1, p, inventory)
	if err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: r}
}

func TestCrashReplayInventoryHistory(t *testing.T) {
	prototype, requests, e, _ := inventoryFacadeFixture(t)
	profile := *prototype.effective.inventoryPlan
	// Freeze original saved facts before any tested command. No ResolveScan
	// result or production receipt decoder supplies this expected journal.
	journal, err := readInventoryCrashJournal(t.Context(), e.db, requests["hit"].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.close(); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"resolve", "ack"} {
		boundaries := []string{"before_begin", "after_begin", "resolve_receipt", "before_commit", "after_commit", "physical_return", "api_return"}
		if operation == "ack" {
			boundaries = append(boundaries, "ack_floor")
		}
		for _, boundary := range boundaries {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				witness := runCrashChild(t, crashCase{"inventory", operation, boundary, ""})
				verifyInventoryCrash(t, requests["hit"], profile, journal, witness)
			})
		}
		for _, fault := range []string{"before", "after"} {
			t.Run(operation+"/uncertain_return/"+fault, func(t *testing.T) {
				witness := runCrashChild(t, crashCase{"inventory", operation, "uncertain_return", fault})
				verifyInventoryCrash(t, requests["hit"], profile, journal, witness)
			})
		}
	}
}
