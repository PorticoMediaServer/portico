package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/integrationids"
)

type integrationCountFact struct{ id, library, kind, parent, show string }

// Derive the oracle one identity at a time through the ordinary item/container
// visibility boundary, independently of the integration list/count query.
func (m *restrictionEnv) integrationCountEntities(t *testing.T) []integrationCountFact {
	t.Helper()
	rows, err := m.db.Query(`SELECT pid(e.public_id),cl.library_id,k.name,COALESCE(pid(p.public_id),''),COALESCE(pid(sh.public_id),'') FROM catalog_entities e JOIN catalog_browse_rows b ON b.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_entities p ON p.id=e.parent_id LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id WHERE e.retired=0 AND cl.retired=0 ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []integrationCountFact{}
	for rows.Next() {
		var e integrationCountFact
		if err = rows.Scan(&e.id, &e.library, &e.kind, &e.parent, &e.show); err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	visible := []integrationCountFact{}
	for _, e := range candidates {
		switch e.kind {
		case "movie", "episode", "song", "audiobook_file", "extra", "ebook", "comic":
			err = m.catalog.VisibleItem(context.Background(), m.visibilityOracle, e.id)
		default:
			err = m.catalog.VisibleEntity(context.Background(), m.visibilityOracle, e.kind, e.id)
		}
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		visible = append(visible, e)
	}
	return visible
}

func integrationCountIDs(entities []integrationCountFact, keep func(integrationCountFact) bool) []string {
	ids := []string{}
	for _, e := range entities {
		if keep == nil || keep(e) {
			ids = append(ids, e.id)
		}
	}
	return ids
}

func assertIntegrationIDs(t *testing.T, got, want []string) {
	t.Helper()
	// Equality also proves that no hidden item or restricted container was
	// returned, no visible identity was lost, and no row was duplicated.
	sort.Strings(got)
	want = append([]string{}, want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authorized identities differ: got %v want %v", got, want)
	}
}

func assertNativeIntegrationCount(t *testing.T, m *restrictionEnv, path string, want []string) {
	t.Helper()
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("limit", "2") // Traverse real continuations, including empty tails.
	got := []string{}
	cursors := map[string]bool{}
	for {
		parsed.RawQuery = query.Encode()
		code, body := m.call("GET", parsed.String(), "", "restricted")
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Total *int   `json:"total"`
			Next  string `json:"nextCursor"`
		}
		if code != 200 || json.Unmarshal([]byte(body), &page) != nil {
			t.Fatalf("%s: %d %s", parsed, code, body)
		}
		if page.Total == nil || *page.Total != len(want) {
			t.Fatalf("%s: total=%v want %d authorized identities", parsed, page.Total, len(want))
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if page.Next == "" {
			break
		}
		if cursors[page.Next] || len(got) > len(want) {
			t.Fatal("non-progressing or oversized integration page")
		}
		cursors[page.Next] = true
		query.Set("cursor", page.Next)
	}
	assertIntegrationIDs(t, got, want)
}

func assertCompatibilityIntegrationCount(t *testing.T, m *restrictionEnv, path string, want []string) {
	t.Helper()
	assertCompatibilityCountDomain(t, m, path, want, "item")
}

func assertCompatibilityCountDomain(t *testing.T, m *restrictionEnv, path string, want []string, domain string) {
	t.Helper()
	code, body := m.call("GET", path, "", "restricted")
	var page struct {
		Items []struct {
			ID string `json:"Id"`
		} `json:"Items"`
		Total *int `json:"TotalRecordCount"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &page) != nil {
		t.Fatalf("%s: %d %s", path, code, body)
	}
	if page.Total == nil || *page.Total != len(want) {
		t.Fatalf("%s: total=%v want %d authorized identities", path, page.Total, len(want))
	}
	got := []string{}
	for _, item := range page.Items {
		id, err := (integrationids.Store{DB: m.db}).Decode(context.Background(), domain, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	assertIntegrationIDs(t, got, want)
}

func TestIntegrationCountsMatchExactAuthorizedIdentitiesOnEveryAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	entities := m.integrationCountEntities(t)
	all := integrationCountIDs(entities, nil)
	playable, _ := m.visibleItemCounts(t)
	if len(all) <= playable {
		t.Fatal("fixture must include visible containers as well as playable items")
	}
	assertNativeIntegrationCount(t, m, "/v1/integrations/items", all)
	user := integrationMatrixGUID(m, "user", m.principals["restricted"].ProfileID)
	itemAliases := append(integrationMatrixAliases("/Items"), integrationMatrixAliases("/Users/"+user+"/Items")...)
	for _, path := range itemAliases {
		t.Run(path, func(t *testing.T) {
			assertCompatibilityIntegrationCount(t, m, path+"?Limit=100", all)
			// An offset changes the returned slice, never the full visible total.
			code, body := m.call("GET", path+"?StartIndex=1&Limit=2", "", "restricted")
			var page struct {
				Items            []struct{ ID string }
				TotalRecordCount int
				StartIndex       int
			}
			if code != 200 || json.Unmarshal([]byte(body), &page) != nil || page.TotalRecordCount != len(all) || page.StartIndex != 1 || len(page.Items) != 2 {
				t.Fatalf("offset total: %d %s", code, body)
			}
			want := all[1:3]
			got := []string{}
			for _, item := range page.Items {
				id, err := (integrationids.Store{DB: m.db}).Decode(context.Background(), "item", item.ID)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, id)
			}
			assertIntegrationIDs(t, got, want)
		})
	}
	for _, library := range m.libraries {
		want := integrationCountIDs(entities, func(e integrationCountFact) bool { return e.library == library })
		// A library outside the restricted scope is refused natively; its alias
		// refuses the same request. Exact empty ID filters below cover total zero.
		if !m.visibilityOracle.AllowsLibrary(library) {
			continue
		}
		assertNativeIntegrationCount(t, m, "/v1/integrations/items?libraryId="+url.QueryEscape(library), want)
		for _, path := range itemAliases {
			assertCompatibilityIntegrationCount(t, m, path+"?Limit=100&Recursive=true&ParentId="+integrationMatrixGUID(m, "library", library), want)
		}
	}
	// These aliases count authorized libraries, a distinct domain from titles.
	for _, template := range []string{"/Library/MediaFolders", "/Users/" + user + "/Views"} {
		for _, path := range integrationMatrixAliases(template) {
			assertCompatibilityCountDomain(t, m, path, m.visibilityOracle.Libraries, "library")
		}
	}
	// Native media-info totals count this visible entity's stored source links.
	// A hidden identity must not expose a file count through the same route.
	for _, e := range entities {
		var want int
		if err := m.db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, e.id).Scan(&want); err != nil {
			t.Fatal(err)
		}
		code, body := m.call("GET", "/v1/integrations/items/"+e.id+"/media-info?limit=100", "", "restricted")
		var page struct {
			Total        *int
			MediaSources []struct{ ID string }
			NextCursor   string
		}
		if code != 200 || json.Unmarshal([]byte(body), &page) != nil || page.Total == nil || *page.Total != want || len(page.MediaSources) != want || page.NextCursor != "" {
			t.Fatalf("media count for %s: %d %s", e.id, code, body)
		}
	}

	kinds := map[string]bool{}
	for _, e := range entities {
		kinds[e.kind] = true
	}
	for kind := range kinds {
		want := integrationCountIDs(entities, func(e integrationCountFact) bool { return e.kind == kind })
		assertNativeIntegrationCount(t, m, "/v1/integrations/items?kind="+kind, want)
	}
	for _, show := range m.visibleEntities["show"] {
		for _, part := range []struct{ suffix, kind, alias string }{{"seasons", "season", "Seasons"}, {"episodes", "episode", "Episodes"}} {
			want := integrationCountIDs(entities, func(e integrationCountFact) bool {
				return e.kind == part.kind && (e.parent == show || e.show == show)
			})
			assertNativeIntegrationCount(t, m, "/v1/integrations/shows/"+show+"/"+part.suffix, want)
			for _, path := range integrationMatrixAliases("/Shows/" + integrationMatrixGUID(m, "item", show) + "/" + part.alias) {
				assertCompatibilityIntegrationCount(t, m, path+"?Limit=100", want)
			}
		}
	}
	hidden := []string{}
	for _, ids := range m.hiddenItems {
		hidden = append(hidden, ids...)
	}
	for _, ids := range m.hiddenEntities {
		hidden = append(hidden, ids...)
	}
	if len(hidden) == 0 {
		t.Fatal("fixture must include hidden items and containers")
	}
	for _, id := range hidden {
		assertNativeIntegrationCount(t, m, "/v1/integrations/items?ids="+url.QueryEscape(id), []string{})
		if code, body := m.call("GET", "/v1/integrations/items/"+id+"/media-info", "", "restricted"); code != 404 {
			t.Fatalf("hidden media identity leaked a count: %d %s", code, body)
		}
		for _, path := range itemAliases {
			assertCompatibilityIntegrationCount(t, m, path+"?Ids="+integrationMatrixGUID(m, "item", id), []string{})
		}
	}
}

