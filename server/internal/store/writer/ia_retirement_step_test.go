package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Canonical sqliteconn/bootstrap with the complete adopted F018 schema. The
// security producers are reused from the censused IA05 fixture, never legacy
// persistence.Open. No CREATE/ALTER overlay or substitute parent is installed.
func iaRetirementUnitFixture(t *testing.T, folder bool) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRetirementUnitScope, iaRetirementMediaVictim) {
	t.Helper()
	w, keeper, p := iaEntrySecurityFixture(t)
	if schema.SHA256 != "484242fbb520ace5b4bfc1db015e2f3530dc01cfa5ff08bfd22a5244812905ca" {
		t.Fatal("F018 candidate source changed; report binding drift, do not record a new baseline", schema.SHA256)
	}
	for _, q := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0)`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('retire-library','Retire','book','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root,retired) VALUES(7,'retire-library','/media',1)`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed,enabled) VALUES('retire-source','retire-library','/media','/media','root-v1','root:1',2,1,0)`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(10,zeroblob(16),7,20)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired) VALUES(10,zeroblob(16),7,20,'Retained','retained','r','retained','r',1,zeroblob(16),zeroblob(16),1)`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(41,'retire-asset',7,'/media/file',42,11,'','','',0,0,0)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('retire-object','retire-source','retire-asset','root-v1','file','revision','{}',42,11)`,
		`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id,mp_retired_generation,mp_exited_generation,mp_retire_request_id,mp_exit_supervisor_incarnation,mp_exited_ms,mp_exit_request_id,mp_revision,mp_cleanup_complete,mp_exit_digest,mp_grant_retired,mp_delivery_ready) VALUES('retire-session','hash','issuer','issuer-profile',10,'retire-asset',2,'stopped','retire-grant','opaque','2099-01-01T00:00:00Z','request',1,'execution-v2',2,2,'stop',zeroblob(16),1,'stop',3,1,printf('%064d',0),1,0)`,
		`INSERT INTO personal_items(id,profile_id,item_public_id,item_id,library_id,revision,reading_revision,retained_kind,retained_title,retained_at_ms) VALUES(71,'issuer-profile',zeroblob(16),10,7,1,2,1,'Retained',1)`,
		`INSERT INTO personal_reading_resumes(personal_item_id,asset_id,source_token,source_revision,format,page_index,progression,revision,observed_ms) VALUES(71,41,'retire-asset',printf('%064d',0),'pdf',7,0.5,1,1)`,
	} {
		wc008NativeExec(t, w, q)
	}
	s := iaRetirementUnitScope{kind: "library", target: "retire-library", libraryID: 7, libraryKey: "retire-library", resolvedRoot: "/media", revision: 1, leaseOwner: 1, leaseGeneration: 1, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), phase: 2}
	if folder {
		s.kind = "folder"
		s.target = "retire-source"
		s.sourceIncarnation = "root-v1"
		s.sourceGeneration = 2
		s.rootIdentity = "root:1"
	}
	wc008NativeExec(t, w, `INSERT INTO catalog_removal_progress(kind,target,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms) VALUES(?,?,2,1,1,1,?)`, s.kind, s.target, s.leaseExpiresMS)
	r, err := stmttest.New(t, iaRetirementUnitDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{r, keeper}, p, s, iaRetirementMediaVictim{assetID: 41, assetToken: "retire-asset", sessionID: "retire-session", executionID: "execution-v2", generation: 2, revision: 3}
}
func iaRetirementUnitTurn(t *testing.T, w *executor, r *iaEntryRegistry) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireStep})
}
func iaRetirementUnitCount(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestIARetirementScopeLeaseAndAuthority(t *testing.T) {
	for _, folder := range []bool{false, true} {
		for _, mode := range []string{"current", "lease", "cursor", "root", "enabled", "wrong_library", "wrong_owner", "underfunded"} {
			t.Run(map[bool]string{true: "folder/", false: "library/"}[folder]+mode, func(t *testing.T) {
				w, r, p, s, _ := iaRetirementUnitFixture(t, folder)
				switch mode {
				case "lease":
					wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET job_lease_generation=2`)
				case "cursor":
					wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET object_cursor='changed'`)
				case "root":
					if folder {
						wc008NativeExec(t, w, `UPDATE library_sources SET root='/changed' WHERE id='retire-source'`)
					} else {
						wc008NativeExec(t, w, `UPDATE catalog_libraries SET root='/changed' WHERE id=7`)
					}
				case "enabled":
					if folder {
						wc008NativeExec(t, w, `UPDATE library_sources SET enabled=1 WHERE id='retire-source'`)
					} else {
						wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=0 WHERE id=7`)
					}
				case "wrong_library":
					s.libraryID = 99
				case "wrong_owner":
					p.witness.issuer.memberIncarnation = strings.Repeat("x", 32)
				}
				turn, b := iaRetirementUnitTurn(t, w, r)
				if mode == "underfunded" {
					b.memory = 1
				}
				out, err := enterIARetirementUnit(t.Context(), turn, s, p, t.Context(), b)
				if mode == "current" {
					if err != nil || out == nil {
						t.Fatal(out, err)
					}
				} else if err == nil || out != nil {
					t.Fatal("denial hidden", out, err)
				}
				turn.transaction.rollback(err)
			})
		}
	}
}
func TestIARetirementCataloguePersonalHoldPreservesResume(t *testing.T) {
	w, r, p, s, _ := iaRetirementUnitFixture(t, false)
	turn, b := iaRetirementUnitTurn(t, w, r)
	e, err := enterIARetirementUnit(t.Context(), turn, s, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = requireIACataloguePersonalReferencesAbsent(t.Context(), turn, e, 10); !errors.Is(err, ErrConflict) {
		t.Fatal("retained personal parent was not a hold", err)
	}
	turn.transaction.rollback(err)
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM personal_reading_resumes`) != 1 || iaRetirementUnitCount(t, w, `SELECT retained_at_ms FROM personal_items WHERE id=71`) != 1 {
		t.Fatal("retirement purged retained state")
	}
	// F's absence of an asset FK deliberately preserves stale source resumes.
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(42,'unreferenced',7,'/media/other',1,1,'','','',0,0,0)`)
	wc008NativeExec(t, w, `INSERT INTO personal_reading_resumes(personal_item_id,asset_id,source_token,source_revision,format,page_index,progression,revision,observed_ms) VALUES(71,42,'unreferenced',printf('%064d',0),'pdf',3,0.25,1,1)`)
	wc008NativeExec(t, w, `DELETE FROM catalog_assets WHERE id=42`)
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM personal_reading_resumes WHERE asset_id=42`) != 1 {
		t.Fatal("asset erasure destroyed stale resume")
	}
}
func TestIARetirementIncomingChildInventory(t *testing.T) {
	w, _, _, _, _ := iaRetirementUnitFixture(t, false)
	rows, err := w.db.Query(`SELECT m.name,f."from" FROM sqlite_schema AS m JOIN pragma_foreign_key_list(m.name) AS f WHERE m.type='table' AND f."table"='playback_sessions' AND f.seq=0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var table, column string
		if err = rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		got[table] = column
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, c := range mpSessionCustodyPoints {
		want[c.table] = c.key
	}
	for table, column := range got {
		if want[table] != column {
			t.Fatal("incoming physical FK requires an explicit MP hold", got, want)
		}
	}
}

