package read

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
)

func TestRBPersonalTitleCursorWholeFence(t *testing.T) {
	for _, mode := range []string{"sort-direction", "account-profile"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := rbPersonalWindowFixture(t)
				query := json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":true}`)
				in := PersonalTitleFilterInput{View: f.input, Query: query}
				first, _, err := rbPersonalWindowRead(t, f, in)
				if err != nil || first.After == nil {
					t.Fatal("missing initial cursor", err)
				}
				if mode == "sort-direction" {
					in.View.Descending = true
				} else {
					// Create a distinct, actually authorized b/q family. Keep the query
					// unchanged, so a query-hash mismatch cannot conceal a missing fence.
					f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies"]',revision=revision+1 WHERE account_id='b'`)
					f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device-q','b','installation-q','fixture','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
					f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation) SELECT 'family-q','s','b','q','local','member',1,'2026-11-07T00:00:00Z',1,'device-q',m.incarnation,p.lifetime,x'00800000000000000000000000000002' FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='b' AND p.id='q'`)
					f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token-q','family-q',1,'2026-11-07T00:00:00Z')`)
					in.View.TokenHash = "token-q"
					fresh, _, err := rbPersonalWindowRead(t, f, in)
					if err != nil || len(fresh.Items) == 0 {
						t.Fatal("second viewer fixture was not authorized", fresh, err)
					}
					for _, item := range fresh.Items {
						if item.ID%5 != 0 {
							t.Fatal("second viewer read another profile", item.ID)
						}
					}
				}
				in.After = first.After
				got, _, err := rbPersonalWindowRead(t, f, in)
				if !errors.Is(err, ErrStale) || !reflect.DeepEqual(got, PersonalTitleFilterPage{}) {
					t.Fatal("cursor crossed unchanged-query fence", mode, got, err)
				}
			})
		})
	}
}
