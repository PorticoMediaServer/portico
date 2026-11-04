package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnknownCooloffMatchesKnownWithoutExtendingRefusals(t *testing.T) {
	s, db, _ := directFixture(t)
	var account string
	if err := db.QueryRow(`SELECT id FROM accounts WHERE username='owner'`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	ctx := WithSignInSource(context.Background(), "198.51.100.9:443")
	subject := s.unknownSubject("unlisted")
	for range 8 {
		if err := s.passwordAttempt(ctx, account, credentialFailure); err != nil {
			t.Fatal(err)
		}
		if err := s.unknownAttempt(ctx, "unlisted"); err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().Unix() + 30
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET locked_until=? WHERE account_id=?`, until, account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE identity_unknown_attempts SET locked_until=? WHERE subject_hash=?`, until, subject); err != nil {
		t.Fatal(err)
	}
	// Lock checks must be read-only, before either of the shared hashing slots.
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	defer func() { <-s.hashSlots; <-s.hashSlots }()
	for range 3 {
		var known, unknown *AccountLockError
		if err := s.credentialCooloff(ctx, account); !errors.As(err, &known) {
			t.Fatal(err)
		}
		if err := s.unknownCooloff(ctx, " Unlisted "); !errors.As(err, &unknown) {
			t.Fatal(err)
		}
		if d := unknown.RetryAfter - known.RetryAfter; d < -1 || d > 1 {
			t.Fatalf("different retry: known=%d unknown=%d", known.RetryAfter, unknown.RetryAfter)
		}
	}
	var minAttempts, maxAttempts int
	var minUntil, maxUntil int64
	if err := db.QueryRow(`SELECT MIN(failed_attempts),MAX(failed_attempts),MIN(locked_until),MAX(locked_until) FROM identity_unknown_attempts WHERE subject_hash=?`, subject).Scan(&minAttempts, &maxAttempts, &minUntil, &maxUntil); err != nil {
		t.Fatal(err)
	}
	if minAttempts != 8 || maxAttempts != 8 || minUntil != until || maxUntil != until {
		t.Fatalf("refusal changed synthetic ledger: %d/%d %d/%d", minAttempts, maxAttempts, minUntil, maxUntil)
	}
	fresh := WithSignInSource(context.Background(), "203.0.113.9:443")
	if err := s.credentialCooloff(fresh, account); err != nil {
		t.Fatal(err)
	}
	if err := s.unknownCooloff(fresh, "unlisted"); err != nil {
		t.Fatalf("fresh source inherited synthetic lock: %v", err)
	}
	// A wrong guess from that source inherits the global evaluated-failure budget,
	// just as a known account does; the global bucket must not become a free pass.
	if err := s.passwordAttempt(fresh, account, credentialFailure); err != nil {
		t.Fatal(err)
	}
	if err := s.unknownAttempt(fresh, "unlisted"); err != nil {
		t.Fatal(err)
	}
	var realDelay, syntheticDelay int64
	if err := db.QueryRow(`SELECT locked_until-last_failure FROM identity_credential_attempts WHERE account_id=? AND source_bucket='203.0.113.0/24'`, account).Scan(&realDelay); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT locked_until-last_failure FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket='203.0.113.0/24'`, subject).Scan(&syntheticDelay); err != nil {
		t.Fatal(err)
	}
	if syntheticDelay != realDelay || realDelay != 16 {
		t.Fatalf("global evaluated-failure delay: known=%d unknown=%d", realDelay, syntheticDelay)
	}
}
