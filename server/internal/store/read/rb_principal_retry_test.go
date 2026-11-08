package read

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRBSeparateRetryPinsPrincipal(t *testing.T) {
	for _, mode := range []string{"saved-execute", "saved-directory", "home-manifest", "home-recent", "home-activity"} {
		savedViewRun(t, mode, func(t *testing.T) {
			var f *browseFixture
			var saved *SavedViewReader
			var manifest *HomeManifestReader
			finalWindow := 2
			switch mode {
			case "saved-execute", "saved-directory":
				f, saved = savedViewFixture(t, 1)
				f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('view-00','local','a','p2','viewer')`)
				if mode == "saved-execute" {
					finalWindow = 3
				}
			case "home-manifest":
				f, manifest = homeManifestFixture(t, nil)
			default:
				f = newRBHomeFixture(t)
				if mode == "home-recent" {
					finalWindow = 3
				}
			}
			retrySecondProfile(t, f)
			hook := &savedViewWindowHook{base: f.snapshots}
			hook.hook = func(n int) {
				if n == finalWindow {
					switch mode {
					case "saved-execute", "saved-directory":
						f.exec(t, `UPDATE saved_resources SET revision=revision+1 WHERE id='view-00'`)
					case "home-manifest":
						f.exec(t, `UPDATE libraries SET name='Fresh name' WHERE id='movies'`)
					default:
						f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
					}
				}
				if n == finalWindow+1 {
					retrySwitchProfile(t, f)
				}
			}
			var page any
			var zero any
			var cost BrowseReadResult
			var e error
			switch mode {
			case "saved-execute":
				saved.snapshots = hook
				page, cost, e = savedViewExecute(t, f, saved, SavedViewExecutionInput{View: f.input, Resource: "view-00"})
				zero = SavedViewExecutionPage{}
			case "saved-directory":
				saved.snapshots = hook
				page, cost, e = savedViewDirectory(t, f, saved, SavedViewDirectoryInput{View: f.input})
				zero = SavedViewDirectoryPage{}
			case "home-manifest":
				manifest.native.snapshots = hook
				page, cost, e = homeManifestRead(t, f, manifest)
				zero = HomeManifestDocument{}
			case "home-recent":
				f.reader.snapshots = hook
				page, cost, e = f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, HomeRecentInput{View: f.input, Sources: []HomeSource{{1, 1}}})
				zero = HomeRecentRow{}
			case "home-activity":
				f.reader.snapshots = hook
				page, cost, e = f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, HomeActivityInput{View: f.input, Family: HomeContinueActivity})
				zero = HomeActivityPage{}
			}
			if !errors.Is(e, ErrDenied) || cost.Attempts != 2 || !reflect.DeepEqual(page, zero) {
				t.Fatalf("retry changed principal: zero=%t attempts=%d windows=%d error=%v", reflect.DeepEqual(page, zero), cost.Attempts, hook.calls, e)
			}
			savedViewSameDeadline(t, hook)
		})
	}
}
