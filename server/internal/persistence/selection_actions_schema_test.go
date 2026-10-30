package persistence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectionActionJournalStorageAndTransitionGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := Open(filepath.Join(t.TempDir(), "actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"selection_action_groups", "selection_action_targets", "selection_action_files", "selection_action_history", "download_action_transfers"} {
		var strict int
		if err = db.QueryRow(`SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Fatalf("%s strict=%d %v", table, strict, err)
		}
	}
	insert := `INSERT INTO selection_action_groups(binding,operation_id,request_hash,account_id,library_id,action,profile_id,server_id,authority,role,session_hash,session_epoch,expires_at,principal_device_id,grant_kind,delete_files,device_id,quality,state,total,expected_digest,created_ms) VALUES('binding','op','hash','account','library','delete','profile','server','local','owner','session',1,'2100-01-01T00:00:00Z','','',1,'','','capturing',1,?,1)`
	if _, err = db.Exec(insert, strings.Repeat("a", 64)); err != nil {
		t.Fatal("valid capture rejected", err)
	}
	for _, invalid := range []string{
		`UPDATE selection_action_groups SET state='sealed'`,
		`UPDATE selection_action_groups SET admitted=-1`,
		`UPDATE selection_action_groups SET delete_files=2`,
		`UPDATE selection_action_groups SET done=1`,
		`UPDATE selection_action_groups SET state='complete'`,
		`UPDATE selection_action_groups SET seal_count=1,seal_digest='wrong',state='sealed'`,
	} {
		if _, err = db.Exec(invalid); err == nil {
			t.Fatalf("accepted invalid journal transition: %s", invalid)
		}
	}
	if _, err = db.Exec(`UPDATE selection_action_groups SET admitted=1,seal_count=total,seal_digest=expected_digest,state='sealed'`); err != nil {
		t.Fatal("valid exact seal rejected", err)
	}
	if _, err = db.Exec(`INSERT INTO selection_action_targets(group_id,entity_id,seq,item_id,library_id,kind,title,identity,version_id,download_identity,lifetime) VALUES(1,7,1,'item','library','movie','Title',?,'','','')`, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE selection_action_targets SET ready=2`); err == nil {
		t.Fatal("non-boolean ready accepted")
	}
	if _, err = db.Exec(`UPDATE selection_action_targets SET kind='show'`); err == nil {
		t.Fatal("container command slipped into leaf-only journal")
	}
	if _, err = db.Exec(`INSERT INTO selection_action_files(group_id,seq,target_id,asset_id,token,path,root,root_identity,size,modified_ns,shared,present,part,start,end,edition,source_id,source_incarnation,source_generation) VALUES(1,2,7,9,'asset','/media/file','/media','root',9,1,0,1,0,0,NULL,'','source','life',1)`); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`UPDATE selection_action_files SET size=-1`, `UPDATE selection_action_files SET started=2`, `UPDATE selection_action_files SET present=2`, `UPDATE selection_action_files SET source_generation=-1`} {
		if _, err = db.Exec(invalid); err == nil {
			t.Fatalf("accepted invalid file command: %s", invalid)
		}
	}
}
