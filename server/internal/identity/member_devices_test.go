package identity

import (
	"context"
	"errors"
	"testing"
)

func onDevice(t *testing.T, installation, name string) context.Context {
	t.Helper()
	ctx, err := WithIssuingDevice(context.Background(), DeviceRegistration{InstallationID: installation, Name: name, Platform: "ios", App: "portico", AppVersion: "1.0"}, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

const (
	memberPhone = "installation-member-phone-0000000000000001"
	memberTV    = "installation-member-tv-000000000000000001"
	ownerPhone  = "installation-owner-phone-00000000000000001"
)

func ownerPrincipal(owner DirectSignIn) Principal {
	return Principal{Viewer: owner.Session.Viewer, Hash: Digest(owner.Session.AccessToken), Epoch: 1}
}

// With "require approval" on, a member's new device waits, the owner sees it
// and approves it, and only then can it be used. The owner's own devices are
// never held.
func TestAnAdministratorDecidesOnlyAboutMembersDevices(t *testing.T) {
	ctx := context.Background()
	s, owner, member, _ := memberWithSession(t)
	admin, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "admin", Password: "Testing6!", Name: "Admin", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id=?`, admin.ID); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := s.DirectLogin(onDevice(t, "installation-admin-phone-00000000000000001", "Admin's phone"), "admin", "Testing6!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(onDevice(t, ownerPhone, "Owner's phone"), "owner", "Testing1!"); err != nil {
		t.Fatal(err)
	}
	actor, err := s.Authenticate(adminLogin.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.MemberDevices(ctx, actor, MemberDeviceQuery{})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("an administrator's list: %+v %v", page, err)
	}
	for _, d := range page.Items {
		if d.AccountID != member.ID {
			t.Fatalf("an administrator was shown a device it may not decide about: %+v", d)
		}
	}
	all, err := s.MemberDevices(ctx, ownerPrincipal(owner), MemberDeviceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	decided := 0
	for _, d := range all.Items {
		err = s.SignOutMemberDevices(ctx, actor, d.AccountID, d.ID)
		if d.AccountID == member.ID {
			if err != nil {
				t.Fatalf("an administrator blocking a member's device: %v", err)
			}
			decided++
			continue
		}
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("an administrator blocking %s's device: %v", d.Username, err)
		}
	}
	if decided == 0 || len(all.Items) <= decided {
		t.Fatalf("the fixture did not cover both cases: %d of %d", decided, len(all.Items))
	}
	// Paging returns every device once.
	seen := map[string]bool{}
	cursor := ""
	for {
		p, err := s.MemberDevices(ctx, ownerPrincipal(owner), MemberDeviceQuery{Limit: "1", Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range p.Items {
			if seen[d.ID] {
				t.Fatalf("device %s listed twice", d.ID)
			}
			seen[d.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != len(all.Items) {
		t.Fatalf("paging saw %d devices of %d", len(seen), len(all.Items))
	}
	if _, err = s.MemberDevices(ctx, ownerPrincipal(owner), MemberDeviceQuery{Cursor: "not a cursor"}); !errors.Is(err, ErrDeviceInput) {
		t.Fatalf("a malformed cursor: %v", err)
	}
	if _, err = s.MemberDevices(ctx, ownerPrincipal(owner), MemberDeviceQuery{Limit: "5000"}); !errors.Is(err, ErrDeviceInput) {
		t.Fatalf("an oversized page: %v", err)
	}
}

// A television signed in by code under the policy: the member's approval of
// the code is not the owner's approval of the device. The television's record
// is kept so the owner can approve it, and the same code then works.
