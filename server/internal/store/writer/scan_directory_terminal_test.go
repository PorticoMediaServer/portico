package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Run on lane-focused.yml with Go1.26.10 AFTER owner purpose/codec/HN hooks.
// These are narrow domain/native SQL controls, not a new runtime/driver/profile
// or whole-frame qualification. Shared dispatch rollback/authority/clock tests
// must exercise these purposes at integration.
func terminalInput() (CommandIdentity, inventoryClaimRequest, nativeinventory.Page, nativeinventory.Page) {
	id := CommandIdentity{Sequence: 2}
	id.Incarnation[0] = 1
	id.OperationID[0] = 9
	s := inventoryClaimRequest{Original: scanRunOriginalScope{RunID: "lifecycle-run", LibraryID: 7, LibraryKey: "lifecycle-library", SourceID: "lifecycle-source", SourceGeneration: 23, SourceIncarnation: "source-incarnation", RootIdentity: "source-root", ResolvedRoot: []byte("/media"), PolicyRevision: 7, ScanMode: "first-import"}, Run: inventoryRun{revision: 1, policyRevision: 7, phase: "inventory", scanMode: "first-import", leaseOwner: 11, leaseGeneration: 1, leaseExpiresMS: 4000000000000, lastSequence: 1, acknowledgedSequence: 1}, Root: scanRootWitness{Path: ".", Identity: "actual-directory", Revision: "directory-revision", State: "pending"}, JobStatus: "running", JobCreatedAt: "2026-11-06T00:00:00.000000000Z"}
	s.Original.CreateOperationID[0] = 2
	s.Original.CreateRequestDigest[0] = 3
	page := nativeinventory.Page{Version: 1, RootIdentity: "source-root", DirectoryIdentity: s.Root.Identity, DirectoryRevision: s.Root.Revision, NextCursor: "42", Entries: []nativeinventory.Entry{{Name: []byte{'b', 0xff}, Path: []byte{'/', 'm', 'e', 'd', 'i', 'a', '/', 'b', 0xff}, Directory: true, ObjectIdentity: "bad-child"}}}
	child := nativeinventory.Page{Version: 1, RootIdentity: "source-root", DirectoryIdentity: "bad-child", DirectoryRevision: "child-revision"}
	return id, s, page, child
}

func TestScanDirectoryTerminalRawBoundary(t *testing.T) {
	for _, v := range []struct {
		root, absolute, parent, name string
		good                         bool
	}{
		{"/media", "/media/b\xff", ".", "b\xff", true},
		{"/", "/b\xff", ".", "b\xff", true},
		{"/media", "/media/parent/b\xff", "parent", "b\xff", true},
		{"/media", "/media-extra/b\xff", ".", "b\xff", false},
		{"/media", "b\xff", ".", "b\xff", false},
		{"/media", "/media/../b\xff", ".", "b\xff", false},
		{"/media", "/media/b\x00\xff", ".", "b\x00\xff", false},
		{"/media", "/media/good", ".", "good", false},
	} {
		raw, err := terminalRelativePath([]byte(v.root), []byte(v.absolute))
		good := err == nil && validTerminalRawChild(v.parent, []byte(v.name), raw)
		if good != v.good {
			t.Fatalf("%q: raw=%q err=%v accepted=%v", v.absolute, raw, err, good)
		}
	}
	display, err := terminalDiagnostic([]byte{'b', 0xff})
	if err != nil || display != `b\xff` {
		t.Fatal(display, err)
	}
	literal, err := terminalDiagnostic([]byte(`b\xff`))
	if err != nil || literal == display {
		t.Fatal("display confused literal escape and byte", literal, err)
	}
}

