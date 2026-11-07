package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func homeSourceFixture(t *testing.T, seed func(*browseFixture)) (*browseFixture, *HomeManifestSourceReader) {
	t.Helper()
	f := newBrowseSource(t)
	if seed != nil {
		seed(f)
	}
	data, e := os.ReadFile("rb_home_manifest_sources_test.go")
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	defs := append(append(append(append(browseDefinitions(), rbSavedViewAllDraftDefinitions()...), rbHomeManifestDefinitions()...), rbSavedViewScalarDefinitions()...), UOPersonalDefinitions()...)
	defs = append(defs, rbHomeManifestSourceDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, hash)
	r, e := NewHomeManifestSourceReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func homeSourceManifest(t *testing.T, f *browseFixture, r *HomeManifestSourceReader) (HomeManifestSourceDocument, BrowseReadResult, error) {
	t.Helper()
	var doc HomeManifestSourceDocument
	var cost BrowseReadResult
	live, request, output := liveRequests("home-sources")
	request.Budget.RawRows = 2048
	request.Budget.VMOps = 200000
	request.Budget.Pages = 4096
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		doc, cost, e = r.Manifest(ctx, request, time.Second, out, HomeManifestInput{View: f.input})
		return e
	})
	return doc, cost, e
}
func homeSourceContent(t *testing.T, f *browseFixture, r *HomeManifestSourceReader, in HomeManifestContentInput) (HomeManifestContentPage, BrowseReadResult, error) {
	t.Helper()
	var page HomeManifestContentPage
	var cost BrowseReadResult
	live, request, output := liveRequests("home-content")
	request.Budget.RawRows = 2048
	request.Budget.VMOps = 200000
	request.Budget.Pages = 4096
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, cost, e = r.Content(ctx, request, time.Second, out, in)
		return e
	})
	return page, cost, e
}
func homeSourcePolicySeed(t *testing.T, f *browseFixture, value bool, source string) {
	t.Helper()
	bit := 0
	if value {
		bit = 1
	}
	f.exec(t, `INSERT INTO server_settings(singleton,updated_ms,"home.communityActivityEnabled") VALUES(1,1,?)`, bit)
	f.exec(t, `INSERT INTO console_settings_revision(singleton,revision) VALUES(1,4)`)
	if source != "default" {
		f.exec(t, `INSERT INTO server_setting_sources(field,source) VALUES('home.communityActivityEnabled',?)`, source)
	}
}

