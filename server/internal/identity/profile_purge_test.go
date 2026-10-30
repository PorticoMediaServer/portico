package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// purgeTestAccount is the account every ownership fixture builds.
const purgeTestAccount = "account"

func purgeTestPersonal(profile string) string {
	return persistence.PersonalOwnerKey("local", purgeTestAccount, profile)
}

func purgeTestViewer(profile string) string {
	raw, _ := json.Marshal([3]string{"local", purgeTestAccount, profile})
	return string(raw)
}

func purgeTestJob(account, profile string) *purgeJob {
	return &purgeJob{
		id:              "job_0123456789abcdefg",
		account:         account,
		profile:         profile,
		accountLifetime: strings.Repeat("a", 32),
		profileLifetime: strings.Repeat("b", 32),
	}
}

// TestProfilePurgeRegistryShape checks the compiled plan against the shared
// engine's own contract before any database is touched.
func TestProfilePurgeRegistryShape(t *testing.T) {
	if len(profilePurgeRegistry) == 0 {
		t.Fatal("empty purge registry")
	}
	seen := map[string]bool{}
	phase := -1
	for i := range profilePurgeRegistry {
		st := &profilePurgeRegistry[i]
		if st.table == "authorization_family_tokens" && !st.tokenDrain {
			t.Fatalf("token unit %s can lose private child custody", st.name)
		}
		if st.name == "" || st.table == "" || seen[st.name] {
			t.Fatalf("unit %d has a missing or duplicate name %q", i, st.name)
		}
		seen[st.name] = true
		if st.phase < phase {
			t.Fatalf("unit %s phase %d is below the previous phase %d", st.name, st.phase, phase)
		}
		phase = st.phase
		if st.readBound < 1 || st.readBound > phasedpurge.ReadLimit-purgeOwnerReadBound {
			t.Fatalf("unit %s read bound %d outside 1..%d", st.name, st.readBound, phasedpurge.ReadLimit-purgeOwnerReadBound)
		}
		keys := purgeSplit(st.keys)
		if len(keys) == 0 {
			t.Fatalf("unit %s has no keyset columns", st.name)
		}
		if len(st.keyKinds) != len(keys) {
			t.Fatalf("unit %s declares %d key kinds for %d key columns", st.name, len(st.keyKinds), len(keys))
		}
		if len(st.guards) > 0 && !st.rewind {
			t.Fatalf("guarded unit %s must rewind the phase", st.name)
		}
		if (st.action == purgeScrub) == (st.set == "") {
			t.Fatalf("unit %s scrub/set mismatch", st.name)
		}
		if st.action == purgeScrub && st.extra == "" && st.scope != scopeCursor && st.scope != scopePreparedActor {
			t.Fatalf("scrub unit %s would rewrite the same row forever without a pending exclusion", st.name)
		}
		if st.action == purgeRevokeFamily && (len(st.guards) > 0 || len(st.of) > 0) {
			t.Fatalf("family revocation must be an unguarded single-row unit")
		}
		if len(st.of) > 0 && st.hopScope == scopeNone {
			t.Fatalf("unit %s has a parent chain without a hop scope", st.name)
		}
	}
	var sessionsPhase, familiesPhase int
	for _, st := range profilePurgeRegistry {
		if st.table == "playback_sessions" && st.action == purgeDelete {
			sessionsPhase = st.phase
		}
		if st.action == purgeRevokeFamily {
			familiesPhase = st.phase
		}
	}
	if familiesPhase <= sessionsPhase {
		t.Fatal("family revocation trigger can fan out before legacy sessions drain")
	}
	// The shared engine must accept the empty starting cursor against this plan.
	plan := profilePurgeUnits(nil, purgeTestJob(purgeTestAccount, "profile"))
	if _, err := phasedpurge.DecodeCursor("", plan); err != nil {
		t.Fatalf("engine rejects the compiled plan: %v", err)
	}
}

// profilePurgeSchemaFixture opens the real baseline and returns the database and
// the two profiles a retirement fixture creates.
func profilePurgeFixture(t *testing.T) (*Service, *sql.DB, DirectSignIn, DirectProfile, ProfileDeletionPreview) {
	t.Helper()
	return retirementFixture(t)
}

// TestProfilePurgeUnitsPrepareAndPlan proves every unit prepares against the real
// schema, seeks through an index instead of scanning, uses an indexed foreign key
// for every hop and guard, and carries a read bound that covers the trigger
// worst case. A LIMIT alone is never treated as proof.
func TestProfilePurgeUnitsPrepareAndPlan(t *testing.T) {
	s, db, _, kid, _ := profilePurgeFixture(t)
	job := purgeTestJob(purgeTestAccount, kid.ID)
	plan := profilePurgeUnits(s, job)
	if len(plan) != len(profilePurgeRegistry) {
		t.Fatalf("plan has %d units for %d registry entries", len(plan), len(profilePurgeRegistry))
	}
	for i := range profilePurgeRegistry {
		st := &profilePurgeRegistry[i]
		args := job.purgeArgs(st)
		query := purgeStepSQL(st, "")
		switch {
		case st.preparationRoot:
			query = purgePreparationActorSQL
			args = []any{job.account, job.profile}
		case st.tokenDrain || st.avatarObject:
			// Custom compiled units are prepared through their exact statements
			// below; they have no generic candidate predicate.
			query = purgeTokenProbeSQL
			args = []any{job.account, job.profile}
			if st.avatarObject {
				query = `SELECT digest FROM profile_avatars WHERE profile_id=?`
				args = []any{job.profile}
			}
		case st.action == purgeRevokeFamily:
			query = `SELECT id FROM authorization_session_families WHERE authority='local' AND account_id=? AND profile_id=? AND revoked=0 ORDER BY id LIMIT 1`
			args = []any{job.account, job.profile}
		}
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatalf("unit %s does not prepare: %v\n%s", st.name, err, query)
		}
		stmt.Close()
		rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("unit %s plan: %v\n%s", st.name, err, query)
		}
		searched := false
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err = rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("unit %s plan scan: %v", st.name, err)
			}
			if strings.Contains(detail, "SEARCH") {
				searched = true
			}
			if strings.HasPrefix(detail, "SCAN ") && !purgeScanAllowed(detail) {
				t.Errorf("unit %s scans instead of seeking: %s", st.name, detail)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatalf("unit %s plan rows: %v", st.name, err)
		}
		if st.action != purgeRevokeFamily && !searched {
			t.Errorf("unit %s has no indexed search in its plan", st.name)
		}
		if st.tokenDrain {
			for _, child := range purgeTokenChildSQL() {
				if _, childErr := db.Prepare(child); childErr != nil {
					t.Errorf("token child statement does not prepare: %v", childErr)
				}
			}
			if _, childErr := db.Prepare(purgeTokenDrainSQL); childErr != nil {
				t.Errorf("token drain statement does not prepare: %v", childErr)
			}
		}
		if st.avatarObject {
			if _, childErr := db.Prepare(`DELETE FROM profile_avatar_objects WHERE (digest,rendition) IN(SELECT digest,rendition FROM profile_avatar_objects WHERE digest=? AND NOT EXISTS(SELECT 1 FROM profile_avatars a WHERE a.digest=? AND a.profile_id<>?) ORDER BY digest,rendition LIMIT 1)`); childErr != nil {
				t.Errorf("avatar object statement does not prepare: %v", childErr)
			}
		}
		if st.name == "prepared-media-actor" {
			named := false
			// Re-plan to inspect every detail for the approved expression index.
			plan2, planErr := db.Query("EXPLAIN QUERY PLAN "+query, args...)
			if planErr != nil {
				t.Fatalf("unit %s plan: %v", st.name, planErr)
			}
			for plan2.Next() {
				var id, parent, unused int
				var detail string
				if planErr = plan2.Scan(&id, &parent, &unused, &detail); planErr != nil {
					plan2.Close()
					t.Fatalf("unit %s plan scan: %v", st.name, planErr)
				}
				if strings.Contains(detail, "p2_62_purge_prepared_actor") {
					named = true
				}
			}
			plan2.Close()
			if !named {
				t.Errorf("unit %s must use the approved p2_62_purge_prepared_actor index", st.name)
			}
		}
		// Every parent hop and owned-child guard must be served by an index whose
		// leading columns are the referencing columns.
		child := st.table
		for _, h := range st.of {
			if !purgeHasLeadingIndex(t, db, child, purgeSplit(h.fk)) {
				t.Errorf("unit %s: %s.%s has no leading index for its parent hop", st.name, child, h.fk)
			}
			child = h.table
		}
		for _, g := range st.guards {
			if !purgeHasLeadingIndex(t, db, g.table, purgeSplit(g.fk)) {
				t.Errorf("unit %s: %s.%s has no leading index for its guard", st.name, g.table, g.fk)
			}
		}
		// The declared bound must cover the candidate, nested probes, guards and
		// the declared worst case of every trigger body on the table.
		want := 2 + 2*purgeParentProbe*len(st.of) + len(st.guards) + purgeTriggerReads(t, db, st.table, st.action)
		if st.action == purgeRevokeFamily {
			want = 3
		}
		if st.readBound < want {
			t.Errorf("unit %s read bound %d does not cover %d", st.name, st.readBound, want)
		}
	}
}

