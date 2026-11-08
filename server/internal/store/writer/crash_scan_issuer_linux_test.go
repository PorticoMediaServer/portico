package writer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func scanCrashEvidence(t *testing.T, e *ExecutionEvidence, purpose CommandID) {
	t.Helper()
	if e == nil || e.Purpose != purpose || e.Class != governor.ScanMinimum || e.Durability != Normal || e.Commit != CommitSucceeded || e.Policy == nil || e.CommitAttemptedAt == nil || e.CommitReturnedAt == nil || e.PhysicalReturnedAt == nil || e.ReturnedAt.IsZero() {
		t.Fatal("missing actual scan frame observations", e)
	}
}

func scanCrashCheckResult(t *testing.T, source scanIssuerPreparedCrashSource, result *ScanRunResult) {
	t.Helper()
	_, want, err := expectedScanIssuer(source)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := result.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scanCrashEvidence(t, actual.Outcome.Evidence, source.Reference.Purpose)
	actual.Outcome.Evidence = nil
	if !reflect.DeepEqual(want, actual) {
		t.Fatal("complete scan issuer return differs", actual, want)
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
}

func scanCrashCheckJournal(t *testing.T, e *executor, want effectiveRawJournal) {
	t.Helper()
	actual, err := readEffectiveJournal(t.Context(), e.db, scanIssuerPreparedCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("scan issuer raw journal differs", err)
	}
}

func scanCrashPrepare(t *testing.T, action string) (*scanDriverFixture, *ScanRunOperation, scanIssuerPreparedCrashSource) {
	t.Helper()
	f, op, source := prepareScanIssuerCreateCrash(t)
	if action == "create" {
		return f, op, source
	}
	// Preparatory accepting body is verified independently BEFORE its receipt
	// becomes a later baseline. A candidate cannot poison expected history.
	result, err := op.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scanCrashCheckResult(t, source, result)
	want, _, err := expectedScanIssuer(source)
	if err != nil {
		t.Fatal(err)
	}
	scanCrashCheckJournal(t, f.driver.opening.executor, want)
	if action == "claim" || action == "retired" {
		ack, err := op.Ack(t.Context())
		if err != nil || ack.AcknowledgedThrough != 1 {
			t.Fatal(ack, err)
		}
		scanCrashEvidence(t, ack.Evidence, InventoryCreate)
		ackSource := source
		ackSource.Action = "ack"
		ackSource.Initial = want
		want, _, err = expectedScanIssuer(ackSource)
		if err != nil {
			t.Fatal(err)
		}
		scanCrashCheckJournal(t, f.driver.opening.executor, want)
	}
	if action == "claim" {
		previous := source.Reference
		if err := op.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		op, err = f.driver.PrepareClaim(t.Context(), previous)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := op.Reference()
		if err != nil {
			t.Fatal(err)
		}
		source.Reference = ref
		source.Initial, err = readEffectiveJournal(t.Context(), f.driver.opening.executor.db, scanIssuerPreparedCrashTables)
		if err != nil {
			t.Fatal(err)
		}
		// OLD native rows are immutable INPUT. Only the declared lease/revision
		// transition is changed in this independent initial-claim model.
		run := scanCrashFind(source.Initial, "inventory_runs", "job_id", ref.Scan.RunID)
		job := scanCrashFind(source.Initial, "jobs", "id", ref.Scan.RunID)
		source.RunInsert = map[string]effectiveRawCell{}
		for k, v := range run {
			source.RunInsert[k] = v
		}
		// The lease starts beyond the ORIGINAL funded preparation horizon,
		// not at command start. Freeze that pre-command deadline as input.
		deadline := op.preparation.expected.Deadline
		expiresMS := deadline.UnixMilli()
		if deadline.After(time.UnixMilli(expiresMS)) {
			expiresMS++
		}
		expiresMS += f.driver.leaseDuration.Milliseconds()
		for k, v := range map[string]any{"job_lease_owner": f.driver.leaseOwner, "job_lease_generation": run["job_lease_generation"].Int + 1, "job_lease_expires_ms": expiresMS, "job_revision": run["job_revision"].Int + 1, "last_command_sequence": ref.Scan.Receipt.Identity.Sequence} {
			progressSet(source.RunInsert, k, v)
		}
		source.JobInsert = map[string]effectiveRawCell{}
		for k, v := range job {
			source.JobInsert[k] = v
		}
		progressSet(source.JobInsert, "status", "running")
	} else {
		source.Initial, err = readEffectiveJournal(t.Context(), f.driver.opening.executor.db, scanIssuerPreparedCrashTables)
		if err != nil {
			t.Fatal(err)
		}
	}
	source.Action = action
	return f, op, source
}

func closeScanCrashFixture(t *testing.T, f *scanDriverFixture) {
	t.Helper()
	if err := f.driver.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.opening.close(); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func scanIssuerCrashSupported(c crashCase) bool {
	if c.Domain != "scan_issuer" {
		return false
	}
	switch c.Operation {
	case "create", "claim", "resolve", "retired", "ack":
	default:
		return false
	}
	if c.CommitFault != "" {
		return c.Operation == "create" && c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	}
	if strings.HasPrefix(c.Boundary, "native_") || c.Boundary == "api_return" {
		return true
	}
	if c.Operation == "create" {
		for _, p := range []string{"before_begin", "after_begin", "before_commit", "after_commit", "physical_return"} {
			if p == c.Boundary {
				return true
			}
		}
	}
	return false
}

func crashScanIssuerFixture(t *testing.T, witness *crashWitness) (*scanDriverFixture, scanIssuerPreparedCrashSource) {
	t.Helper()
	if witness.Case.Boundary != "trace" && !scanIssuerCrashSupported(witness.Case) {
		t.Fatal("unsupported issuer crash", witness.Case)
	}
	f, op, source := scanCrashPrepare(t, witness.Case.Operation)
	e, path := f.driver.opening.executor, f.driver.opening.binding.path
	witness.Path, witness.ScanIssuer = path, &source
	ids := map[string]string{}
	for _, entry := range f.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	armed := true
	installCrashDriverAt(t, e, path, witness, crashClock, func(query string) {
		if id, ok := ids[strings.TrimSpace(query)]; armed && ok {
			// One realistic boundary per registered SQL statement in this command;
			// repeated readback uses are not an extra combinatorial dimension.
			phase := "native_" + id
			for _, old := range source.Trace {
				if old == phase {
					return
				}
			}
			source.Trace = append(source.Trace, phase)
			crashPoint(t, witness, phase)
		}
	})
	var result *ScanRunResult
	var ack AckResult
	var err error
	if source.Action == "ack" {
		ack, err = op.Ack(t.Context())
	} else if source.Action == "resolve" || source.Action == "retired" {
		result, err = op.Resolve(t.Context())
	} else {
		result, err = op.Apply(t.Context())
	}
	armed = false
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || result != nil || ack != (AckResult{}) || !e.ambiguous || !f.driver.writer.effective.fenced {
			t.Fatal("issuer uncertain commit escaped", result, ack, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	}
	if err != nil {
		t.Fatal("actual scan issuer refused", err)
	}
	if source.Action == "ack" {
		if ack.AcknowledgedThrough != source.Reference.Scan.Receipt.Identity.Sequence {
			t.Fatal("issuer ACK projection", ack)
		}
		scanCrashEvidence(t, ack.Evidence, source.Reference.Purpose)
	} else {
		scanCrashCheckResult(t, source, result)
	}
	want, _, err := expectedScanIssuer(source)
	if err != nil {
		t.Fatal(err)
	}
	scanCrashCheckJournal(t, e, want)
	if e.db.Stats().InUse != 0 {
		t.Fatal("issuer physical connection retained")
	}
	crashPoint(t, witness, "api_return")
	if witness.Case.Boundary != "trace" {
		t.Fatal("issuer boundary was not reached", witness.Case)
	}
	return f, source
}

func reopenScanCrashFixture(t *testing.T, path string) *scanDriverFixture {
	t.Helper()
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner) // canonical existing-file policy; no initialize/reseed
	var integrity string
	if err := opening.executor.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("scan reopen integrity", integrity, err)
	}
	foreign, err := opening.executor.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Next() {
		t.Fatal("scan reopen foreign key violation")
	}
	if err := foreign.Err(); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}
	f := scanDriverFixtureOnOpening(t, opening, owner, scanRunCreateEffects{}, []scanDriverStatementFixture{scanIssuerRuntimeFixture}, false)
	*f.clock = crashClock
	_, plan, ready := scanIssuerRuntimePlans(t, f)
	if err := configureScanIssuer(t.Context(), f.driver, plan, plan, ready, time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeScanCrashFixture(t, f) })
	return f
}

func verifyScanIssuerCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	if !scanIssuerCrashSupported(witness.Case) || witness.ScanIssuer == nil || witness.ScanIssuer.Action != witness.Case.Operation || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("unsupported scan original witness")
	}
	source := *witness.ScanIssuer
	committed := witness.Case.Boundary == "api_return" || witness.Case.Boundary == "after_commit" || witness.Case.Boundary == "physical_return" || witness.Case.CommitFault == "after"
	want := source.Initial
	if committed {
		var err error
		want, _, err = expectedScanIssuer(source)
		if err != nil {
			t.Fatal(err)
		}
	}
	f := reopenScanCrashFixture(t, witness.Path)
	scanCrashCheckJournal(t, f.driver.opening.executor, want)
	// Read exact ORIGINAL history after actual old-process death/Wait. A saved
	// lease is not a current lease and a history miss cannot mint fresh work.
	op, err := f.driver.OpenRun(t.Context(), source.Reference)
	if err != nil {
		if committed || source.Action != "create" || op != nil {
			t.Fatal("original scan recovery", op, err)
		}
		scanCrashCheckJournal(t, f.driver.opening.executor, want)
		return
	}
	result, err := op.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if committed || source.Action == "resolve" || source.Action == "retired" || source.Action == "ack" {
		recovery := source
		recovery.Initial = want
		recovery.Action = "resolve"
		if scanCrashFind(want, "inventory_runs", "job_id", source.Reference.Scan.RunID)["acknowledged_command_sequence"].Int >= source.Reference.Scan.Receipt.Identity.Sequence {
			recovery.Action = "retired"
		}
		scanCrashCheckResult(t, recovery, result)
	} else {
		value, err := result.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		scanCrashEvidence(t, value.Outcome.Evidence, source.Reference.Purpose)
		value.Outcome.Evidence = nil
		wantPending := ScanRunSnapshot{Outcome: ReceiptOutcome{State: Pending, Reference: source.Reference.Scan.Receipt}}
		if !reflect.DeepEqual(value, wantPending) {
			t.Fatal("receipt miss fabricated payload", value)
		}
		if err := result.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if lease, err := op.Lease(t.Context()); err == nil || lease != nil {
		t.Fatal("restart history minted current lease")
	}
	scanCrashCheckJournal(t, f.driver.opening.executor, want)
}