func TestRBHomeManifestCommittedPolicy(t *testing.T) {
	b, e := os.ReadFile("testdata/rb-home-manifest-policy-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != "99315d068ed9397dab64e904aa328bc7e598a84bdd027d3271daa749c5e1e765" {
		t.Fatal("accepted parser oracle changed without its external producer")
	}
	var oracle struct {
		Producer, Blob, Key string
		Rows                []struct {
			Input  string
			Value  bool
			Source string
			Locked bool
		}
	}
	if json.Unmarshal(b, &oracle) != nil || oracle.Blob != "8e09a3caefff32fc93db4753e884575c6cea18e2" || oracle.Key != "people.community_activity" || len(oracle.Rows) != 4 {
		t.Fatal("wrong frozen parser oracle")
	}
	// Actual parser was run OUTSIDE this native test binary. Native tests consume
	// only checksum-pinned outputs and actual normalized DB facts, no legacy graph.
	for n, row := range oracle.Rows {
		homeSourceCase(t, fmt.Sprint(n), func(t *testing.T) {
			f, r := homeSourceFixture(t, func(f *browseFixture) {
				if row.Source != "default" {
					homeSourcePolicySeed(t, f, row.Value, row.Source)
				}
			})
			doc, _, e := homeSourceManifest(t, f, r)
			if e != nil || !doc.CommunityPolicy.Resolved || doc.CommunityPolicy.Enabled != row.Value || !doc.CommunityPolicy.CommittedOnly || doc.CommunityPolicy.DiskFreshnessProved {
				t.Fatal(doc.CommunityPolicy, e)
			}
			state := "disabled_by_owner"
			if row.Value {
				state = "available"
			}
			descriptor := homeManifestFind(doc.Manifest.Catalogue, "community_watching")
			if descriptor == nil || descriptor.PolicyState != state || descriptor.ReadState != "unsupported" {
				t.Fatal("setting fabricated community content", descriptor)
			}
			if row.Source == "file" && doc.CommunityPolicy.Provenance != "committed_file_import" {
				t.Fatal(doc.CommunityPolicy)
			}
		})
	}
	homeSourceCase(t, "pending-app-value-wins-and-is-fenced", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) {
			homeSourcePolicySeed(t, f, false, "server")
			f.exec(t, `INSERT INTO server_configuration_pending(field,revision) VALUES('home.communityActivityEnabled',4)`)
		})
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE server_configuration_pending SET revision=3 WHERE field='home.communityActivityEnabled'`)
			}
		}
		r.base.native.snapshots = hook
		doc, cost, e := homeSourceManifest(t, f, r)
		p := doc.CommunityPolicy
		if e != nil || cost.Attempts != 2 || !p.Resolved || p.Enabled || p.PendingRevision == nil || *p.PendingRevision != 3 || p.Provenance != "committed_pending_file_projection" {
			t.Fatal(p, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
	homeSourceCase(t, "unsealed-file-value-does-not-resolve", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) {
			f.exec(t, `INSERT INTO server_settings(singleton,"home.communityActivityEnabled") VALUES(1,1)`)
			f.exec(t, `INSERT INTO server_setting_sources(field,source) VALUES('home.communityActivityEnabled','file')`)
		})
		doc, _, e := homeSourceManifest(t, f, r)
		if e != nil || doc.CommunityPolicy.Resolved || doc.CommunityPolicy.Reason != "committed_settings_revision_missing" || homeManifestFind(doc.Manifest.Catalogue, "community_watching").PolicyState != "unresolved" {
			t.Fatal(doc.CommunityPolicy, e)
		}
	})
}
func homeSourceViewSeed(t *testing.T, f *browseFixture) {
	t.Helper()
	homeManifestScope(t, f, "a", "p", 1)
	homeManifestList(t, f, "a", "p", "home.rowOrder", []string{"view:home"})
	savedViewAdd(t, f, "home", "Home saved view", "a", savedViewTestDefinition)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,watchlisted,not_interested,rating) SELECT 'p',public_id,id,library_id,1,1,1,0 FROM catalog_entities WHERE id=1`)
}
func TestRBHomeManifestContentSources(t *testing.T) {
	homeSourceCase(t, "saved-view-with-actual-UO-state", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) { homeSourceViewSeed(t, f) })
		in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
		in.View.Limit = 1
		p, _, e := homeSourceContent(t, f, r, in)
		if e != nil || len(p.Items) != 1 || p.Items[0].ID != 1 || !p.Items[0].Personal.Favorite || !p.Items[0].Personal.Watchlisted || !p.Items[0].Personal.NotInterested || p.Items[0].Personal.Rating == nil || *p.Items[0].Personal.Rating != 0 || p.Next == nil || p.ResourceRevision != 1 {
			t.Fatal(p, e)
		}
		in.After = p.Next
		p, _, e = homeSourceContent(t, f, r, in)
		if e != nil || len(p.Items) != 1 || p.Items[0].ID != 2 || p.Items[0].Personal.Favorite || p.Items[0].Personal.Rating != nil {
			t.Fatal("sparse actual state is not defaulted correctly", p, e)
		}
		data, _ := os.ReadFile("rb_home_manifest_sources_test.go")
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
			if stats == plan.Analyzed {
				f.exec(t, "ANALYZE")
			}
			captureBrowse(t, f, rbHomeManifestSourceDefinitions(), stats, hash)
		}
	})
	homeSourceCase(t, "filtered-raw-cap-is-pending", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) {
			homeSourceViewSeed(t, f)
			f.exec(t, `UPDATE saved_resources SET definition=? WHERE id='home'`, `{"libraryId":"movies","pivot":"movies","query":{"variant":"predicate","field":"year","operator":"equals","value":2020},"sort":[{"field":"title","direction":"asc"}],"presentation":"grid"}`)
			f.exec(t, `UPDATE catalog_entities SET year=2020,retired=0 WHERE id=100`)
		})
		in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
		in.View.Limit = 1
		p, _, e := homeSourceContent(t, f, r, in)
		if e != nil || len(p.Items) != 0 || p.RawRows != 64 || p.RawPositions != 64 || p.Exhausted || p.Next == nil {
			t.Fatal("raw work became an empty complete shelf", p, e)
		}
		in.After = p.Next
		p, _, e = homeSourceContent(t, f, r, in)
		if e != nil || len(p.Items) != 1 || p.Items[0].ID != 100 {
			t.Fatal("filtered tail became unreachable", p, e)
		}
	})

	homeSourceCase(t, "unversioned-personal-state-change", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) { homeSourceViewSeed(t, f) })
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 3 {
				f.exec(t, `UPDATE personal_items SET favorite=0,rating=NULL WHERE profile_id='p' AND item_id=1`)
			}
		}
		r.base.native.snapshots = hook
		in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
		in.View.Limit = 1
		p, cost, e := homeSourceContent(t, f, r, in)
		if e != nil || cost.Attempts != 2 || len(p.Items) != 1 || p.Items[0].Personal.Favorite || p.Items[0].Personal.Rating != nil {
			t.Fatal("old personal facts published", p, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
	homeSourceCase(t, "current-layout-hiding-rejects-content", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) { homeSourceViewSeed(t, f) })
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 3 {
				homeManifestList(t, f, "a", "p", "home.hiddenRowIds", []string{"view:home"})
			}
		}
		r.base.native.snapshots = hook
		p, _, e := homeSourceContent(t, f, r, HomeManifestContentInput{View: f.input, RowID: "view:home"})
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, HomeManifestContentPage{}) {
			t.Fatal("hidden home content published", p, e)
		}
	})
	homeSourceCase(t, "unsupported-family-is-not-successful-empty", func(t *testing.T) {
		f, r := homeSourceFixture(t, nil)
		for _, id := range []string{"continue", "recommended", "on_now", "new_on_server", "listen_next", "continue_books"} {
			p, _, e := homeSourceContent(t, f, r, HomeManifestContentInput{View: f.input, RowID: id})
			if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(p, HomeManifestContentPage{}) {
				t.Fatal("fake successful row", id, p, e)
			}
		}
	})
	homeSourceCase(t, "other-personal-profile-is-not-borrowed", func(t *testing.T) {
		f, r := homeSourceFixture(t, func(f *browseFixture) {
			homeSourceViewSeed(t, f)
			f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='b'`)
			f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,rating) SELECT 'q',public_id,id,library_id,0,5 FROM catalog_entities WHERE id=1`)
		})
		in := HomeManifestContentInput{View: f.input, RowID: "view:home"}
		in.View.Limit = 1
		p, _, e := homeSourceContent(t, f, r, in)
		if e != nil || len(p.Items) != 1 || !p.Items[0].Personal.Favorite || p.Items[0].Personal.Rating == nil || *p.Items[0].Personal.Rating != 0 {
			t.Fatal("other profile's state borrowed", p, e)
		}
	})
}

// Match the integrated native functional controls without repeating their
// matrices. Native SQL/physical cleanup still runs; only functional clock time
// is virtualized inside each new test case.
func homeSourceCase(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, fn) })
}
