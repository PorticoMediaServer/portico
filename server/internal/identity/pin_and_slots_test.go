package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func profileWithPIN(t *testing.T) (*Service, DirectSignIn, DirectProfile) {
	t.Helper()
	ctx := context.Background()
	s, _, _, login := memberWithSession(t)
	profiles, err := s.CreateDirectProfile(ctx, login.AccountToken, "Grown-up", "coral")
	if err != nil {
		t.Fatal(err)
	}
	profile := childProfile(t, profiles)
	pin := "4821"
	edited, err := s.EditDirectProfile(ctx, login.AccountToken, profile.ID, DirectProfileEdit{ExpectedRevision: profile.Revision, PIN: &pin})
	if err != nil {
		t.Fatal(err)
	}
	return s, login, childProfile(t, edited)
}

// A PIN is hashed with no transaction open. With both hashing slots taken, a
// PIN entry waits for one, and while it waits the database's one writer is
// free: another write goes straight through.
func TestAProfilePINIsCheckedWithoutHoldingTheWriter(t *testing.T) {
	ctx := context.Background()
	s, login, profile := profileWithPIN(t)
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	waiting := make(chan struct{})
	s.hashSlotWaiting = func() { close(waiting) }
	done := make(chan error, 1)
	go func() {
		_, err := s.SelectDirectProfile(ctx, login.AccountToken, profile.ID, DirectSelection{PIN: "4821"})
		done <- err
	}()
	// The observer runs only after admission found both slots occupied.
	select {
	case <-waiting:
	case err := <-done:
		t.Fatalf("the PIN entry did not wait for a hashing slot: %v", err)
	case <-time.After(time.Second):
		t.Fatal("the PIN entry never reached hashing admission")
	}
	write, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	if _, err := dbwork.ExecWrite(write, s.db, dbwork.ClassSecurityFence, `UPDATE configuration SET value=value WHERE key='id'`); err != nil {
		t.Fatalf("a write was held up while a PIN waited to be hashed (%v): %v", time.Since(started), err)
	}
	select {
	case err := <-done:
		t.Fatalf("the PIN entry finished while no hashing slot was free: %v", err)
	default:
	}
	// A slot frees up; the waiting entry gets it and the right PIN is accepted.
	<-s.hashSlots
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the right PIN after waiting for a slot: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the PIN entry never finished")
	}
	<-s.hashSlots
}

// A brief shortage of hashing slots is waited out; a lasting one still answers "busy".
func TestAPasswordCheckWaitsBrieflyForAHashingSlot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	s, _, _, _ := memberWithSession(t)
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	go func() {
		time.Sleep(200 * time.Millisecond)
		<-s.hashSlots
	}()
	started := time.Now()
	if out, err := s.DirectLogin(ctx, "member", "Testing2!"); err != nil || out.Session == nil {
		t.Fatalf("a sign-in that had to wait a moment for a slot: %v", err)
	}
	if waited := time.Since(started); waited < 150*time.Millisecond {
		t.Fatalf("the sign-in did not wait for a slot (%v)", waited)
	}
	// Both slots stay taken: the wait is bounded and the answer is "busy".
	s.hashSlots <- struct{}{}
	started = time.Now()
	if _, err := s.DirectLogin(ctx, "member", "Testing2!"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a sign-in with no slot for the whole wait: %v", err)
	}
	if waited := time.Since(started); waited < hashSlotWait-200*time.Millisecond || waited > hashSlotWait+2*time.Second {
		t.Fatalf("the wait for a slot lasted %v, want about %v", waited, hashSlotWait)
	}
	// A caller that gives up stops waiting at once.
	gone, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started = time.Now()
	if _, err := s.DirectLogin(gone, "member", "Testing2!"); err == nil || time.Since(started) > time.Second {
		t.Fatalf("a cancelled sign-in kept waiting for a slot: %v after %v", err, time.Since(started))
	}
	<-s.hashSlots
	<-s.hashSlots
}

