package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// SQL-fact populations only, not supervisor/driver issuance or qualification.
func seedScanDirectory(t *testing.T, e *executor) inventoryClaimRequest {
	t.Helper()
	seedScanLifecycle(t, e)
	for _, s := range []string{`INSERT INTO library_scan_policies(library_id,revision) VALUES('lifecycle-library',7)`, `UPDATE jobs SET status='running' WHERE id='lifecycle-run'`, `UPDATE inventory_runs SET job_lease_owner=11,job_lease_generation=1,job_lease_expires_ms=4000000000000 WHERE job_id='lifecycle-run'`} {
		if _, err := e.db.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	var operation [16]byte
	operation[0] = 2
	var create RequestDigest
	create[0] = 3
	return inventoryClaimRequest{Original: scanRunOriginalScope{RunID: "lifecycle-run", LibraryID: 7, LibraryKey: "lifecycle-library", SourceID: "lifecycle-source", SourceGeneration: 23, SourceIncarnation: "source-incarnation", RootIdentity: "source-root", ResolvedRoot: []byte("/media"), PolicyRevision: 7, ScanMode: "first-import", CreateOperationID: operation, CreateRequestDigest: create},
		Run: inventoryRun{revision: 1, policyRevision: 7, phase: "inventory", scanMode: "first-import", leaseOwner: 11, leaseGeneration: 1, leaseExpiresMS: 4000000000000, lastSequence: 1, acknowledgedSequence: 1}, Root: scanRootWitness{Path: ".", Identity: "actual-directory", Revision: "directory-revision", State: "pending"}, JobStatus: "running", JobCreatedAt: "2026-11-06T00:00:00.000000000Z"}
}
func directoryFixture(t *testing.T) (*Writer, *executor, *governor.Authority, inventoryClaimRequest) {
	t.Helper()
	e, path, _ := testExecutor(t)
	input := seedScanDirectory(t, e)
	base, profile := effectiveFixturePlan(), effectiveFixturePlan()
	profile.steps = nil
	profile.need[governor.MemoryBytes] = 8 << 20
	profile.directoryMemory = commandMemoryBudget{4 << 20, 4 << 20}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	for _, d := range scanDirectoryDefinitions() {
		defs = append(defs, d)
		profile.steps = append(profile.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "scan_directory_test.go", "scan_run_lifecycle_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	base.registration, profile.registration = golden.Receipt(), golden.Receipt()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var parts [4]governor.Amounts
	for i := range parts {
		parts[i] = base.need
	}
	g, a, err := governor.New(governor.Config{Partitions: parts, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeScanDirectories(t.Context(), r, profile, profile); err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: r}, e, a, input
}
func directoryCommandFixture(t *testing.T, s inventoryClaimRequest, kind CommandID) *scanDirectoryCommand {
	t.Helper()
	id := CommandIdentity{Sequence: s.Run.lastSequence + 1}
	id.Incarnation[0] = 1
	id.OperationID[0] = 9
	page := nativeinventory.Page{Version: 1, RootIdentity: s.Original.RootIdentity, DirectoryIdentity: s.Root.Identity, DirectoryRevision: s.Root.Revision, NextCursor: "42"}
	var c *scanDirectoryCommand
	var err error
	if kind == InventoryDirectoryDiscover {
		page.Entries = []nativeinventory.Entry{{Name: []byte("child"), Path: []byte("child"), Directory: true, ObjectIdentity: "new-directory"}}
		child := nativeinventory.Page{Version: 1, RootIdentity: s.Original.RootIdentity, DirectoryIdentity: "new-directory", DirectoryRevision: "new-revision"}
		c, err = newScanDirectoryDiscoverCommand(id, s, s.Root, page, child)
	} else {
		page.Complete = true
		page.NextCursor = s.Root.Cursor
		c, err = newScanDirectoryEOFCommand(id, s, s.Root, page)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func directoryExecute(t *testing.T, w *Writer, c *scanDirectoryCommand) (scanDirectoryCommandResult, error) {
	t.Helper()
	q := c.reference()
	out, _, err := w.effective.runScanDirectory(t.Context(), w, inventoryTestDisclosure(t, w, q), q, c.purpose, c, false)
	return out, err
}
func directoryCounts(t *testing.T, e *executor) [8]int64 {
	t.Helper()
	var v [8]int64
	if err := e.db.QueryRow(`SELECT r.job_revision,r.last_command_sequence,r.acknowledged_command_sequence,d.pages,r.discovered,j.processed,(SELECT count(*) FROM inventory_directories WHERE job_id=r.job_id AND relative_path<>'.'),(SELECT count(*) FROM scan_command_receipts WHERE run_id=r.job_id) FROM inventory_runs r JOIN jobs j ON j.id=r.job_id JOIN inventory_directories d ON d.job_id=r.job_id AND d.relative_path='.' WHERE r.job_id='lifecycle-run'`).Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7]); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestScanDirectoryDiscoverReplayAndFloorOnlyAck(t *testing.T) {
	w, e, a, s := directoryFixture(t)
	c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
	q := c.reference()
	cap := inventoryTestDisclosure(t, w, q)
	out, err := directoryExecute(t, w, c)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Directory.Path != "child" || out.Value.ParentPages != 1 || out.Value.AcceptedRunRevision != 2 || out.Digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	if v := out.Outcome.Evidence; v == nil || v.Purpose != InventoryDirectoryDiscover || v.Class != governor.ScanMinimum || v.Durability != Normal {
		t.Fatal("missing shared frame evidence", v)
	}
	if v := directoryCounts(t, e); v != ([8]int64{2, 2, 1, 1, 0, 0, 1, 1}) {
		t.Fatal(v)
	}
	for _, sql := range []string{`UPDATE catalog_libraries SET retired=1 WHERE id=7`, `UPDATE library_sources SET root='/changed',generation=generation+1 WHERE id='lifecycle-source'`, `UPDATE inventory_runs SET job_lease_expires_ms=1 WHERE job_id='lifecycle-run'`, `UPDATE inventory_directories SET revision='changed' WHERE job_id='lifecycle-run' AND relative_path='.'`} {
		if _, err := e.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	replay, _, err := w.effective.runScanDirectory(t.Context(), w, cap, q, c.purpose, c, false)
	if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(out.Value, replay.Value) || out.Digest != replay.Digest {
		t.Fatal("saved result changed", replay, err)
	}
	_, floor, err := w.effective.runScanDirectory(t.Context(), w, cap, q, c.purpose, nil, true)
	if err != nil || floor.AcknowledgedThrough != 2 {
		t.Fatal(floor, err)
	}
	retired, _, err := w.effective.runScanDirectory(t.Context(), w, cap, q, c.purpose, nil, false)
	if err != nil || retired.Outcome.State != ResultRetired || !reflect.DeepEqual(retired.Value, scanDirectorySaved{}) {
		t.Fatal(retired, err)
	}
	if v := directoryCounts(t, e); v != ([8]int64{2, 2, 2, 1, 0, 0, 1, 1}) {
		t.Fatal("ACK changed effects or deleted header", v)
	}
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("custody leak")
	}
}
func TestScanDirectoryGenuineEOFEmptyAndNonempty(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "previously_nonempty"}[populated], func(t *testing.T) {
			w, e, _, s := directoryFixture(t)
			if populated {
				s.Root.Cursor = "42"
				s.Root.Pages = 3
				s.Root.Skipped = 1
				if _, err := e.db.Exec(`UPDATE inventory_directories SET cursor='42',pages=3,skipped=1 WHERE job_id='lifecycle-run'`); err != nil {
					t.Fatal(err)
				}
			}
			out, err := directoryExecute(t, w, directoryCommandFixture(t, s, InventoryDirectoryEOF))
			if err != nil || out.Outcome.State != Applied || out.Value.Directory.State != "done" || out.Value.Directory.Cursor != s.Root.Cursor || out.Value.Directory.Pages != s.Root.Pages+1 || out.Value.Directory.Skipped != s.Root.Skipped {
				t.Fatal(out, err)
			}
			var phase, finished, complete string
			var authority int
			if err := e.db.QueryRow(`SELECT phase,finished_at,inventory_completed_at,authoritative FROM inventory_runs WHERE job_id='lifecycle-run'`).Scan(&phase, &finished, &complete, &authority); err != nil || phase != "inventory" || finished != "" || complete != "" || authority != 0 {
				t.Fatal(phase, finished, complete, authority, err)
			}
		})
	}
}
func TestScanDirectoryFreshFencesAndAliases(t *testing.T) {
	for _, fault := range []string{"lease_generation", "source_active", "source_generation", "parent_cursor", "path_conflict", "visited_alias", "memory"} {
		t.Run(fault, func(t *testing.T) {
			w, e, _, s := directoryFixture(t)
			c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
			sql := ""
			switch fault {
			case "lease_generation":
				sql = `UPDATE inventory_runs SET job_lease_generation=2 WHERE job_id='lifecycle-run'`
			case "source_active":
				sql = `DELETE FROM inventory_source_active WHERE source_id='lifecycle-source'`
			case "source_generation":
				sql = `UPDATE library_sources SET generation=24 WHERE id='lifecycle-source'`
			case "parent_cursor":
				sql = `UPDATE inventory_directories SET cursor='41' WHERE job_id='lifecycle-run' AND relative_path='.'`
			case "path_conflict":
				sql = `INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES('lifecycle-run','child','other','revision')`
			case "visited_alias":
				sql = `INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES('lifecycle-run','other','new-directory','revision')`
			case "memory":
				w.effective.directoryPlans[0].directoryMemory = commandMemoryBudget{1024, (8 << 20) - 1024}
			}
			if sql != "" {
				if _, err := e.db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			out, err := directoryExecute(t, w, c)
			if err == nil || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) {
				t.Fatal(out, err)
			}
			v := directoryCounts(t, e)
			if v[0] != 1 || v[1] != 1 || v[2] != 1 || v[3] != 0 || v[7] != 0 {
				t.Fatal("partial mutation", v)
			}
		})
	}
}
func TestScanDirectoryReceiptFailureRollsBackAllEffects(t *testing.T) {
	w, e, _, s := directoryFixture(t)
	c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
	if _, err := e.db.Exec(`CREATE TRIGGER directory_receipt_fault BEFORE INSERT ON scan_command_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := directoryExecute(t, w, c)
	if err == nil || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) {
		t.Fatal(out, err)
	}
	if v := directoryCounts(t, e); v != ([8]int64{1, 1, 1, 0, 0, 0, 0, 0}) {
		t.Fatal("partial rollback", v)
	}
}
func TestScanDirectoryNativeBoundaries(t *testing.T) {
	e, _, _ := testExecutor(t)
	s := seedScanDirectory(t, e)
	id := CommandIdentity{Sequence: 2}
	id.Incarnation[0] = 1
	page := nativeinventory.Page{Version: 1, RootIdentity: s.Original.RootIdentity, DirectoryIdentity: s.Root.Identity, DirectoryRevision: s.Root.Revision, Complete: true}
	for _, fault := range []string{"entry_at_eof", "unstable", "io", "issue"} {
		p := page
		switch fault {
		case "entry_at_eof":
			p.Entries = []nativeinventory.Entry{{Name: []byte("a")}}
		case "unstable":
			p.Unstable = true
		case "io":
			p.ErrorCode = "unreadable"
		case "issue":
			p.Issues = []nativeinventory.Issue{{Path: []byte("a")}}
		}
		if c, err := newScanDirectoryEOFCommand(id, s, s.Root, p); err == nil || c != nil {
			t.Fatal(fault, c, err)
		}
	}
	c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
	q := c.request
	q.NextCursor = "0"
	if _, err := newScanDirectoryCommand(c.purpose, c.identity, q); err == nil {
		t.Fatal("empty and zero equivalent cookies accepted")
	}
}
func TestScanDirectoryLateCommitCancellationResolvesOriginal(t *testing.T) {
	w, e, _, s := directoryFixture(t)
	c := directoryCommandFixture(t, s, InventoryDirectoryEOF)
	q := c.reference()
	cap := inventoryTestDisclosure(t, w, q)
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(w.effective.path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	installFaultPool(e, &faultConnector{Connector: connector, afterNativeCommit: cancel})
	primeEffectiveFixturePool(t, e)
	out, ack, err := w.effective.runScanDirectory(ctx, w, cap, q, c.purpose, c, false)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("late output leaked", out, ack, err)
	}
	recovered, _, err := w.effective.runScanDirectory(t.Context(), w, cap, q, c.purpose, nil, false)
	if err != nil || recovered.Outcome.State != Applied || !recovered.Outcome.Replayed || recovered.Value.Directory.State != "done" {
		t.Fatal(recovered, err)
	}
}
