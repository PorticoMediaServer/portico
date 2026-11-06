package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

// Finite SC-owned native effects only. W composes these definitions into its
// closed lifecycle purpose; no generic registry, authority or executor here.
const (
	scanLifecycleRun           stmt.ID = "scan.run_lifecycle.run"
	scanLifecycleJob           stmt.ID = "scan.run_lifecycle.job"
	scanLifecycleRoot          stmt.ID = "scan.run_lifecycle.root"
	scanLifecycleActive        stmt.ID = "scan.run_lifecycle.active"
	scanLifecycleInsertJob     stmt.ID = "scan.run_lifecycle.insert_job"
	scanLifecycleInsertRun     stmt.ID = "scan.run_lifecycle.insert_run"
	scanLifecycleInsertRoot    stmt.ID = "scan.run_lifecycle.insert_root"
	scanLifecycleInsertActive  stmt.ID = "scan.run_lifecycle.insert_active"
	scanLifecycleClaimNull     stmt.ID = "scan.run_lifecycle.claim_null"
	scanLifecycleClaimDue      stmt.ID = "scan.run_lifecycle.claim_due"
	scanLifecycleClaimJob      stmt.ID = "scan.run_lifecycle.claim_job"
	scanLifecycleReceiptInsert stmt.ID = "scan.run_lifecycle.receipt_insert"
)

func scanRunLifecycleDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullableInteger := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	incarnation, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	incarnation[0], operation[0], digest[0] = 1, 2, 3
	defs := []stmt.Definition{
		{ID: scanLifecycleRun, Kind: stmt.Read, SQL: `SELECT job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,root_rebound,finished_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{text("run_id")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}}, Cohorts: []stmt.Cohort{{Name: "existing_run", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanLifecycleJob, Kind: stmt.Read, SQL: `SELECT library_id,status,processed,created_at,error FROM jobs WHERE id=?`,
			Params: []stmt.Param{text("run_id")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "existing_job", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanLifecycleRoot, Kind: stmt.Read, SQL: `SELECT identity,revision,cursor,state,pages,skipped FROM inventory_directories WHERE job_id=? AND relative_path=?`,
			Params: []stmt.Param{text("run_id"), text("root_path")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, Seeks: []string{"job_id=? AND relative_path=?"}}, Cohorts: []stmt.Cohort{{Name: "actual_root", Bindings: []any{"lifecycle-run", "."}}}},
		{ID: scanLifecycleActive, Kind: stmt.Read, SQL: `SELECT job_id FROM inventory_source_active WHERE source_id=?`,
			Params: []stmt.Param{text("source_id")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_source_active_1"}, Seeks: []string{"source_id=?"}}, Cohorts: []stmt.Cohort{{Name: "active_source", Bindings: []any{"lifecycle-source"}}}},
		{ID: scanLifecycleInsertJob, Kind: stmt.Write, SQL: `INSERT INTO jobs(id,library_id,status,processed,created_at,error) VALUES(?,?,?,?,?,?) RETURNING library_id,status,processed,created_at,error`,
			Params: []stmt.Param{text("run_id"), text("library_key"), text("status"), integer("processed"), text("created_at"), text("error")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_job", Bindings: []any{"lifecycle-new", "lifecycle-library", "queued", int64(0), "2026-11-06T00:00:00.000000000Z", ""}}}},
		{ID: scanLifecycleInsertRun, Kind: stmt.Write, SQL: `INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,root_rebound,finished_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,root_rebound,finished_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered`,
			Params: []stmt.Param{text("job_id"), integer("original_library_id"), text("original_library_key"), text("original_resolved_root"), integer("original_follow_symlinks"), blob("create_operation_id"), blob("create_request_digest"), text("source_id"), integer("source_generation"), text("root_incarnation"), text("root_identity"), integer("policy_revision"), text("phase"), integer("authoritative"), integer("root_rebound"), text("finished_at"), text("scan_mode"), integer("job_lease_owner"), integer("job_lease_generation"), integer("job_lease_expires_ms"), integer("job_attempts"), nullableInteger("job_next_attempt_ms"), integer("job_revision"), blob("command_incarnation"), integer("last_command_sequence"), integer("acknowledged_command_sequence"), integer("discovered")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_unleased_run", Bindings: []any{"lifecycle-new", int64(7), "lifecycle-library", "/media", int64(0), operation, digest, "lifecycle-source", int64(23), "source-incarnation", "source-root", int64(7), "inventory", int64(0), int64(0), "", "first-import", int64(0), int64(0), int64(0), int64(0), nil, int64(1), incarnation, int64(1), int64(0), int64(0)}}}},
		{ID: scanLifecycleInsertRoot, Kind: stmt.Write, SQL: `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES(?,?,?,?,?,?,?,?) RETURNING identity,revision,cursor,state,pages,skipped`,
			Params: []stmt.Param{text("run_id"), text("root_path"), text("identity"), text("revision"), text("cursor"), text("state"), integer("pages"), integer("skipped")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_actual_root", Bindings: []any{"lifecycle-new", ".", "actual-directory", "directory-revision", "", "pending", int64(0), int64(0)}}}},
		{ID: scanLifecycleInsertActive, Kind: stmt.Write, SQL: `INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?) RETURNING source_id,job_id`,
			Params: []stmt.Param{text("source_id"), text("run_id")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_active_source", Bindings: []any{"lifecycle-new-source", "lifecycle-new"}}}},
		{ID: scanLifecycleClaimNull, Kind: stmt.Write, SQL: `UPDATE inventory_runs INDEXED BY inventory_runs_create_operation SET job_lease_owner=?,job_lease_generation=?,job_lease_expires_ms=?,job_revision=?,last_command_sequence=? WHERE job_id=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=? AND create_operation_id=? AND create_request_digest=? AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase=? AND authoritative=? AND root_rebound=? AND finished_at=? AND scan_mode=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND discovered=? AND job_next_attempt_ms IS NULL RETURNING job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,root_rebound,finished_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered`,
			Params: []stmt.Param{integer("next_owner"), integer("next_generation"), integer("next_expiry"), integer("next_revision"), integer("next_sequence"), text("job_id"), integer("original_library_id"), text("original_library_key"), text("original_resolved_root"), integer("original_follow_symlinks"), blob("create_operation_id"), blob("create_request_digest"), text("source_id"), integer("source_generation"), text("root_incarnation"), text("root_identity"), integer("policy_revision"), text("phase"), integer("authoritative"), integer("root_rebound"), text("finished_at"), text("scan_mode"), integer("job_lease_owner"), integer("job_lease_generation"), integer("job_lease_expires_ms"), integer("job_attempts"), integer("job_revision"), blob("command_incarnation"), integer("last_command_sequence"), integer("acknowledged_command_sequence"), integer("discovered")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"inventory_runs_create_operation"}, Seeks: []string{"create_operation_id=?"}}, Cohorts: []stmt.Cohort{{Name: "settled_initial_claim", Bindings: []any{int64(11), int64(1), int64(9000), int64(2), int64(2), "lifecycle-run", int64(7), "lifecycle-library", "/media", int64(0), operation, digest, "lifecycle-source", int64(23), "source-incarnation", "source-root", int64(7), "inventory", int64(0), int64(0), "", "first-import", int64(0), int64(0), int64(0), int64(0), int64(1), incarnation, int64(1), int64(1), int64(0)}}}},
		{ID: scanLifecycleClaimDue, Kind: stmt.Write, SQL: `UPDATE inventory_runs INDEXED BY inventory_runs_create_operation SET job_lease_owner=?,job_lease_generation=?,job_lease_expires_ms=?,job_revision=?,last_command_sequence=? WHERE job_id=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=? AND create_operation_id=? AND create_request_digest=? AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase=? AND authoritative=? AND root_rebound=? AND finished_at=? AND scan_mode=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND discovered=? AND job_next_attempt_ms =? RETURNING job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,root_rebound,finished_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered`,
			Params: []stmt.Param{integer("next_owner"), integer("next_generation"), integer("next_expiry"), integer("next_revision"), integer("next_sequence"), text("job_id"), integer("original_library_id"), text("original_library_key"), text("original_resolved_root"), integer("original_follow_symlinks"), blob("create_operation_id"), blob("create_request_digest"), text("source_id"), integer("source_generation"), text("root_incarnation"), text("root_identity"), integer("policy_revision"), text("phase"), integer("authoritative"), integer("root_rebound"), text("finished_at"), text("scan_mode"), integer("job_lease_owner"), integer("job_lease_generation"), integer("job_lease_expires_ms"), integer("job_attempts"), integer("job_revision"), blob("command_incarnation"), integer("last_command_sequence"), integer("acknowledged_command_sequence"), integer("discovered"), integer("expected_next_attempt")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"inventory_runs_create_operation"}, Seeks: []string{"create_operation_id=?"}}, Cohorts: []stmt.Cohort{{Name: "scheduled_claim", Bindings: []any{int64(11), int64(1), int64(9000), int64(2), int64(2), "lifecycle-run", int64(7), "lifecycle-library", "/media", int64(0), operation, digest, "lifecycle-source", int64(23), "source-incarnation", "source-root", int64(7), "inventory", int64(0), int64(0), "", "first-import", int64(0), int64(0), int64(0), int64(0), int64(1), incarnation, int64(1), int64(1), int64(0), int64(100)}}}},
		{ID: scanLifecycleClaimJob, Kind: stmt.Write, SQL: `UPDATE jobs SET status=? WHERE id=? AND library_id=? AND status=? AND processed=? AND created_at=? AND error=? RETURNING library_id,status,processed,created_at,error`,
			Params: []stmt.Param{text("next_status"), text("run_id"), text("library_key"), text("status"), integer("processed"), text("created_at"), text("error")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "queued_job", Bindings: []any{"running", "lifecycle-run", "lifecycle-library", "queued", int64(0), "2026-11-06T00:00:00.000000000Z", ""}}}},
		{ID: scanLifecycleReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_acknowledged_sequence,result_discovered,result_processed,result_root_identity,result_root_revision,result_root_cursor,result_root_pages,result_root_skipped) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_acknowledged_sequence,result_discovered,result_processed,result_root_identity,result_root_revision,result_root_cursor,result_root_pages,result_root_skipped`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence"), blob("operation_id"), blob("request_digest"), text("command_kind"), integer("accepted_revision"), integer("applied_ms"), text("source_id"), text("source_incarnation"), nullableText("source_revision"), text("next_cursor"), integer("result_count"), blob("result_digest"), integer("result_lease_owner"), integer("result_lease_generation"), integer("result_lease_expires_ms"), integer("result_acknowledged_sequence"), integer("result_discovered"), integer("result_processed"), text("result_root_identity"), text("result_root_revision"), text("result_root_cursor"), integer("result_root_pages"), integer("result_root_skipped")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "created_scalar_receipt", Bindings: []any{"lifecycle-new", incarnation, int64(1), operation, digest, "inventory_create", int64(1), int64(1000), "lifecycle-new-source", "source-incarnation", nil, "", int64(0), digest, int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), "actual-directory", "directory-revision", "", int64(0), int64(0)}}}},
	}
	// Distinct immutable native populations: initial all-zero lease, live
	// same-owner renewal, expired reclaim, and nullable scheduling branches.
	for i := range defs {
		switch defs[i].ID {
		case scanLifecycleRun, scanLifecycleJob:
			defs[i].Cohorts = append(defs[i].Cohorts, stmt.Cohort{Name: "missing", Bindings: []any{"lifecycle-missing"}})
		case scanLifecycleRoot:
			defs[i].Cohorts = append(defs[i].Cohorts, stmt.Cohort{Name: "missing", Bindings: []any{"lifecycle-missing", "."}})
		case scanLifecycleActive:
			defs[i].Cohorts = append(defs[i].Cohorts, stmt.Cohort{Name: "missing", Bindings: []any{"lifecycle-missing-source"}})
		case scanLifecycleClaimDue:
			// Binding positions are the five next values followed by the
			// complete native run vector (without NULL scheduling) then due.
			b := defs[i].Cohorts[0].Bindings
			b[8] = "/media-due"
			b[5], b[10], b[12] = "lifecycle-due", []byte{4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "lifecycle-due-source"
		case scanLifecycleClaimNull:
			base := defs[i].Cohorts[0].Bindings
			for _, population := range []struct {
				name, runID, source string
				operation           byte
				expiry              int64
			}{
				{"live_renew", "lifecycle-live", "lifecycle-live-source", 6, 2000},
				{"expired_reclaim", "lifecycle-expired", "lifecycle-expired-source", 8, 900},
			} {
				b := append([]any(nil), base...)
				op := make([]byte, 16)
				op[0] = population.operation
				b[1], b[5], b[10], b[12] = int64(18), population.runID, op, population.source
				b[8] = "/media-" + strings.TrimPrefix(population.runID, "lifecycle-")
				b[22], b[23], b[24] = int64(11), int64(17), population.expiry
				defs[i].Cohorts = append(defs[i].Cohorts, stmt.Cohort{Name: population.name, Bindings: b})
			}
		case scanLifecycleReceiptInsert:
			b := append([]any(nil), defs[i].Cohorts[0].Bindings...)
			b[0], b[2], b[3], b[5], b[6], b[8] = "lifecycle-run", int64(2), []byte{31, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "inventory_claim", int64(2), "lifecycle-source"
			b[14], b[15], b[16], b[17] = int64(11), int64(1), int64(9000), int64(1)
			defs[i].Cohorts = append(defs[i].Cohorts, stmt.Cohort{Name: "claimed_scalar_receipt", Bindings: b})
		}
	}
	for i := range defs {
		defs[i].Owner = "store/writer SC lifecycle effects"
		defs[i].CallSites = []string{"store/writer/scan_run_lifecycle.go"}
	}
	return defs
}
