package administration

import (
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestRetainedRecordingCreatorIsMarkedRemoved(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "retained.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','main')`,
		`INSERT INTO direct_profiles(id,account_id,name,deleted,art) VALUES('kid','owner','Kids',1,'slate')`,
		`INSERT INTO live_source_identities(id) VALUES('source')`,
		`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms) VALUES('kept','key','local','owner','kid','source','channel','programme','guide','{}','{}',1,2,0,1,'completed',1,1)`,
		`INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms) VALUES('lease','source','channel','kept','recording','local','owner','kid','token',1,'active',1000,1)`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := assignments(t.Context(), tx, "source", "r.start_ms>=?", 0)
	if err != nil || len(rows) != 1 || rows[0].ProfileName != "Kids (removed)" {
		t.Fatalf("recording label: %+v %v", rows, err)
	}
	active, err := activeAssignments(t.Context(), tx, "source", 0)
	if err != nil || len(active) != 1 || active[0].ProfileName != "Kids (removed)" {
		t.Fatalf("capture label: %+v %v", active, err)
	}
}