// purgeScanAllowed permits only scans of the bounded materialized candidate
// window, a one-row constant scan, or SQLite's own small lookups.
func purgeScanAllowed(detail string) bool {
	switch {
	case detail == "SCAN heads", detail == "SCAN h", detail == "SCAN chosen":
		return true
	case detail == "SCAN candidate":
		return true
	case strings.HasPrefix(detail, "SCAN c "), detail == "SCAN c":
		return true
	case strings.Contains(detail, "SCAN CONSTANT ROW"):
		return true
	case strings.Contains(detail, "SCAN SUBQUERY"):
		return true
	}
	return false
}

func purgeHasLeadingIndex(t *testing.T, db *sql.DB, table string, columns []string) bool {
	t.Helper()
	if len(columns) == 0 {
		return true
	}
	rows, err := db.Query(`SELECT l.name,i.name FROM pragma_index_list(?) l JOIN pragma_index_info(l.name) i ORDER BY l.name,i.seqno`, table)
	if err != nil {
		t.Fatalf("index list %s: %v", table, err)
	}
	defer rows.Close()
	byName := map[string][]string{}
	var order []string
	for rows.Next() {
		var name, column string
		if err = rows.Scan(&name, &column); err != nil {
			t.Fatalf("index info %s: %v", table, err)
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], column)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("index rows %s: %v", table, err)
	}
	for _, name := range order {
		idx := byName[name]
		if len(idx) < len(columns) {
			continue
		}
		match := true
		for i := range columns {
			if idx[i] != columns[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// purgeTriggerReads recomputes the trigger worst case exactly as the compiled
// read-bound comment describes: one read per DML statement and per subquery SELECT
// in every AFTER trigger body of the requested event.
func purgeTriggerReads(t *testing.T, db *sql.DB, table string, action purgeAction) int {
	t.Helper()
	event := "AFTER DELETE"
	if action == purgeScrub {
		event = "AFTER UPDATE"
	}
	if action == purgeRevokeFamily {
		event = "AFTER UPDATE"
	}
	rows, err := db.Query(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND tbl_name=?`, table)
	if err != nil {
		t.Fatalf("trigger list %s: %v", table, err)
	}
	defer rows.Close()
	dml := regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE)\b`)
	read := regexp.MustCompile(`(?i)\bSELECT\b`)
	total := 0
	for rows.Next() {
		var body sql.NullString
		if err = rows.Scan(&body); err != nil {
			t.Fatalf("trigger body %s: %v", table, err)
		}
		if !body.Valid {
			continue
		}
		upper := strings.ToUpper(body.String)
		if !strings.Contains(upper, event) {
			continue
		}
		total += len(dml.FindAllString(body.String, -1)) + len(read.FindAllString(body.String, -1))
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("trigger rows %s: %v", table, err)
	}
	return total
}

// TestProfilePurgeCoverageReconcilesSchema fails when a table with a profile
// dimension is neither purged nor classified in the compiled ledger.
func TestProfilePurgeCoverageReconcilesSchema(t *testing.T) {
	_, db, _, _, _ := profilePurgeFixture(t)
	covered := map[string]string{}
	for _, e := range profilePurgeCoverage() {
		covered[e.table] = e.reason
	}
	profileColumns := map[string]bool{
		"profile_id": true, "profile_key": true, "owner_key": true, "owner_profile": true,
		"actor_profile_id": true, "recipient_profile_id": true, "last_profile_id": true,
		"profile_cursor": true, "owner_authority": true,
	}
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		cols, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("columns %s: %v", table, err)
		}
		bearing := ""
		for cols.Next() {
			var name string
			if err = cols.Scan(&name); err != nil {
				cols.Close()
				t.Fatal(err)
			}
			if profileColumns[name] {
				bearing = name
				break
			}
		}
		cols.Close()
		if err = cols.Err(); err != nil {
			t.Fatalf("column rows %s: %v", table, err)
		}
		if bearing == "" || purgeCovered(table) {
			continue
		}
		if _, ok := covered[table]; ok {
			continue
		}
		if _, ok := covered[table+"."+bearing]; ok {
			continue
		}
		t.Errorf("%s.%s carries a profile dimension but is neither purged nor classified", table, bearing)
	}
	// Newest source additions must be explicit, not inherited.
	for _, table := range []string{"download_action_claims", "download_action_transfers"} {
		if !purgeCovered(table) {
			t.Errorf("%s must be in the purge registry", table)
		}
	}
	for _, table := range []string{"selection_action_groups", "selection_action_targets", "selection_action_files", "selection_action_history"} {
		if purgeCovered(table) {
			t.Errorf("%s is owner/primary command history, not a profile purge row", table)
		}
		if _, ok := covered[table]; !ok {
			t.Errorf("%s must be classified in the coverage ledger", table)
		}
	}
	if _, ok := covered["identity_installations"]; !ok {
		t.Error("identity_installations must be classified as an installation lifetime row")
	}
	if !purgeCovered("user_mutation_roots") {
		t.Error("user_mutation_roots carries the retired profile family and must be purged")
	}
	if !purgeCovered("lc_playback_refs") {
		t.Error("lc_playback_refs must close with its v1 session")
	}
}

// purgeDrainReportForTest drives the real worker and names the unit it is stuck
// on when the drain does not converge.
func purgeDrainReportForTest(t *testing.T, s *Service, ctx context.Context, bearer, id string, expected int64) error {
	t.Helper()
	var lifetime string
	if err := s.db.QueryRow(`SELECT lifetime FROM direct_profiles WHERE id=?`, id).Scan(&lifetime); err != nil {
		return err
	}
	if _, err := s.RetireDirectProfile(ctx, bearer, id, ProfileRetirementRequest{ExpectedRevision: expected, Lifetime: lifetime, DeleteSavedResources: true}); err != nil {
		return err
	}
	var job purgeJob
	if err := s.db.QueryRow(`SELECT job_id,account_id,profile_id,account_incarnation,profile_incarnation,cursor_key,attempt,state,process_epoch,next_attempt_ms FROM security_purge_jobs WHERE profile_id=?`, id).Scan(&job.id, &job.account, &job.profile, &job.accountLifetime, &job.profileLifetime, &job.cursor, &job.attempt, &job.state, &job.epoch, &job.due); err != nil {
		return err
	}
	plan := profilePurgeUnits(s, &job)
	for i := 0; i < 20000; i++ {
		worked, err := s.PurgeProfileStep(ctx)
		if err != nil {
			return err
		}
		if !worked {
			return nil
		}
		if i%2500 == 0 {
			var cursor string
			var phase int
			_ = s.db.QueryRow(`SELECT cursor_key,phase FROM security_purge_jobs WHERE job_id=?`, job.id).Scan(&cursor, &phase)
			unit := "?"
			if stored, decodeErr := purgeDecodeStoredCursor(cursor); decodeErr == nil {
				if c, cursorErr := phasedpurge.DecodeCursor(stored, plan); cursorErr == nil {
					if c.Unit < len(plan) {
						unit = plan[c.Unit].Name
					} else {
						unit = "verify/finalize"
					}
				}
			}
			t.Logf("purge step %d phase=%d unit=%s cursor=%s", i, phase, unit, cursor)
		}
	}
	return fmt.Errorf("purge failed to converge")
}

func purgeMustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("fixture %q: %v", query, err)
	}
}