// A device's wrong PINs never lock a profile out on other devices. The
// offending device still has its persisted retry delay.
func TestWrongPINsDoNotLockOtherDevicesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	s, _, profile := profileWithPIN(t)
	var firstToken, firstDevice string
	attempt := func(n int, pin string) error {
		t.Helper()
		login, err := s.DirectLogin(onDevice(t, "installation-pin-guess-"+string(rune('a'+n%26))+string(rune('a'+n/26))+"-000000000000000000", "Borrowed phone"), "member", "Testing2!")
		if err != nil || login.Session == nil {
			t.Fatalf("attempt %d sign-in: %v", n, err)
		}
		if n == 0 {
			firstToken, firstDevice = login.AccountToken, login.DeviceID
		}
		_, err = s.SelectDirectProfile(ctx, login.AccountToken, profile.ID, DirectSelection{PIN: pin})
		return err
	}
	for n := 0; n < 25; n++ {
		if err := attempt(n, "0000"); !errors.Is(err, ErrProfilePIN) {
			t.Fatalf("wrong PIN %d from a new device: %v", n+1, err)
		}
	}
	// No elapsed-time allowance: the right PIN works immediately elsewhere.
	if err := attempt(25, "4821"); err != nil {
		t.Fatalf("wrong PINs on other devices blocked the right PIN: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=? WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, time.Now().Add(time.Minute).UnixMilli(), profile.ID, firstDevice); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectDirectProfile(ctx, firstToken, profile.ID, DirectSelection{PIN: "4821"}); !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("the offending device lost its retry delay: %v", err)
	}
}

// The record of a session that ended long ago is removed; a live session and a
// recently ended one are kept.
func TestRecordsOfSessionsThatEndedLongAgoArePruned(t *testing.T) {
	ctx := context.Background()
	s, _, _, live := memberWithSession(t)
	ended, err := s.DirectLogin(onDevice(t, memberPhone, "Phone"), "member", "Testing2!")
	if err != nil {
		t.Fatal(err)
	}
	recent, err := s.DirectLogin(onDevice(t, memberTV, "Television"), "member", "Testing2!")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{ended.Session.AccessToken, recent.Session.AccessToken} {
		if err = s.LogoutToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-EndedSessionRetention - time.Hour).UTC().Format(time.RFC3339)
	if _, err = s.db.Exec(`UPDATE authorization_session_families SET revoked_at=? WHERE id=?`, long, ended.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PruneEndedSessions(ctx)
	if err != nil || removed != 1 {
		t.Fatalf("pruned %d session records: %v", removed, err)
	}
	exists := func(family string) bool {
		var found bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM authorization_session_families WHERE id=?)`, family).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if exists(ended.Session.SessionFamilyID) || !exists(recent.Session.SessionFamilyID) || !exists(live.Session.SessionFamilyID) {
		t.Fatalf("after pruning: long-ended kept=%v, recently ended kept=%v, live kept=%v", exists(ended.Session.SessionFamilyID), exists(recent.Session.SessionFamilyID), exists(live.Session.SessionFamilyID))
	}
	var leftovers int
	if err = s.db.QueryRow(`SELECT (SELECT count(*) FROM authorization_family_tokens WHERE family_id=?1)+(SELECT count(*) FROM identity_device_families WHERE family_id=?1)`, ended.Session.SessionFamilyID).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("a pruned session left %d rows behind: %v", leftovers, err)
	}
	if _, err = s.Authenticate(live.Session.AccessToken); err != nil {
		t.Fatalf("pruning ended a live session: %v", err)
	}
	if removed, err = s.PruneEndedSessions(ctx); err != nil || removed != 0 {
		t.Fatalf("a second pass pruned %d: %v", removed, err)
	}
}

// A refresh credential used twice ends the session through the same clean-up
// as any other sign-out, so what the session had open is retired with it.
func TestARefreshCredentialUsedTwiceEndsTheSessionThroughTheCleanUpHook(t *testing.T) {
	s, _, _, _ := memberWithSession(t)
	phone := onDevice(t, memberPhone, "Phone")
	login, err := s.DirectLogin(phone, "member", "Testing2!")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.RefreshSession(phone, login.Session.RefreshToken, memberPhone, "renew-0001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshSession(phone, first.RefreshToken, memberPhone, "renew-0002"); err != nil {
		t.Fatal(err)
	}
	var retired []string
	s.OnFamilyRevokedTx = func(_ context.Context, _ *sql.Tx, family FamilyState) error {
		retired = append(retired, family.ID)
		return nil
	}
	// The first credential again, long after its successor was used.
	if _, err = s.RefreshSession(phone, login.Session.RefreshToken, memberPhone, "renew-0003"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a reused refresh credential: %v", err)
	}
	if len(retired) != 1 || retired[0] != login.Session.SessionFamilyID {
		t.Fatalf("the clean-up ran for %v", retired)
	}
	var reason string
	if err = s.db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, login.Session.SessionFamilyID).Scan(&reason); err != nil || reason != string(RevokedRefreshReuse) {
		t.Fatalf("reason %q: %v", reason, err)
	}
}
