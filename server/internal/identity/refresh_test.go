package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDeviceRefreshRotatesAccessAndRefreshTogether(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx, err := WithIssuingDevice(context.Background(), registration(Token()), "198.51.100.4:9999")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueTx(ctx, tx, "account", "profile", "local", "owner", 1, time.Time{})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	accessUntil, err := familyTime(first.ExpiresAt)
	if err != nil || accessUntil.After(time.Now().Add(16*time.Minute)) || first.RefreshToken == "" || first.DeviceID == "" {
		t.Fatal("access lifetime or bound refresh missing", first.ExpiresAt, err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, first.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	request := Token()
	second, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceID != first.DeviceID || second.SessionFamilyID != first.SessionFamilyID || second.AccessToken == first.AccessToken || second.RefreshToken == first.RefreshToken {
		t.Fatal("rotation changed the device/family or retained a credential")
	}
	if _, err = s.Authenticate(first.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("predecessor access survived", err)
	}
	if _, err = s.Authenticate(second.AccessToken); err != nil {
		t.Fatal("successor access refused", err)
	}
	replayed, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil || replayed.AccessToken != second.AccessToken || replayed.RefreshToken != second.RefreshToken {
		t.Fatal("retry did not return exact committed credentials", err)
	}
	if _, err = s.RefreshSession(context.Background(), second.RefreshToken, Token(), Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("refresh accepted another installation", err)
	}
	if _, err = s.RefreshSession(context.Background(), first.RefreshToken, installation, Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("predecessor accepted with a different request", err)
	}
	if _, err = s.Authenticate(second.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stolen predecessor did not revoke successor access", err)
	}
	var reuseReason string
	if err = db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, first.SessionFamilyID).Scan(&reuseReason); err != nil || reuseReason != string(RevokedRefreshReuse) {
		t.Fatalf("refresh reuse reason %q: %v", reuseReason, err)
	}
	if _, err = s.RefreshSession(context.Background(), second.RefreshToken, installation, Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stolen predecessor did not revoke successor refresh", err)
	}

}

func TestRefreshStolenPredecessorRaceRevokesFamily(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx, err := WithIssuingDevice(context.Background(), registration(Token()), "198.51.100.8:9999")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueTx(ctx, tx, "account", "profile", "local", "owner", 1, time.Time{})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, first.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	requests := []string{Token(), Token()}
	type result struct {
		out Envelope
		err error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i].out, results[i].err = s.RefreshSession(context.Background(), first.RefreshToken, installation, requests[i])
		}(i)
	}
	wg.Wait()
	var successes int
	for _, r := range results {
		if r.err == nil {
			successes++
			if _, err := s.Authenticate(r.out.AccessToken); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("racing stolen refresh did not revoke minted access", err)
			}
		} else if !errors.Is(r.err, ErrUnauthorized) {
			t.Fatal("unexpected racing refresh error", r.err)
		}
	}
	if successes != 1 {
		t.Fatal("expected one rotation followed by family revocation", results)
	}
}