// TestProfilePurgeChildHeavyDrain retires one profile with a deliberately
// child-heavy dataset and drains the real bounded worker. Every owned child must
// disappear, retained policy rows and their creator attribution must stay,
// and the completed job must be gone.
func TestProfilePurgeChildHeavyDrain(t *testing.T) {
	s, db, login, kid, preview := profilePurgeFixture(t)
	ctx := context.Background()
	key := purgeTestPersonal(kid.ID)
	viewer := purgeTestViewer(kid.ID)
	primaryKey := purgeTestPersonal("profile")
	_ = s

	// A small library so personal rows have real targets.
	purgeMustExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES(1,'Films','movie','/fixtures/films')`)
	purgeMustExec(t, db, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES
(1001,X'01010101010101010101010101010101',1,1,'One','one','o'),
(1002,X'02020202020202020202020202020202',1,1,'Two','two','t')`)
	purgeMustExec(t, db, `INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES('asset-1','one.mkv',10,1,'matroska','h264','aac',1920,1080,60)`)

	// Personal facts and their child rows.
	purgeMustExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,1001,5,0,0,'pb1'),(?,1002,6,0,0,'pb2')`, key, key)
	purgeMustExec(t, db, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES(?,?,1001,'now','playing'),(?,?,1002,'now','ended')`, key, "1", key, "1")
	purgeMustExec(t, db, `INSERT INTO personal_items(profile_id,item_id,revision) VALUES(?,1001,1),(?,1002,1)`, key, key)
	purgeMustExec(t, db, `INSERT INTO personal_items(profile_id,item_id,revision) VALUES(?,1001,1)`, primaryKey)
	purgeMustExec(t, db, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES
('h1',?,1001,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',1,1,0,1),
('h2',?,1002,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',2,1,0,0)`, key, key)

	// Profile-owned playlists and saved resources, with cascade children.
	purgeMustExec(t, db, `INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES
(101,'tok-101','local',?,?,'Kids list','op1',?, '2026-01-01T00:00:00Z'),
(102,'tok-102','local',?,?,'Kids list 2','op2',?, '2026-01-01T00:00:00Z')`, purgeTestAccount, kid.ID, strings.Repeat("a", 64), purgeTestAccount, kid.ID, strings.Repeat("b", 64))
	purgeMustExec(t, db, `INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,position,order_key) VALUES
(1,'entry-1',101,1001,0,'a'),(2,'entry-2',101,1002,1,'b'),(3,'entry-3',102,1001,0,'a')`)
	purgeMustExec(t, db, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES
('res1','collection',?,'local',?,?,'Saved one','2026-01-01T00:00:00Z'),
('res2','collection',?,'local',?,?,'Saved two','2026-01-01T00:00:00Z')`, key, purgeTestAccount, kid.ID, key, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES
('e1','res1',1001,'a'),('e2','res1',1002,'b'),('e3','res2',1001,'a')`)
	purgeMustExec(t, db, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES
('res1','local',?,?,'viewer'),('res2','local',?,?,'viewer')`, purgeTestAccount, kid.ID, purgeTestAccount, kid.ID)

	// Download metadata, including the newest admitted claims and transfers.
	purgeMustExec(t, db, `INSERT INTO download_requests(id,profile_key,operation_id,request_hash,principal,device_id,target_kind,target_id,quality,episodes,keep_next,selection_fence,created_ms,updated_ms) VALUES
('dr1',?,'op1','h','p','dev','movie',1001,'best','[]',0,'f',1,1),
('dr2',?,'op2','h','p','dev','movie',1002,'best','[]',0,'f',1,1)`, viewer, viewer)
	purgeMustExec(t, db, `INSERT INTO download_request_items(request_id,ordinal,item_id) VALUES('dr1',0,1001),('dr1',1,1002),('dr2',0,1001)`)
	purgeMustExec(t, db, `INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,created_ms,updated_ms) VALUES
('prep1',?,'local',?,?,1001,'1','best','item','ready',1,1),
('prep2',?,'local',?,?,1002,'1','best','item','ready',1,1)`, viewer, purgeTestAccount, kid.ID, viewer, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO download_grants(preparation_id,profile_key,item_id,issued_ms,expires_ms,hash) VALUES('prep1',?,1001,1,2,'h1'),('prep2',?,1002,1,2,'h2')`, viewer, viewer)
	purgeMustExec(t, db, `INSERT INTO download_preparation_authority(preparation_id,principal) VALUES('prep1','p'),('prep2','p')`)
	purgeMustExec(t, db, `INSERT INTO download_action_transfers(profile_key,device_id,operation_id,created_ms) VALUES(?,'dev','op1',1)`, viewer)
	purgeMustExec(t, db, `INSERT INTO download_action_claims(profile_key,device_id,operation_id,preparation_id) VALUES(?,'dev','op1','prep1'),(?,'dev','op1','prep2')`, viewer, viewer)

	// Queue runtime tree.
	purgeMustExec(t, db, `INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES
('q1','device','dev','local',?,?,1,1,1)`, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width) VALUES('snap1','q1','manual','sel','Label',2,1),('snap2','q1','manual','sel','Label 2',1,1)`)
	purgeMustExec(t, db, `INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES('snap1',0,X'00'),('snap1',1,X'01'),('snap2',0,X'02')`)
	purgeMustExec(t, db, `INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES('seg1','q1','a','snap1',0,2),('seg2','q1','b','snap2',0,1)`)

	// One v1 session with a channel child.
	purgeMustExec(t, db, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES
('v1a','dev',?,?,'local','sk',?,'vod','member','playing','{}',1,1,2,1,1)`, purgeTestAccount, kid.ID, strings.Repeat("c", 64))
	purgeMustExec(t, db, `INSERT INTO playback_channel_sessions(session_id,authority,account_id,profile_id,server_id,role,account_epoch,family_id,kind,source_id,channel_id,selection_json,transport_json,created_ms) VALUES
('v1a','local',?,?,'srv','member',1,'fam','live-source','src','chan','{}','{}',1)`, purgeTestAccount, kid.ID)

	// Social group tree.
	purgeMustExec(t, db, `INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,created_at_ms,updated_at_ms) VALUES
('g1','Together','local',?,?,'m1','host-only','ended',1,1)`, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO social_group_members(id,group_id,authority,account_id,profile_id,display_name,role,state,joined_at_ms) VALUES
('m1','g1','local',?,?,'Kids','host','joined',1)`, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO social_group_events(group_id,ordinal,kind,payload,created_at_ms) VALUES('g1',1,'created','{}',1),('g1',2,'ended','{}',1)`)
	purgeMustExec(t, db, `INSERT INTO social_group_queue(group_id,entry_id,position,item_id) VALUES('g1','e1',0,1001)`)
	purgeMustExec(t, db, `INSERT INTO social_group_receipts(group_id,member_id,idempotency_key,digest,response,created_at_ms) VALUES('g1','m1','k1','d','r',1)`)

	// Console rows for the exact profile scope.
	purgeMustExec(t, db, `INSERT INTO console_documents(scope,revision,body,updated_ms) VALUES(?,'1','{}',1)`, "profile:"+viewer)
	purgeMustExec(t, db, `INSERT INTO console_receipts(scope,key,digest,result,expires_ms) VALUES(?,'k','d','r',1)`, "profile:"+viewer)
	purgeMustExec(t, db, `INSERT INTO console_notifications(id,scope,created_ms,code,target_id,expires_ms,dedupe) VALUES('n1',?,1,'c','t',1,'d')`, "profile:"+viewer)
	purgeMustExec(t, db, `INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision) VALUES(?,'personal.changed','personal','library','1')`, "profile:local:"+purgeTestAccount+":"+kid.ID)

	// Retained-by-policy history for the retired profile and for the primary one.
	purgeMustExec(t, db, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms) VALUES
('pb1','local',?,?,'owner','Kids',1001,'1',1,'One',1,1),
('pb2','local',?,?,'owner','Kids',1002,'1',1,'Two',1,1)`, purgeTestAccount, kid.ID, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms) VALUES
('pb9','local',?,?,'owner','Owner',1001,'1',1,'One',1,1)`, purgeTestAccount, "profile")

	// Recording bookkeeping survives unchanged, including creator attribution.
	purgeMustExec(t, db, `INSERT INTO live_source_identities(id) VALUES('src1')`)
	purgeMustExec(t, db, `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms)
VALUES('rec1',?,'local',?,?,'src1','chan','prog','g','{}','{}',1,2,0,1,'completed',1,1)`, key, purgeTestAccount, kid.ID)

	// Prepared media: the actor tuple in principal_json is private, the media row
	// itself is library media keyed by an encoding preset.
	purgeMustExec(t, db, `INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES
('prepjob-ours',1001,'asset-1','1','audio-aac-v3','server','{}','rev',?,'queued','waiting',1,1),
('prepjob-other',1002,'asset-1','1','audio-aac-v3','server','{}','rev',?,'queued','waiting',1,1)`,
		`{"authority":"local","accountId":"account","profileId":"`+kid.ID+`"}`,
		`{"authority":"local","accountId":"account","profileId":"profile"}`)

	purgeMustExec(t, db, `INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES('kept-version',1001,'asset-1','1','audio-aac-v3','server','rev','{}',0,'{}','transformation','digest',10,'{}','published',1,'prepjob-ours')`)

	// One P251 receipt bound to the exact profile incarnation.
	purgeMustExec(t, db, `INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES('local',?,?,0)`, purgeTestAccount, purgeTestAccount)
	var scopeID int64
	if err := db.QueryRow(`SELECT id FROM user_mutation_accounts WHERE authority='local' AND account_id=?`, purgeTestAccount).Scan(&scopeID); err != nil {
		t.Fatal(err)
	}
	purgeMustExec(t, db, `INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,operation,digest,is_stop,issued_ms,status,profile_incarnation,root_id)
VALUES('p251_0000000000001_0123456789abcdef0123456789abcdef',?,?,'fam','progress.stop',?,0,1,'applied',?,'')`, scopeID, kid.ID, strings.Repeat("d", 64), preview.Lifetime)

	beforeRecording := purgeRecordingSnapshot(t, db, "rec1")

	if err := purgeDrainReportForTest(t, s, ctx, login.AccountToken, kid.ID, preview.Revision); err != nil {
		t.Fatal(err)
	}

	count := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}
	for _, check := range []struct {
		query string
		args  []any
	}{
		{`SELECT count(*) FROM progress WHERE profile_id=?`, []any{key}},
		{`SELECT count(*) FROM progress_activity WHERE profile_id=?`, []any{key}},
		{`SELECT count(*) FROM personal_items WHERE profile_id=?`, []any{key}},
		{`SELECT count(*) FROM personal_history WHERE profile_id=?`, []any{key}},
		{`SELECT count(*) FROM catalog_playlists WHERE owner_profile=?`, []any{kid.ID}},
		{`SELECT count(*) FROM catalog_playlist_entries`, nil},
		{`SELECT count(*) FROM saved_resources WHERE owner_profile=?`, []any{kid.ID}},
		{`SELECT count(*) FROM saved_resource_entries`, nil},
		{`SELECT count(*) FROM saved_resource_shares WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM download_requests WHERE profile_key=?`, []any{viewer}},
		{`SELECT count(*) FROM download_request_items`, nil},
		{`SELECT count(*) FROM download_preparations WHERE profile_key=?`, []any{viewer}},
		{`SELECT count(*) FROM download_grants WHERE profile_key=?`, []any{viewer}},
		{`SELECT count(*) FROM download_preparation_authority`, nil},
		{`SELECT count(*) FROM download_action_claims WHERE profile_key=?`, []any{viewer}},
		{`SELECT count(*) FROM download_action_transfers WHERE profile_key=?`, []any{viewer}},
		{`SELECT count(*) FROM queues_v1 WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM queue_v1_snapshots WHERE queue_id='q1'`, nil},
		{`SELECT count(*) FROM queue_v1_chunks`, nil},
		{`SELECT count(*) FROM queue_v1_segments WHERE queue_id='q1'`, nil},
		{`SELECT count(*) FROM playback_v1_sessions WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM playback_channel_sessions WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM social_groups WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM social_group_members WHERE group_id='g1'`, nil},
		{`SELECT count(*) FROM social_group_events WHERE group_id='g1'`, nil},
		{`SELECT count(*) FROM social_group_queue WHERE group_id='g1'`, nil},
		{`SELECT count(*) FROM social_group_receipts WHERE group_id='g1'`, nil},
		{`SELECT count(*) FROM console_documents WHERE scope=?`, []any{"profile:" + viewer}},
		{`SELECT count(*) FROM console_receipts WHERE scope=?`, []any{"profile:" + viewer}},
		{`SELECT count(*) FROM console_notifications WHERE scope=?`, []any{"profile:" + viewer}},
		{`SELECT count(*) FROM api_change_pending WHERE audience=?`, []any{"profile:local:" + purgeTestAccount + ":" + kid.ID}},
		{`SELECT count(*) FROM user_mutation_receipts WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM security_purge_jobs WHERE profile_id=?`, []any{kid.ID}},
	} {
		if got := count(check.query, check.args...); got != 0 {
			t.Errorf("private rows remain: %s = %d", check.query, got)
		}
	}

	var ours, other string
	if err := db.QueryRow(`SELECT principal_json FROM prepared_media_jobs WHERE id='prepjob-ours'`).Scan(&ours); err != nil || ours != "{}" {
		t.Fatalf("private prepared actor remains: %q %v", ours, err)
	}
	if err := db.QueryRow(`SELECT principal_json FROM prepared_media_jobs WHERE id='prepjob-other'`).Scan(&other); err != nil || other == "{}" {
		t.Fatalf("other prepared actor changed: %q %v", other, err)
	}
	if count(`SELECT count(*) FROM prepared_media_versions WHERE id='kept-version' AND profile_id='audio-aac-v3' AND state='published'`) != 1 {
		t.Fatal("library optimized media was erased")
	}

	// Retained policy rows: play history, the primary profile's own state, the
	// recording bookkeeping (unchanged attribution) and the sanitized root.
	if got := count(`SELECT count(*) FROM play_history WHERE profile_id=?`, kid.ID); got != 2 {
		t.Errorf("retained play history for the removed profile = %d, want 2", got)
	}
	if got := count(`SELECT count(*) FROM play_history WHERE profile_id='profile'`); got != 1 {
		t.Errorf("primary profile play history changed = %d", got)
	}
	if got := count(`SELECT count(*) FROM personal_items WHERE profile_id=?`, primaryKey); got != 1 {
		t.Errorf("primary profile personal rows changed = %d", got)
	}
	if !reflect.DeepEqual(beforeRecording, purgeRecordingSnapshot(t, db, "rec1")) {
		t.Fatal("a retained recording row changed during profile erase")
	}
	var recordingProfile, ownerKey string
	if err := db.QueryRow(`SELECT profile_id,owner_key FROM dvr_recordings WHERE id='rec1'`).Scan(&recordingProfile, &ownerKey); err != nil {
		t.Fatalf("recording was deleted: %v", err)
	}
	if recordingProfile != kid.ID || ownerKey != key {
		t.Errorf("retained recording attribution changed: profile=%q owner=%q", recordingProfile, ownerKey)
	}
	var name, art string
	var deleted, position int
	var pin, libraries sql.NullString
	err := db.QueryRow(`SELECT name,deleted,pin_hash,art,allowed_libraries,position FROM direct_profiles WHERE id=?`, kid.ID).Scan(&name, &deleted, &pin, &art, &libraries, &position)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Kids" || deleted != 1 || pin.Valid || libraries.Valid || art != "slate" || position != 0 {
		t.Errorf("retired root not sanitized: name=%q deleted=%d pin=%v art=%q libraries=%v position=%d", name, deleted, pin.Valid, art, libraries.Valid, position)
	}
	// The helper's job row must be gone, and the drain must not have left a second.
	if got := count(`SELECT count(*) FROM security_purge_jobs WHERE account_id=?`, purgeTestAccount); got != 0 {
		t.Errorf("purge job rows remain = %d", got)
	}
	// Receipt capacity must be released by the receipt delete.
	var ordinary int64
	if err := db.QueryRow(`SELECT ordinary_count FROM user_mutation_accounts WHERE id=?`, scopeID).Scan(&ordinary); err != nil {
		t.Fatal(err)
	}
	if ordinary != 0 {
		t.Errorf("receipt capacity not released: %d", ordinary)
	}
}

