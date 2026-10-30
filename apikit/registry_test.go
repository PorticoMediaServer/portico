package apikit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

type testRequest struct {
	Name string `json:"name"`
}
type testResponse struct {
	Name  string   `json:"name"`
	Items []string `json:"items"`
}

func testMetadata() Metadata {
	return Metadata{Audience: Clients, ID: "test", Method: "POST", Path: "/v1/test", Summary: "Test route", Access: Device, Requires: ScopeRead, Lane: Default, Cost: Constant, BodyLimit: 128, Status: 200}
}
func TestRegistryRequiresAccessAndEnforcesBeforeParsing(t *testing.T) {
	r := New(nil, nil)
	m := testMetadata()
	m.Access = ""
	route := Route[testRequest, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, testRequest) (testResponse, error) {
		t.Fatal("unapproved handler ran")
		return testResponse{}, nil
	}}
	if Register(r, route) == nil {
		t.Fatal("missing access accepted")
	}
	route.Access = Device
	if Register(r, route) == nil {
		t.Fatal("missing authorizer accepted")
	}
	r = New(func(context.Context, *http.Request, Metadata) error { return &apierror.Error{Code: "not_permitted"} }, nil)
	if err := Register(r, route); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/test", strings.NewReader("invalid")))
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestStreamingRouteKeepsRegistryAccessAndQueryRules(t *testing.T) {
	authorized := false
	served := false
	r := New(func(context.Context, *http.Request, Metadata) error {
		if !authorized {
			return &apierror.Error{Code: "unauthorized"}
		}
		return nil
	}, nil)
	m := Metadata{Audience: Integrations, ID: "events", Method: "GET", Path: "/v1/events", Summary: "Stream events", Access: Device, Requires: ScopeRead, Lane: Realtime, Cost: Constant, Status: 200, SSE: true, Query: []string{"after"}}
	if err := Register(r, Route[struct{}, testResponse]{Metadata: m, RawHandler: func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/events?unknown=1", nil))
	if w.Code != 401 || served {
		t.Fatalf("stream bypassed access: %d %v", w.Code, served)
	}
	authorized = true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/events?unknown=1", nil))
	if w.Code != 400 || served {
		t.Fatalf("stream bypassed query rule: %d %v", w.Code, served)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/events?after=0", nil))
	if w.Code != 200 || !served || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream refused: %d %v", w.Code, served)
	}
}
func TestRegistryStrictRequestsTolerantCaching(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	m := testMetadata()
	err := Register(r, Route[testRequest, testResponse]{Metadata: m, Handler: func(_ context.Context, _ *http.Request, in testRequest) (testResponse, error) {
		return testResponse{in.Name, []string{}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"one","name":"two"}`, `{"name":"ok","NAME":"two"}`, `{"NAME":"two"}`, `{"name":"ok","unknown":1}`, `{} {}`} {
		q := httptest.NewRequest("POST", "/v1/test", strings.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	m.Method = "GET"
	m.ID = "read"
	m.BodyLimit = 0
	if err = Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, struct{}) (testResponse, error) {
		return testResponse{"ok", []string{}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/test", nil))
	q := httptest.NewRequest("GET", "/v1/test", nil)
	q.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal("conditional read", w.Code, w.Body)
	}
	r.Freeze()
	if Register(r, Route[struct{}, struct{}]{Metadata: m}) == nil {
		t.Fatal("frozen registry changed")
	}
}

func TestExactFieldNamesPromoteEmbeddedJSONFields(t *testing.T) {
	type Embedded struct {
		ItemID string `json:"itemId"`
	}
	type Request struct {
		Embedded
		Label string `json:"label"`
	}
	var out Request
	if err := decodeStrict([]byte(`{"itemId":"item","label":"ok"}`), &out); err != nil || out.ItemID != "item" {
		t.Fatalf("promoted field rejected: %+v %v", out, err)
	}
	for _, body := range []string{`{"ItemID":"item","label":"ok"}`, `{"itemId":"item","LABEL":"ok"}`} {
		if err := decodeStrict([]byte(body), &out); err == nil {
			t.Fatalf("case-mismatched field accepted: %s", body)
		}
	}
}
func TestCanonicalBodyAndSafeErrors(t *testing.T) {
	a, err := CanonicalJSON([]byte(`{"b":2,"a":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := CanonicalJSON([]byte(`{"a":9007199254740993,"b":2}`))
	if string(a) != string(b) {
		t.Fatal("key order changed digest")
	}
	c, _ := CanonicalJSON([]byte(`{"a":9007199254740992,"b":2}`))
	if string(a) == string(c) {
		t.Fatal("integer precision lost")
	}
	r, _ := apierror.New()
	w := httptest.NewRecorder()
	r.Write(w, "test-id", errors.New("secret path"))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestGeneratedErrorsUseRegistryCatalogue(t *testing.T) {
	catalogue, err := apierror.New(apierror.Definition{Code: "invitation_not_found", Status: 404, Message: "This invitation is not available.", Retry: apierror.Never})
	if err != nil {
		t.Fatal(err)
	}
	r := New(nil, catalogue)
	artifacts, err := GenerateWith(r.Definitions(), Options{Errors: r.ErrorDefinitions()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifacts.Errors), `"code": "invitation_not_found"`) {
		t.Fatal("generated contracts omitted a registered server error")
	}
}

func TestNamedCommandPathsAndEncodedSeparators(t *testing.T) {
	r := New(nil, nil)
	for _, verb := range []string{"move", "cancel"} {
		m := testMetadata()
		m.ID = verb
		m.Path = "/v1/items/{id}:" + verb
		m.Access = Public
		m.Requires = ScopeOpen
		m.BodyLimit = 0
		if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: func(_ context.Context, r *http.Request, _ struct{}) (testResponse, error) {
			return testResponse{r.PathValue("id"), []string{}}, nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/items/abc:move", "/v1/items/abc:cancel"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "abc") {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/items/a%2Fb:move", nil))
	if w.Code != 404 {
		t.Fatal("encoded separator matched", w.Code)
	}
}

func TestEveryRouteMustDeclareACredentialScopeAndTheGateRunsFirst(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	handler := func(context.Context, *http.Request, testRequest) (testResponse, error) {
		return testResponse{"ok", []string{}}, nil
	}
	missing := testMetadata()
	missing.Requires = ""
	if err := Register(r, Route[testRequest, testResponse]{Metadata: missing, Handler: handler}); err == nil {
		t.Fatal("a route with no credential scope was registered")
	}
	open := testMetadata()
	open.Requires = ScopeOpen
	if err := Register(r, Route[testRequest, testResponse]{Metadata: open, Handler: handler}); err == nil {
		t.Fatal("an authenticated route was registered as open")
	}
	public := testMetadata()
	public.Access, public.Requires = Public, ScopeRead
	if err := Register(r, Route[testRequest, testResponse]{Metadata: public, Handler: handler}); err == nil {
		t.Fatal("a public route was registered with a grantable scope")
	}
	m := testMetadata()
	m.Requires = ScopeLibrary
	if err := Register(r, Route[testRequest, testResponse]{Metadata: m, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	var asked []Scope
	refuse := true
	r.GateScopes(func(_ *http.Request, need Scope) error {
		asked = append(asked, need)
		if refuse {
			return &apierror.Error{Code: "not_permitted"}
		}
		return nil
	})
	call := func() int {
		q := httptest.NewRequest("POST", "/v1/test", strings.NewReader(`{"name":"x"}`))
		q.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w.Code
	}
	if code := call(); code != 403 || len(asked) != 1 || asked[0] != ScopeLibrary {
		t.Fatalf("gate refusal: %d %v", code, asked)
	}
	refuse = false
	if code := call(); code != 200 {
		t.Fatalf("gate pass: %d", code)
	}
	if found, ok := r.Lookup("POST", "/v1/test"); !ok || found.Requires != ScopeLibrary {
		t.Fatalf("lookup: %+v %v", found, ok)
	}
	if _, ok := r.Lookup("GET", "/v1/test"); ok {
		t.Fatal("lookup matched another method")
	}
}

func TestAccessLabelsMustNameWhatTheAuthorizerChecks(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	handler := func(context.Context, *http.Request, struct{}) (testResponse, error) {
		return testResponse{"ok", []string{}}, nil
	}
	for path, access := range map[string]Access{"/v1/items/{id}/thing": ViewerItem, "/v1/things": ViewerItem, "/v1/libraries/{id}/thing": ViewerLibrary, "/v1/list": ViewerLibrary} {
		m := Metadata{Audience: Clients, ID: "labelled", Method: "GET", Path: path, Summary: "Labelled", Access: access, Requires: ScopeRead, Lane: Default, Cost: Constant, Status: 200}
		if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: handler}); err == nil {
			t.Fatalf("%s was registered as %s without naming what is checked", path, access)
		}
	}
	for path, access := range map[string]Access{"/v1/items/{itemId}/thing": ViewerItem, "/v1/libraries/{libraryId}/thing": ViewerLibrary} {
		m := Metadata{Audience: Clients, ID: "ok_" + string(access), Method: "GET", Path: path, Summary: "Labelled", Access: access, Requires: ScopeRead, Lane: Default, Cost: Constant, Status: 200}
		if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: handler}); err != nil {
			t.Fatalf("%s as %s: %v", path, access, err)
		}
	}
}

func TestRegistryRejectsNullRequestBeforeHandler(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	m := testMetadata()
	if err := Register(r, Route[testRequest, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, testRequest) (testResponse, error) {
		t.Fatal("null request reached handler")
		return testResponse{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	q := httptest.NewRequest("POST", "/v1/test", strings.NewReader("null"))
	q.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != 400 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestResponseConformancePreservesRequiredAndNullableValues(t *testing.T) {
	type response struct {
		Items    []string `json:"items"`
		Optional []string `json:"optional,omitempty"`
		Nullable *string  `json:"nullable"`
	}
	if _, err := marshalResponse(response{Items: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := marshalResponse(response{}); err == nil {
		t.Fatal("required nil list accepted")
	}
	if _, err := marshalResponse(testResponse{Name: strings.Repeat("x", (512<<10)+1), Items: []string{}}); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestPathRewriteReachesTypedAndHTTPHandlers(t *testing.T) {
	r := New(nil, nil)
	m := testMetadata()
	m.ID, m.Method, m.Path, m.Access, m.Requires, m.BodyLimit = "read_item", "GET", "/v1/items/{id}", Public, ScopeOpen, 0
	if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: func(_ context.Context, q *http.Request, _ struct{}) (testResponse, error) {
		return testResponse{q.PathValue("id"), []string{}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	h := testMetadata()
	h.ID, h.Method, h.Path, h.Access, h.Requires, h.BodyLimit = "item_art", "GET", "/v1/items/{id}/art", Public, ScopeOpen, 0
	if err := RegisterHTTP[struct{}, testResponse](r, h); err != nil {
		t.Fatal(err)
	}
	r.RewritePathValues(func(q *http.Request, name, value string) string {
		if name == "id" && value == "old" && strings.HasPrefix(q.Pattern, "GET /v1/items/") {
			return "new"
		}
		return value
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/items/old", nil))
	if !strings.Contains(w.Body.String(), `"new"`) {
		t.Fatal("typed route missed the rewrite", w.Body)
	}
	mux := http.NewServeMux()
	seen := ""
	if err := r.MountHTTP(mux, "GET /v1/items/{id}/art", http.HandlerFunc(func(_ http.ResponseWriter, q *http.Request) { seen = q.PathValue("id") })); err != nil {
		t.Fatal(err)
	}
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/items/old/art", nil))
	if seen != "new" {
		t.Fatal("HTTP route missed the rewrite", seen)
	}
}

func TestUnregisteredErrorNamesItsRouteAndCode(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	m := testMetadata()
	m.Errors = []string{"missing_declared_outcome"}
	err := Register(r, Route[testRequest, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, testRequest) (testResponse, error) { return testResponse{}, nil }})
	if err == nil || !strings.Contains(err.Error(), m.ID) || !strings.Contains(err.Error(), "missing_declared_outcome") {
		t.Fatalf("missing registration context: %v", err)
	}
	if len(r.routes) != 0 {
		t.Fatal("invalid route was registered")
	}
}

func TestReadRoutesCannotDeclareARequestBody(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	for _, method := range []string{"GET", "HEAD"} {
		m := testMetadata()
		m.ID, m.Method, m.BodyLimit = "read_with_body_"+method, method, 1024
		route := Route[testRequest, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, testRequest) (testResponse, error) { return testResponse{}, nil }}
		if err := Register(r, route); err == nil || !strings.Contains(err.Error(), "read route declares a request body") {
			t.Fatalf("%s route with a body limit was accepted: %v", method, err)
		}
	}
}
