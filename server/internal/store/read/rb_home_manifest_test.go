package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func homeManifestScope(t *testing.T, f *browseFixture, account, profile string, revision int64) {
	t.Helper()
	f.exec(t, `INSERT INTO preference_scopes(authority,account_id,profile_id,scope,device_key,revision,updated_ms) VALUES('local',?,?,'profile-server','',?,1)`, account, profile, revision)
}
func homeManifestList(t *testing.T, f *browseFixture, account, profile, key string, values []string) {
	t.Helper()
	f.exec(t, `INSERT INTO preference_values(authority,account_id,profile_id,scope,device_key,key,kind) VALUES('local',?,?,'profile-server','',?,'string-list')`, account, profile, key)
	for n, value := range values {
		f.exec(t, `INSERT INTO preference_list_items(authority,account_id,profile_id,scope,device_key,key,position,value) VALUES('local',?,?,'profile-server','',?,?,?)`, account, profile, key, n, value)
	}
}
func homeManifestFixture(t *testing.T, seed func(*browseFixture)) (*browseFixture, *HomeManifestReader) {
	t.Helper()
	f := newBrowseSource(t)
	if seed != nil {
		seed(f)
	}
	data, e := os.ReadFile("rb_home_manifest_test.go")
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	// Only dependencies used by the new manifest are composed; no old tests or
	// frozen goldens are re-recorded. Every receipt uses this actual merged Reader.
	defs := append(append(browseDefinitions(), rbSavedViewAllDraftDefinitions()...), rbHomeManifestDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, hash)
	r, e := NewHomeManifestReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func homeManifestRead(t *testing.T, f *browseFixture, r *HomeManifestReader) (HomeManifestDocument, BrowseReadResult, error) {
	t.Helper()
	var doc HomeManifestDocument
	var cost BrowseReadResult
	live, request, output := liveRequests("home-manifest")
	request.Budget.RawRows = 2048
	request.Budget.VMOps = 200000
	request.Budget.Pages = 4096
	in := HomeManifestInput{View: f.input}
	in.View.Library = 0
	in.View.Kind = 0
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		doc, cost, e = r.Read(ctx, request, time.Second, out, in)
		return e
	})
	return doc, cost, e
}
func homeManifestFind(rows []HomeManifestDescriptor, id string) *HomeManifestDescriptor {
	for n := range rows {
		if rows[n].ID == id {
			return &rows[n]
		}
	}
	return nil
}
func homeManifestServerPolicy(t *testing.T, f *browseFixture, enabled int) {
	t.Helper()
	f.exec(t, `INSERT INTO server_settings(singleton,updated_ms,"home.communityActivityEnabled") VALUES(1,1,?)`, enabled)
	f.exec(t, `INSERT INTO server_setting_sources(field,source) VALUES('home.communityActivityEnabled','server')`)
}

