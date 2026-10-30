package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPeopleBatchAndCollectionRoutesAreAuthorizedAndBounded(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('a','Allowed','movie','/a'),('b','Private','movie','/b');`)
	if e != nil {
		t.Fatal(e)
	}
	a1, a1ID := seedHTTPAPICatalogEntity(t, db, "a", compactcatalog.Movie, compactcatalog.ItemKey("/a", "/a/a1.mkv", 0), "Harbor First", 0, nil)
	secret, secretID := seedHTTPAPICatalogEntity(t, db, "b", compactcatalog.Movie, compactcatalog.ItemKey("/b", "/b/secret.mkv", 0), "Harbor Secret", 0, nil)
	if e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if e := compactcatalog.SetCreditsTx(context.Background(), tx, a1ID, "tmdb", []compactcatalog.Credit{{
			PersonKey: "tmdb:42", PersonName: "Ada Lovelace", ProviderPersonID: "42", CreditID: "c1",
			CreditedName: "Ada Lovelace", Role: "Herself", Department: "Acting", Ordinal: 0,
		}}); e != nil {
			return e
		}
		if e := compactcatalog.SetCreditsTx(context.Background(), tx, secretID, "tmdb", []compactcatalog.Credit{{
			PersonKey: "tmdb:99", PersonName: "Hidden Person", ProviderPersonID: "99", CreditID: "c2",
			CreditedName: "Hidden Person", Role: "Herself", Department: "Acting", Ordinal: 0,
		}}); e != nil {
			return e
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", testHostedRootPin(), testHostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: []string{"a"}}}}
	raw, _ := json.Marshal(policy)
	if e = storeHTTPFixturePolicy(db, raw); e != nil {
		t.Fatal(e)
	}
	session, e := issuePorticoFixture(t, db, ident, "member", "p", "member")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		var reader *strings.Reader = strings.NewReader(body)
		r := httptest.NewRequest(method, path, reader)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	directory := request("GET", "/v1/people?q=Lovelace", session.AccessToken, "")
	if directory.Code != 200 {
		t.Fatal("people directory", directory.Code, directory.Body.String())
	}
	var people catalog.PeopleDirectory
	if e = json.Unmarshal(directory.Body.Bytes(), &people); e != nil {
		t.Fatal(e)
	}
	if len(people.People) != 1 || people.People[0].Name != "Ada Lovelace" {
		t.Fatal("directory", people)
	}
	// A person credited only in a library this viewer cannot read is not findable.
	hidden := request("GET", "/v1/people?q=Hidden", session.AccessToken, "")
	var hiddenPage catalog.PeopleDirectory
	if e = json.Unmarshal(hidden.Body.Bytes(), &hiddenPage); e != nil {
		t.Fatal(e)
	}
	if len(hiddenPage.People) != 0 {
		t.Fatal("unauthorized person exposed", hiddenPage)
	}
	person := people.People[0].ID
	page := request("GET", "/v1/people/"+person+"?limit=10", session.AccessToken, "")
	if page.Code != 200 {
		t.Fatal("person page", page.Code, page.Body.String())
	}
	var detail catalog.PersonPage
	if e = json.Unmarshal(page.Body.Bytes(), &detail); e != nil {
		t.Fatal(e)
	}
	if detail.PageInfo.Total != 1 || len(detail.Credits) != 1 || detail.Credits[0].Media.ID != a1 {
		t.Fatal("credits", detail)
	}
	for _, path := range []string{"/v1/people/" + person + "?limit=101", "/v1/people/" + person + "?role=director", "/v1/people/" + person + "?unknown=1"} {
		if w := request("GET", path, session.AccessToken, ""); w.Code != 400 {
			t.Fatal("unbounded person request", path, w.Code, w.Body.String())
		}
	}
	// One character is a valid search; the answer is still one bounded page.
	if w := request("GET", "/v1/people?q=a", session.AccessToken, ""); w.Code != 200 {
		t.Fatal("one-character person search", w.Code, w.Body.String())
	}
	if w := request("GET", "/v1/people/"+person, "", ""); w.Code != 401 {
		t.Fatal("anonymous person page", w.Code)
	}
	if w := request("GET", "/v1/people/does-not-exist", session.AccessToken, ""); w.Code != 404 {
		t.Fatal("unknown person", w.Code, w.Body.String())
	}
	batchBody, _ := json.Marshal(map[string]any{"operationId": "batch-1", "items": []map[string]any{{"itemId": a1, "watchlisted": true}, {"itemId": secret, "watchlisted": true}}})
	batch := request("PUT", "/v1/items/personal-state:batch", session.AccessToken, string(batchBody))
	if batch.Code != 200 {
		t.Fatal("batch", batch.Code, batch.Body.String())
	}
	var receipt catalog.PersonalBatchReceipt
	if e = json.Unmarshal(batch.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	// The authorized row lands; the row outside this viewer's libraries does not.
	if receipt.Updated != 1 || receipt.Failed != 1 || !receipt.Results[0].OK || receipt.Results[1].OK {
		t.Fatal("batch outcomes", receipt)
	}
	if w := request("PUT", "/v1/items/personal-state:batch", "", `{"operationId":"batch-2","items":[{"itemId":"a1","watchlisted":true}]}`); w.Code != 401 {
		t.Fatal("anonymous batch", w.Code)
	}
	if w := request("PUT", "/v1/items/personal-state:batch", session.AccessToken, `{"operationId":"batch-3","items":[]}`); w.Code != 400 {
		t.Fatal("empty batch", w.Code, w.Body.String())
	}
	// Library collection batches belong to the owner alone.
	collectionBody, _ := json.Marshal(map[string]any{"addItemIds": []string{a1}})
	if w := request("POST", "/v1/collections/missing/items:batch", session.AccessToken, string(collectionBody)); w.Code != 401 && w.Code != 403 {
		t.Fatal("member reached an owner route", w.Code, w.Body.String())
	}
	if _, ok := handler.(http.Handler); !ok {
		t.Fatal("router")
	}
}
