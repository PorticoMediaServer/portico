package writer

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Native012 affected fixture inventory: dvr_defaults and dvr_storage_policy
// retain their original scalar binds. live_source_identities, live_generations
// and live_sources provide matching immutable source/head facts for the marker.
// dvr_recordings native columns (all retained, including inactive receipt slots):
// command_incarnation, create_operation_id, birth_request_digest.
// command_member_incarnation, command_profile_lifetime, last_command_sequence.
// acknowledged_command_sequence, guide_pin_generation, capture_launch_id.
// capture_allocation_generation, capture_allocation_token, capture_source_revision.
// capture_source_fence, capture_writer_retired_sequence, capture_physical_exit_sequence.
// capture_exit_launch_id, capture_exit_claim_generation, capture_exit_cancel_generation.
// capture_shared_ready_sequence, capture_producer_succeeded, capture_boundary_reached.
// capture_gap_count, share_capture_claim_generation, share_capture_launch_id.
// receipt_rows, receipt_variable_bytes, receipt_reclaim_after_sequence.
// receipt_reclaim_due_ms, reclaim_command_sequence, reclaim_operation_id.
// reclaim_request_digest, reclaim_command_kind, reclaim_result_version.
// reclaim_captured_acknowledged_sequence, reclaim_old_after_sequence, reclaim_removed_sequence.
// reclaim_removed_count, reclaim_removed_variable_bytes, reclaim_result_after_sequence.
// reclaim_result_no_next, reclaim_applied_ms, reclaim_result_count.
// reclaim_result_digest, control_variable_bytes (generated, observed only).
// Birth IDs/digest derive from the fixed marker facts below; active capture
// matches claim 2 and guide revision 7. Zero exit/share/receipt counters and NULL
// exit/reclaim slots mean those events have never happened, not missing facts.
func mgDVRPolicyFixture(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, mgDVRDefaultsSnapshot, mgDVRStorageSnapshot) {
	t.Helper()
	w, _ := testCanonicalExecutor(t, decorate...)
	defaults := mgDVRDefaultsSnapshot{7, 60, 180, 0, 0, "keep-watching", "{series}/{title}", "{title} ({year})"}
	storage := mgDVRStorageSnapshot{11, mgDVRStorageDesired{}}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_defaults(singleton,revision,before_seconds,after_seconds,retention_days,episode_limit,live_tuner_conflict,show_folder_template,movie_folder_template) VALUES(1,?,?,?,?,?,?,?,?)`, defaults.values()...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_storage_policy(singleton,revision,retention_days,episode_limit,floor_bytes,cap_bytes) VALUES(1,?,?,?,?,?)`, storage.values()...); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO live_source_identities(id) VALUES('policy-source')`,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('guide','policy-source','Held guide',1,1,1,'2026-11-07T00:00:00.000Z')`,
		`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('policy-source','Held source',7,'active',1,'guide','2026-11-07T00:00:00.000Z')`,
		`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,claim_generation,claim_token,lease_until_ms,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation,capture_launch_id,capture_allocation_generation,capture_allocation_token,capture_source_revision,capture_source_fence,owner_instance,allocation_id) VALUES('policy-marker','fixture-owner','local','account','profile','policy-source','channel','programme','guide','{"title":"Held recording"}','{"beforeMs":1000,"afterMs":2000,"retentionDays":0,"episodeLimit":0,"priority":0}',2,3,0,42,'recording',2,'held-token',1,1,1,?,?,?,?,?,'guide',?,2,'held-allocation-token',7,'guide','policy-worker','policy-allocation')`,
	} {
		var args []any
		if strings.HasPrefix(q, "INSERT INTO dvr_recordings") {
			incarnation := sha256.Sum256([]byte("policy-marker command incarnation"))
			operation := sha256.Sum256([]byte("policy-marker create operation"))
			birth := sha256.Sum256([]byte("fixture-owner/local/account/profile/policy-source/channel/programme/guide/2/3"))
			launch := sha256.Sum256([]byte("policy-marker held-token claim 2 launch"))
			member := sha256.Sum256([]byte("account member incarnation"))
			profile := sha256.Sum256([]byte("profile lifetime"))
			args = []any{incarnation[:16], operation[:16], birth[:], hex.EncodeToString(member[:16]), hex.EncodeToString(profile[:16]), launch[:16]}
		}
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	return w, defaults, storage
}
func mgDVRPolicyTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	defs := mgDVRPolicyDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	// NORMAL component frame, not new descriptor aggregation, authority or a
	// qualified accepting profile. The policy IDs are not installed by this leaf.
	recipe := turnRecipe{limit: Work{256, 1000000, 1024, 4 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), MGSetDVRDefaults, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}
func mgDVRPolicyCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

// Captured non-input custody, never request input or authority. The active
// launch retains guide-pin debt; exit, sharing and reclaim have not occurred.
func TestMGDVRPolicyNativeFixtureCustody(t *testing.T) {
	w, _, _ := mgDVRPolicyFixture(t)
	var n int
	err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_recordings r
 JOIN live_generations g ON g.id=r.guide_pin_generation AND g.source_id=r.source_id
 JOIN live_sources s ON s.id=r.source_id AND s.active_generation=g.id
 WHERE r.id='policy-marker' AND r.owner_key='fixture-owner' AND r.authority='local'
 AND r.account_id='account' AND r.profile_id='profile' AND r.state='recording'
 AND typeof(r.command_incarnation)='blob' AND length(r.command_incarnation)=16
 AND typeof(r.create_operation_id)='blob' AND length(r.create_operation_id)=16
 AND typeof(r.birth_request_digest)='blob' AND length(r.birth_request_digest)=32
 AND length(r.command_member_incarnation)=32 AND length(r.command_profile_lifetime)=32
 AND r.claim_generation=2 AND r.claim_token='held-token' AND r.capture_launch_id IS NOT NULL
 AND r.capture_allocation_generation=2 AND r.capture_allocation_token='held-allocation-token'
 AND r.capture_source_revision=s.revision AND r.capture_source_fence=g.id
 AND r.capture_writer_retired_sequence=0 AND r.capture_physical_exit_sequence=0
 AND r.capture_exit_launch_id IS NULL AND r.capture_exit_claim_generation=0 AND r.capture_exit_cancel_generation=0
 AND r.capture_shared_ready_sequence=0 AND r.capture_producer_succeeded=0 AND r.capture_boundary_reached=0 AND r.capture_gap_count=0
 AND r.share_capture_claim_generation=0 AND r.share_capture_launch_id IS NULL
 AND r.last_command_sequence=0 AND r.acknowledged_command_sequence=0
 AND r.receipt_rows=0 AND r.receipt_variable_bytes=0 AND r.receipt_reclaim_after_sequence=0
 AND r.receipt_reclaim_due_ms IS NULL AND r.reclaim_command_sequence IS NULL`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatal("captured non-input native custody / retained pin debt", n, err)
	}
}
