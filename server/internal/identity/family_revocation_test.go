package identity

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestEveryFamilyRevocationReasonAppearsInOwnerSessionHistory(t *testing.T) {
	s, db, owner := directFixture(t)
	ctx := context.Background()
	cases := []struct {
		reason RevocationReason
		text   string
	}{
		{RevokedRefreshReuse, "Signed out: sign-in was used twice"},
		{RevokedExplicitSignout, "Signed out"},
		{RevokedAdminRevoke, "Signed out by an administrator"},
		{RevokedMembershipRemoved, "Signed out: account access was removed"},
		{RevokedPasswordChange, "Signed out: account security changed"},
	}
	for _, tc := range cases {
		login, err := s.DirectLogin(ctx, "owner", "Testing1!")
		if err != nil {
			t.Fatal(err)
		}
		id := login.Session.SessionFamilyID
		if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			if tc.reason == RevokedAdminRevoke {
				return s.revokeDirectTx(ctx, tx, "account", "", id)
			}
			return RevokeFamilyTx(ctx, tx, id, tc.reason)
		}); err != nil {
			t.Fatal(err)
		}
		var reason, at string
		if err = db.QueryRow(`SELECT revoked_reason,revoked_at FROM authorization_session_families WHERE id=?`, id).Scan(&reason, &at); err != nil || reason != string(tc.reason) {
			t.Fatalf("%s reason: %s %v", id, reason, err)
		}
		if _, err = time.Parse(time.RFC3339, at); err != nil {
			t.Fatalf("%s timestamp: %q %v", id, at, err)
		}
	}
	sessions, err := s.DirectSessions(ctx, owner.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, session := range sessions {
		if session.RevokedReason != "" {
			seen[session.RevokedReason] = true
			for _, tc := range cases {
				if string(tc.reason) == session.RevokedReason && session.StatusText != tc.text {
					t.Fatalf("%s status: %q", tc.reason, session.StatusText)
				}
			}
		}
	}
	for _, tc := range cases {
		if !seen[string(tc.reason)] {
			t.Fatalf("owner history missing %s: %+v", tc.reason, sessions)
		}
	}
}