func iaRetirementRunUnit(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, s iaRetirementUnitScope, v iaRetirementMediaVictim) (iaRetirementUnitResult, error) {
	t.Helper()
	turn, b := iaRetirementUnitTurn(t, w, r)
	entry, err := enterIARetirementUnit(t.Context(), turn, s, p, t.Context(), b)
	if err != nil {
		turn.transaction.rollback(err)
		return iaRetirementUnitResult{}, err
	}
	out, err := retireIAMediaParentUnit(t.Context(), turn, entry, v)
	if err != nil {
		turn.transaction.rollback(err)
		return out, err
	}
	if _, err = turn.transaction.commit(); err != nil {
		return iaRetirementUnitResult{}, err
	}
	return out, nil
}
func iaRetirementReleasedPin(t *testing.T, w *executor, generation int64, execution, request string) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO mp_external_guide_pins(media_session_id,occurrence_generation,execution_id,source_request_id,captured_source_id,captured_generation_id,captured_channel_id,captured_programme_id,captured_ms,released_ms) VALUES('retire-session',?,?,?,'guide-source','guide-generation','guide-channel',NULL,1,2)`, generation, execution, request)
}
func TestIARetirementReleasedOccurrencesAndSessionAreSeparateUnits(t *testing.T) {
	w, r, p, s, v := iaRetirementUnitFixture(t, false)
	iaRetirementReleasedPin(t, w, 1, "execution-old-a", "request-a")
	iaRetirementReleasedPin(t, w, 1, "execution-old-b", "request-b")
	for n := int64(2); n > 0; n-- {
		before := iaRetirementUnitCount(t, w, `SELECT sum(variable_bytes) FROM mp_external_guide_pins`)
		out, err := iaRetirementRunUnit(t, w, r, p, s, v)
		if err != nil || out.removedRows != 1 || out.removedGuidePins != 1 || out.removedSessions != 0 || out.nextOwnerRevision != s.revision+1 || !out.assetHasMore {
			t.Fatal(out, err)
		}
		after := int64(0)
		if n > 1 {
			after = iaRetirementUnitCount(t, w, `SELECT sum(variable_bytes) FROM mp_external_guide_pins`)
		}
		if before-after != out.variableBytes || iaRetirementUnitCount(t, w, `SELECT count(*) FROM mp_external_guide_pins`) != n-1 || iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 {
			t.Fatal("one exact occurrence/accounting", out)
		}
		s.revision = out.nextOwnerRevision
		s.walkRemoved = 1
	}
	out, err := iaRetirementRunUnit(t, w, r, p, s, v)
	if err != nil || out.removedRows != 1 || out.removedSessions != 1 || out.removedGuidePins != 0 || out.assetHasMore || out.variableBytes <= 0 {
		t.Fatal("drained parent", out, err)
	}
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 0 || iaRetirementUnitCount(t, w, `SELECT count(*) FROM personal_reading_resumes`) != 1 || iaRetirementUnitCount(t, w, `SELECT retained_at_ms FROM personal_items WHERE id=71`) != 1 {
		t.Fatal("parent erased personal state")
	}
}
func TestIARetirementChildlessExitAndCustodyHolds(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE playback_sessions SET mp_cleanup_complete=0,mp_exited_generation=0,mp_exit_request_id='',mp_exit_supervisor_incarnation=NULL,mp_exited_ms=0 WHERE id='retire-session'`,
		`UPDATE playback_sessions SET mp_cleanup_complete=0 WHERE id='retire-session'`,
		`UPDATE playback_sessions SET mp_grant_retired=0 WHERE id='retire-session'`,
		`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('retire-session','retire-asset',42,11)`,
		`INSERT INTO mp_media_read_pins(pin_id,media_session_id,execution_id,occurrence_generation,source_version,from_byte,size) VALUES('held-read','retire-session','execution-v2',2,'v',0,1)`,
	} {
		t.Run(mutation, func(t *testing.T) {
			w, r, p, s, v := iaRetirementUnitFixture(t, false)
			wc008NativeExec(t, w, mutation)
			out, err := iaRetirementRunUnit(t, w, r, p, s, v)
			if err == nil || !reflect.DeepEqual(out, iaRetirementUnitResult{}) {
				t.Fatal("exit/custody bypass", out, err)
			}
			if iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 || iaRetirementUnitCount(t, w, `SELECT job_revision FROM catalog_removal_progress`) != 1 {
				t.Fatal("held parent/prefix removed")
			}
		})
	}
}
func TestIARetirementSourceProvenanceIsNotInferred(t *testing.T) {
	w, r, p, s, v := iaRetirementUnitFixture(t, true)
	out, err := iaRetirementRunUnit(t, w, r, p, s, v)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, iaRetirementUnitResult{}) {
		t.Fatal("source join became session provenance", out, err)
	}
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 || iaRetirementUnitCount(t, w, `SELECT count(*) FROM personal_reading_resumes`) != 1 {
		t.Fatal("held source erased state")
	}
}
func TestIARetirementRollbackAfterEachMutationPrefix(t *testing.T) {
	for _, point := range []string{"pin", "parent", "owner"} {
		t.Run(point, func(t *testing.T) {
			w, r, p, s, v := iaRetirementUnitFixture(t, false)
			switch point {
			case "pin":
				iaRetirementReleasedPin(t, w, 1, "old", "request")
				wc008NativeExec(t, w, `CREATE TRIGGER ia_retire_deny AFTER DELETE ON mp_external_guide_pins BEGIN UPDATE catalog_libraries SET retired=0 WHERE id=7; END`)
			case "parent":
				wc008NativeExec(t, w, `CREATE TRIGGER ia_retire_deny AFTER DELETE ON playback_sessions BEGIN UPDATE catalog_libraries SET retired=0 WHERE id=7; END`)
			case "owner":
				wc008NativeExec(t, w, `CREATE TRIGGER ia_retire_deny AFTER UPDATE ON catalog_removal_progress BEGIN UPDATE catalog_libraries SET retired=0 WHERE id=7; END`)
			}
			out, err := iaRetirementRunUnit(t, w, r, p, s, v)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRetirementUnitResult{}) {
				t.Fatal("late denial not redacted", out, err)
			}
			if iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 || iaRetirementUnitCount(t, w, `SELECT job_revision FROM catalog_removal_progress`) != 1 || iaRetirementUnitCount(t, w, `SELECT count(*) FROM personal_reading_resumes`) != 1 {
				t.Fatal("rollback left an erasure prefix")
			}
			if point == "pin" && iaRetirementUnitCount(t, w, `SELECT count(*) FROM mp_external_guide_pins`) != 1 {
				t.Fatal("released pin prefix survived rollback")
			}
		})
	}
}

