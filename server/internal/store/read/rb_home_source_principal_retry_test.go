package read

import (
	"errors"
	"reflect"
	"testing"
)

func TestRBHomeSourceRetryPinsPrincipal(t *testing.T) {
	for _, mode := range []string{"manifest", "content"} {
		homeSourceCase(t, mode, func(t *testing.T) {
			f, r := homeSourceFixture(t, func(f *browseFixture) {
				homeSourceViewSeed(t, f)
				retrySecondProfile(t, f)
				homeManifestScope(t, f, "a", "p2", 1)
				homeManifestList(t, f, "a", "p2", "home.rowOrder", []string{"view:home"})
				f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('home','local','a','p2','viewer')`)
			})
			hook := &savedViewWindowHook{base: f.snapshots}
			finalWindow := 2
			if mode == "content" {
				finalWindow = 3
			}
			hook.hook = func(n int) {
				if n == finalWindow {
					f.exec(t, `UPDATE libraries SET name='Fresh name' WHERE id='movies'`)
				}
				if n == finalWindow+1 {
					retrySwitchProfile(t, f)
				}
			}
			r.base.native.snapshots = hook
			var page, zero any
			var cost BrowseReadResult
			var e error
			if mode == "manifest" {
				page, cost, e = homeSourceManifest(t, f, r)
				zero = HomeManifestSourceDocument{}
			} else {
				in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
				in.View.Limit = 1
				page, cost, e = homeSourceContent(t, f, r, in)
				zero = HomeManifestContentPage{}
			}
			if !errors.Is(e, ErrDenied) || cost.Attempts != 2 || !reflect.DeepEqual(page, zero) {
				t.Fatalf("security drift: zero=%t attempts=%d err=%v", reflect.DeepEqual(page, zero), cost.Attempts, e)
			}
			savedViewSameDeadline(t, hook)
			// The replacement profile is genuinely allowed to execute this source;
			// rejection above comes from operation identity, not missing source setup.
			r.base.native.snapshots = f.snapshots
			in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
			in.View.Limit = 1
			fresh, _, e := homeSourceContent(t, f, r, in)
			if e != nil || len(fresh.Items) != 1 {
				t.Fatal("replacement profile fixture not authorized", e)
			}
		})
	}
}
