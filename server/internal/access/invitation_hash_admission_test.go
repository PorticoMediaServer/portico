package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestUnacceptableInvitationsNeverReachPasswordHashing(t *testing.T) {
	for _, useShort := range []bool{false, true} {
		for _, state := range []string{"unknown", "expired", "revoked", "used"} {
			t.Run(fmt.Sprintf("%s/short=%t", state, useShort), func(t *testing.T) {
				s, db, _ := localFixture(t)
				ctx := context.Background()
				invite, err := s.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "hash-admission-invite", Role: identity.TierMember})
				if err != nil {
					t.Fatal(err)
				}
				shortCode := ""
				if useShort {
					short, err := s.IssueShortInvitation(ctx, nil, owner(), invite.ID, InvitationShortRequest{ExpectedRevision: invite.Revision, OperationID: "hash-admission-short"})
					if err != nil {
						t.Fatal(err)
					}
					shortCode = short.ShortCode
				}
				code := invite.Code
				switch state {
				case "unknown":
					code = strings.Repeat("x", 43)
					shortCode = strings.Repeat("0", 16)
				case "expired":
					_, err = db.Exec(`UPDATE access_invitations SET expires_ms=? WHERE id=?`, s.now()-1, invite.ID)
				case "revoked":
					_, err = s.RevokeInvitation(ctx, nil, invite.ID, InvitationRevoke{ExpectedRevision: invite.Revision, OperationID: "hash-admission-revoke"})
				case "used":
					_, err = s.Accept(ctx, Acceptance{Code: code, Username: "firstguest", Password: "Correct-Horse-9"}, []byte("fixture-hash"))
				}
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				q := Acceptance{Code: code, Username: "newguest", Password: "Correct-Horse-9"}
				if useShort {
					q.Code, q.ShortCode = "", shortCode
				}
				_, err = s.AcceptWithPassword(ctx, q, func(context.Context, string) ([]byte, error) {
					calls++
					return []byte("fixture-hash"), nil
				})
				if !errors.Is(err, ErrExpired) || calls != 0 {
					t.Fatalf("%s: err=%v hash calls=%d", state, err, calls)
				}
			})
		}
	}
}

func TestInvitationPreflightDoesNotConsumeAndFinalAcceptWinsOnce(t *testing.T) {
	s, db, _ := localFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	invite, err := s.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "hash-race-invitation", Role: identity.TierMember})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	hash := func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return []byte("fixture-hash"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan error, 2)
	for i := range 2 {
		go func() {
			_, err := s.AcceptWithPassword(ctx, Acceptance{Code: invite.Code, Username: fmt.Sprint("raceguest", i), Password: "Correct-Horse-9"}, hash)
			done <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("both preflights did not finish", ctx.Err())
		}
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM access_invitations WHERE id=?`, invite.ID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("preflight consumed the invitation: %s %v", state, err)
	}
	once.Do(func() { close(release) })
	successes, expired := 0, 0
	for range 2 {
		switch err := <-done; {
		case err == nil:
			successes++
		case errors.Is(err, ErrExpired):
			expired++
		default:
			t.Fatal(err)
		}
	}
	var accounts int
	if err := db.QueryRow(`SELECT count(*) FROM accounts WHERE username LIKE 'raceguest%'`).Scan(&accounts); err != nil || accounts != 1 || successes != 1 || expired != 1 || calls.Load() != 2 {
		t.Fatalf("accept race: accounts=%d successes=%d expired=%d hashes=%d err=%v", accounts, successes, expired, calls.Load(), err)
	}
}

func TestInvitationRevokedWhileHashingCannotCreateAnAccount(t *testing.T) {
	s, db, _ := localFixture(t)
	ctx := context.Background()
	invite, err := s.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "late-revoke-invitation", Role: identity.TierMember})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AcceptWithPassword(ctx, Acceptance{Code: invite.Code, Username: "lateguest", Password: "Correct-Horse-9"}, func(context.Context, string) ([]byte, error) {
		// A live writer must remain available while the hash is running.
		_, err := s.RevokeInvitation(ctx, nil, invite.ID, InvitationRevoke{ExpectedRevision: invite.Revision, OperationID: "late-hash-revoke"})
		return []byte("fixture-hash"), err
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatal("late revocation bypassed", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM accounts WHERE username='lateguest'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked invitation created an account: %d %v", count, err)
	}
}
