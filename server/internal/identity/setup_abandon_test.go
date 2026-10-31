package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// Back from an unapproved Portico Account setup returns the server to the start of
// setup; local is then a fresh choice with a fresh token.
func TestAbandonReturnsAnUnapprovedSetupToTheStart(t *testing.T) {
	for _, stage := range []string{"", "prepared", "cancel_pending"} {
		t.Run("claim="+stage, func(t *testing.T) {
			s, db, token := pendingHostedSetup(t, stage)
			ctx := context.Background()
			const request = "11111111-2222-4333-8444-555555555555"
			if stage == "cancel_pending" {
				if _, err := db.Exec(`INSERT INTO networking_claim_cancellations(operation_id,request_id,mode,claim_generation,payload,signature,state,created_at) VALUES('op_setup','cancel-1','intent','',X'7b7d',zeroblob(64),'pending','2026-10-31T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AbandonSetup(ctx, request, Token(), 1, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("another token abandoned setup: %v", err)
			}
			if _, err := s.AbandonSetup(ctx, request, token, 2, nil); !errors.Is(err, setupflow.ErrStateChanged) {
				t.Fatalf("a stale revision abandoned setup: %v", err)
			}
			var stopped []string
			state, err := s.AbandonSetup(ctx, request, token, 1, func(op string) { stopped = append(stopped, op) })
			if err != nil || state.Phase != setupflow.Open {
				t.Fatalf("abandon: %+v %v", state, err)
			}
			if stage != "" && (len(stopped) != 1 || stopped[0] != "op_setup") {
				t.Fatalf("the claim's watch was not stopped: %v", stopped)
			}
			var rows, active, pending int
			var claimStage string
			if err = db.QueryRow(`SELECT (SELECT count(*) FROM onboarding_state_v1),(SELECT count(*) FROM networking_claim_identity WHERE active_operation_id IS NOT NULL),(SELECT count(*) FROM networking_claim_cancellations WHERE state='pending')`).Scan(&rows, &active, &pending); err != nil || rows != 0 || active != 0 || pending != 0 {
				t.Fatalf("setup left behind: rows=%d active=%d queued withdrawals=%d %v", rows, active, pending, err)
			}
			if stage != "" {
				if err = db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id='op_setup'`).Scan(&claimStage); err != nil || claimStage != "cancelled" {
					t.Fatalf("claim stage %q %v", claimStage, err)
				}
			}
			// A replay (a lost answer) finds setup open, and the old token proves nothing.
			if again, err := s.AbandonSetup(ctx, request, token, 1, nil); err != nil || again.Phase != setupflow.Open {
				t.Fatalf("replay: %+v %v", again, err)
			}
			if _, err = s.ResumeSetup(ctx, request, token, false); err == nil {
				t.Fatal("the abandoned setup still resumes")
			}
			// Local is now a fresh choice, with the fresh token a start hands out.
			fresh, err := s.BrowserSetupToken(ctx)
			if err != nil || fresh == token {
				t.Fatalf("fresh start: %v", err)
			}
			local, err := s.SetupWithOptions(ctx, SetupOptions{SetupToken: fresh, Name: "Family server", AuthMode: "local", Username: "owner", Password: "Local-password1!"}, true)
			if err != nil || local.AccessToken == "" {
				t.Fatalf("local after Back: %v", err)
			}
		})
	}
}

// Once Portico Services has approved, Back is no longer offered: setup finishes and the
// owner can switch kinds afterwards (lead call, 31 Oct).
func TestAbandonRefusesAnApprovedSetup(t *testing.T) {
	for _, stage := range []string{"approved", "finalizing"} {
		t.Run(stage, func(t *testing.T) {
			s, db, token := pendingHostedSetup(t, stage)
			if _, err := s.AbandonSetup(context.Background(), "11111111-2222-4333-8444-555555555555", token, 1, func(string) { t.Fatal("stopped an approved claim") }); !errors.Is(err, setupflow.ErrStateChanged) {
				t.Fatalf("abandon after approval: %v", err)
			}
			var rows int
			if err := db.QueryRow(`SELECT count(*) FROM onboarding_state_v1`).Scan(&rows); err != nil || rows != 1 {
				t.Fatal("the approved setup was removed", rows, err)
			}
		})
	}
}

// A refused token is refused before anything is said about the phase.
func TestAbandonNeedsItsOwnToken(t *testing.T) {
	s, _, _ := pendingHostedSetup(t, "approved")
	if _, err := s.AbandonSetup(context.Background(), "11111111-2222-4333-8444-555555555555", Token(), 1, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("another token learned the phase: %v", err)
	}
}
