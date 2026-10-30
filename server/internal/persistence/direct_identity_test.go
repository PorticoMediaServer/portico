package persistence

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestDirectIdentityFullProfileAccountSurvivesRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "server.sqlite")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'01','primary',1)`); e != nil {
		db.Close()
		t.Fatal(e)
	}
	for i := 1; i < 8; i++ {
		if _, e = db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,'account','Child')`, fmt.Sprintf("child-%d", i)); e != nil {
			db.Close()
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES('ninth','account','Ninth')`); e == nil {
		db.Close()
		t.Fatal("capacity trigger did not reject ninth profile")
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = Open(path)
	if e != nil {
		t.Fatal("restart failed with eight profiles", e)
	}
	defer db.Close()
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM direct_profiles WHERE account_id='account' AND deleted=0`).Scan(&count); e != nil || count != 8 {
		t.Fatal("restart changed active profiles", count, e)
	}
}