// TestProfilePurgeManyParentsConverge proves the bounded hop windows converge
// with more parents than one probe returns (18 playlists, 10 session families),
// including scope-derived console rows, route failures and the P251 root.
func TestProfilePurgeManyParentsConverge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, login, kid, preview := profilePurgeFixture(t)
	ctx := context.Background()
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
	// More than one hop window of playlist parents, each with a child row.
	for i := 0; i < 18; i++ {
		id := 200 + i
		token := "many-token-" + strconv.Itoa(i)
		must(`INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(?,?,'local',?,?,'List',?,?,'2026-01-01T00:00:00Z')`,
			id, token, purgeTestAccount, kid.ID, "many-op-"+strconv.Itoa(i), strings.Repeat("a", 64))
		must(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) VALUES(?,?,NULL,0,'a')`, "many-entry-"+strconv.Itoa(i), id)
	}
	// More than one console token window of families, each with session-scope
	// console rows and a route-failure child.
	for i := 0; i < 18; i++ {
		family := "many-family-" + strconv.Itoa(i)
		must(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,'srv',?,?,'local','member',1,'2099-01-01T00:00:00Z',18)`, family, purgeTestAccount, kid.ID)
		for generation := 1; generation <= 18; generation++ {
			token := "many-session-" + strconv.Itoa(i) + "-" + strconv.Itoa(generation)
			must(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,?,'2099-01-01T00:00:00Z')`, token, family, generation)
			must(`INSERT INTO console_documents(scope,revision,body,updated_ms) VALUES(?,'1','{}',1)`, "session:"+token)
			for _, key := range []string{"k1", "k2"} {
				must(`INSERT INTO console_receipts(scope,key,digest,result,expires_ms) VALUES(?,?,'d','r',1)`, "session:"+token, key)
			}
			must(`INSERT INTO playback_route_failures(session_hash,asset_id,engine,route,code,detail,reported_at_ms) VALUES(?,'a','mpv','original','E','d',1)`, token)
		}
	}

	// One v1 session with a live playback reference child.
	must(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('many-v1','dev',?,?,'local','sk',?,'vod','member','playing','{}',1,1,2,1,1)`,
		purgeTestAccount, kid.ID, strings.Repeat("c", 64))
	must(`INSERT INTO lc_playback_refs(playback_id,channel_id,generation_id,occurrence_id,end_ms,lease_until_ms) VALUES('many-v1','chan','g','o',1,2)`)

	// Library rows so the legacy session/disc chain has real targets.
	must(`INSERT INTO libraries(id,name,kind,root) VALUES(1,'Films','movie','/fixtures/films')`)
	must(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES
(1001,X'01010101010101010101010101010101',1,1,'One','one','o'),
(1002,X'02020202020202020202020202020202',1,1,'Two','two','t')`)
	must(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES('asset-1','one.mkv',10,1,'matroska','h264','aac',1920,1080,60)`)
	// More than one hop window of legacy sessions; the deepest nested chain
	// (session -> disc snapshot -> title -> clip) hangs off the last one.
	for i := 1; i <= 28; i++ {
		id := "many-session-" + strconv.Itoa(i)
		must(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration)
VALUES(?,?,?,?,1001,'asset-1',1,'playing',?,?,'2099-01-01T00:00:00Z',?,60)`,
			id, "many-session-0-1", purgeTestAccount, kid.ID, "grant-"+id, id, id)
	}
	for i := 1; i <= 18; i++ {
		q := "many-queue-" + strconv.Itoa(i)
		must(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES(?,'device','dev','local',?,?,1,1,1)`, q, purgeTestAccount, kid.ID)
		snap := "many-snap-" + strconv.Itoa(i)
		must(`INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width) VALUES(?,?,'manual','sel','Label',1,1)`, snap, q)
		must(`INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES(?,0,X'00')`, snap)
		must(`INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES(?,?,'a',?,0,1)`, "many-seg-"+strconv.Itoa(i), q, snap)
	}

	// Retained title state in both current representations: the raw profile id and
	// the canonical personal owner key, with state_id children.
	rawState := 0
	must(`INSERT INTO retained_title_state(profile_id,kind,title,retained_ms) VALUES(?,1,'Raw owner',1)`, kid.ID)
	must(`INSERT INTO retained_title_state(profile_id,kind,title,retained_ms) VALUES(?,1,'Canonical owner',1)`, purgeTestPersonal(kid.ID))
	if err := db.QueryRow(`SELECT id FROM retained_title_state WHERE profile_id=?`, kid.ID).Scan(&rawState); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO retained_title_identities(identity,profile_id,state_id) VALUES('ident-raw',?,?)`, kid.ID, rawState)
	must(`INSERT INTO retained_reading_progress(state_id,source_id,source_revision,format,progression,revision,observed_at) VALUES(?, 'src','r','epub',0.5,1,1)`, rawState)

	// One P251 root with the exact retired incarnation and a linked receipt.
	root := "rootmany0123456789abcd"
	must(`INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,'fam',1,'active')`,
		root, strings.Repeat("e", 32), purgeTestAccount, kid.ID, strings.Repeat("a", 32), preview.Lifetime)
	var scopeID2 int64
	must(`INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES('local',?,?,0)`, purgeTestAccount, purgeTestAccount)
	if err := db.QueryRow(`SELECT id FROM user_mutation_accounts WHERE authority='local' AND account_id=?`, purgeTestAccount).Scan(&scopeID2); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,operation,digest,is_stop,issued_ms,status,profile_incarnation,root_id) VALUES('p251_0000000000002_0123456789abcdef0123456789abcdef',?,?,'fam','progress.stop',?,0,2,'applied',?,?)`,
		scopeID2, kid.ID, strings.Repeat("d", 64), preview.Lifetime, root)

	if err := purgeDrainReportForTest(t, s, ctx, login.AccountToken, kid.ID, preview.Revision); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	for _, check := range []struct {
		q    string
		args []any
	}{
		{`SELECT count(*) FROM catalog_playlists WHERE owner_profile=?`, []any{kid.ID}},
		{`SELECT count(*) FROM catalog_playlist_entries`, nil},
		{`SELECT count(*) FROM authorization_session_families WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM authorization_family_tokens WHERE family_id LIKE 'many-family-%'`, nil},
		{`SELECT count(*) FROM console_documents WHERE scope LIKE 'session:many-session-%'`, nil},
		{`SELECT count(*) FROM console_receipts WHERE scope LIKE 'session:many-session-%'`, nil},
		{`SELECT count(*) FROM playback_route_failures WHERE session_hash LIKE 'many-session-%'`, nil},
		{`SELECT count(*) FROM lc_playback_refs WHERE playback_id='many-v1'`, nil},
		{`SELECT count(*) FROM user_mutation_roots WHERE id=?`, []any{root}},
		{`SELECT count(*) FROM user_mutation_receipts WHERE root_id=?`, []any{root}},
		{`SELECT count(*) FROM playback_sessions WHERE profile_id=?`, []any{kid.ID}},
		{`SELECT count(*) FROM playback_disc_snapshots`, nil},
		{`SELECT count(*) FROM playback_disc_snapshot_titles`, nil},
		{`SELECT count(*) FROM queue_v1_chunks`, nil},
		{`SELECT count(*) FROM queue_v1_segments WHERE queue_id LIKE 'many-queue-%'`, nil},
		{`SELECT count(*) FROM retained_title_state WHERE profile_id IN(?,?)`, []any{kid.ID, purgeTestPersonal(kid.ID)}},
		{`SELECT count(*) FROM retained_title_state_counts WHERE profile_id IN(?,?)`, []any{kid.ID, purgeTestPersonal(kid.ID)}},
		{`SELECT count(*) FROM retained_title_identities`, nil},
		{`SELECT count(*) FROM retained_reading_progress`, nil},
		{`SELECT count(*) FROM prepared_media_jobs WHERE json_extract(principal_json,'$.profileId')=?`, []any{kid.ID}},
		{`SELECT count(*) FROM prepared_media_jobs WHERE id='prepjob-ours' AND principal_json<>'{}'`, nil},
	} {
		if got := count(check.q, check.args...); got != 0 {
			t.Errorf("residue: %s = %d", check.q, got)
		}
	}
}

