package read

import (
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func retrySecondProfile(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `INSERT INTO direct_profiles(id,account_id,name) VALUES('p2','a','Second')`)
}
func retrySwitchProfile(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `UPDATE authorization_session_families SET profile_id='p2',command_profile_lifetime=(SELECT lifetime FROM direct_profiles WHERE id='p2') WHERE id='f'`)
}

// A real RS read exercises the shared roots-based retry, including its native
// snapshots. Both profiles remain authorized for the item throughout.
func TestPublicationRetryPinsPrincipal(t *testing.T) {
	for _, mode := range []string{"profile-before-final", "profile-between-attempts", "permissions-before-final", "permissions-between-attempts", "content-only"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := newRSDetailFixture(t, false)
				retrySecondProfile(t, f)
				f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
				hook := &savedViewWindowHook{base: f.snapshots}
				hook.hook = func(n int) {
					if n == 3 {
						f.exec(t, `UPDATE catalog_entities SET original_title='Fresh source',revision=revision+1 WHERE id=1101`)
					}
					switch {
					case mode == "profile-before-final" && n == 3, mode == "profile-between-attempts" && n == 4:
						retrySwitchProfile(t, f)
					case mode == "permissions-before-final" && n == 3, mode == "permissions-between-attempts" && n == 4:
						// Movies remains allowed: denial must come from the pinned permission
						// context, rather than failure to authorize this particular item.
						f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='a'`)
					}
				}
				r.snapshots = hook
				page, cost, e := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, TitleFactsInput{View: f.input, Item: 1101})
				if mode == "content-only" {
					if e != nil || cost.Attempts != 2 || page.OriginalTitle != "Fresh source" {
						t.Fatal(page, cost, e)
					}
				} else if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, TitleFacts{}) {
					t.Fatalf("security drift published: attempts=%d original=%q error=%v", cost.Attempts, page.OriginalTitle, e)
				}
				savedViewSameDeadline(t, hook)
			})
		})
	}
}
