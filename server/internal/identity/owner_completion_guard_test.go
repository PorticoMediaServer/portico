package identity

import (
	"context"
	"errors"
	"testing"
)

func TestOwnerCompletionAndUnknownGrantsDenyOrdinaryRequests(t *testing.T) {
	for _, grant := range []string{GrantOwnerClaimCompletion, "future_unknown_grant"} {
		p := Principal{Grant: grant}
		for _, ctx := range []context.Context{context.Background(), WithPlaybackRequest(context.Background())} {
			if err := p.PermitsRequest(ctx); !errors.Is(err, ErrForbidden) {
				t.Fatal("narrowed or unknown grant borrowed generic authority", grant, err)
			}
		}
	}
}

func TestNarrowedFamilyCannotUseRetainedRefreshOrAccountAuthority(t *testing.T) {
	for _, grant := range []string{GrantCastTarget, GrantOwnerClaimCompletion} {
		t.Run(grant, func(t *testing.T) {
			s, db, owner := directFixture(t)
			// Keep a real preexisting refresh credential deliberately. Narrowing must
			// remain deny-only even if a caller omitted its normal deletion cleanup.
			if _, err := db.Exec(`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES(?,?)`, owner.Session.SessionFamilyID, grant); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DirectMe(context.Background(), owner.Session.AccessToken); !errors.Is(err, ErrForbidden) {
				t.Fatal("narrowed family kept ordinary account authority", err)
			}
			if _, err := s.RefreshSession(context.Background(), owner.Session.RefreshToken, owner.Session.InstallationID, "narrowed-refresh"); !errors.Is(err, ErrForbidden) {
				t.Fatal("narrowed family renewed with a retained refresh credential", err)
			}
			var generation int
			if err := db.QueryRow(`SELECT current_generation FROM authorization_session_families WHERE id=?`, owner.Session.SessionFamilyID).Scan(&generation); err != nil || generation != int(owner.Session.TokenGeneration) {
				t.Fatal("refused refresh rotated the family", err)
			}
		})
	}
}
