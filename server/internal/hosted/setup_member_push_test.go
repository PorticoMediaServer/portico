package hosted

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Between a setup claim installing and its owner being admitted by the Return, this
// server has no accounts. It must not push its (empty) member list: Portico Services
// would drop the claim's owner from its index and refuse the Return. Once the owner is
// admitted, the very next control step pushes the full list naming them.
func TestNoMemberPushUntilTheSetupOwnerIsAdmitted(t *testing.T) {
	f, secret := pendingClaimFixture(t)
	index := &fakeIndex{members: map[string]string{}}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	steps := func() {
		t.Helper()
		for range 4 {
			if _, e := controlStep(t, f); e != nil {
				t.Fatal(e)
			}
		}
	}
	steps()
	if len(index.pushes) != 0 {
		t.Fatalf("pushed %d member list(s) before the owner was admitted: %+v", len(index.pushes), index.pushes)
	}

	who, e := verify(f, freshClaims(f, "account"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.id.CompleteAccountSetup(context.Background(), pendingSetupID, secret, who); e != nil {
		t.Fatal("owner admission", e)
	}
	if _, e = controlStep(t, f); e != nil {
		t.Fatal(e)
	}
	if len(index.pushes) == 0 || !index.pushes[0].Full || index.members["account"] != identity.TierOwner {
		t.Fatalf("the first push after admission was not the full list naming the owner: %+v %v", index.pushes, index.members)
	}
}
