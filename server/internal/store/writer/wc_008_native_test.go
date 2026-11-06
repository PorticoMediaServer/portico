package writer

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func wc008NativeProgress(t *testing.T, w *executor, until int64) wc008ProgressCommand {
	t.Helper()
	q := wcTest008Progress()
	q.proof.playbackID = "playback-音"
	q.memberIncarnation, q.profileLifetime = strings.Repeat("a", 32), strings.Repeat("b", 32)
	q.untilMS, q.acceptedOrdinal, q.lastAcceptedSequence = until, 71, 17
	q.proof.reportSequence, q.proof.occurrenceGeneration = 13, 5
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,'/fixture')`, q.libraryID, q.libraryKey)
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,1)`, q.subjectID, q.subjectPublicID[:], q.libraryID)
	wc008NativeExec(t, w, `INSERT INTO personal_activity_revisions(profile_id,revision,activity_generation,policy_authority,policy_account_id,policy_profile_id,policy_member_incarnation,policy_profile_lifetime,history_policy_generation) VALUES(?,31,?,?,?,?,?,?,?)`, q.profileKey, q.proof.activityGeneration, q.authority, q.accountKey, q.profileID, q.memberIncarnation, q.profileLifetime, q.proof.rememberGeneration)
	wc008NativeExec(t, w, `INSERT INTO play_history(id,occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,library_public_id,subject_id,started_ms,membership_incarnation,profile_lifetime,item_title,source_revision) VALUES(?,'1970-01-01T00:00:00Z','started',?,?,?,?,?,?,?,?,0,?,?,'original title','original snapshot')`, q.startEventID, q.accountKey, q.profileID, q.proof.playbackID, q.proof.occurrenceGeneration, int64(9), q.subjectPublicID[:], q.libraryKey, q.subjectID, q.memberIncarnation, q.profileLifetime)
	wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_id,item_public_id,library_id,revision,activity_generation) VALUES(?,?,?,?,19,?)`, q.profileKey, q.subjectID, q.subjectPublicID[:], q.libraryID, q.proof.activityGeneration)
	wc008NativeExec(t, w, `INSERT INTO viewer_revisions(profile_id,library_id,revision,observation_revision) VALUES(?,?,23,41)`, q.profileKey, q.libraryKey)
	wc008NativeExec(t, w, `INSERT INTO playback_personal_claims(playback_id,profile_key,item_id,accepted_ordinal,last_accepted_sequence,command_authority,command_account_id,command_profile_id,command_member_incarnation,command_profile_lifetime,command_library_id,command_incarnation,source_occurrence_generation,occurrence_start_event_id,pending_time_from_ms,pending_time_until_ms,pending_time_cursor_ms,pending_time_activity_generation,pending_time_remember_generation,pending_time_clock_incarnation,pending_time_sequence,pending_time_start_event_id,pending_time_occurrence_generation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,0,?,?,?,?,?,?)`, q.proof.playbackID, q.profileKey, q.subjectID, q.acceptedOrdinal, q.lastAcceptedSequence, q.authority, q.accountKey, q.profileID, q.memberIncarnation, q.profileLifetime, q.libraryID, q.identity.Incarnation[:], q.proof.occurrenceGeneration, q.startEventID, q.untilMS, q.proof.activityGeneration, q.proof.rememberGeneration, q.proof.clockIncarnation[:], q.proof.reportSequence, q.startEventID, q.proof.occurrenceGeneration)
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims WHERE playback_id=?`, q.proof.playbackID).Scan(&q.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestWC008NativeAllStatementsPrepare(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativePrepare(t, w, wcProgressDefinitions())
}

func TestWC008NativeDrainFinishedAndPendingResults(t *testing.T) {
	for _, until := range []int64{1000, wcQuarterMS + 1} {
		t.Run(fmt.Sprint(until), func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wc008NativeProgress(t, w, until)
			turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
			e, err := applyWC008ProgressMetrics(t.Context(), turn, q)
			if err != nil {
				t.Fatal(err)
			}
			if e.viewerRevision != 23 || e.personalRevision != 19 || e.observationRevision != 42 || e.historyRevision != 32 {
				t.Fatal("time changed semantic revisions or missed observations", e)
			}
			n, err := storeWC008ProgressResult(t.Context(), turn, q, e, RequestDigest{1}, ResultDigest{2})
			if err != nil || n <= 0 {
				t.Fatal("typed drain receipt", n, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var kind, title, source string
			var report, pending, ordinal, played, rows, bytes int64
			var cursor sql.NullInt64
			if err := w.db.QueryRowContext(t.Context(), `SELECT command_kind,source_accepted_ordinal,source_last_accepted_sequence,result_report_sequence,result_pending_sequence,result_pending_cursor_ms FROM playback_claim_command_receipts WHERE playback_id=?`, q.proof.playbackID).Scan(&kind, &ordinal, &report, &played, &pending, &cursor); err != nil {
				t.Fatal(err)
			}
			if kind != "drain_piece" || ordinal != 71 || report != 17 || played != 17 || pending != 13 || cursor.Valid == e.finished {
				t.Fatal("native source units or final NULL shape changed", kind, ordinal, report, played, pending, cursor)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT played_ms,item_title,source_revision FROM play_history WHERE event_kind='time'`).Scan(&played, &title, &source); err != nil || played != e.creditedMS || title != "original title" || source != "original snapshot" {
				t.Fatal("original START snapshot/time credit", played, title, source, err)
			}
			wantBytes := int64(len(q.proof.playbackID) + 16 + 16 + 32 + len("drain_piece") + 16 + 32)
			if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM playback_personal_claims WHERE playback_id=?`, q.proof.playbackID).Scan(&rows, &bytes); err != nil || rows != 1 || bytes != n || n != wantBytes {
				t.Fatal("native receipt debt", rows, bytes, n, err)
			}
			var bucketSum, leafMS, spanMS int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT sum(played_ms) FROM listening_item_buckets)+(SELECT sum(played_ms) FROM listening_profile_buckets),
 (SELECT played_ms FROM personal_items),(SELECT sum((until_ms-from_ms)*copies) FROM listening_time_spans)`).Scan(&bucketSum, &leafMS, &spanMS); err != nil || bucketSum != 6*e.creditedMS || leafMS != e.creditedMS || spanMS != e.creditedMS {
				t.Fatal("six native buckets/leaf/exact span diverged", bucketSum, leafMS, spanMS, err)
			}
		})
	}
}

func TestWC008NativeDrainStalePolicyAndReceiptFailureRollback(t *testing.T) {
	for _, failure := range []string{"policy", "receipt"} {
		t.Run(failure, func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wc008NativeProgress(t, w, 1000)
			if failure == "policy" {
				wc008NativeExec(t, w, `UPDATE personal_activity_revisions SET history_policy_generation=history_policy_generation+1`)
			}
			turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
			e, err := applyWC008ProgressMetrics(t.Context(), turn, q)
			if failure == "policy" {
				if err == nil || e != (wc008ProgressEffect{}) {
					t.Fatal("stale policy exposed effects", e, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if n, err := storeWC008ProgressResult(t.Context(), turn, q, e, RequestDigest{}, ResultDigest{2}); err == nil || n != 0 {
					t.Fatal("invalid receipt accepted after metric prefix", n, err)
				}
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var spanRows, bucketRows, timeRows, played, cursor int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM listening_time_spans),
 (SELECT count(*) FROM listening_item_buckets)+(SELECT count(*) FROM listening_profile_buckets),
 (SELECT count(*) FROM play_history WHERE event_kind='time'),(SELECT played_ms FROM personal_items),
 (SELECT pending_time_cursor_ms FROM playback_personal_claims)`).Scan(&spanRows, &bucketRows, &timeRows, &played, &cursor); err != nil || spanRows != 0 || bucketRows != 0 || timeRows != 0 || played != 0 || cursor != 0 {
				t.Fatal("drain prefix survived failure", spanRows, bucketRows, timeRows, played, cursor, err)
			}
		})
	}
}
