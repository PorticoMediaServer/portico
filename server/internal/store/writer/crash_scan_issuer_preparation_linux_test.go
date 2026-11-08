package writer

import (
	"reflect"
	"testing"
	"time"
)

// Genuine PRE-command inputs from W's sole issuer, never candidate outputs.
type scanIssuerPreparedCrashSource struct {
	Action                           string
	Reference                        ScanRunReference
	Initial                          effectiveRawJournal
	Defaults                         map[string]map[string]effectiveRawCell
	Trace                            []string
	AppliedMS                        int64
	RunInsert, JobInsert, RootInsert map[string]effectiveRawCell
}

var scanIssuerPreparedCrashTables = []string{
	"libraries", "catalog_libraries", "library_sources", "library_scan_policies",
	"jobs", "inventory_runs", "inventory_directories", "inventory_source_active",
	"scan_command_receipts", "scan_command_results", "inventory_objects", "search_revision",
}

// Calls W's ONE actual opening/governor/issuer fixture and public preparation
// API. It never constructs an identity, source permit, lease or resource plan.
// Snapshot maps below are inert INSERT inputs, not a complete expected database
// image and not a public permission or producer observation proof.
func prepareScanIssuerCreateCrash(t *testing.T) (*scanDriverFixture, *ScanRunOperation, scanIssuerPreparedCrashSource) {
	t.Helper()
	f := scanIssuerRuntime(t)
	*f.clock = crashClock
	op, err := f.driver.PrepareCreate(t.Context(), ScanRunInput{LibraryID: 7, LibraryKey: "lifecycle-library", SourceID: "lifecycle-new-source", ScanMode: "first-import"})
	if err != nil {
		t.Fatal("actual issuer preparation", err)
	}
	ref, err := op.Reference()
	if err != nil {
		t.Fatal(err)
	}
	c := op.create
	if c == nil || ref.Purpose != InventoryCreate || ref.Scan.RunID != c.request.Scope.runID || ref.Scan.Receipt != (ReceiptReference{Identity: c.identity, RequestDigest: c.digest}) {
		t.Fatal("original prepared identity was not retained")
	}
	initial, err := readEffectiveJournal(t.Context(), f.driver.opening.executor.db, scanIssuerPreparedCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range initial["inventory_runs"].Rows {
		if row["job_id"].Text == ref.Scan.RunID {
			t.Fatal("prepared create already executed its body")
		}
	}
	in := c.request
	follow := int64(0)
	if in.Source.followSymlinks {
		follow = 1
	}
	at := f.clock.UnixMilli()
	source := scanIssuerPreparedCrashSource{Action: "create", Reference: ref, Initial: initial, AppliedMS: at,
		RunInsert: effectiveModelRow(map[string]any{
			"job_id": ref.Scan.RunID, "original_library_id": in.Scope.libraryID, "original_library_key": in.Scope.libraryKey,
			"original_resolved_root": string(in.Source.resolvedRoot), "original_follow_symlinks": follow,
			"create_operation_id": c.identity.OperationID[:], "create_request_digest": c.digest[:],
			"source_id": in.Source.id, "source_generation": in.Source.generation, "root_incarnation": in.Source.incarnation,
			"root_identity": in.Source.rootIdentity, "policy_revision": in.PolicyRevision, "phase": "inventory",
			"authoritative": int64(0), "root_rebound": int64(0), "finished_at": "", "scan_mode": in.ScanMode,
			"job_lease_owner": int64(0), "job_lease_generation": int64(0), "job_lease_expires_ms": int64(0),
			"job_attempts": int64(0), "job_next_attempt_ms": nil, "job_revision": int64(1), "command_incarnation": c.identity.Incarnation[:],
			"last_command_sequence": int64(1), "acknowledged_command_sequence": int64(0), "discovered": int64(0),
		}),
		JobInsert:  effectiveModelRow(map[string]any{"id": ref.Scan.RunID, "library_id": in.Scope.libraryKey, "status": "queued", "processed": int64(0), "created_at": time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000000000Z"), "error": ""}),
		RootInsert: effectiveModelRow(map[string]any{"job_id": ref.Scan.RunID, "relative_path": ".", "identity": in.RootIdentity, "revision": in.RootRevision, "cursor": "", "state": "pending", "pages": int64(0), "skipped": int64(0)}),
	}
	source.Defaults = scanCrashDefaults(t, f.driver.opening.executor)
	// A SECOND raw read guards accidental mutation by input freezing itself.
	check, err := readEffectiveJournal(t.Context(), f.driver.opening.executor.db, scanIssuerPreparedCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(initial, check); err != nil {
		t.Fatal("input checkpoint mutated native state", err)
	}
	if again, err := op.Reference(); err != nil || !reflect.DeepEqual(ref, again) {
		t.Fatal("input freezing replaced the original reference", err)
	}
	return f, op, source
}

// Recovery must call W's existing canonical opener on the SAME existing file,
// then scanDriverFixtureOnOpening(..., initialize=false). Never call
// seedScanLifecycle/newScanDriverFixture on that file. OpenRun is history-only:
// ORIGINAL Resolve -> consumer Close -> ACK cannot synthesize a current lease
// or a fresh retry after a receipt miss. Claim and preparation/birth boundaries
// require their own independent baselines before being wired into crash cases.
