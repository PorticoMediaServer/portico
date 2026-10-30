package apikit

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func audienceFixture() Metadata {
	return Metadata{Audience: Clients, ID: "audience_fixture", Method: "GET", Path: "/v1/audience-fixture", Summary: "Audience fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}
}

func TestAudienceRequiredAtEveryRegistrationBoundary(t *testing.T) {
	for _, audience := range []Audience{"", "invalid", "clients", "Integrations "} {
		t.Run(string(audience), func(t *testing.T) {
			m := audienceFixture()
			m.Audience = audience
			handler := func(context.Context, *http.Request, struct{}) (struct{}, error) { return struct{}{}, nil }
			attempts := map[string]func() error{
				"typed": func() error { return Register(New(nil, nil), Route[struct{}, struct{}]{Metadata: m, Handler: handler}) },
				"raw": func() error {
					return RegisterRaw(New(nil, nil), RawRoute[struct{}, struct{}]{Metadata: m, Serve: func(http.ResponseWriter, *http.Request) {}})
				},
				"http": func() error { return RegisterHTTP[struct{}, struct{}](New(nil, nil), m) },
				"generator": func() error {
					_, err := Generate([]Definition{{Metadata: m, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[struct{}]()}})
					return err
				},
			}
			for name, attempt := range attempts {
				if err := attempt(); err == nil || !strings.Contains(err.Error(), "audience") {
					t.Errorf("%s accepted audience %q, or refused it for an unrelated reason: %v", name, audience, err)
				}
			}
		})
	}
	// Audience labels do not select credential scopes, access, or lane policy.
	for _, audience := range []Audience{Clients, Integrations} {
		for _, scope := range []Scope{ScopeRead, ScopeSession} {
			m := audienceFixture()
			m.Audience, m.Access, m.Requires = audience, Device, scope
			r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
			if err := RegisterHTTP[struct{}, struct{}](r, m); err != nil {
				t.Errorf("audience %q changed scope %q validation: %v", audience, scope, err)
			}
		}
	}
}

func TestCompatibilityRequiresExplicitAudienceProtocolAndExactTemplate(t *testing.T) {
	for _, test := range []struct {
		name, path string
		audience   Audience
		protocol   string
		valid      bool
	}{
		{"native client", "/v1/things", Clients, "", true},
		{"native integration", "/v1/things", Integrations, "", true},
		{"health unchanged", "/health/live", Clients, "", true},
		{"unmarked foreign client", "/Items", Clients, "", false},
		{"unmarked foreign integration", "/Items", Integrations, "", false},
		{"missing audience", "/Items", "", ProtocolCompatibility, false},
		{"wrong audience", "/Items", Clients, ProtocolCompatibility, false},
		{"unknown protocol", "/Items", Integrations, "some-other-protocol", false},
		{"unknown protocol native", "/v1/things", Integrations, "some-other-protocol", false},
		{"marker is foreign only", "/v1/things", Integrations, ProtocolCompatibility, false},
		{"foreign exact", "/Items", Integrations, ProtocolCompatibility, true},
		{"foreign template", "/Items/{id}/Images/{type}", Integrations, ProtocolCompatibility, true},
		{"legacy prefix", "/mediabrowser/Users/{id}/Items", Integrations, ProtocolCompatibility, true},
		{"root", "/", Integrations, ProtocolCompatibility, false},
		{"relative", "Items", Integrations, ProtocolCompatibility, false},
		{"subtree", "/Items/", Integrations, ProtocolCompatibility, false},
		{"rest wildcard", "/Items/{path...}", Integrations, ProtocolCompatibility, false},
		{"star", "/Items/*", Integrations, ProtocolCompatibility, false},
		{"root wildcard", "/{root}/Items", Integrations, ProtocolCompatibility, false},
		{"end wildcard", "/Items/{$}", Integrations, ProtocolCompatibility, false},
		{"repeated wildcard", "/Items/{id}/{id}", Integrations, ProtocolCompatibility, false},
		{"bad wildcard", "/Items/{bad-name}", Integrations, ProtocolCompatibility, false},
		{"partial wildcard", "/Items/{id}:action", Integrations, ProtocolCompatibility, false},
		{"empty segment", "/Items//Detail", Integrations, ProtocolCompatibility, false},
		{"dot segment", "/Items/../Users", Integrations, ProtocolCompatibility, false},
		{"query", "/Items?limit=1", Integrations, ProtocolCompatibility, false},
		{"encoded separator", "/Items/%2fUsers", Integrations, ProtocolCompatibility, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := audienceFixture()
			m.Path, m.Audience, m.Protocol = test.path, test.audience, test.protocol
			r := New(nil, nil)
			err := RegisterHTTP[struct{}, struct{}](r, m)
			if (err == nil) != test.valid {
				t.Fatalf("registration error = %v, want valid=%v", err, test.valid)
			}
			if test.valid {
				pattern := m.Method + " " + m.Path
				got, ok := r.MetadataForPattern(pattern)
				if !ok || got.Audience != test.audience || got.Protocol != test.protocol {
					t.Fatalf("registry lost audience/protocol: %#v", got)
				}
			}
		})
	}
}

func TestGeneratePublishesAudienceTagsDescriptionsAndProtocol(t *testing.T) {
	client := audienceFixture()
	integration := audienceFixture()
	integration.ID, integration.Path, integration.Audience = "integration_fixture", "/v1/integrations/things", Integrations
	foreign := integration
	foreign.ID, foreign.Path, foreign.Protocol = "compatibility_fixture", "/Items/{id}", ProtocolCompatibility
	defs := []Definition{}
	for _, m := range []Metadata{client, integration, foreign} {
		defs = append(defs, Definition{Metadata: m, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[struct{}]()})
	}
	out, err := Generate(defs)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Tags  []struct{ Name, Description string }
		Paths map[string]map[string]struct {
			Tags     []string `json:"tags"`
			Audience Audience `json:"x-audience"`
			Protocol string   `json:"x-protocol"`
			Scope    Scope    `json:"x-scope"`
		}
	}
	if err := json.Unmarshal(out.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, tag := range spec.Tags {
		descriptions[tag.Name] = tag.Description
	}
	for _, audience := range []Audience{Integrations, Clients} {
		if descriptions[string(audience)] == "" {
			t.Errorf("missing top-level description for %s", audience)
		}
	}
	for _, d := range defs {
		operation := spec.Paths[d.Path]["get"]
		if !reflect.DeepEqual(operation.Tags, []string{string(d.Audience)}) || operation.Audience != d.Audience || operation.Protocol != d.Protocol || operation.Scope != d.Requires {
			t.Errorf("operation %s metadata changed: %#v", d.ID, operation)
		}
	}
	// Decode generated TS route-table data, rather than inspecting source files.
	_, table, ok := strings.Cut(string(out.Routes), "export const routes = ")
	if !ok {
		t.Fatal("generated route table missing")
	}
	table, _, ok = strings.Cut(table, " as const;")
	if !ok {
		t.Fatal("generated route table terminator missing")
	}
	var routes []Metadata
	if err := json.Unmarshal([]byte(table), &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != len(defs) {
		t.Fatalf("generated routes: %d", len(routes))
	}
	byID := map[string]Metadata{}
	for _, route := range routes {
		byID[route.ID] = route
	}
	for _, d := range defs {
		got := byID[d.ID]
		if got.Audience != d.Audience || got.Protocol != d.Protocol || got.Requires != d.Requires {
			t.Errorf("generated TS route %s lost metadata: %#v", d.ID, got)
		}
	}
}