func TestScanDirectoryTerminalPreparation(t *testing.T) {
	id, s, page, child := terminalInput()
	c, err := newScanDirectoryRefuseCommand(id, s, s.Root, 7, page, child)
	if err != nil || c == nil || !bytes.Equal(c.request.RefusedRelativePath, []byte{'b', 0xff}) {
		t.Fatal(c, err)
	}
	other, err := newScanDirectoryRefuseCommand(id, s, s.Root, 8, page, child)
	if err != nil || other.digest == c.digest {
		t.Fatal("captured warning omitted from request digest", err)
	}
	page.Entries[0].Path[7] = 'x'
	page.Entries[0].Name[0] = 'x'
	s.Original.ResolvedRoot[1] = 'x'
	if !bytes.Equal(c.request.RawChildName, []byte{'b', 0xff}) || string(c.request.Snapshot.Original.ResolvedRoot) != "/media" || c.request.RawChildPath[7] != 'b' {
		t.Fatal("constructor retained borrowed native input")
	}
	for _, fault := range []string{"equivalent_cookie", "wrong_child", "not_directory", "combined_eof", "io"} {
		id, s, p, ch := terminalInput()
		switch fault {
		case "equivalent_cookie":
			p.NextCursor = "0"
		case "wrong_child":
			ch.DirectoryIdentity = "different"
		case "not_directory":
			p.Entries[0].Directory = false
		case "combined_eof":
			p.Complete = true
		case "io":
			p.ErrorCode = "permission"
		}
		if got, err := newScanDirectoryRefuseCommand(id, s, s.Root, 0, p, ch); err == nil || got != nil {
			t.Fatal(fault, got, err)
		}
	}
	_, s, _, _ = terminalInput()
	s.Root.State = "done"
	s.Root.Pages = 1
	if _, err := newScanTraversalCompleteCommand(id, s); err != nil {
		t.Fatal(err)
	}
	s.Root.State = "pending"
	if got, err := newScanTraversalCompleteCommand(id, s); err == nil || got != nil {
		t.Fatal("pending root completed", got, err)
	}
}