// TestProfilePurgeParentGuardBlocksUntilChildDrained proves a parent unit refuses
// to remove the row while one owned child remains, instead of relying on a
// cascade or on the fixture being small.
func TestProfilePurgeParentGuardBlocksUntilChildDrained(t *testing.T) {
	s, db, _, kid, _ := profilePurgeFixture(t)
	ctx := context.Background()
	job := purgeTestJob(purgeTestAccount, kid.ID)
	key := purgeTestPersonal(kid.ID)
	if _, err := db.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES('guard-res','collection',?,'local',?,?,'Saved','2026-01-01T00:00:00Z')`, key, purgeTestAccount, kid.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES('guard-entry','guard-res',NULL,'a')`); err != nil {
		t.Fatal(err)
	}
	plan := profilePurgeUnits(s, job)
	index := -1
	for i := range plan {
		if plan[i].Name == "saved-resources" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("parent unit missing")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := plan[index].Run(ctx, tx, nil)
	if err != nil {
		t.Fatalf("guarded parent run: %v", err)
	}
	if out.Changed || out.Drained || !out.Rewind {
		t.Fatalf("parent with a child must rewind, got %+v", out)
	}
	if _, err = tx.Exec(`DELETE FROM saved_resource_entries WHERE id='guard-entry'`); err != nil {
		t.Fatal(err)
	}
	out, err = plan[index].Run(ctx, tx, nil)
	if err != nil {
		t.Fatalf("unguarded parent run: %v", err)
	}
	if !out.Changed || out.Rewind {
		t.Fatalf("parent without children must delete, got %+v", out)
	}
}

