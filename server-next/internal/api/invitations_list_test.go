package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An invitation still waiting to be used is always in the list, first, however many newer
// invitations a server has made and revoked since.
func TestAWaitingInvitationIsNeverPushedOffTheList(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var waiting Invitation
	if w := call(t, s, "POST", "/v1/admin/invitations", owner.AccessToken, InvitationCreate{}, &waiting); w.Code != 200 {
		t.Fatalf("inviting answered %d: %s", w.Code, w.Body)
	}
	ctx := context.Background()
	now := s.now()
	for n := range 500 {
		id := "inv_used" + strconv.Itoa(n)
		later := now.Add(time.Duration(n+1) * time.Second)
		if _, err := s.db.CreateInvitation(ctx, db.NewInvitation{ActorID: 1, ActorRole: "owner", PublicID: id, CodeHash: "hash" + strconv.Itoa(n),
			Role: "member", AllLibraries: true, ExpiresAt: later.Add(7 * 24 * time.Hour), Now: later}); err != nil {
			t.Fatal(err)
		}
		if err := s.db.RevokeInvitation(ctx, 1, id, later); err != nil {
			t.Fatal(err)
		}
	}
	listed := get[Page[Invitation]](t, s, owner.AccessToken, "/v1/admin/invitations").Items
	if len(listed) != 500 || listed[0].ID != waiting.ID || listed[0].State != "open" {
		t.Fatalf("the list holds %d, the first %s %s; the waiting invitation is %s", len(listed), listed[0].ID, listed[0].State, waiting.ID)
	}
	for _, i := range listed[1:] {
		if i.State == "open" {
			t.Fatalf("%s is listed as waiting after the waiting one", i.ID)
		}
	}
}
