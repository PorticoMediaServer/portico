package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"math"
	"strings"
	"testing"
	"time"
)

// Native SQL-fact fixture only, not a source witness, birth/exclusion permit
// or canonical lifecycle request/result commitment.
func seedScanLifecycle(t *testing.T, w *executor) scanRunCreateEffects {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('lifecycle-library','Movies','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'lifecycle-library','/media')`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('lifecycle-source','lifecycle-library','/media','/media','source-incarnation','source-root',23,1)`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('lifecycle-new-source','lifecycle-library','/media-new','/media-new','source-incarnation','source-root',23,1)`,
		`INSERT INTO jobs(id,library_id,status,created_at) VALUES('lifecycle-run','lifecycle-library','queued','2026-11-06T00:00:00.000000000Z')`,
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES('lifecycle-run','.','actual-directory','directory-revision')`,
		`INSERT INTO inventory_source_active VALUES('lifecycle-source','lifecycle-run')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	var inc, op [16]byte
	var digest RequestDigest
	inc[0], op[0], digest[0] = 1, 2, 3
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('lifecycle-run',7,'lifecycle-library','/media',0,?,?,'lifecycle-source',23,'source-incarnation','source-root',7,'first-import',?,1,1)`, op[:], digest[:], inc[:]); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		name                      string
		op                        byte
		owner, generation, expiry int64
		due                       any
	}{
		{"due", 4, 0, 0, 0, int64(100)}, {"live", 6, 11, 17, 2000, nil}, {"expired", 8, 11, 17, 900, nil},
	} {
		id, source, root := "lifecycle-"+p.name, "lifecycle-"+p.name+"-source", "/media-"+p.name
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES(?,'lifecycle-library',?,?,'source-incarnation','source-root',23,1)`, source, root, root); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'lifecycle-library','queued','2026-11-06T00:00:00.000000000Z')`, id); err != nil {
			t.Fatal(err)
		}
		operation := make([]byte, 16)
		operation[0] = p.op
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,command_incarnation,last_command_sequence,acknowledged_command_sequence,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_next_attempt_ms) VALUES(?,7,'lifecycle-library',?,0,?,?,?,23,'source-incarnation','source-root',7,'first-import',?,1,1,?,?,?,?)`, id, root, operation, digest[:], source, inc[:], p.owner, p.generation, p.expiry, p.due); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES(?,'.','actual-directory','directory-revision')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_source_active VALUES(?,?)`, source, id); err != nil {
			t.Fatal(err)
		}
	}
	op[0] = 5
	return scanRunCreateEffects{scope: scanRunScope{runID: "lifecycle-new", libraryKey: "lifecycle-library", libraryID: 7, resolvedRoot: "/media-new", sourceID: "lifecycle-new-source", incarnation: "source-incarnation", rootIdentity: "source-root", sourceGeneration: 23, policyRevision: 7, scanMode: "first-import", commandIncarnation: inc, createOperation: op, createDigest: digest}, root: scanRunRoot{identity: "actual-directory", revision: "directory-revision", state: "pending"}, identity: CommandIdentity{inc, 1, op}, request: digest, appliedMS: 1000}
}
func scanLifecycleFixture(t *testing.T) (*executor, *stmt.Registry, scanRunCreateEffects) {
	t.Helper()
	w, _, _ := testExecutor(t)
	in := seedScanLifecycle(t, w)
	registry := cachedNativeInventoryFixtureRegistry(t, w, scanRunLifecycleDefinitions(), "scan_run_lifecycle_test.go")
	return w, registry, in
}
func scanLifecycleTestTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	// Synthetic component frame, NOT a production lifecycle purpose/profile.
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, e := range r.Manifest() {
		steps = append(steps, statementStep{e.ID, Work{3, 200, 3, 4096, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func TestScanRunLifecycleClosedDefinitions(t *testing.T) {
	defs := scanRunLifecycleDefinitions()
	if len(defs) != 12 {
		t.Fatal(len(defs))
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || strings.Contains(d.SQL, "INDEXED BY sqlite_autoindex_") {
			t.Fatal(d.ID, issues)
		}
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
}
func TestScanRunLifecycleCreateNativeReceipt(t *testing.T) {
	w, registry, in := scanLifecycleFixture(t)
	turn := scanLifecycleTestTurn(t, w, registry)
	f, err := applyScanRunCreate(t.Context(), turn, in)
	if err != nil {
		t.Fatal(err)
	}
	if f.post.run.scope != in.scope || f.post.run.revision != 1 || f.post.run.leaseOwner != 0 || f.post.run.lastSequence != 1 || f.post.run.acknowledgedSequence != 0 || f.post.root != in.root || f.post.root.identity == in.scope.rootIdentity || f.post.job.status != "queued" {
		t.Fatal(f)
	}
	digest := ResultDigest{11}
	if err := writeScanLifecycleReceipt(t.Context(), turn, scanLifecycleReceiptEffects{f, digest}); err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var revision, seq, members int64
	var sourceRev sql.NullString
	var result []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT accepted_revision,command_sequence,source_revision,result_digest,result_count FROM scan_command_receipts WHERE run_id='lifecycle-new'`).Scan(&revision, &seq, &sourceRev, &result, &members); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || seq != 1 || sourceRev.Valid || members != 0 || !bytes.Equal(result, digest[:]) {
		t.Fatal(revision, seq, sourceRev, members, result)
	}
}
func TestScanRunLifecycleClaimNativeModes(t *testing.T) {
	for _, mode := range []scanRunClaimMode{scanRunInitialClaim, scanRunLiveRenew, scanRunExpiredReclaim} {
		t.Run(map[scanRunClaimMode]string{scanRunInitialClaim: "initial", scanRunLiveRenew: "renew", scanRunExpiredReclaim: "reclaim"}[mode], func(t *testing.T) {
			w, registry, _ := scanLifecycleFixture(t)
			if mode != scanRunInitialClaim {
				expiry := int64(2000)
				if mode == scanRunExpiredReclaim {
					expiry = 900
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE inventory_runs SET job_lease_owner=11,job_lease_generation=17,job_lease_expires_ms=?,job_revision=41,last_command_sequence=18,acknowledged_command_sequence=18,discovered=59,job_attempts=3,job_next_attempt_ms=500 WHERE job_id='lifecycle-run'`, expiry); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE jobs SET processed=67,status='running' WHERE id='lifecycle-run'`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE inventory_directories SET cursor='opaque:42',pages=5,skipped=3 WHERE job_id='lifecycle-run'`); err != nil {
					t.Fatal(err)
				}
			}
			turn := scanLifecycleTestTurn(t, w, registry)
			before, err := readScanRunSnapshot(t.Context(), turn, "lifecycle-run")
			if err != nil {
				t.Fatal(err)
			}
			identity := CommandIdentity{before.run.scope.commandIncarnation, before.run.lastSequence + 1, [16]byte{31}}
			owner := int64(11)
			if mode == scanRunExpiredReclaim {
				owner = 19
			}
			f, err := applyScanRunClaim(t.Context(), turn, scanRunClaimEffects{before, mode, identity, RequestDigest{9}, owner, 3000, 1000})
			if err != nil {
				t.Fatal(err)
			}
			want := before
			want.run.leaseOwner, want.run.leaseGeneration, want.run.leaseExpiresMS = owner, before.run.leaseGeneration+1, 3000
			want.run.revision, want.run.lastSequence = before.run.revision+1, before.run.lastSequence+1
			want.job.status = "running"
			if f.post != want {
				t.Fatalf("native preservation mismatch: got%+v want%+v", f.post, want)
			}
			if err := writeScanLifecycleReceipt(t.Context(), turn, scanLifecycleReceiptEffects{f, ResultDigest{7}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestScanRunLifecycleRollbackNoPartialCohort(t *testing.T) {
	for _, fail := range []string{"active_conflict", "duplicate_receipt", "receipt_post_drift"} {
		t.Run(fail, func(t *testing.T) {
			w, registry, in := scanLifecycleFixture(t)
			if fail == "active_conflict" {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO jobs(id,library_id,status,created_at) VALUES('lifecycle-conflict','lifecycle-library','running','2026-11-06T00:00:00.000000000Z')`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_source_active VALUES('lifecycle-new-source','lifecycle-conflict')`); err != nil {
					t.Fatal(err)
				}
			}
			turn := scanLifecycleTestTurn(t, w, registry)
			f, err := applyScanRunCreate(t.Context(), turn, in)
			if fail == "active_conflict" {
				if err == nil || f != (scanRunLifecycleFacts{}) {
					t.Fatal("conflict leaked facts", f, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if fail == "duplicate_receipt" {
					if err := writeScanLifecycleReceipt(t.Context(), turn, scanLifecycleReceiptEffects{f, ResultDigest{7}}); err != nil {
						t.Fatal(err)
					}
				} else {
					f.post.root.revision = "invented"
				}
				err = writeScanLifecycleReceipt(t.Context(), turn, scanLifecycleReceiptEffects{f, ResultDigest{7}})
				if err == nil {
					t.Fatal("receipt conflict accepted")
				}
			}
			_ = turn.closeRows()
			if _, err := turn.transaction.rollback(err); err == nil {
				t.Fatal("rollback lost cause")
			}
			var count int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM jobs WHERE id='lifecycle-new')+(SELECT count(*) FROM inventory_runs WHERE job_id='lifecycle-new')+(SELECT count(*) FROM inventory_directories WHERE job_id='lifecycle-new')+(SELECT count(*) FROM scan_command_receipts WHERE run_id='lifecycle-new')`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial cohort", count, err)
			}
		})
	}
}
func TestScanRunLifecycleClaimRefusesStaleAndOverflow(t *testing.T) {
	for _, fail := range []string{"stale_root", "unacked", "overflow", "wrong_live_owner"} {
		t.Run(fail, func(t *testing.T) {
			w, registry, _ := scanLifecycleFixture(t)
			var nativeFault string
			switch fail {
			case "unacked":
				nativeFault = `UPDATE inventory_runs SET acknowledged_command_sequence=0 WHERE job_id='lifecycle-run'`
			case "overflow":
				nativeFault = `UPDATE inventory_runs SET job_revision=9223372036854775807 WHERE job_id='lifecycle-run'`
			case "wrong_live_owner":
				nativeFault = `UPDATE inventory_runs SET job_lease_owner=19,job_lease_generation=3,job_lease_expires_ms=2000 WHERE job_id='lifecycle-run'`
			}
			if nativeFault != "" {
				if _, err := w.db.ExecContext(t.Context(), nativeFault); err != nil {
					t.Fatal(err)
				}
			}
			turn := scanLifecycleTestTurn(t, w, registry)
			before, err := readScanRunSnapshot(t.Context(), turn, "lifecycle-run")
			if err != nil {
				t.Fatal(err)
			}
			in := scanRunClaimEffects{before, scanRunInitialClaim, CommandIdentity{before.run.scope.commandIncarnation, 2, [16]byte{31}}, RequestDigest{9}, 11, 3000, 1000}
			switch fail {
			case "stale_root":
				in.before.root.revision = "stale"
			case "unacked":
				// Native floor was changed before capture.
			case "overflow":
				if before.run.revision != math.MaxInt64 {
					t.Fatal(before)
				}
			case "wrong_live_owner":
				in.mode = scanRunLiveRenew
			}
			value, err := applyScanRunClaim(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || value != (scanRunLifecycleFacts{}) {
				t.Fatal(value, err)
			}
		})
	}
}
func TestScanRunLifecycleCancellationClearsNativeFacts(t *testing.T) {
	w, r, in := scanLifecycleFixture(t)
	turn := scanLifecycleTestTurn(t, w, r)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	value, err := applyScanRunCreate(ctx, turn, in)
	if !errors.Is(err, context.Canceled) || value != (scanRunLifecycleFacts{}) {
		t.Fatal(value, err)
	}
}