// TestProfilePurgeCursorDigest binds a stored cursor to this exact compiled plan
// and builder output.
func TestProfilePurgeCursorDigest(t *testing.T) {
	if len(purgePlanDigest) != 64 || purgePlanVersion == "" {
		t.Fatalf("plan digest/version missing: %q %q", purgePlanDigest, purgePlanVersion)
	}
	if raw, err := purgeDecodeStoredCursor(""); err != nil || raw != "" {
		t.Fatalf("initial blank cursor must be accepted: %q %v", raw, err)
	}
	inner, err := purgeEncodeStoredCursor(phasedpurge.Cursor{Unit: 1, Verify: 0})
	if err != nil {
		t.Fatal(err)
	}
	back, err := purgeDecodeStoredCursor(inner)
	if err != nil || back == "" {
		t.Fatalf("stored cursor did not round-trip: %q %v", back, err)
	}
	if _, err = purgeDecodeStoredCursor(`{"u":1}`); err == nil {
		t.Fatal("a cursor without the plan digest must be rejected")
	}
	tampered := `{"d":"` + strings.Repeat("0", 64) + `","c":"{\"u\":1}"}`
	if _, err = purgeDecodeStoredCursor(tampered); err == nil {
		t.Fatal("a cursor from another plan must be rejected")
	}
}