func TestRBHomeManifest(t *testing.T) {
	t.Run("actual-layout-current-libraries-and-unsupported", func(t *testing.T) {
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			homeManifestScope(t, f, "a", "p", 7)
			homeManifestList(t, f, "a", "p", "home.rowOrder", []string{"community_watching", "trending_now", "new_on_server", "continue"})
			// Required rows cannot be hidden even if old stored data named them.
			homeManifestList(t, f, "a", "p", "home.hiddenRowIds", []string{"new_on_server", "continue", "listen_next", "continue_books"})
			homeManifestScope(t, f, "b", "q", 99)
			homeManifestList(t, f, "b", "q", "home.rowOrder", []string{"PRIVATE-FOREIGN-LAYOUT"})
			homeManifestServerPolicy(t, f, 1)
			f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["other","movies"]' WHERE account_id='a'`)
			f.exec(t, `UPDATE libraries SET position=2 WHERE id='movies'`)
			f.exec(t, `UPDATE libraries SET position=1 WHERE id='other'`)
		})
		doc, cost, e := homeManifestRead(t, f, r)
		if e != nil || doc.Pending || !doc.SourceExhausted || doc.Layout.Revision != 7 || !doc.Layout.Present || cost.Attempts != 1 || len(doc.Libraries) != 2 || doc.Libraries[0].ID != "other" || doc.Libraries[1].ID != "movies" {
			t.Fatal(doc, cost, e)
		}
		if len(doc.Catalogue) != 11 || len(doc.Rows) == 0 || doc.Rows[0].ID != "community_watching" || homeManifestFind(doc.Rows, "new_on_server") != nil {
			t.Fatal(doc)
		}
		for _, id := range []string{"continue", "listen_next", "continue_books"} {
			row := homeManifestFind(doc.Rows, id)
			if row == nil || row.Hidden || !row.Required {
				t.Fatal("required row hidden", id, doc.Rows)
			}
		}
		for _, row := range doc.Catalogue {
			if row.ReadState != "unsupported" || row.Reason == "" {
				t.Fatal("uncomposed family claimed success", row)
			}
		}
		if homeManifestFind(doc.Catalogue, "on_now").PolicyState != "unresolved" || homeManifestFind(doc.Rows, "on_now") != nil || doc.SpotlightState != "unsupported_recommendation_source" {
			t.Fatal(doc)
		}
		if strings.Contains(fmt.Sprint(doc.Layout), "PRIVATE-FOREIGN-LAYOUT") {
			t.Fatal("foreign preference exposed")
		}
		data, _ := os.ReadFile("rb_home_manifest_test.go")
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
			if stats == plan.Analyzed {
				f.exec(t, "ANALYZE")
			}
			captureBrowse(t, f, rbHomeManifestDefinitions(), stats, hash)
		}
	})
	t.Run("defaults-and-unrepresented-file-policy", func(t *testing.T) {
		f, r := homeManifestFixture(t, nil)
		doc, _, e := homeManifestRead(t, f, r)
		if e != nil || doc.Layout.Present || doc.Layout.Revision != 1 || len(doc.Layout.RowOrder) != 0 || len(doc.Layout.HiddenRowIDs) != 0 || len(doc.Libraries) != 1 || homeManifestFind(doc.Catalogue, "community_watching").PolicyState != "unresolved" {
			t.Fatal(doc, e)
		}
		homeManifestServerPolicy(t, f, 0)
		doc, _, e = homeManifestRead(t, f, r)
		if e != nil || homeManifestFind(doc.Catalogue, "community_watching").PolicyState != "disabled_by_owner" {
			t.Fatal(doc, e)
		}
		f.exec(t, `UPDATE server_setting_sources SET source='file' WHERE field='home.communityActivityEnabled'`)
		doc, _, e = homeManifestRead(t, f, r)
		if e != nil || homeManifestFind(doc.Catalogue, "community_watching").PolicyState != "unresolved" {
			t.Fatal("file value fabricated", doc, e)
		}
	})
	t.Run("unversioned-layout-drift", func(t *testing.T) {
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			homeManifestScope(t, f, "a", "p", 3)
			homeManifestList(t, f, "a", "p", "home.rowOrder", []string{"new_on_server"})
		})
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE preference_list_items SET value='trending_now' WHERE account_id='a' AND key='home.rowOrder'`)
			}
		}
		r.native.snapshots = hook
		doc, cost, e := homeManifestRead(t, f, r)
		if e != nil || cost.Attempts != 2 || !reflect.DeepEqual(doc.Layout.RowOrder, []string{"trending_now"}) || doc.Rows[0].ID != "trending_now" {
			t.Fatal("old layout published", doc, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
	t.Run("unversioned-library-projection-drift", func(t *testing.T) {
		f, r := homeManifestFixture(t, nil)
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE libraries SET name='Current name' WHERE id='movies'`)
			}
		}
		r.native.snapshots = hook
		doc, cost, e := homeManifestRead(t, f, r)
		if e != nil || cost.Attempts != 2 || len(doc.Libraries) != 1 || doc.Libraries[0].Name != "Current name" {
			t.Fatal("old library name published", doc, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})

	t.Run("principal-rebirth", func(t *testing.T) {
		f, r := homeManifestFixture(t, nil)
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE direct_profiles SET lifetime='cccccccccccccccccccccccccccccccc' WHERE id='p'`)
			}
		}
		r.native.snapshots = hook
		doc, _, e := homeManifestRead(t, f, r)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(doc, HomeManifestDocument{}) {
			t.Fatal("old principal published", doc, e)
		}
	})
	t.Run("complete-library-authority-and-pending", func(t *testing.T) {
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			f.exec(t, `UPDATE direct_memberships SET role='owner',allowed_libraries='[]' WHERE account_id='a'`)
			for n := 0; n < 15; n++ {
				f.exec(t, `INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'movie','/media')`, fmt.Sprintf("extra-%02d", n), fmt.Sprintf("Extra %02d", n))
			}
		})
		doc, _, e := homeManifestRead(t, f, r)
		if e != nil || !doc.Pending || doc.SourceExhausted || doc.RawLibraryPositions != 17 || doc.PendingReason != "library_projection_requires_preparation" || len(doc.Rows) != 0 || len(doc.Libraries) != 0 {
			t.Fatal("truncated library projection", doc, e)
		}
		f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE id='p'`)
		doc, _, e = homeManifestRead(t, f, r)
		if e != nil || doc.Pending || len(doc.Libraries) != 1 || doc.Libraries[0].ID != "movies" {
			t.Fatal("profile library ceiling bypassed", doc, e)
		}
	})
	t.Run("malformed-stored-preference-is-not-default", func(t *testing.T) {
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			homeManifestScope(t, f, "a", "p", 1)
			homeManifestList(t, f, "a", "p", "home.rowOrder", []string{strings.Repeat("x", 513)})
		})
		doc, cost, e := homeManifestRead(t, f, r)
		if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(doc, HomeManifestDocument{}) || cost.LookupStatements != 6 {
			t.Fatal("invalid list fabricated defaults or hydrated payload", doc, cost, e)
		}
	})
	t.Run("native-descriptor-route-bounds", func(t *testing.T) {
		longResource := strings.Repeat("界", 90)
		longLibrary := strings.Repeat("l", 257)
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			homeManifestScope(t, f, "a", "p", 1)
			homeManifestList(t, f, "a", "p", "home.rowOrder", []string{"view:" + longResource, "view:wide-library"})
			savedViewAdd(t, f, longResource, "Long resource identifier", "a", savedViewTestDefinition)
			savedViewAdd(t, f, "wide-library", "Long library identifier", "a", strings.Replace(savedViewTestDefinition, "movies", longLibrary, 1))
			f.exec(t, `INSERT INTO libraries(id,name,kind,root) VALUES(?,'Long library','movie','/media')`, longLibrary)
			f.exec(t, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(3,?,'/media')`, longLibrary)
			f.exec(t, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='a'`, `["movies","`+longLibrary+`"]`)
		})
		doc, _, e := homeManifestRead(t, f, r)
		if e != nil {
			t.Fatal(e)
		}
		for _, id := range []string{"view:" + longResource, "view:wide-library"} {
			row := homeManifestFind(doc.Catalogue, id)
			if row == nil || row.ReadState != "unsupported" {
				t.Fatal("unexecutable native route advertised", id, row)
			}
		}
	})

	t.Run("configured-view-authority-and-validation", func(t *testing.T) {
		f, r := homeManifestFixture(t, func(f *browseFixture) {
			homeManifestScope(t, f, "a", "p", 1)
			homeManifestList(t, f, "a", "p", "home.rowOrder", []string{"view:owned", "view:shared", "view:private"})
			savedViewAdd(t, f, "owned", "Owned view", "a", savedViewTestDefinition)
			savedViewAdd(t, f, "shared", "Shared view", "foreign", savedViewTestDefinition)
			savedViewAdd(t, f, "private", "PRIVATE VIEW NAME", "foreign", savedViewTestDefinition)
			f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('shared','local','a','p','viewer')`)
		})
		doc, _, e := homeManifestRead(t, f, r)
		if e != nil || homeManifestFind(doc.Catalogue, "view:owned") == nil || homeManifestFind(doc.Catalogue, "view:shared") == nil || homeManifestFind(doc.Catalogue, "view:private") != nil {
			t.Fatal(doc, e)
		}
		for _, id := range []string{"view:owned", "view:shared"} {
			row := homeManifestFind(doc.Catalogue, id)
			if row.ReadState != "deferred" || row.ResourceRevision != 1 {
				t.Fatal(row)
			}
		}
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `DELETE FROM saved_resource_shares WHERE resource_id='shared'`)
			}
		}
		r.native.snapshots = hook
		doc, cost, e := homeManifestRead(t, f, r)
		if e != nil || cost.Attempts != 2 || homeManifestFind(doc.Catalogue, "view:shared") != nil {
			t.Fatal("revoked view name published", doc, cost, e)
		}
	})
}