func TestScanDirectoryTerminalResultCodec(t *testing.T) {
	id, s, _, _ := terminalInput()
	v := scanDirectoryTerminalSaved{Original: s.Original, AcceptedRunRevision: 2, AppliedMS: 1000, RefusedRelativePath: []byte{'b', 0xff}, RefusedDirectoryIdentity: []byte("bad-child"), RefusedDirectoryRevision: []byte("child-revision"), ParentPath: ".", ParentIdentity: s.Root.Identity, ParentRevision: s.Root.Revision, NextCursor: "42"}
	var request RequestDigest
	request[0] = 3
	d, err := directoryTerminalResultDigest(InventoryDirectoryRefuse, id, request, v)
	if err != nil || d == (ResultDigest{}) {
		t.Fatal(d, err)
	}
	v.RefusedDirectoryRevision = []byte("different")
	other, err := directoryTerminalResultDigest(InventoryDirectoryRefuse, id, request, v)
	if err != nil || d == other {
		t.Fatal("child witness not bound", err)
	}
	if _, err := directoryTerminalResultDigest(InventoryTraversalComplete, id, request, v); err == nil {
		t.Fatal("refusal relabeled as traversal")
	}
	v = scanDirectoryTerminalSaved{Original: s.Original, AcceptedRunRevision: 2, AppliedMS: 1000, Directory: s.Root, InventoryCompletedAt: "1970-01-01T00:00:01.000123456Z"}
	v.Directory.State = "done"
	v.Directory.Pages = 1
	if _, err := directoryTerminalResultDigest(InventoryTraversalComplete, id, request, v); err != nil {
		t.Fatal(err)
	}
	v.InventoryCompletedAt = "1970-01-01T00:00:01.001123456Z"
	if _, err := directoryTerminalResultDigest(InventoryTraversalComplete, id, request, v); err == nil {
		t.Fatal("different applied timestamp accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := finishCommand(ctx, ctx, v, errors.New("late terminal failure"))
	if err == nil || !reflect.DeepEqual(out, scanDirectoryTerminalSaved{}) {
		t.Fatal("error released output", out, err)
	}
}

func terminalDefinition(t *testing.T, id stmt.ID) stmt.Definition {
	t.Helper()
	for _, d := range scanDirectoryTerminalOwnDefinitions() {
		if d.ID == id {
			return d
		}
	}
	t.Fatal("missing definition", id)
	return stmt.Definition{}
}

func terminalExecReturning(t *testing.T, tx *sql.Tx, d stmt.Definition, dest ...any) {
	t.Helper()
	rows, err := tx.QueryContext(t.Context(), d.SQL, d.Cohorts[0].Bindings...)
	if err != nil {
		t.Fatal(d.ID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(d.ID, "no native result", rows.Err())
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(d.ID, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(d.ID, "non-point result", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanDirectoryTerminalNativeRefusalAndTwoMutants(t *testing.T) {
	e, _, _ := testExecutor(t)
	seedScanDirectory(t, e)
	for _, mutant := range []string{"control", "drop_skipped", "drop_warning"} {
		t.Run(mutant, func(t *testing.T) {
			tx, err := e.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			parent, run := terminalDefinition(t, scanTerminalParentCAS), terminalDefinition(t, scanTerminalRefuseRunCAS)
			// TWO real SQL mutants, each executed against the native schema.
			if mutant == "drop_skipped" {
				parent.SQL = strings.Replace(parent.SQL, "skipped=? WHERE", "skipped=skipped+0*? WHERE", 1)
			}
			if mutant == "drop_warning" {
				run.SQL = strings.Replace(run.SQL, "SET warnings=?", "SET warnings=warnings+0*?", 1)
			}
			var p scanRootWitness
			terminalExecReturning(t, tx, parent, &p.Path, &p.Identity, &p.Revision, &p.Cursor, &p.State, &p.Pages, &p.Skipped)
			var revision, sequence, warnings int64
			terminalExecReturning(t, tx, run, &revision, &sequence, &warnings)
			valid := p.Pages == 1 && p.Skipped == 1 && warnings == 1 && revision == 2 && sequence == 2
			if valid != (mutant == "control") {
				t.Fatal("native effect invariant failed to kill mutant", mutant, p, warnings, revision, sequence)
			}
			if mutant != "control" {
				return
			}
			receipt, issue := terminalDefinition(t, scanTerminalRefuseReceipt), terminalDefinition(t, scanTerminalIssueInsert)
			if _, err := tx.ExecContext(t.Context(), issue.SQL, issue.Cohorts[0].Bindings...); err == nil {
				t.Fatal("issue preceded its NO ACTION receipt parent")
			}
			if _, err := tx.ExecContext(t.Context(), receipt.SQL, receipt.Cohorts[0].Bindings...); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(t.Context(), issue.SQL, issue.Cohorts[0].Bindings...); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(t.Context(), issue.SQL, issue.Cohorts[0].Bindings...); err == nil {
				t.Fatal("fresh duplicate retargeted issue pin")
			}
			conflict := terminalDefinition(t, scanTerminalIssueConflict)
			var conflictFound int64
			if err := tx.QueryRow(conflict.SQL, "lifecycle-run", issue.Cohorts[0].Bindings[1]).Scan(&conflictFound); err != nil || conflictFound != 1 {
				t.Fatal("same-display pin conflict was not found", conflictFound, err)
			}
			var ack stmt.Definition
			for _, d := range scanDirectoryTerminalDefinitions() {
				if d.ID == scanReceiptAck {
					ack = d
				}
			}
			if _, err := tx.Exec(ack.SQL, int64(2), "lifecycle-run", receipt.Cohorts[0].Bindings[1], int64(1), int64(2)); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`DELETE FROM scan_command_receipts WHERE run_id='lifecycle-run' AND command_incarnation=? AND command_sequence=2`, receipt.Cohorts[0].Bindings[1]); err == nil {
				t.Fatal("ACK discharged the NO ACTION issue pin")
			}
			var media, members, issues int64
			if err := tx.QueryRow(`SELECT r.discovered+j.processed,(SELECT count(*) FROM scan_command_results WHERE run_id=r.job_id),(SELECT count(*) FROM inventory_scan_issues WHERE job_id=r.job_id) FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id='lifecycle-run'`).Scan(&media, &members, &issues); err != nil || media != 0 || members != 0 || issues != 1 {
				t.Fatal(media, members, issues, err)
			}
		})
	}
	var revision, pages, warnings, receipts, issues int64
	if err := e.db.QueryRow(`SELECT job_revision,(SELECT pages FROM inventory_directories WHERE job_id=inventory_runs.job_id AND relative_path='.'),warnings,(SELECT count(*) FROM scan_command_receipts),(SELECT count(*) FROM inventory_scan_issues) FROM inventory_runs WHERE job_id='lifecycle-run'`).Scan(&revision, &pages, &warnings, &receipts, &issues); err != nil || revision != 1 || pages != 0 || warnings != 0 || receipts != 0 || issues != 0 {
		t.Fatal("rolled-back native cohort leaked", revision, pages, warnings, receipts, issues, err)
	}
}

func TestScanDirectoryTerminalStatementCapture(t *testing.T) {
	e, _, _ := testExecutor(t)
	seedScanDirectory(t, e)
	defs := scanDirectoryTerminalOwnDefinitions()
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// This deliberately fails before the new exact HN octet point is installed.
	// Never waive the point or copy raw receipt fields to measure their lengths.
	golden := captureEffectiveFixture(t, conn, e.config, defs, "scan_directory_terminal_test.go", "scan_directory_test.go", "scan_run_lifecycle_test.go")
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	if golden.ForeignKeys == nil || len(golden.ForeignKeys.Plans) != 671 {
		t.Fatal("canonical671FK audit missing")
	}
	golden.ForeignKeys = nil
	for _, p := range golden.Plans {
		for _, row := range p.QueryPlan.Values {
			if !strings.HasPrefix(row[3].(string), "SEARCH ") {
				t.Fatal("nonindexed terminal plan", p.ID, row)
			}
		}
		if p.ID == scanTerminalRefuseReceipt || p.ID == scanTerminalTraversalReceipt || p.ID == scanTerminalIssueInsert {
			if p.Insert == nil || p.Insert.Conflict != nil {
				t.Fatal("strict native INSERT destination missing", p.ID)
			}
		}
	}
	payload, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "scan-directory-terminal.json"))
	if os.IsNotExist(err) {
		if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
			if err := os.MkdirAll(evidence, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(evidence, "scan-directory-terminal.json"), append(payload, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("new terminal capture candidate requires review; written to runner evidence")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}

	for _, d := range scanDirectoryTerminalDefinitions() {
		if directoryTerminalStatementMaxUses(d.ID) <= 0 {
			t.Fatal("unbudgeted native statement", d.ID)
		}
	}
	// The new receipt preflight is a full-key point with exactly these 21
	// projected fields; its literal SHA makes the owner hook easy to reconcile.
	t.Logf("HN exact receipt SQL SHA256 %x", sha256.Sum256([]byte(scanTerminalReceiptLengthsSQL)))
}

func TestScanDirectoryTerminalNativeTraversalAndFloor(t *testing.T) {
	e, _, _ := testExecutor(t)
	seedScanDirectory(t, e)
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	pending := terminalDefinition(t, scanTerminalPending)
	var one int64
	if err := tx.QueryRow(pending.SQL, "lifecycle-run").Scan(&one); err != nil || one != 1 {
		t.Fatal("pending root was not unresolved", one, err)
	}
	if _, err := tx.Exec(`UPDATE inventory_directories SET state='done',pages=1,cursor='42' WHERE job_id='lifecycle-run' AND relative_path='.'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(pending.SQL, "lifecycle-run").Scan(&one); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("closed root retained pending frontier", err)
	}
	if _, err := tx.Exec(`INSERT INTO inventory_scan_issues(job_id,relative_path,reason,protect,warning) VALUES('lifecycle-run','child','unreadable',1,1)`); err != nil {
		t.Fatal(err)
	}
	protected, blocked := terminalDefinition(t, scanTerminalProtected), terminalDefinition(t, scanTerminalBlocked)
	if err := tx.QueryRow(protected.SQL, "lifecycle-run").Scan(&one); err != nil || one != 1 {
		t.Fatal(one, err)
	}
	if err := tx.QueryRow(blocked.SQL, "lifecycle-run").Scan(&one); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("issue invented a blocked work row", err)
	}
	cas := terminalDefinition(t, scanTerminalTraversalCAS)
	var revision, sequence int64
	var completed string
	terminalExecReturning(t, tx, cas, &revision, &sequence, &completed)
	if revision != 2 || sequence != 2 || completed != "2026-11-07T00:00:00.123456789Z" {
		t.Fatal(revision, sequence, completed)
	}
	receipt := terminalDefinition(t, scanTerminalTraversalReceipt)
	args := append([]any(nil), receipt.Cohorts[0].Bindings...)
	args[len(args)-1] = int64(1) // actual protect EXISTS, independent of blocked
	if _, err := tx.Exec(receipt.SQL, args...); err != nil {
		t.Fatal(err)
	}
	var phase, finished, stored string
	var authority, discovered, processed int64
	if err := tx.QueryRow(`SELECT r.phase,r.finished_at,r.inventory_completed_at,r.authoritative,r.discovered,j.processed FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id='lifecycle-run'`).Scan(&phase, &finished, &stored, &authority, &discovered, &processed); err != nil || phase != "verifying" || finished != "" || stored != completed || authority != 0 || discovered != 0 || processed != 0 {
		t.Fatal(phase, finished, stored, authority, discovered, processed, err)
	}
	var ack stmt.Definition
	for _, d := range scanDirectoryTerminalDefinitions() {
		if d.ID == scanReceiptAck {
			ack = d
		}
	}
	nonce := make([]byte, 16)
	nonce[0] = 1
	changed, err := tx.Exec(ack.SQL, int64(2), "lifecycle-run", nonce, int64(1), int64(2))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var floor, headers, members int64
	if err := tx.QueryRow(`SELECT acknowledged_command_sequence,(SELECT count(*) FROM scan_command_receipts WHERE run_id='lifecycle-run'),(SELECT count(*) FROM scan_command_results WHERE run_id='lifecycle-run') FROM inventory_runs WHERE job_id='lifecycle-run'`).Scan(&floor, &headers, &members); err != nil || floor != 2 || headers != 1 || members != 0 {
		t.Fatal("ACK changed scalar cohort", floor, headers, members, err)
	}
}