// TestProfilePurgeScrubUnitsLeaveThePendingPredicate runs each owner-key scrub
// twice: the second turn must find nothing, proving the scrub removes its own row
// from the pending predicate instead of rewriting it forever.
func TestProfilePurgeScrubUnitsLeaveThePendingPredicate(t *testing.T) {
	s, db, _, kid, _ := profilePurgeFixture(t)
	key := purgeTestPersonal(kid.ID)
	purgeMustExec(t, db, `INSERT INTO catalog_personal_sort_entity_jobs(entity_id,profile_cursor) VALUES(1,?)`, key)
	job := purgeTestJob(purgeTestAccount, kid.ID)
	var unit phasedpurge.Unit
	for _, u := range profilePurgeUnits(s, job) {
		if u.Name == "catalog-sort-entity-cursor" {
			unit = u
		}
	}
	for _, want := range []bool{true, false} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		out, err := unit.Run(t.Context(), tx, nil)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if out.Changed != want {
			t.Fatalf("cursor rewrite repeated: %+v", out)
		}
	}
	var cursor string
	if err := db.QueryRow(`SELECT profile_cursor FROM catalog_personal_sort_entity_jobs WHERE entity_id=1`).Scan(&cursor); err != nil || cursor != "" {
		t.Fatalf("cursor not scrubbed: %q %v", cursor, err)
	}
}

