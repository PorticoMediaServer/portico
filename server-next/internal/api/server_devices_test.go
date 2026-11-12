package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner reads every account's devices in one list, a device per account it is signed in
// to, most recently seen first; an administrator reads only the members' (never the owner's,
// another administrator's or its own); a member reads none. Pages go on by cursor without
// repeating or missing a row.
func TestTheOwnerReadsEveryAccountsDevices(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "ada", "admin")
	addMember(t, s, owner.AccessToken, "bob", "admin")
	addMember(t, s, owner.AccessToken, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 1)
	signInAs(t, s, "bob", testPassword, 2)
	phone := signInAs(t, s, "max", testPassword, 3)
	signInAs(t, s, "max", testPassword, 4)
	signInAs(t, s, "max", testPassword, 4) // a second sign-in on the same device

	read := func(token string) Page[ServerDevice] {
		t.Helper()
		var out Page[ServerDevice]
		if w := call(t, s, "GET", "/v1/admin/devices", token, nil, &out); w.Code != 200 {
			t.Fatalf("the device list answered %d %s", w.Code, w.Body)
		}
		return out
	}
	byOwner := read(owner.AccessToken)
	users := map[string]int{}
	sessions := 0
	for i, d := range byOwner.Items {
		users[d.Account.Username]++
		if d.Account.Username == "max" {
			sessions += d.Sessions
		}
		if d.Account.ID == "" || d.Name == "" || !d.SignedIn || d.Current || d.Account.PorticoAccount {
			t.Errorf("row %d reads %+v", i, d)
		}
		if i > 0 && d.LastSeenAt.After(byOwner.Items[i-1].LastSeenAt) {
			t.Errorf("row %d was seen after the row above it", i)
		}
	}
	if users["ada"] != 1 || users["bob"] != 1 || users["max"] != 2 || len(byOwner.Items) != 5 || sessions != 3 {
		t.Fatalf("the owner's list has %v accounts' devices and %d of max's sign-ins: %+v", users, sessions, byOwner.Items)
	}

	byAdmin := read(admin.AccessToken)
	if len(byAdmin.Items) != 2 || byAdmin.Items[0].Account.Username != "max" || byAdmin.Items[1].Account.Username != "max" {
		t.Fatalf("an administrator's list holds %+v, want only the member's two devices", byAdmin.Items)
	}
	if code := status(t, s, "GET", "/v1/admin/devices", phone.AccessToken, nil); code != 403 {
		t.Fatalf("a member reading every device answered %d", code)
	}

	var members Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner.AccessToken, nil, &members)
	for _, m := range members.Items {
		if m.PorticoAccount {
			t.Errorf("%s is marked as a Portico Account on a local server", m.Username)
		}
	}

	// One row a page, every row once.
	var paged []string
	var after db.AccountDeviceCursor
	for range 10 {
		rows, next, err := s.db.AccountDevices(context.Background(), "owner", after, 1, s.now())
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			paged = append(paged, r.PublicID+"/"+r.AccountID)
		}
		if next == nil {
			break
		}
		after = *next
	}
	if len(paged) != len(byOwner.Items) {
		t.Fatalf("paging one at a time read %v, the whole list %d rows", paged, len(byOwner.Items))
	}
	for i, d := range byOwner.Items {
		if paged[i] != d.ID+"/"+d.Account.ID {
			t.Fatalf("page %d read %s, the list %s", i, paged[i], d.ID+"/"+d.Account.ID)
		}
	}
}
