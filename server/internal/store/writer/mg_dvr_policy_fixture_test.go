package writer

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

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
		`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,claim_generation,claim_token,lease_until_ms,created_ms,updated_ms) VALUES('policy-marker','fixture-owner','local','account','profile','policy-source','channel','programme','guide','{"title":"Held recording"}','{"beforeMs":1000,"afterMs":2000,"retentionDays":0,"episodeLimit":0,"priority":0}',2,3,0,42,'recording',2,'held-token',1,1,1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
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
	tx, err := w.begin(t.Context(), MGGuidePublish, recipe, &testCustody{})
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