func runScanIssuerCrashCase(t *testing.T, action string) {
	witness := crashWitness{Case: crashCase{"scan_issuer", action, "trace", ""}}
	f, source := crashScanIssuerFixture(t, &witness)
	closeScanCrashFixture(t, f)
	phases := append([]string{}, source.Trace...)
	if action == "create" {
		phases = append(phases, "before_begin", "after_begin", "before_commit", "after_commit", "physical_return")
	}
	for _, phase := range append(phases, "api_return") {
		t.Run(phase, func(t *testing.T) {
			verifyScanIssuerCrash(t, runCrashChild(t, crashCase{"scan_issuer", action, phase, ""}))
		})
	}
	if action == "create" {
		for _, fault := range []string{"before", "after"} {
			t.Run("uncertain_return/"+fault, func(t *testing.T) {
				verifyScanIssuerCrash(t, runCrashChild(t, crashCase{"scan_issuer", action, "uncertain_return", fault}))
			})
		}
	}
}

func TestCrashReplayScanIssuerCreate(t *testing.T) {
	skipRaceCrashMatrix(t)
	runScanIssuerCrashCase(t, "create")
}

func TestCrashReplayScanIssuerClaim(t *testing.T) {
	skipRaceCrashMatrix(t)
	runScanIssuerCrashCase(t, "claim")
}

func TestCrashReplayScanIssuerResolve(t *testing.T) {
	skipRaceCrashMatrix(t)
	runScanIssuerCrashCase(t, "resolve")
}

func TestCrashReplayScanIssuerAck(t *testing.T) {
	skipRaceCrashMatrix(t)
	runScanIssuerCrashCase(t, "ack")
}

func TestCrashReplayScanIssuerRetired(t *testing.T) { runScanIssuerCrashCase(t, "retired") }

