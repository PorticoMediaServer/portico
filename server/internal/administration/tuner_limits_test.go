package administration

import (
	"context"
	"fmt"
	"testing"
)

func TestTunerAssignmentsIncludeReservationsBeyondTwoHundred(t *testing.T) {
	_, db, _ := newService(t)
	if _, err := db.Exec(`INSERT INTO live_source_identities(id) VALUES('source')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("recording-%03d", i)
		_, err = tx.Exec(`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms) VALUES(?,'owner','local','account','profile','source','channel',?,'generation','{"title":"Episode"}','{}',1000,2000,1,1,'scheduled',0,0)`, id, id)
		if err != nil {
			break
		}
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := assignments(context.Background(), tx, "source", `state='scheduled' AND start_ms>=?`, 0)
	if err != nil || len(got) != 201 || overlapExcess(got, 1) != 0 {
		t.Fatalf("assignments=%d conflicts=%d: %v", len(got), overlapExcess(got, 1), err)
	}
}
