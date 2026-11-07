package writer

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// Exact remaining empty-table overlay from candidate
// 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// The existing capture fixture supplies the exact native dvr_recordings DDL.
const mgRecordingIntentDraftSQL = `CREATE TABLE dvr_capture_retirements(
 recording_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 create_operation_id BLOB NOT NULL CHECK(length(create_operation_id)=16),
 birth_request_digest BLOB NOT NULL CHECK(length(birth_request_digest)=32),
 source_id TEXT NOT NULL REFERENCES live_source_identities(id),
 final_sequence INTEGER NOT NULL CHECK(final_sequence>0),
 final_result_digest BLOB NOT NULL CHECK(length(final_result_digest)=32),
 retired_ms INTEGER NOT NULL CHECK(retired_ms>=0),
 owner_key TEXT NOT NULL,
 authority TEXT NOT NULL,
 account_id TEXT NOT NULL,
 profile_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 PRIMARY KEY(recording_id),
 CHECK(typeof("recording_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("create_operation_id") IN('null','blob')),
 CHECK(typeof("birth_request_digest") IN('null','blob')),
 CHECK(typeof("source_id") IN('null','text')),
 CHECK(typeof("final_sequence") IN('null','integer')),
 CHECK(typeof("final_result_digest") IN('null','blob')),
 CHECK(typeof("retired_ms") IN('null','integer')),
 CHECK(typeof("owner_key") IN('null','text')),
 CHECK(typeof("authority") IN('null','text')),
 CHECK(typeof("account_id") IN('null','text')),
 CHECK(typeof("profile_id") IN('null','text')),
 CHECK(typeof("member_incarnation") IN('null','text')),
 CHECK(typeof("profile_lifetime") IN('null','text'))
) WITHOUT ROWID;

CREATE INDEX dvr_capture_retirements_scope ON dvr_capture_retirements(source_id,recording_id);`

func mgRecordingIntentDraft(t *testing.T, w *executor) {
	t.Helper()
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(mgRecordingIntentDraftSQL))); got != "4e5848fe17b1b9eb406feaf0e4ae3243a3d4536a88aa577af7e65256e216959b" {
		t.Fatal("pinned intent overlay changed", got)
	}
	mgCaptureDraftFixture(t, w)
	if _, err := w.db.ExecContext(t.Context(), mgRecordingIntentDraftSQL); err != nil {
		t.Fatal(err)
	}
}