func TestProfilePurgeAvatarCustodyUniqueSharedAndRestart(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			s, db, login, kid, preview := profilePurgeFixture(t)
			digest := strings.Repeat("d", 64)
			purgeMustExec(t, db, `INSERT INTO profile_avatar_objects(digest,rendition,bytes,image) VALUES(?,1,3,X'010203')`, digest)
			purgeMustExec(t, db, `INSERT INTO profile_avatars(profile_id,digest,source_mime,version,updated_at) VALUES(?,?,'image/png',1,'2026-01-01T00:00:00Z')`, kid.ID, digest)
			if shared {
				purgeMustExec(t, db, `INSERT INTO profile_avatars(profile_id,digest,source_mime,version,updated_at) VALUES('profile',?,'image/png',1,'2026-01-01T00:00:00Z')`, digest)
			}
			if _, err := s.RetireDirectProfile(t.Context(), login.AccountToken, kid.ID, ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}); err != nil {
				t.Fatal(err)
			}
			job := purgeTestJob(purgeTestAccount, kid.ID)
			plan := profilePurgeUnits(s, job)
			var child, parent phasedpurge.Unit
			for _, u := range plan {
				if u.Name == "avatar-objects" {
					child = u
				}
				if u.Name == "profile-avatars" {
					parent = u
				}
			}
			if !shared {
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				out, err := parent.Run(t.Context(), tx, nil)
				tx.Rollback()
				if err != nil || !out.Rewind || out.Changed {
					t.Fatalf("lost avatar custody: %+v %v", out, err)
				}
				tx, err = db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				out, err = child.Run(t.Context(), tx, nil)
				if err != nil || !out.Changed {
					tx.Rollback()
					t.Fatalf("image not drained: %+v %v", out, err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				// Crash boundary: bytes are committed gone, mapper is still durable. The
				// next owner turn reconstructs all progress from the job, without a cursor
				// held by this test or an orphan collector.
				var links int
				if err = db.QueryRow(`SELECT count(*) FROM profile_avatars WHERE profile_id=?`, kid.ID).Scan(&links); err != nil || links != 1 {
					t.Fatalf("mapper disappeared before restart: %d %v", links, err)
				}
			}
			for n := 0; n < 2000; n++ {
				worked, err := s.PurgeProfileStep(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !worked {
					break
				}
				if n == 1999 {
					t.Fatal("avatar purge did not converge")
				}
			}
			var blobs, links int
			if err := db.QueryRow(`SELECT count(*) FROM profile_avatar_objects WHERE digest=?`, digest).Scan(&blobs); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM profile_avatars WHERE profile_id=?`, kid.ID).Scan(&links); err != nil {
				t.Fatal(err)
			}
			want := 0
			if shared {
				want = 1
			}
			if blobs != want || links != 0 {
				t.Fatalf("avatar privacy: blobs=%d want%d links=%d", blobs, want, links)
			}
		})
	}
}

func purgeRecordingSnapshot(t *testing.T, db *sql.DB, id string) []any {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM dvr_recordings WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("recording missing")
	}
	values := make([]any, len(cols))
	ptr := make([]any, len(cols))
	for i := range values {
		ptr[i] = &values[i]
	}
	if err = rows.Scan(ptr...); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestProfilePurgeRecordingRevisionLivesExactlyAsLongAsRecording(t *testing.T) {
	s, db, _, kid, _ := profilePurgeFixture(t)
	ctx := context.Background()
	key := purgeTestPersonal(kid.ID)
	purgeMustExec(t, db, `INSERT INTO live_source_identities(id) VALUES('kept-source')`)
	purgeMustExec(t, db, `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms) VALUES('kept-recording',?,'local',?,?,'kept-source','c','p','g','{}','{}',1,2,0,1,'completed',1,1)`, key, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO dvr_owner_revisions VALUES(?,77)`, key)
	var unit phasedpurge.Unit
	for _, candidate := range profilePurgeUnits(s, purgeTestJob(purgeTestAccount, kid.ID)) {
		if candidate.Name == "dvr-owner-revisions" {
			unit = candidate
		}
	}
	if unit.Name == "" {
		t.Fatal("missing revision unit")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := unit.Run(ctx, tx, nil)
	if err != nil || out.Changed || !out.Drained {
		t.Fatalf("retained revision: %+v %v", out, err)
	}
	var revision int64
	if err = tx.QueryRow(`SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, key).Scan(&revision); err != nil || revision != 77 {
		t.Fatalf("revision lost: %d %v", revision, err)
	}
	// Any recording row, including a state='deleted' media tombstone, retains the
	// counter. Physical removal of the last row makes the counter erasable.
	if _, err = tx.Exec(`DELETE FROM dvr_recordings WHERE id='kept-recording'`); err != nil {
		t.Fatal(err)
	}
	out, err = unit.Run(ctx, tx, nil)
	if err != nil || !out.Changed {
		t.Fatalf("last recording gone: %+v %v", out, err)
	}
	var n int
	if err = tx.QueryRow(`SELECT count(*) FROM dvr_owner_revisions WHERE owner_key=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orphan counter: %d %v", n, err)
	}
}

// Recipient authority and resource ownership are independent erasure edges.
// A received local grant on a Hosted resource is private to the local recipient;
// every grant on an owned resource must drain before its guarded parent can go.
func TestProfilePurgeSharesDrainOwnedAndReceivedEdgesIndependently(t *testing.T) {
	s, db, login, kid, preview := profilePurgeFixture(t)
	key := purgeTestPersonal(kid.ID)
	for i := 0; i < 17; i++ {
		id := fmt.Sprintf("owned-share-%02d", i)
		purgeMustExec(t, db, `INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(?,'local',?,?,'Owned',?,'hash','2026-01-01T00:00:00Z')`, id, purgeTestAccount, kid.ID, id)
		purgeMustExec(t, db, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'collection',?,'local',?,?,'Owned','2026-01-01T00:00:00Z')`, id, key, purgeTestAccount, kid.ID)
		recipients := 1
		if i == 0 {
			recipients = 35
		} // A parent cascade would exceed the turn budget.
		for n := 0; n < recipients; n++ {
			recipient := fmt.Sprintf("someone-else-%02d", n)
			purgeMustExec(t, db, `INSERT INTO playlist_shares(playlist_id,authority,account_id,profile_id,role) VALUES(?,'hosted','elsewhere',?,'editor')`, id, recipient)
			purgeMustExec(t, db, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES(?,'hosted','elsewhere',?,'editor')`, id, recipient)
		}
	}
	hostedKey := persistence.PersonalOwnerKey("hosted", purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('foreign-shared','hosted',?,?,'Kept','create','hash','2026-01-01T00:00:00Z')`, purgeTestAccount, kid.ID)
	purgeMustExec(t, db, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES('foreign-shared','collection',?,'hosted',?,?,'Kept','2026-01-01T00:00:00Z')`, hostedKey, purgeTestAccount, kid.ID)
	for _, table := range []string{"playlist_shares", "saved_resource_shares"} {
		col := "playlist_id"
		if table == "saved_resource_shares" {
			col = "resource_id"
		}
		for _, who := range []struct{ authority, profile string }{{"local", kid.ID}, {"hosted", kid.ID}, {"local", "profile"}} {
			purgeMustExec(t, db, "INSERT INTO "+table+"("+col+",authority,account_id,profile_id,role) VALUES('foreign-shared',?,?,?,'viewer')", who.authority, purgeTestAccount, who.profile)
		}
	}
	if err := retireProfileAndDrainForTest(t, s, t.Context(), login.AccountToken, kid.ID, preview.Revision, true); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"catalog_playlists", "saved_resources"} {
		col := "token"
		if table == "saved_resources" {
			col = "id"
		}
		var owned, foreign int
		if err := db.QueryRow("SELECT count(*) FROM " + table + " WHERE " + col + " LIKE 'owned-share-%'").Scan(&owned); err != nil || owned != 0 {
			t.Fatalf("owned %s parents/foreign recipients remain: %d %v", table, owned, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM " + table + " WHERE " + col + "='foreign-shared'").Scan(&foreign); err != nil || foreign != 1 {
			t.Fatalf("foreign %s resource lost: %d %v", table, foreign, err)
		}
	}
	for _, table := range []string{"playlist_shares", "saved_resource_shares"} {
		var ours, hosted, other int
		if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE authority='local' AND account_id=? AND profile_id=?", purgeTestAccount, kid.ID).Scan(&ours); err != nil || ours != 0 {
			t.Fatalf("received %s grant retained: %d %v", table, ours, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE authority='hosted' AND account_id=? AND profile_id=?", purgeTestAccount, kid.ID).Scan(&hosted); err != nil || hosted != 1 {
			t.Fatalf("Hosted same-id %s grant lost: %d %v", table, hosted, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM " + table + " WHERE authority='local' AND profile_id='profile'").Scan(&other); err != nil || other != 1 {
			t.Fatalf("other local %s recipient lost: %d %v", table, other, err)
		}
	}
}
