package read

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// The shared principal fence is the one native statement that joins a viewing
// token to its family AND its device; every fenced publication (browse, search
// permission, home, saved, personal) runs it first in the same snapshot. A
// credential revoke (command 522) logically retires the credential's API device
// (retired_ms>0) and revokes its family (revoked=1, teardown_due_ms set); the
// fence must treat either as absent, and a sibling live device of the same
// profile must stay fully visible with an exact paged traversal.
//
// Plain SQL only: no identity/access/usermutation import (those register legacy
// scalars that would pollute every plan golden).
func TestBrowsePrincipalExcludesRetiredDeviceAndRevokedFamily(t *testing.T) {
	const (
		retireDevice = `UPDATE identity_devices SET retired_ms=7,revision=revision+1 WHERE id='d'`
		revokeFamily = `UPDATE authorization_session_families SET revoked=1,revoked_reason='admin_revoke',revoked_at='2026-11-06T00:00:00Z',teardown_due_ms=7 WHERE id='f'`
	)
	addSibling := func(t *testing.T, f *browseFixture) {
		t.Helper()
		f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d2','a','i2','sibling','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
		f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) SELECT 'f2','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'d2',command_member_incarnation,command_profile_lifetime,x'00800000000000000000000000000002',0 FROM authorization_session_families WHERE id='f'`)
		f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token2','f2',1,'2026-11-07T00:00:00Z')`)
	}
	// The independent oracle is plain SQL over the seeded catalogue: every
	// non-retired entity of the viewer's library and kind.
	oracle := func(t *testing.T, f *browseFixture) []int64 {
		t.Helper()
		rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM catalog_entities WHERE library_id=1 AND kind=1 AND retired=0 ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil || len(ids) == 0 {
			t.Fatal("oracle", err, len(ids))
		}
		return ids
	}
	traverse := func(t *testing.T, f *browseFixture, token string, limit int) []int64 {
		t.Helper()
		in := f.input
		in.TokenHash, in.Limit = token, limit
		var got []int64
		for windows := 0; ; windows++ {
			if windows > 1100 {
				t.Fatal("cursor did not terminate")
			}
			page, result, err := f.window(t, t.Context(), in)
			if err != nil || result.Refusal != nil {
				t.Fatalf("live traversal %s: %v %+v", token, err, result)
			}
			for _, item := range page.Items {
				got = append(got, item.ID)
			}
			if page.Exhausted {
				if page.Next != nil {
					t.Fatal("terminal cursor")
				}
				break
			}
			if page.Next == nil {
				t.Fatal("unfinished page has no continuation")
			}
			in.After = page.Next
		}
		slices.Sort(got)
		return got
	}
	denied := func(t *testing.T, f *browseFixture, token string) {
		t.Helper()
		in := f.input
		in.TokenHash = token
		page, _, err := f.window(t, t.Context(), in)
		if !errors.Is(err, ErrDenied) || len(page.Items) != 0 || page.Next != nil {
			t.Fatalf("%s must be absent: page=%+v err=%v", token, page, err)
		}
	}

	for _, tt := range []struct {
		name string
		sql  []string
	}{
		{"retired-device-live-family", []string{retireDevice}},
		{"revoked-teardown-family-live-device", []string{revokeFamily}},
		{"command-522-state", []string{retireDevice, revokeFamily}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newBrowseFixture(t)
			addSibling(t, f)
			want := oracle(t, f)
			if got := traverse(t, f, "token", 40); !reflect.DeepEqual(got, want) {
				t.Fatalf("live device before retire: got %d want %d", len(got), len(want))
			}
			for _, q := range tt.sql {
				f.exec(t, q)
			}
			denied(t, f, "token")
			if got := traverse(t, f, "token2", 40); !reflect.DeepEqual(got, want) {
				t.Fatalf("live sibling device after retire: got %d want %d", len(got), len(want))
			}
			if got := traverse(t, f, "token2", 7); !reflect.DeepEqual(got, want) {
				t.Fatalf("live sibling device small pages: got %d want %d", len(got), len(want))
			}
		})
	}

	t.Run("continuation-after-retire", func(t *testing.T) {
		f := newBrowseFixture(t)
		addSibling(t, f)
		in := f.input
		in.Limit = 7
		first, _, err := f.window(t, t.Context(), in)
		if err != nil || len(first.Items) == 0 || first.Next == nil {
			t.Fatalf("first page: %+v %v", first, err)
		}
		f.exec(t, retireDevice)
		in.After = first.Next
		page, _, err := f.window(t, t.Context(), in)
		if !errors.Is(err, ErrDenied) || len(page.Items) != 0 || page.Next != nil {
			t.Fatalf("a retired device's continuation must publish nothing: page=%+v err=%v", page, err)
		}
	})
}