// Key totals count only the caller's original app grant on every spelling of
// the compatibility route; another grant or owner key must not affect them.
func TestIntegrationCredentialCountsRespectGrantOnEveryAlias(t *testing.T) {
	f := integrationFixture(t)
	app := newApp(t, f, "count-app")
	other := newApp(t, f, "count-other")
	one := appLogin(t, f, app)
	two := appLogin(t, f, other)
	w := f.call("POST", "/v1/integrations/keys", one.Token, `{"name":"Counted key"}`)
	var issued access.IssuedCredential
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &issued) != nil {
		t.Fatal("key issuance failed")
	}
	issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"counts-hidden-owner","name":"Other owner's key","scopes":["admin"]}`)
	want := integrationMatrixCredentialGUID(t, f, issued.Credential.ID)
	for _, path := range integrationMatrixAliases("/Auth/Keys") {
		for _, test := range []struct {
			token string
			count int
		}{{one.Token, 1}, {two.Token, 0}} {
			w = f.call("GET", path, test.token, "")
			var page struct {
				Items            []struct{ ID, AccessToken string }
				TotalRecordCount *int
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.TotalRecordCount == nil || *page.TotalRecordCount != test.count || len(page.Items) != test.count {
				t.Fatalf("%s grant count: status %d", path, w.Code)
			}
			if test.count == 1 && (page.Items[0].ID != want || page.Items[0].AccessToken != issued.Secret) {
				t.Fatalf("%s returned another grant's identity or secret", path)
			}
		}
	}
}

func integrationMatrixCredentialGUID(t *testing.T, f administrationFixture, id string) string {
	t.Helper()
	guid, err := (integrationids.Store{DB: f.deps.DB}).Encode(context.Background(), "key", id)
	if err != nil {
		t.Fatal(err)
	}
	return guid
}
