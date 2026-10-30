package identity

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Exercise the installed SQLite capacity fence, not an in-memory profile list.
func TestDirectConcurrentCapacitySurvivesReopen(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	results := make(chan error, 20)
	var workers sync.WaitGroup
	for n := 0; n < 20; n++ {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			_, e := s.CreateDirectProfile(ctx, login.AccountToken, fmt.Sprintf("Child %d", n), "mint")
			results <- e
		}(n)
	}
	workers.Wait()
	close(results)
	admitted, denied := 0, 0
	for e := range results {
		switch {
		case e == nil:
			admitted++
		case errors.Is(e, ErrProfileCapacity):
			denied++
		default:
			t.Fatal("unexpected concurrent failure", e)
		}
	}
	if admitted != 7 || denied != 13 {
		t.Fatal("capacity was not atomic", admitted, denied)
	}
	var seq int
	var name, path string
	if e := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	fresh, e := New(reopened, filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	view, e := fresh.DirectMe(ctx, login.AccountToken)
	if e != nil || len(view.Profiles) != 8 {
		t.Fatal("restart changed capacity", len(view.Profiles), e)
	}
	if _, e = fresh.CreateDirectProfile(ctx, login.AccountToken, "Ninth after restart", "blue"); !errors.Is(e, ErrProfileCapacity) {
		t.Fatal("restart lost capacity fence", e)
	}
}

func TestDirectPINFailureDebtSurvivesServiceReconstruction(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	profiles, e := s.CreateDirectProfile(ctx, login.AccountToken, "Protected", "mint")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, profiles)
	pin := "0123"
	profiles, e = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, PIN: &pin})
	if e != nil {
		t.Fatal(e)
	}
	child = childProfile(t, profiles)
	if _, e = s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: "9999"}); !errors.Is(e, ErrProfilePIN) {
		t.Fatal(e)
	}
	var seq int
	var name, path string
	if e = db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	// Keep a deterministic future debt horizon so a slow test host cannot make
	// the first two-second delay expire while reopening the database.
	if _, e = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=4102444800000 WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, login.DeviceID); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	fresh, e := New(reopened, filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: pin}); !errors.Is(e, ErrProfileLocked) {
		t.Fatal("restart cleared PIN debt", e)
	}
	var attempts int
	if e = reopened.QueryRow(`SELECT failed_attempts FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, login.DeviceID).Scan(&attempts); e != nil || attempts != 1 {
		t.Fatal("durable debt changed", attempts, e)
	}
}

func TestPINLockoutIsScopedToTheDeviceAndEscalates(t *testing.T) {
	ctx := context.Background()
	s, db, first := directFixture(t)
	profiles, err := s.CreateDirectProfile(ctx, first.AccountToken, "Protected", "mint")
	if err != nil {
		t.Fatal(err)
	}
	child := childProfile(t, profiles)
	pin := "0123"
	if _, err = s.EditDirectProfile(ctx, first.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, PIN: &pin}); err != nil {
		t.Fatal(err)
	}
	second, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil || first.DeviceID == second.DeviceID {
		t.Fatal("independent device sign-in", err)
	}
	for attempt := 1; attempt <= 15; attempt++ {
		if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, first.DeviceID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SelectDirectProfile(ctx, first.AccountToken, child.ID, DirectSelection{PIN: "9999"}); !errors.Is(err, ErrProfilePIN) {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	var firstDebt, firstUntil, lastFailed int64
	if err = db.QueryRow(`SELECT failed_attempts,locked_until_ms,last_failed_ms FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, first.DeviceID).Scan(&firstDebt, &firstUntil, &lastFailed); err != nil || firstDebt != 15 || firstUntil-lastFailed != 3600000 {
		t.Fatalf("first-device escalation: attempts=%d delay=%d err=%v", firstDebt, firstUntil-lastFailed, err)
	}
	if _, err = s.SelectDirectProfile(ctx, first.AccountToken, child.ID, DirectSelection{PIN: pin}); !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("locked device selected profile: %v", err)
	}
	if _, err = s.SelectDirectProfile(ctx, second.AccountToken, child.ID, DirectSelection{PIN: pin}); err != nil {
		t.Fatalf("another device inherited the lockout: %v", err)
	}
}