func TestIARetirementUnreleasedPinIsNeverSkippedOrDeleted(t *testing.T) {
	w, r, p, s, v := iaRetirementUnitFixture(t, false)
	wc008NativeExec(t, w, `INSERT INTO live_source_identities(id) VALUES('guide-source')`)
	wc008NativeExec(t, w, `INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('guide-generation','guide-source','Guide',1,1,0,'now')`)
	wc008NativeExec(t, w, `INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('guide-generation','guide-channel','key','Guide','1','',1,X'01','','')`)
	wc008NativeExec(t, w, `INSERT INTO mp_external_guide_pins(media_session_id,occurrence_generation,execution_id,source_request_id,captured_source_id,captured_generation_id,captured_channel_id,captured_ms,current_occurrence_generation,current_execution_id,current_source_id,current_generation_id,current_channel_id) VALUES('retire-session',2,'execution-v2','request-current','guide-source','guide-generation','guide-channel',1,2,'execution-v2','guide-source','guide-generation','guide-channel')`)
	// A released row ordered AFTER the held candidate is not permission to skip it.
	iaRetirementReleasedPin(t, w, 3, "execution-later", "later-request")
	out, err := iaRetirementRunUnit(t, w, r, p, s, v)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRetirementUnitResult{}) {
		t.Fatal("unreleased candidate skipped", out, err)
	}
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM mp_external_guide_pins`) != 2 || iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 || iaRetirementUnitCount(t, w, `SELECT job_revision FROM catalog_removal_progress`) != 1 {
		t.Fatal("held occurrence/parent removed")
	}
}

func TestIARetirementSessionBytesAreActualAndBounded(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "multibyte", true: "capacity_hold"}[large], func(t *testing.T) {
			w, r, p, s, v := iaRetirementUnitFixture(t, false)
			token := "opaque α"
			if large {
				token = strings.Repeat("x", 65537)
			}
			wc008NativeExec(t, w, `UPDATE playback_sessions SET grant_token=? WHERE id='retire-session'`, token)
			rows, err := w.db.Query(`PRAGMA table_xinfo(playback_sessions)`)
			if err != nil {
				t.Fatal(err)
			}
			var fields []string
			for rows.Next() {
				var id, nn, pk, hidden int
				var name, kind string
				var def any
				if err = rows.Scan(&id, &name, &kind, &nn, &def, &pk, &hidden); err != nil {
					t.Fatal(err)
				}
				if hidden == 0 && (kind == "TEXT" || kind == "BLOB") {
					fields = append(fields, "coalesce(length(CAST("+name+" AS BLOB)),0)")
				}
			}
			rows.Close()
			want := iaRetirementUnitCount(t, w, "SELECT "+strings.Join(fields, "+")+" FROM playback_sessions WHERE id='retire-session'")
			out, err := iaRetirementRunUnit(t, w, r, p, s, v)
			if large {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, iaRetirementUnitResult{}) || iaRetirementUnitCount(t, w, `SELECT count(*) FROM playback_sessions`) != 1 {
					t.Fatal("oversized parent was not held", out, err)
				}
			} else if err != nil || out.variableBytes != want {
				t.Fatal("native parent bytes", out, want, err)
			}
		})
	}
}
func TestIARetirementEntryCannotReleaseTwoUnits(t *testing.T) {
	w, r, p, s, v := iaRetirementUnitFixture(t, false)
	iaRetirementReleasedPin(t, w, 1, "old-a", "a")
	iaRetirementReleasedPin(t, w, 1, "old-b", "b")
	turn, b := iaRetirementUnitTurn(t, w, r)
	e, err := enterIARetirementUnit(t.Context(), turn, s, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := retireIAMediaParentUnit(t.Context(), turn, e, v); err != nil || out.removedGuidePins != 1 {
		t.Fatal(out, err)
	}
	out, err := retireIAMediaParentUnit(t.Context(), turn, e, v)
	if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(out, iaRetirementUnitResult{}) {
		t.Fatal("entry reused", out, err)
	}
	turn.transaction.rollback(err)
	if iaRetirementUnitCount(t, w, `SELECT count(*) FROM mp_external_guide_pins`) != 2 {
		t.Fatal("aborted prefix retained a deletion")
	}
}
