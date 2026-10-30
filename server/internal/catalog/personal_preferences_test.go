package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// storePreference writes one profile-server override the way ApplyPreferences
// would, so the catalog reads exactly the rows the console writes.
func storePreference(t *testing.T, db *sql.DB, v identity.Viewer, values map[string]any) {
	t.Helper()
	store := operations.New(db)
	auth := func(context.Context, *sql.Tx, string) error { return nil }
	p := identity.Principal{Viewer: v}
	current, e := store.Preferences(context.Background(), p, "web", auth)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.ApplyPreferences(context.Background(), p, auth, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: current.Documents[0].Revision, IdempotencyKey: identity.Token(), Values: operations.PreferencePatch(values)}); e != nil {
		t.Fatal(e)
	}

}

// Community fixtures opt in explicitly; production defaults keep activity private.
func shareFixtureProfiles(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT id,profile_id FROM accounts`)
	if err != nil {
		t.Fatal(err)
	}
	var viewers []identity.Viewer
	for rows.Next() {
		var v identity.Viewer
		v.Authority = "local"
		if err = rows.Scan(&v.AccountID, &v.ProfileID); err != nil {
			t.Fatal(err)
		}
		viewers = append(viewers, v)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, v := range viewers {
		storePreference(t, db, v, map[string]any{"privacy.showActivityToMembers": true})
	}
}

func accept(t *testing.T, s *Service, db *sql.DB, v identity.Viewer, item string, seq int64, position, duration float64, state string) {
	t.Helper()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = s.AcceptPlaybackProgress(tx, v, "play", item, seq, position, duration, state, true); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func TestPlaybackThresholdsFollowViewerPreferences(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); e != nil {
		t.Fatal(e)
	}
	// Default policy: 80 of 100 seconds is started, not played.
	accept(t, s, db, v, item.Public, 1, 80, 100, "playing")
	p, e := s.Personal(key, item.Public)
	if e != nil || p.Watched || p.ProgressSeconds != 80 {
		t.Fatalf("default played threshold moved: %+v %v", p, e)
	}
	// A viewer who counts 75 percent as played reaches watched at the same point.
	storePreference(t, db, v, map[string]any{"playback.playedThresholdPercent": 75})
	accept(t, s, db, v, item.Public, 2, 80, 100, "playing")
	if p, e = s.Personal(key, item.Public); e != nil || !p.Watched {
		t.Fatalf("played threshold override ignored: %+v %v", p, e)
	}
}

func TestLibraryVideoCompletionUsesTrustedCreditsOrThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name, mode string
		position   float64
		threshold  int
		marker     bool
		approved   int
		want       bool
		provenance string
		detection  string
	}{
		{"default ninety percent", "earliest", 90, 90, false, 0, true, "", ""},
		{"owner threshold eighty percent", "threshold", 80, 80, false, 0, true, "", ""},
		{"approved credits finish early", "earliest", 85, 90, true, 1, true, "", ""},
		{"threshold ignores credits", "threshold", 85, 90, true, 1, false, "", ""},
		{"credits mode uses marker", "credits", 85, 90, true, 1, true, "", ""},
		{"credits mode falls back without approval", "credits", 95, 90, true, 0, true, "", ""},
		{"credits mode falls back without a marker", "credits", 95, 90, false, 0, true, "", ""},
		{"unapproved guess cannot finish", "earliest", 85, 90, true, 0, false, "", ""},
		{"credits confirmed by other episodes finish early", "earliest", 85, 90, true, 0, true, "season_audio_match:credits:v1", ""},
		{"spoofed measured family cannot finish", "earliest", 85, 90, true, 0, false, "seasonXaudioXmatch:credits:v1", ""},
		{"confirmed credits do not count once detection is off", "earliest", 85, 90, true, 0, false, "season_audio_match:credits:v1", "off"},
		{"approved credits count with detection off", "earliest", 85, 90, true, 1, true, "", "off"},
		{"a picture guess cannot finish", "earliest", 85, 90, true, 0, false, "visual_credits:v1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, v := savedFixture(t)
			item := phase34SavedFixtureItem(t, db)
			key := identity.PersonalKey(v)
			if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('source','library','/markers','/markers')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
	 VALUES('object','source',?,'root','item.mkv','revision','{}',1,1)`, item.Token); err != nil {
				t.Fatal(err)
			}
			document := map[string]any{"continueWatching": map[string]any{"videoPlayedThreshold": tc.threshold, "videoCompletion": tc.mode}}
			if tc.detection != "" {
				document["segmentDetection"] = tc.detection
			}
			body, _ := json.Marshal(document)
			if _, err := db.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:library',2,?,0)`, string(body)); err != nil {
				t.Fatal(err)
			}
			if tc.marker {
				provenance, confidence := "test", 1.0
				if tc.provenance != "" {
					provenance, confidence = tc.provenance, 0.9
				}
				if _, err := db.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved)
 VALUES('credits','object','revision','binding','credits',80000000,100000000,?,?,'',?)`, confidence, provenance, tc.approved); err != nil {
					t.Fatal(err)
				}
			}
			catalogtest.New(t, db).Drain()
			accept(t, s, db, v, item.Public, 1, tc.position, 100, "playing")
			personal, err := s.Personal(key, item.Public)
			if err != nil || personal.Watched != tc.want {
				t.Fatalf("watched=%v want %v: %v", personal.Watched, tc.want, err)
			}
		})
	}
}

func TestStartedThresholdHonoursPercentForLongAndShortTitles(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); e != nil {
		t.Fatal(e)
	}
	// Five percent of a long feature is 360 seconds. Brief playback stays below it.
	accept(t, s, db, v, item.Public, 1, 40, 7200, "playing")
	p, e := s.Personal(key, item.Public)
	if e != nil || p.ProgressSeconds != 0 {
		t.Fatalf("percentage ignored: %+v %v", p, e)
	}
	accept(t, s, db, v, item.Public, 2, 360, 7200, "playing")
	if p, e = s.Personal(key, item.Public); e != nil || p.ProgressSeconds != 360 {
		t.Fatal(p, e)
	}
	// A one-percent viewer on a short item starts sooner than the default five.
	s2, db2, v2 := savedFixture(t)
	item2 := phase34SavedFixtureItem(t, db2)
	key2 := identity.PersonalKey(v2)
	if _, e = db2.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key2, item2.ID); e != nil {
		t.Fatal(e)
	}
	accept(t, s2, db2, v2, item2.Public, 1, 5, 200, "playing")
	if p, e = s2.Personal(key2, item2.Public); e != nil || p.ProgressSeconds != 0 {
		t.Fatalf("default started threshold moved: %+v %v", p, e)
	}
	storePreference(t, db2, v2, map[string]any{"playback.startedThresholdPercent": 1})
	accept(t, s2, db2, v2, item2.Public, 2, 5, 200, "playing")
	if p, e = s2.Personal(key2, item2.Public); e != nil || p.ProgressSeconds != 5 {
		t.Fatalf("started threshold override ignored: %+v %v", p, e)
	}
}

func TestPauseWatchHistoryStopsNewProgressWrites(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); e != nil {
		t.Fatal(e)
	}
	accept(t, s, db, v, item.Public, 1, 40, 100, "playing")
	storePreference(t, db, v, map[string]any{"privacy.pauseWatchHistory": true})
	accept(t, s, db, v, item.Public, 2, 90, 100, "playing")
	p, e := s.Personal(key, item.Public)
	if e != nil || p.Watched || p.ProgressSeconds != 40 {
		t.Fatalf("a paused history recorded new progress: %+v %v", p, e)
	}
	var rows int
	if e = db.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id=?`, key).Scan(&rows); e != nil || rows != 1 {
		t.Fatalf("a paused history appended an occurrence: %d %v", rows, e)
	}
	// Manual watched still works while history is paused.
	yes := true
	if p, e = s.SetPersonal("local:account", key, item.Public, PersonalMutation{OperationID: "manual", ExpectedRevision: p.Revision, Watched: &yes}, nil); e != nil || !p.Watched {
		t.Fatalf("a paused history blocked a manual mutation: %+v %v", p, e)
	}
	// Resuming records again from the existing state.
	storePreference(t, db, v, map[string]any{"privacy.pauseWatchHistory": false})
	accept(t, s, db, v, item.Public, 3, 60, 100, "playing")
	var rowsAfter int
	if e = db.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id=?`, key).Scan(&rowsAfter); e != nil || rowsAfter != 1 {
		t.Fatalf("resume did not reuse the occurrence: %d %v", rowsAfter, e)
	}
	var position float64
	if e = db.QueryRow(`SELECT position FROM personal_history WHERE profile_id=?`, key).Scan(&position); e != nil || position != 60000 {
		t.Fatalf("resume did not record: %v %v", position, e)
	}
}

func TestSearchHistoryRemembersOnlyWhenPreferred(t *testing.T) {
	s, db, v := savedFixture(t)
	if e := s.RecordSearch(v, "  Harbor   Lights "); e != nil {
		t.Fatal(e)
	}
	page, e := s.SearchHistory(v)
	if e != nil || !page.Remembered || len(page.Entries) != 1 || page.Entries[0].Query != "Harbor Lights" {
		t.Fatalf("history not recorded: %+v %v", page, e)
	}
	// The same query in another case updates one entry rather than adding a second.
	if e = s.RecordSearch(v, "harbor lights"); e != nil {
		t.Fatal(e)
	}
	if page, e = s.SearchHistory(v); e != nil || len(page.Entries) != 1 {
		t.Fatalf("duplicate entry: %+v %v", page, e)
	}
	storePreference(t, db, v, map[string]any{"search.rememberHistory": false})
	if e = s.RecordSearch(v, "Another Title"); e != nil {
		t.Fatal(e)
	}
	if page, e = s.SearchHistory(v); e != nil || page.Remembered || len(page.Entries) != 0 {
		t.Fatalf("history returned while remembering is off: %+v %v", page, e)
	}
	storePreference(t, db, v, map[string]any{"search.rememberHistory": true})
	if page, e = s.SearchHistory(v); e != nil || len(page.Entries) != 1 || page.Entries[0].Query != "harbor lights" {
		t.Fatalf("a paused search recorded anyway: %+v %v", page, e)
	}
	if page, e = s.ClearSearchHistory(v); e != nil || len(page.Entries) != 0 {
		t.Fatalf("clear left entries: %+v %v", page, e)
	}
}

func TestSearchHistoryIsBoundedAndPerViewer(t *testing.T) {
	s, _, v := savedFixture(t)
	for i := 0; i < SearchHistoryLimit+8; i++ {
		if e := s.RecordSearch(v, string(rune('a'+i%26))+"query"+string(rune('0'+i%10))+"-"+string(rune('A'+i))); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.SearchHistory(v)
	if e != nil || len(page.Entries) != SearchHistoryLimit {
		t.Fatalf("history is unbounded: %d %v", len(page.Entries), e)
	}
	other := v
	other.AccountID = "other"
	if page, e = s.SearchHistory(other); e != nil || len(page.Entries) != 0 {
		t.Fatalf("history leaked across viewers: %+v %v", page, e)
	}
}

func TestProgressObservationsKeepHomeAndCommandsStable(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); err != nil {
		t.Fatal(err)
	}
	accept(t, s, db, v, item.Public, 1, 40, 100, "playing")
	before, err := s.Personal(key, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	var home, snapshots int64
	if err = db.QueryRow(`SELECT COALESCE(sum(revision),0) FROM viewer_revisions WHERE profile_id=?`, key).Scan(&home); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM personal_snapshots WHERE profile_id=?`, key).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	accept(t, s, db, v, item.Public, 2, 50, 100, "playing")
	after, err := s.Personal(key, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	var nextHome, nextSnapshots, observations int64
	_ = db.QueryRow(`SELECT COALESCE(sum(revision),0),COALESCE(sum(observation_revision),0) FROM viewer_revisions WHERE profile_id=?`, key).Scan(&nextHome, &observations)
	_ = db.QueryRow(`SELECT count(*) FROM personal_snapshots WHERE profile_id=?`, key).Scan(&nextSnapshots)
	if before.Revision != after.Revision || home != nextHome || snapshots != nextSnapshots || observations == 0 {
		t.Fatalf("command %d/%d home %d/%d snapshots %d/%d observations %d", before.Revision, after.Revision, home, nextHome, snapshots, nextSnapshots, observations)
	}
	if after.ProgressSeconds != 50 {
		t.Fatalf("progress lost: %v", after.ProgressSeconds)
	}
	yes := true
	if _, err = s.SetPersonal("local:account", key, item.Public, PersonalMutation{OperationID: "after-tick", ExpectedRevision: before.Revision, Favorite: &yes}, nil); err != nil {
		t.Fatalf("tick invalidated command: %v", err)
	}
}

