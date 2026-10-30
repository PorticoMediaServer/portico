package apikit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestQueryIntegerMetadataDoesNotParseQueries(t *testing.T) {
	r := New(nil, nil)
	m := Metadata{Audience: Clients, ID: "get_page", Method: "GET", Path: "/v1/page", Summary: "Read a page", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: PageSized, Status: 200, Query: []string{"limit"}, QueryIntegers: []QueryInteger{{Name: "limit", Minimum: 1, Maximum: 100, Required: true}}}
	if err := RegisterHTTP[struct{}, struct{}](r, m); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := r.MountHTTP(mux, "GET /v1/page", http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { _, _ = w.Write([]byte(q.URL.RawQuery)) })); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "limit=0", "limit=101", "limit=bad"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/page?"+query, nil))
		if w.Code != http.StatusOK || w.Body.String() != query {
			t.Fatalf("metadata changed query %q: %d %q", query, w.Code, w.Body.String())
		}
	}
}

func TestGeneratedQueryIntegerFallbackPolicies(t *testing.T) {
	for name, tc := range map[string]struct {
		integer  QueryInteger
		want     map[string]any
		required bool
	}{
		"strict":            {QueryInteger{Name: "limit", Minimum: 1, Default: 40, Maximum: 100}, map[string]any{"type": "integer", "minimum": float64(1), "default": float64(40), "maximum": float64(100)}, false},
		"legacy clamp":      {QueryInteger{Name: "limit", Minimum: 1, Default: 40, Maximum: 100, DefaultBelowMinimum: true, DefaultOnInvalid: true, Clamped: true}, map[string]any{"type": []any{"integer", "string"}, "x-default-below-minimum": float64(1), "default": float64(40), "x-clamp-maximum": float64(100), "x-default-on-invalid": true}, false},
		"legacy reset":      {QueryInteger{Name: "limit", Minimum: 1, Default: 50, Maximum: 200, DefaultBelowMinimum: true, DefaultAboveMaximum: true}, map[string]any{"type": "integer", "x-default-below-minimum": float64(1), "default": float64(50), "x-default-above-maximum": float64(200)}, false},
		"empty":             {QueryInteger{Name: "limit", Minimum: 1, Default: 50, Maximum: 200, EmptyIsDefault: true}, map[string]any{"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "const": ""}}, "minimum": float64(1), "default": float64(50), "maximum": float64(200), "x-empty-is-default": true}, false},
		"validated clamp":   {QueryInteger{Name: "limit", Minimum: 1, Default: 24, Maximum: 100, ClampMaximum: 24}, map[string]any{"type": "integer", "minimum": float64(1), "default": float64(24), "maximum": float64(100), "x-clamp-maximum": float64(24)}, false},
		"required":          {QueryInteger{Name: "limit", Minimum: 1, Maximum: 50, Required: true}, map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(50)}, true},
		"unbounded absence": {QueryInteger{Name: "limit", Minimum: 0, Maximum: 5000000, NoDefault: true}, map[string]any{"type": "integer", "minimum": float64(0), "maximum": float64(5000000)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := New(nil, nil)
			m := Metadata{Audience: Clients, ID: "get_page", Method: "GET", Path: "/v1/page", Summary: "Read a page", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: PageSized, Status: 200, Query: []string{"limit"}, QueryIntegers: []QueryInteger{tc.integer}}
			if err := RegisterHTTP[struct{}, struct{}](r, m); err != nil {
				t.Fatal(err)
			}
			generated, err := Generate(r.Definitions())
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Paths map[string]map[string]struct {
					Parameters []struct {
						Name     string
						Required bool
						Schema   map[string]any
					}
				}
			}
			if err := json.Unmarshal(generated.OpenAPI, &doc); err != nil {
				t.Fatal(err)
			}
			parameters := doc.Paths["/v1/page"]["get"].Parameters
			if len(parameters) != 1 || parameters[0].Name != "limit" || parameters[0].Required != tc.required || !reflect.DeepEqual(parameters[0].Schema, tc.want) {
				t.Fatalf("generated parameter = %+v, want required=%v schema=%v", parameters, tc.required, tc.want)
			}
		})
	}
}

func TestQueryIntegerRejectsContradictoryFallbackPolicies(t *testing.T) {
	for _, integer := range []QueryInteger{
		{Name: "limit", Minimum: 1, Default: 40, Maximum: 100, Clamped: true, DefaultAboveMaximum: true},
		{Name: "limit", Minimum: 1, Maximum: 100, Required: true, DefaultOnInvalid: true},
		{Name: "limit", Minimum: 1, Maximum: 100, NoDefault: true, DefaultBelowMinimum: true},
		{Name: "limit", Minimum: 1, Default: 40, Maximum: 100, ClampMaximum: 101},
	} {
		r := New(nil, nil)
		m := Metadata{Audience: Clients, ID: "get_page", Method: "GET", Path: "/v1/page", Summary: "Read a page", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: PageSized, Status: 200, Query: []string{"limit"}, QueryIntegers: []QueryInteger{integer}}
		if err := RegisterHTTP[struct{}, struct{}](r, m); err == nil {
			t.Fatalf("registered contradictory integer: %+v", integer)
		}
	}
}