func TestExpiredRefreshReceiptPrunesWithoutLosingReuseDetection(t *testing.T) {
	s, db, _ := familyFixture(t)
	first := issueFamily(t, s)
	second, err := s.RefreshSession(context.Background(), first.RefreshToken, first.InstallationID, Token())
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err = db.Exec(`UPDATE identity_refresh_receipts SET expires_at=? WHERE predecessor_hash=?`, past, Digest(first.RefreshToken)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, past, Digest(first.AccessToken)); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PruneSessionHistory(context.Background())
	if err != nil || removed != 2 {
		t.Fatalf("expected expired receipt and token pruned: %d %v", removed, err)
	}
	var receipts, retired, marker int
	if err = db.QueryRow(`SELECT count(*) FROM identity_refresh_receipts WHERE predecessor_hash=?`, Digest(first.RefreshToken)).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM authorization_family_tokens WHERE token_hash=?`, Digest(first.AccessToken)).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM identity_refresh_predecessors WHERE predecessor_hash=?`, Digest(first.RefreshToken)).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || retired != 0 || marker != 1 {
		t.Fatalf("prune state: receipt=%d token=%d marker=%d", receipts, retired, marker)
	}
	if _, err = s.RefreshSession(context.Background(), first.RefreshToken, first.InstallationID, Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pruned predecessor did not revoke: %v", err)
	}
	if _, err = s.Authenticate(second.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("successor survived old refresh reuse: %v", err)
	}
	if _, err = s.PruneSessionHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM identity_refresh_predecessors WHERE family_id=?`, first.SessionFamilyID).Scan(&marker); err != nil || marker != 0 {
		t.Fatalf("revoked family marker remained: %d %v", marker, err)
	}
}

func TestExpiredFamilyPrunesRefreshPredecessorWithoutRevocation(t *testing.T) {
	s, db, _ := familyFixture(t)
	first := issueFamily(t, s)
	if _, err := s.RefreshSession(context.Background(), first.RefreshToken, first.InstallationID, Token()); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRow(`SELECT count(*) FROM identity_refresh_predecessors WHERE family_id=?`, first.SessionFamilyID).Scan(&before); err != nil || before != 1 {
		t.Fatalf("predecessor missing: %d %v", before, err)
	}
	if _, err := db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), first.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneSessionHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM identity_refresh_predecessors WHERE family_id=?`, first.SessionFamilyID).Scan(&after); err != nil || after != 0 {
		t.Fatalf("expired family retained predecessor: %d %v", after, err)
	}
}

func TestAccountScopedSessionRefreshesWithoutGrantingMedia(t *testing.T) {
	s, _, initial := directFixture(t)
	ctx := context.Background()
	if _, err := s.CreateDirectProfile(ctx, initial.Session.AccessToken, "Child", "mint"); err != nil {
		t.Fatal(err)
	}
	selected, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil || selected.Session == nil || selected.Session.Viewer.Role != "account" {
		t.Fatal("profile chooser did not receive account-scoped session", err)
	}
	if _, err = s.Authenticate(selected.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("account session gained viewer authority", err)
	}
	rotated, err := s.RefreshSession(ctx, selected.Session.RefreshToken, selected.InstallationID, Token())
	if err != nil || rotated.Viewer.Role != "account" || rotated.AccessToken == selected.Session.AccessToken {
		t.Fatal("account session did not rotate", err)
	}
	if _, err = s.Authenticate(rotated.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("rotated account session gained viewer authority", err)
	}
	if _, err = s.CreateDirectProfile(ctx, rotated.AccessToken, "Another", "blue"); err != nil {
		t.Fatal("rotated account session could not manage profiles", err)
	}
}

// A device whose refresh answer was lost can replay the same request after an
// outage longer than the old five-minute window; once its successor is used,
// the predecessor is reuse again and revokes the family.
func TestRefreshReceiptOutlivesAnOutageUntilTheSuccessorIsUsed(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx, err := WithIssuingDevice(context.Background(), registration(Token()), "198.51.100.9:9999")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueTx(ctx, tx, "account", "profile", "local", "owner", 1, time.Time{})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, first.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	request := Token()
	second, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil {
		t.Fatal(err)
	}
	var until string
	if err = db.QueryRow(`SELECT expires_at FROM identity_refresh_receipts WHERE predecessor_hash=?`, Digest(first.RefreshToken)).Scan(&until); err != nil {
		t.Fatal(err)
	}
	if expiry, e := familyTime(until); e != nil || !expiry.After(time.Now().Add(time.Hour)) {
		t.Fatal("receipt expires too soon to cover an outage", until, e)
	}
	replayed, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil || replayed.RefreshToken != second.RefreshToken || replayed.AccessToken != second.AccessToken {
		t.Fatal("lost answer was not replayed exactly", err)
	}
	third, err := s.RefreshSession(context.Background(), second.RefreshToken, installation, Token())
	if err != nil {
		t.Fatal("successor refresh refused", err)
	}
	if _, err = s.RefreshSession(context.Background(), first.RefreshToken, installation, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("predecessor replay accepted after the successor was used", err)
	}
	if _, err = s.Authenticate(third.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reuse after the successor was used did not revoke the family", err)
	}
}
