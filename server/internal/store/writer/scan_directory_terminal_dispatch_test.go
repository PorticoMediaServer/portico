package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func terminalDispatchFixture(t *testing.T) (*Writer, *executor, *governor.Authority, inventoryClaimRequest) {
	t.Helper()
	w, e, a, s := directoryFixture(t)
	profile := *w.effective.directoryPlans[0]
	profile.steps = nil
	for _, d := range scanDirectoryTerminalDefinitions() {
		profile.steps = append(profile.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, scanDirectoryTerminalDefinitions(), "scan_directory_terminal_test.go", "scan_directory_test.go", "scan_run_lifecycle_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	profile.registration = golden.Receipt()
	if err := composeScanDirectoryTerminals(t.Context(), w.effective, profile, profile); err != nil {
		t.Fatal(err)
	}
	return w, e, a, s
}
func executeTerminal(t *testing.T, w *Writer, c *scanDirectoryTerminalCommand) (scanDirectoryTerminalCommandResult, error) {
	t.Helper()
	q := c.reference()
	out, _, err := w.effective.runScanDirectoryTerminal(t.Context(), w, inventoryTestDisclosure(t, w, q), q, c.purpose, c, false)
	return out, err
}
func TestScanDirectoryTerminalRefusalReplayPinAndAck(t *testing.T) {
	w, e, a, s := terminalDispatchFixture(t)
	id, _, page, child := terminalInput()
	c, err := newScanDirectoryRefuseCommand(id, s, s.Root, 0, page, child)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeTerminal(t, w, c)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.AcceptedRunRevision != 2 || string(out.Value.RefusedRelativePath) != "b\xff" {
		t.Fatal(out, err)
	}
	var pages, skipped, warnings, issues, children int64
	if err := e.db.QueryRow(`SELECT d.pages,d.skipped,r.warnings,(SELECT count(*) FROM inventory_scan_issues WHERE job_id=r.job_id),(SELECT count(*) FROM inventory_directories WHERE job_id=r.job_id AND relative_path<>'.') FROM inventory_runs r JOIN inventory_directories d ON d.job_id=r.job_id AND d.relative_path='.' WHERE r.job_id='lifecycle-run'`).Scan(&pages, &skipped, &warnings, &issues, &children); err != nil || pages != 1 || skipped != 1 || warnings != 1 || issues != 1 || children != 0 {
		t.Fatal(pages, skipped, warnings, issues, children, err)
	}
	q := c.reference()
	cap := inventoryTestDisclosure(t, w, q)
	if _, err := e.db.Exec(`UPDATE library_sources SET generation=generation+1,root='/changed' WHERE id='lifecycle-source'`); err != nil {
		t.Fatal(err)
	}
	replay, _, err := w.effective.runScanDirectoryTerminal(t.Context(), w, cap, q, c.purpose, c, false)
	if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, out.Value) || replay.Digest != out.Digest {
		t.Fatal("history was filtered by current source", replay, err)
	}
	_, floor, err := w.effective.runScanDirectoryTerminal(t.Context(), w, cap, q, c.purpose, nil, true)
	if err != nil || floor.AcknowledgedThrough != 2 {
		t.Fatal(floor, err)
	}
	retired, _, err := w.effective.runScanDirectoryTerminal(t.Context(), w, cap, q, c.purpose, nil, false)
	if err != nil || retired.Outcome.State != ResultRetired || !reflect.DeepEqual(retired.Value, scanDirectoryTerminalSaved{}) {
		t.Fatal(retired, err)
	}
	if _, err := e.db.Exec(`DELETE FROM scan_command_receipts WHERE run_id='lifecycle-run' AND command_sequence=2`); err == nil {
		t.Fatal("floor ACK released protected issue pin")
	}
	if v := directoryCounts(t, e); v != ([8]int64{2, 2, 2, 1, 0, 0, 0, 1}) {
		t.Fatal("replay/ACK added effects or removed proof", v)
	}
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("custody leak")
	}
}
func TestScanDirectoryTerminalTraversalAfterRealEOF(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "protected_listing_only"}[protected], func(t *testing.T) {
			w, e, _, s := terminalDispatchFixture(t)
			eof := directoryCommandFixture(t, s, InventoryDirectoryEOF)
			closed, err := directoryExecute(t, w, eof)
			if err != nil {
				t.Fatal(err)
			}
			q := eof.reference()
			if _, floor, err := w.effective.runScanDirectory(t.Context(), w, inventoryTestDisclosure(t, w, q), q, eof.purpose, nil, true); err != nil || floor.AcknowledgedThrough != 2 {
				t.Fatal(floor, err)
			}
			s.Run.revision, s.Run.lastSequence, s.Run.acknowledgedSequence = closed.Value.AcceptedRunRevision, 2, 2
			s.Root = closed.Value.Directory
			if protected {
				if _, err := e.db.Exec(`INSERT INTO inventory_scan_issues(job_id,relative_path,reason,protect,warning) VALUES('lifecycle-run','old','unreadable',1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			id := CommandIdentity{Sequence: 3}
			id.Incarnation[0] = 1
			id.OperationID[0] = 10
			c, err := newScanTraversalCompleteCommand(id, s)
			if err != nil {
				t.Fatal(err)
			}
			out, err := executeTerminal(t, w, c)
			if err != nil || out.Outcome.State != Applied || out.Value.Directory.State != "done" || out.Value.TraversalBlocked || out.Value.TraversalProtected != protected || out.Value.AcceptedRunRevision != 3 {
				t.Fatal(out, err)
			}
			var phase, finished, stamp string
			var authority, discovered, processed int64
			if err := e.db.QueryRow(`SELECT r.phase,r.finished_at,r.inventory_completed_at,r.authoritative,r.discovered,j.processed FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id='lifecycle-run'`).Scan(&phase, &finished, &stamp, &authority, &discovered, &processed); err != nil || phase != "verifying" || finished != "" || stamp != out.Value.InventoryCompletedAt || authority != 0 || discovered != 0 || processed != 0 {
				t.Fatal(phase, finished, stamp, authority, discovered, processed, err)
			}
			replay, err := executeTerminal(t, w, c)
			if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, out.Value) {
				t.Fatal(replay, err)
			}
		})
	}
}
func TestScanDirectoryTerminalPendingAndRollback(t *testing.T) {
	for _, fault := range []string{"pending_child", "issue_insert_failure"} {
		t.Run(fault, func(t *testing.T) {
			w, e, _, s := terminalDispatchFixture(t)
			id, _, page, child := terminalInput()
			var c *scanDirectoryTerminalCommand
			var err error
			if fault == "pending_child" {
				s.Root.State = "done"
				s.Root.Pages = 1
				if _, err := e.db.Exec(`UPDATE inventory_directories SET state='done',pages=1 WHERE job_id='lifecycle-run'; INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES('lifecycle-run','child','child','r')`); err != nil {
					t.Fatal(err)
				}
				c, err = newScanTraversalCompleteCommand(id, s)
			} else {
				if _, err := e.db.Exec(`CREATE TRIGGER terminal_issue_fault BEFORE INSERT ON inventory_scan_issues BEGIN SELECT RAISE(ABORT,'issue failure'); END`); err != nil {
					t.Fatal(err)
				}
				c, err = newScanDirectoryRefuseCommand(id, s, s.Root, 0, page, child)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := directoryCounts(t, e)
			out, err := executeTerminal(t, w, c)
			if err == nil || !reflect.DeepEqual(out, scanDirectoryTerminalCommandResult{}) || directoryCounts(t, e) != before {
				t.Fatal("failed atomic terminal unit changed effects", out, err, directoryCounts(t, e), before)
			}
		})
	}
}
func TestScanDirectoryTerminalCancellationReturnsZero(t *testing.T) {
	w, e, _, s := terminalDispatchFixture(t)
	id, _, page, child := terminalInput()
	c, err := newScanDirectoryRefuseCommand(id, s, s.Root, 0, page, child)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q := c.reference()
	before := directoryCounts(t, e)
	out, floor, err := w.effective.runScanDirectoryTerminal(ctx, w, inventoryTestDisclosure(t, w, q), q, c.purpose, c, false)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, scanDirectoryTerminalCommandResult{}) || !reflect.DeepEqual(floor, AckResult{}) || directoryCounts(t, e) != before {
		t.Fatal(out, floor, err)
	}
}
