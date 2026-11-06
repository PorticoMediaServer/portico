package identity

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

func TestFinishingAccountMovePublishesEachWaitingMembersOwnRevision(t *testing.T) {
	s, _, member, _ := memberWithSession(t)
	ctx := moveWebContext(t)
	web, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('waiting-two','waiting-two',x'00','waiting-two-profile'),('another-move','another-move',x'00','another-move-profile')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('finish',?,1,'local','portico',0,'moving_to_portico',1,2,0,2,'Fixture',0)`, web.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES(?,'finish','waiting',0),('waiting-two','finish','waiting',0),('another-move','elsewhere','waiting',0)`, member.ID); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := s.db.QueryRow(`SELECT COALESCE(max(id),0) FROM api_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAccountMove(ctx, web.AccountToken, "finish", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT audience,revision FROM api_events WHERE id>? AND type='authority.changed'`, before)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var audience, revision string
		if err := rows.Scan(&audience, &revision); err != nil {
			t.Fatal(err)
		}
		if _, duplicate := got[audience]; duplicate {
			t.Fatal("move emitted duplicate authority hints", audience)
		}
		got[audience] = revision
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[apievents.AuthorityAudience("local", member.ID)] != "2" || got[apievents.AuthorityAudience("local", "waiting-two")] != "2" {
		t.Fatal("move did not publish each waiting member's own revision", got)
	}
	var disabled bool
	if err := s.db.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id='another-move'`).Scan(&disabled); err != nil || disabled {
		t.Fatal("finishing one move affected another move's member", disabled, err)
	}
}