func TestEarlyEndKeepsResumeForKnownDuration(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); e != nil {
		t.Fatal(e)
	}
	accept(t, s, db, v, item.Public, 1, 35, 100, "ended")
	p, e := s.Personal(key, item.Public)
	if e != nil || p.Watched || p.ProgressSeconds != 35 {
		t.Fatalf("early end discarded resume: %+v %v", p, e)
	}
	accept(t, s, db, v, item.Public, 2, 100, 100, "ended")
	p, e = s.Personal(key, item.Public)
	if e != nil || !p.Watched || p.ProgressSeconds != 0 {
		t.Fatalf("natural end: %+v %v", p, e)
	}
}

func TestSearchHistoryLateCapturedViewerCannotRecreateRetiredProfileState(t *testing.T) {
	s, db, v := savedFixture(t)
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,x'00',?)`, v.AccountID, "retirement-search", v.ProfileID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSearch(v, "before retirement"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE direct_profiles SET deleted=1 WHERE account_id=? AND id=?`, v.AccountID, v.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM search_history WHERE profile_key=?`, identity.PersonalKey(v)); err != nil {
		t.Fatal(err)
	}
	// This is the old in-flight viewer, already captured before retirement.
	if err := s.RecordSearch(v, "late private query"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM search_history WHERE profile_key=?`, identity.PersonalKey(v)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("late write recreated search history: %d %v", n, err)
	}
}
