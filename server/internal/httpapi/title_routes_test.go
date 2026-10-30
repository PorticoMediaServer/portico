package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestOwnerListsEmptiesMissingTitlesAndRemovesALibrary(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	d.Scheduler = operations.NewScheduler(operations.New(d.DB))
	h := New(d)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if _, err := d.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('films','Films','movie','/films')`); err != nil {
		t.Fatal(err)
	}
	// Nobody but the owner reaches these.
	for _, probe := range [][2]string{{"GET", "/v1/admin/missing-titles"}, {"POST", "/v1/admin/missing-titles/empty"}, {"POST", "/v1/admin/titles/merge"}, {"POST", "/v1/admin/titles/separate-artist"}, {"DELETE", "/v1/admin/libraries/films"}} {
		if w := call(probe[0], probe[1], "", ""); w.Code != 401 {
			t.Fatalf("%s %s without a session answered %d", probe[0], probe[1], w.Code)
		}
	}
	w := call("GET", "/v1/admin/missing-titles", "", owner.AccessToken)
	var page struct {
		Items []any  `json:"items"`
		Total int    `json:"total"`
		Rule  string `json:"rule"`
		Days  int    `json:"days"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Items == nil || page.Total != 0 || page.Rule != "after_days" || page.Days != 30 {
		t.Fatalf("missing titles: %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", "/v1/admin/missing-titles/empty", `{}`, owner.AccessToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"more":false`) {
		t.Fatalf("empty now: %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", "/v1/admin/titles/merge", `{"fromId":"nope","intoId":"neither"}`, owner.AccessToken); w.Code != 404 {
		t.Fatalf("merging unknown titles answered %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", "/v1/admin/titles/separate-artist", `{"albumIds":["nope"]}`, owner.AccessToken); w.Code != 404 {
		t.Fatalf("separating an unknown album answered %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", "/v1/admin/titles/separate-artist", `{"albumIds":[]}`, owner.AccessToken); w.Code != 400 {
		t.Fatalf("separating no album answered %d %s", w.Code, w.Body.String())
	}
	var revision int64
	if err := d.DB.QueryRow(`SELECT revision FROM library_configuration WHERE library_id='films'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	body := func(name string, rev int64) string {
		raw, _ := json.Marshal(map[string]any{"confirmation": name, "expectedRevision": rev})
		return string(raw)
	}
	if w = call("DELETE", "/v1/admin/libraries/films", body("films", revision), owner.AccessToken); w.Code != 409 || !strings.Contains(w.Body.String(), "confirmation_mismatch") {
		t.Fatalf("a wrong name removed the library: %d %s", w.Code, w.Body.String())
	}
	if w = call("DELETE", "/v1/admin/libraries/films", body("Films", revision+5), owner.AccessToken); w.Code != 412 {
		t.Fatalf("a stale revision removed the library: %d %s", w.Code, w.Body.String())
	}
	if w = call("DELETE", "/v1/admin/libraries/films", body("Films", revision), owner.AccessToken); w.Code != 202 || !strings.Contains(w.Body.String(), `"removing":true`) {
		t.Fatalf("remove library: %d %s", w.Code, w.Body.String())
	}
	var retired, queued int
	if err := d.DB.QueryRow(`SELECT (SELECT retired FROM catalog_libraries WHERE library_id='films'),(SELECT count(*) FROM console_operations WHERE kind='library-removal' AND resource='films')`).Scan(&retired, &queued); err != nil || retired != 1 || queued != 1 {
		t.Fatalf("after removal: retired=%d queued=%d %v", retired, queued, err)
	}
	if w = call("GET", "/v1/libraries", "", owner.AccessToken); w.Code != 200 || strings.Contains(w.Body.String(), "Films") {
		t.Fatalf("a library being removed is still listed: %d %s", w.Code, w.Body.String())
	}
	if w = call("DELETE", "/v1/admin/libraries/films", body("Films", revision+1), owner.AccessToken); w.Code != 404 && w.Code != 412 {
		t.Fatalf("removing a library twice answered %d %s", w.Code, w.Body.String())
	}
}
