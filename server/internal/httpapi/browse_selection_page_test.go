package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"net/http/httptest"
	"testing"
)

// Unrelated viewers must not invalidate this page or its exact selection.
func TestInitialSelectionExposureIgnoresOtherViewersPersonalWrites(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	other := f.c.Movie(f.c.Handle("other"), "/other/film.mkv", "Other film", 2020)
	f.c.Drain()
	pending, revision := f.prepare(t, nil)
	r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", nil)
	r.SetPathValue("id", "movies")
	r.Header.Set("Authorization", "Bearer "+f.token)
	p, v, err := f.d.browseSelectionQueryScope(r)
	if err != nil {
		t.Fatal(err)
	}
	access, err := f.d.browseSelectionAccess(p, "movies", v)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.d.browseSelectionExposure(r, p, v, pending.CapturedBasis(), access); err != nil {
		t.Fatalf("before%v", err)
	}
	snapshot := func() [5]int64 {
		var a [5]int64
		err := f.db.QueryRow(`SELECT (SELECT revision FROM library_revisions WHERE library_id='movies'),(SELECT generation FROM catalog_generations WHERE singleton=1),(SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1),(SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1),COALESCE((SELECT revision FROM viewer_revisions WHERE library_id='movies' AND profile_id=?),0)`, v.Profile).Scan(&a[0], &a[1], &a[2], &a[3], &a[4])
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	before := snapshot()
	f.c.Exec(`INSERT INTO personal_items(profile_id,item_id,revision,last_played_at) VALUES('another-viewer',?,1,'2026-01-01T00:00:00Z')`, other.ID)
	after := snapshot()
	err = f.d.browseSelectionExposure(r, p, v, pending.CapturedBasis(), access)
	if err != nil {
		t.Fatalf("exposure error%v", err)
	}
	for _, i := range []int{0, 1, 2, 3, 4} {
		if before[i] != after[i] {
			t.Fatalf("nonselection component%d moved:%v->%v", i, before, after)
		}
	}
	var foreignEpoch int
	if err := f.db.QueryRow(`SELECT revision FROM catalog_selection_profile_epochs WHERE profile_id='another-viewer'`).Scan(&foreignEpoch); err != nil || foreignEpoch <= 0 {
		t.Fatalf("foreign epoch%d: %v", foreignEpoch, err)
	}
	ready, _ := selectionHTTPReady(t, f, browseSelectionQueryPath("movies", pending.QueryID))
	if ready.Total != 1 || len(ready.Selector.Query.SelectionBasis) != 64 {
		t.Fatalf("foreign write lost usable exact selection: %+v", ready)
	}
	// Default fixture composition still has the ordinary synchronous-page branch.
	response := f.request("POST", "/v1/libraries/movies/browse", nil, map[string]any{"pivot": "movies", "limit": 1})
	if response.Code != 200 {
		t.Fatalf("ordinary page%d:%s", response.Code, response.Body.String())
	}
	t.Logf("unchanged movies revision%s; raw/generation/browseEpoch/selectionEpoch/viewer before%v after%v; exposure%v; ordinary page200", revision, before, after, err)
}

func TestInitialPageKeepsRowsWhenOnlySelectionContentChanges(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		policy         bool
		revoke         bool
	}{
		{name: "predicate", mutation: `UPDATE catalog_selection_predicate_epoch SET revision=revision+1 WHERE singleton=1`},
		{name: "classification", mutation: `UPDATE catalog_summary_classification_revision SET revision=revision+1 WHERE singleton=1`, policy: true},
		{name: "policy", mutation: `UPDATE catalog_facet_source_state SET policy_epoch=policy_epoch+1 WHERE singleton=1`, policy: true},
		{name: "revoked session", revoke: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSelectionHTTPFixture(t)
			r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", nil)
			r.SetPathValue("id", "movies")
			r.Header.Set("Authorization", "Bearer "+f.token)
			p, v, err := f.d.browseSelectionQueryScope(r)
			if err != nil {
				t.Fatal(err)
			}
			access, err := f.d.browseSelectionAccess(p, "movies", v)
			if err != nil {
				t.Fatal(err)
			}
			var out catalog.BrowseResult
			var policy string
			err = f.d.compose(r, v.EffectiveRestrictions(), func(cat *catalog.Service) error {
				request := catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 1}
				var e error
				out, e = cat.BrowseEntities(v, request)
				if e != nil {
					return e
				}
				if e = f.d.attachWholeSelectionPreparation(r, p, v, cat, request, browseRequestBody{}, &out); e != nil {
					return e
				}
				policy, e = cat.BrowsePagePolicyFence("movies", v.Profile)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			if out.WholeSelectionPreparing == nil || len(out.Entries) != 1 {
				t.Fatal("missing positive page/control")
			}
			query := out.WholeSelectionPreparing.QueryID
			if tc.revoke {
				f.c.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE authority='local' AND account_id=?`, p.AccountID)
			} else {
				f.c.Exec(tc.mutation)
			}
			err = f.d.exposeBrowseSelectionPage(r, p, v, &out, policy, access)
			if tc.revoke {
				if err == nil {
					t.Fatal("revoked session was downgraded")
				}
				return
			}
			if tc.policy {
				if !errors.Is(err, readprep.ErrStale) {
					t.Fatalf("policy drift was downgraded: %v", err)
				}
				return
			}
			if err != nil || len(out.Entries) != 1 || out.WholeSelectionPreparing != nil || out.WholeSelection != nil || out.WholeSelectionUnavailable == nil || out.WholeSelectionUnavailable.Reason != "source_changed" {
				t.Fatalf("content drift lost page or retained selector: %v %+v", err, out)
			}
			poll := f.request("GET", browseSelectionQueryPath("movies", query), nil, nil)
			if poll.Code != 409 {
				t.Fatalf("stale selection handle accepted: %d %s", poll.Code, poll.Body.String())
			}
			wire, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(wire, []byte(`"source_changed"`)) || bytes.Contains(wire, []byte(`"queryId"`)) || bytes.Contains(wire, []byte(`"selectionBasis"`)) {
				t.Fatalf("stale selection leaked: %s", wire)
			}
		})
	}
}
