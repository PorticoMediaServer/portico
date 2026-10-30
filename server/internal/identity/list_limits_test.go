package identity

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAccountListsIncludeLargeHistoryAndMembership(t *testing.T) {
	s, db, login := directFixture(t)
	var profile string
	if err := db.QueryRow(`SELECT profile_id FROM accounts WHERE id='account'`).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		id := fmt.Sprintf("member-%04d", i)
		_, err = tx.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'00',?)`, id, id, id+"-profile")
		if err != nil {
			break
		}
		if i >= 201 {
			continue
		}
		_, err = tx.Exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,'account',?,'Device','mac','portico','1','2026-09-25','2026-09-25')`, fmt.Sprintf("device-%04d", i), fmt.Sprintf("installation-%04d", i))
		if err != nil {
			break
		}
		_, err = tx.Exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,'server','account',?,'local','owner',1,?,1)`, fmt.Sprintf("family-%04d", i), profile, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
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
	ctx := context.Background()
	devices, err := s.Devices(ctx, login.AccountToken, "")
	if err != nil || len(devices) < 201 {
		t.Fatalf("devices=%d: %v", len(devices), err)
	}
	sessions, err := s.DirectSessions(ctx, login.Session.AccessToken)
	if err != nil || len(sessions) < 201 {
		t.Fatalf("sessions=%d: %v", len(sessions), err)
	}
	members, err := s.DirectMembers(ctx, login.AccountToken)
	if err != nil || len(members) < 1001 {
		t.Fatalf("members=%d: %v", len(members), err)
	}
}