// skipRaceCrashMatrix keeps the long sequential child matrices out of -race
// shards; the same cells run without the detector and a race shard cannot fit
// one 60-100 s child per statement inside its budget.
func skipRaceCrashMatrix(t *testing.T) {
	t.Helper()
	if raceDetector {
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}
}

func TestScanIssuerCrashCorruption(t *testing.T) {
	for _, control := range []struct{ action, name, sql string }{
		{"create", "receipt_digest", `UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE run_id=?`},
		{"create", "immutable_source", `UPDATE inventory_runs SET root_incarnation='wrong-original' WHERE job_id=?`},
		{"ack", "retired_anchor", `UPDATE inventory_runs SET create_request_digest=zeroblob(32) WHERE job_id=?`},
	} {
		t.Run(control.name, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"scan_issuer", control.action, "trace", ""}}
			f, source := crashScanIssuerFixture(t, &witness)
			defer closeScanCrashFixture(t, f)
			want, _, err := expectedScanIssuer(source)
			if err != nil {
				t.Fatal(err)
			}
			e := f.driver.opening.executor
			if _, err := e.db.ExecContext(t.Context(), control.sql, source.Reference.Scan.RunID); err != nil {
				t.Fatal(err)
			}
			actual, err := readEffectiveJournal(t.Context(), e.db, scanIssuerPreparedCrashTables)
			if err != nil {
				t.Fatal(err)
			}
			if compareEffectiveJournal(want, actual) == nil {
				t.Fatal("scan oracle accepted native corruption")
			}
		})
	}
}

func TestScanIssuerCrashMutants(t *testing.T) {
	if os.Getenv("PORTICO_LANE_EVIDENCE_DIR") == "" {
		t.Skip("explicit hosted production-mutant proof")
	}
	for _, m := range []struct{ name, file, old, new, action, reason string }{
		{"return_only", "scan_issuer_api.go", "r.read = true", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {value.LeaseOwner++}; r.read = true", "create", "complete scan issuer return differs"},
		{"saved_digest", "scan_run_lifecycle.go", "args := []any{r.scope.runID", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {in.digest[0]++}; args := []any{r.scope.runID", "create", "scan issuer raw journal differs"},
		{"ack_domain", "scan_receipts.go", "return definitions", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" { for i := range definitions { if definitions[i].ID == scanReceiptAck { definitions[i].SQL = strings.Replace(definitions[i].SQL, \"SET acknowledged_command_sequence=?\", \"SET acknowledged_command_sequence=?,discovered=discovered+1\", 1) } } }; return definitions", "ack", "scan issuer raw journal differs"},
	} {
		t.Run(m.name, func(t *testing.T) {
			data, err := os.ReadFile(m.file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), m.old) != 1 {
				t.Fatal("mutant source changed", m.file)
			}
			mutant := strings.Replace(string(data), m.old, m.new, 1)
			if m.file == "scan_receipts.go" {
				mutant = strings.Replace(mutant, "import \"github.com/PorticoMediaServer/portico/server/internal/store/stmt\"", "import (\"github.com/PorticoMediaServer/portico/server/internal/store/stmt\";\"strings\";\"os\")", 1)
			} else {
				mutant = strings.Replace(mutant, "import (", "import (\n\"os\"", 1)
			}
			root := t.TempDir()
			replacement := filepath.Join(root, "mutant.go")
			if err := os.WriteFile(replacement, []byte(mutant), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := filepath.Abs(m.file)
			if err != nil {
				t.Fatal(err)
			}
			overlayData, err := json.Marshal(map[string]any{"Replace": map[string]string{original: replacement}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(root, "overlay.json")
			if err := os.WriteFile(overlay, overlayData, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "-p", "2", "-parallel", "2", "-count=1", "-timeout=2m", "-run", "^TestCrashReplayScanIssuer"+strings.ToUpper(m.action[:1])+m.action[1:]+"$/^api_return$", ".")
			cmd.Env = append(os.Environ(), "TMPDIR="+root, "GOTMPDIR="+root)
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), m.reason) || !strings.Contains(string(output), "TestCrashReplayChild") || strings.Contains(string(output), "[build failed]") {
				t.Fatalf("mutant escaped intended child check: %v\n%s", err, output)
			}
			t.Log("rejected production mutation at child semantic check:", m.reason)
		})
	}
}
