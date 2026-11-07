package writer

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Exact selected live_reminders declaration/keys from MG-001 candidate
// 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// Only EMPTY owned tables on disposable canonical clones are replaced, FK ON.
// The existing LC/guide fixture constants supply their exact selected parents.
// This is neither canonical/product DDL nor a whole candidate copy/skip path.
const mgPersonalReminderDraftSQL = `CREATE TABLE live_reminders(
 id TEXT PRIMARY KEY,
 owner_key TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN('live-source','library-channel')),
 source_id TEXT NOT NULL,
 channel_id TEXT NOT NULL,
 generation_id TEXT NOT NULL,
 programme_id TEXT NOT NULL,
 revision INTEGER NOT NULL,
 start_utc TEXT NOT NULL,
 external_generation_ref TEXT GENERATED ALWAYS AS (CASE WHEN kind='live-source' THEN generation_id ELSE NULL END) STORED,
 library_generation_ref TEXT GENERATED ALWAYS AS (CASE WHEN kind='library-channel' THEN generation_id ELSE NULL END) STORED,
 FOREIGN KEY(external_generation_ref,source_id) REFERENCES live_generations(id,source_id),
 FOREIGN KEY(library_generation_ref,channel_id) REFERENCES lc_generations(id,channel_id)
) STRICT;
CREATE INDEX live_reminders_owner ON live_reminders(owner_key,id);
CREATE INDEX live_reminders_external_generation ON live_reminders(external_generation_ref,source_id,id);
CREATE INDEX live_reminders_library_generation ON live_reminders(library_generation_ref,channel_id,id);`

type mgPersonalFixtureValues struct {
	owner         mgPersonalOwner
	live, library mgPersonalChannel
	item          mgPersonalLibraryItem
	start         time.Time
}

func (v mgPersonalFixtureValues) reminder(library bool) mgPersonalReminderRequest {
	r := mgPersonalReminderRequest{owner: v.owner, target: v.live, programme: "live-programme"}
	if library {
		r.target = v.library
		r.programme = "lc-occurrence"
		r.libraryItem = v.item
	}
	return r
}
func mgPersonalFixture(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, mgPersonalFixtureValues) {
	t.Helper()
	w, _ := testCanonicalExecutor(t, decorate...)
	// Select only the exact source table from the existing guide draft constant.
	source := mgGuidePublicationDraftSQL[:strings.Index(mgGuidePublicationDraftSQL, "\n\nCREATE TABLE live_operations")]
	overlay := `DROP TABLE live_sources;DROP TABLE live_reminders;DROP TABLE lc_playback_refs;DROP TABLE lc_generations;DROP TABLE lc_channels;` + mgLCDraftSQL + `CREATE UNIQUE INDEX live_generations_source_scope ON live_generations(id,source_id);` + source + `CREATE INDEX live_sources_generation_head ON live_sources(active_generation,id);` + mgPersonalReminderDraftSQL
	if _, err := w.db.ExecContext(t.Context(), overlay); err != nil {
		t.Fatal(err)
	}
	v := mgPersonalFixtureValues{owner: mgPersonalOwner{"local", "acct", "profile"}, live: mgPersonalChannel{mgPersonalLive, "personal-live", "live-channel", "live-generation", 7}, library: mgPersonalChannel{mgPersonalLibrary, "library-channel", "library-channel", "lc-generation", 5}, item: mgPersonalLibraryItem{101, "selected-library", "selected-asset", "observed-source"}, start: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)}
	for _, q := range []string{
		`INSERT INTO live_source_identities(id) VALUES('personal-live')`,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('live-generation','personal-live','Guide',1,2,2,'2026-11-07T00:00:00Z'),('replacement-generation','personal-live','Next guide',1,0,0,'2026-11-07T00:00:00Z')`,
		`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('personal-live','Live',7,'active',1,'live-generation','2026-11-07T00:00:00Z')`,
		`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('live-generation','live-channel','provider-1','Channel','1','Group',0,X'01','',''),('live-generation','other-channel','provider-2','Other','2','Group',1,X'01','','')`,
		`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('library-channel',5,'{}',1,0,'Library channel','healthy')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	start, end := v.start.Format(time.RFC3339), v.start.Add(time.Hour).Format(time.RFC3339)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('live-generation','live-programme','live-channel','p1','Programme',?,?,'lineage'),('live-generation','other-programme','other-channel','p2','Other',?,?,'other')`, start, end, start, end); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,command_incarnation,create_operation_id,birth_request_digest,entry_digest,verify_end_ms,verified_entry_digest) VALUES('lc-generation','library-channel',5,'{}','selected-snapshot','seed','published','published','',?,?,?, ?,1,zeroblob(16),zeroblob(16),zeroblob(32),zeroblob(32),?,zeroblob(32))`, v.start.UnixMilli(), v.start.Add(time.Hour).UnixMilli(), v.start.UnixMilli(), v.start.Add(time.Hour).UnixMilli(), v.start.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET active_generation='lc-generation' WHERE id='library-channel'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_entries VALUES('lc-generation','lc-occurrence','library-channel',?,?,101,'selected-asset','selected-library','observed-source','Entry','rule','block',125,'')`, v.start.UnixMilli()+123, v.start.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return w, v
}
func mgPersonalTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	defs := mgPersonalDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	// Existing private NORMAL frame only; parent aggregates the new descriptors
	// and provides accepting profiles/authority. These are unqualified forecasts.
	recipe := turnRecipe{limit: Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
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
func mgPersonalCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
