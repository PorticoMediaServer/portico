package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestOwnerEndingKeepsOriginalIncarnationAndGenericErasureStaysNeutral(t *testing.T) {
	s, owner, member, _ := movePorticoFixture(t)
	ctx := context.Background()
	var original string
	var revision int64
	if err := s.db.QueryRow(`SELECT incarnation,revision FROM direct_memberships WHERE account_id=?`, member.ID).Scan(&original, &revision); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: revision + 100, Disabled: true, AllowedLibraries: []string{}}); !errors.Is(err, ErrProfileChanged) {
		t.Fatal("stale owner removal was accepted", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM hosted_membership_endings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused mutation produced ending evidence", err)
	}
	if err := s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: revision, Disabled: true, AllowedLibraries: []string{}}); err != nil {
		t.Fatal(err)
	}
	var incarnation, cause string
	var sequence, ended, expires int64
	if err := s.db.QueryRow(`SELECT incarnation,cause,sequence,ended_ms,expires_ms FROM hosted_membership_endings WHERE hosted_account_id=?`, "acc_"+member.ID).Scan(&incarnation, &cause, &sequence, &ended, &expires); err != nil || incarnation != original || cause != "owner_removed" || sequence < 1 || expires-ended != 2592000000 {
		t.Fatal("missing bounded original removal evidence", err)
	}
	if err := s.db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id=?`, member.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: revision, Disabled: false, AllowedLibraries: []string{}}); err != nil {
		t.Fatal(err)
	}
	var successor string
	if err := s.db.QueryRow(`SELECT incarnation FROM direct_memberships WHERE account_id=?`, member.ID).Scan(&successor); err != nil || successor == original {
		t.Fatal("reenabling adopted the ended incarnation", err)
	}
	if err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return s.EraseMemberTx(ctx, tx, member.ID) }); err != nil {
		t.Fatal(err)
	}
	var nextSequence int64
	if err := s.db.QueryRow(`SELECT incarnation,cause,sequence FROM hosted_membership_endings WHERE hosted_account_id=?`, "acc_"+member.ID).Scan(&incarnation, &cause, &nextSequence); err != nil || incarnation != successor || cause != "" || nextSequence <= sequence {
		t.Fatal("generic erasure invented a cause or reused a prior membership", err)
	}
}

func TestExplicitOwnerDeletionRetainsItsCauseAfterTheAccountIsErased(t *testing.T) {
	s, owner, member, _ := movePorticoFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewDirectMemberDeletion(ctx, owner.AccountToken, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, member.ID, MemberDeletion{ExpectedRevision: preview.Revision}); err != nil {
		t.Fatal(err)
	}
	var cause string
	if err = s.db.QueryRow(`SELECT cause FROM hosted_membership_endings WHERE hosted_account_id=?`, "acc_"+member.ID).Scan(&cause); err != nil || cause != "owner_removed" {
		t.Fatal("deletion lost its accepting owner cause", err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM accounts WHERE id=?`, member.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("ending evidence retained the erased account", err)
	}
}
