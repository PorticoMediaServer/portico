package identity

import (
	"errors"
	"testing"
	"time"
)

func approveQuickForTest(t *testing.T, s *Service, bearer, requestID string) (QuickAuthorization, string) {
	t.Helper()
	p, err := s.Authenticate(bearer)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.StartQuick(t.Context(), QuickStart{RequestID: requestID, DeviceName: "Television", Platform: "tvos", AppVersion: "1.0"}, true)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ReviewQuick(t.Context(), code.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideQuick(t.Context(), p, preview.RequestID, code.UserCode, "approve"); err != nil {
		t.Fatal(err)
	}
	return code, preview.RequestID
}

func TestQuickApprovalCannotOutlivePINChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"reset", "edit", "revision check"} {
		t.Run(change, func(t *testing.T) {
			s, login, profile := profileWithPIN(t)
			selected, err := s.SelectDirectProfile(t.Context(), login.AccountToken, profile.ID, DirectSelection{PIN: "4821"})
			if err != nil {
				t.Fatal(err)
			}
			code, id := approveQuickForTest(t, s, selected.Session.AccessToken, "11111111-1111-4111-8111-111111111111")
			other, otherID := approveQuickForTest(t, s, login.Session.AccessToken, "22222222-2222-4222-8222-222222222222")
			switch change {
			case "reset":
				_, err = s.PINReset(t.Context(), login.AccountToken, profile.ID, "")
			case "edit":
				pin := "7392"
				_, err = s.EditDirectProfile(t.Context(), login.AccountToken, profile.ID, DirectProfileEdit{ExpectedRevision: profile.Revision, PIN: &pin})
			case "revision check":
				// Leave the approval present to exercise the collect-time fence,
				// independently of the reset/edit transaction's cancellation.
				_, err = s.db.Exec(`UPDATE direct_profiles SET pin_revision=pin_revision+1 WHERE id=?`, profile.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var status string
			var expiry int64
			var hasGrant, hasCode bool
			if err = s.db.QueryRow(`SELECT status,expires_at,grant_cipher IS NOT NULL,user_hash IS NOT NULL FROM quick_connect_v1 WHERE id=?`, id).Scan(&status, &expiry, &hasGrant, &hasCode); err != nil {
				t.Fatal(err)
			}
			if expiry <= time.Now().Unix() {
				t.Fatal("approval expired before the revocation check")
			}
			if change != "revision check" && (status != "cancelled" || hasGrant || hasCode) {
				t.Fatal("PIN change did not cancel and clear the pending approval")
			}
			if _, err = s.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id IN (?,?)`, id, otherID); err != nil {
				t.Fatal(err)
			}
			_, err = s.PollQuick(t.Context(), code.DeviceCode)
			var protocol *SetupProtocolError
			if !errors.As(err, &protocol) || protocol.Code != "access_denied" {
				t.Fatalf("old PIN approval minted a session: %v", err)
			}
			grant, err := s.PollQuick(t.Context(), other.DeviceCode)
			if err != nil || grant.Viewer.ProfileID != login.Session.Viewer.ProfileID {
				t.Fatalf("unrelated profile approval was lost: %v", err)
			}
			if _, err = s.Authenticate(grant.AccessToken); err != nil {
				t.Fatal("unchanged approval could not authenticate", err)
			}
		})
	}
}

func TestQuickApprovalRejectsDeletedProfile(t *testing.T) {
	s, login, profile := profileWithPIN(t)
	selected, err := s.SelectDirectProfile(t.Context(), login.AccountToken, profile.ID, DirectSelection{PIN: "4821"})
	if err != nil {
		t.Fatal(err)
	}
	code, id := approveQuickForTest(t, s, selected.Session.AccessToken, "33333333-3333-4333-8333-333333333333")
	if _, err = s.db.Exec(`UPDATE direct_profiles SET deleted=1 WHERE id=?`, profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	_, err = s.PollQuick(t.Context(), code.DeviceCode)
	var protocol *SetupProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "access_denied" {
		t.Fatal("deleted profile approval minted a session", err)
	}
}
