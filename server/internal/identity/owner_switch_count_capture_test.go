package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOwnerSwitchPreviewFreezesCountWithoutUnsignedProjection(t *testing.T) {
	s, _, member, login := memberWithSession(t)
	ctx := moveWebContext(t)
	owner, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateDirectProfile(context.Background(), login.AccountToken, "Another profile", "mint"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id=?`, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO access_invitations(id,code_hash,role,created_ms,expires_ms,created_by) VALUES('count-unused','count-hash','member',0,9999999999999,?)`, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAccountMove(ctx, owner.AccountToken, MovePreviewRequest{To: SignInPortico})
	if err != nil {
		t.Fatal(err)
	}
	if preview.otherPeopleCount == nil || *preview.otherPeopleCount != 1 {
		t.Fatal("preview must count the retained disabled person only")
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "otherPeopleCount") {
		t.Fatal("count escaped without its separate signed binding")
	}
	if _, err = s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('count-late','late_person',X'00','count-late-profile',1)`); err != nil {
		t.Fatal(err)
	}
	restored, err := s.AccountMove(ctx, owner.AccountToken)
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || restored.otherPeopleCount == nil || *restored.otherPeopleCount != 1 {
		t.Fatal("restoring a preview recounted live accounts")
	}
	if _, err = s.ConfirmPorticoMove(ctx, owner.AccountToken, MoveConfirmRequest{MoveID: preview.ID, ExpectedRevision: preview.Revision, Confirmation: preview.ServerName}); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("existing authority CAS did not refuse changed counts: %v", err)
	}
}
