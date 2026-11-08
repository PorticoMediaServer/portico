package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact independent pre/post native SQL populations for HN's mechanical fresh
// statement capture. These are not valid command manifests, allocation results,
// producer observations, permission or an accepting turn. HN uses the compiled
// definitions/params; no parallel SQL implementation is provided.
func seedInventoryFreshPlanFacts(t *testing.T, e *executor) {
	t.Helper()
	c := seedInventoryFresh(t, e)
	for _, q := range []string{
		`UPDATE inventory_runs SET command_incarnation=zeroblob(16) WHERE job_id='fresh-run'`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('fresh-empty-library','Empty','movie','/empty')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'fresh-empty-library','/empty')`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(31,zeroblob(16),7,1)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_language,metadata_incarnation,artwork_incarnation)
VALUES(31,zeroblob(16),7,1,'Arrival','Arrival','arrival','a','arrival','a',1,'en-US',zeroblob(16),zeroblob(16))`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(41,'fresh-asset',7,'/media/Arrival.mkv',42,-11,'','','',0,0,0)`,
		`INSERT INTO catalog_asset_links(library_id,entity_id,asset_id) VALUES(7,31,41)`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Same actual existing Revision algorithm, stable identity and complete JSON
	// as the body fixture; this row exists solely for the path hit/miss cohorts.
	observation, err := prepareInventoryFreshObservation(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.ExecContext(t.Context(), `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,analysis_state)
VALUES('fresh-object','fresh-source','fresh-asset','fresh-incarnation','Arrival.mkv',?,?,?,?,?,'fresh-run','.','available','pending')`,
		observation.objectIdentity, observation.revision, observation.evidenceJSON, observation.size, observation.modifiedNS); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryFreshNativeCompiledCohortPopulations(t *testing.T) {
	e, path, _ := testExecutor(t)
	seedInventoryFreshPlanFacts(t, e)
	// Real FULL startup Capture precedes ordinary Try/bind/NORMAL BEGIN.
	// Qualification setup does not execute these compiled cohort effects.
	registry := cachedNativeInventoryFixtureRegistry(t, e, scanInventoryFreshDefinitions(), "scan_fresh_test.go", "scan_fresh_plan_facts_test.go")
	for _, d := range scanFreshDefinitions() {
		for _, cohort := range d.Cohorts {
			t.Run(string(d.ID)+"/"+cohort.Name, func(t *testing.T) {
				turn, _ := inventoryFreshTestTurn(t, e, path, registry)
				want := int64(1)
				if cohort.Name == "native_point_miss" || cohort.Name == "native_cas_miss" {
					want = 0
				}
				var got int64
				if d.Kind == stmt.Read || strings.Contains(d.SQL, "RETURNING ") {
					rows, err := turn.query(t.Context(), turn.steps[d.ID].statementStep, cohort.Bindings...)
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
						got++
						if got > 1 {
							t.Fatal("unbounded cohort", got)
						}
					}
					if err = rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err = rows.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					result, err := turn.exec(t.Context(), turn.steps[d.ID].statementStep, cohort.Bindings...)
					if err != nil {
						t.Fatal(err)
					}
					got, err = result.RowsAffected()
					if err != nil {
						t.Fatal(err)
					}
				}
				if got != want {
					t.Fatal("native fixture does not establish declared population", got, want)
				}
				if err := turn.closeRows(); err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
