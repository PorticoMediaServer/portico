package apikit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

// Hosted declares these labels in its own private contract; apikit publishes
// them but never admits them at runtime.
var hostedServiceAccess = []Access{"account_session", "server_credential", "signed_request", "dns_feed", "provider_notification"}

func serviceAccessDefinition(id, path string, access Access, requires Scope) Definition {
	return Definition{
		Metadata: Metadata{Audience: Clients, ID: id, Method: "POST", Path: path, Summary: "Service-authenticated route.", Access: access, Requires: requires, Lane: Default, Cost: Constant, Status: 200},
		Request:  reflect.TypeFor[struct{}](),
		Response: reflect.TypeFor[struct{}](),
	}
}

func hostedServiceAccessDefinitions() []Definition {
	scopes := []Scope{ScopeSession, ScopeAdmin, ScopeSession, ScopeRead, ScopeSession}
	definitions := make([]Definition, 0, len(hostedServiceAccess))
	for index, label := range hostedServiceAccess {
		definitions = append(definitions, serviceAccessDefinition(string(label), "/v1/service-access/"+string(rune('a'+index)), label, scopes[index]))
	}
	return definitions
}

func TestGenerateRefusesServiceAccessLabelsByDefault(t *testing.T) {
	definitions := hostedServiceAccessDefinitions()
	for _, generate := range []struct {
		name string
		run  func([]Definition) (Artifacts, error)
	}{
		{"Generate", func(d []Definition) (Artifacts, error) { return Generate(d) }},
		{"GenerateWith", func(d []Definition) (Artifacts, error) { return GenerateWith(d, Options{}) }},
	} {
		if _, err := generate.run(definitions); err == nil || !strings.Contains(err.Error(), "access rule required") {
			t.Errorf("%s accepted an undeclared service label: %v", generate.name, err)
		}
	}
}

func TestGenerateAcceptsDeclaredServiceAccessAndPreservesLabel(t *testing.T) {
	definitions := hostedServiceAccessDefinitions()
	generated, err := GenerateWith(definitions, Options{ServiceAccessRules: hostedServiceAccess})
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			XAccess Access `json:"x-access"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(generated.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for index, label := range hostedServiceAccess {
		path := "/v1/service-access/" + string(rune('a'+index))
		if got := spec.Paths[path]["post"].XAccess; got != label {
			t.Errorf("%s published x-access %q, want %q", path, got, label)
		}
		if !strings.Contains(string(generated.Routes), `"access": "`+string(label)+`"`) {
			t.Errorf("%s lost its service label in the route metadata", label)
		}
	}
	if strings.Contains(string(generated.Routes), `"access": "device"`) {
		t.Error("generation replaced a service label with the generic device rule")
	}
	for index, definition := range definitions {
		if definition.Access != hostedServiceAccess[index] {
			t.Errorf("generation mutated the caller's definition: %q", definition.Access)
		}
	}
	// Declaring a label is local to the call that declared it.
	if _, err = GenerateWith(definitions, Options{}); err == nil || !strings.Contains(err.Error(), "access rule required") {
		t.Errorf("a later default generation inherited a service label: %v", err)
	}
}

func TestGenerateServiceAccessRulesStillRefuseIncompleteMetadata(t *testing.T) {
	open := serviceAccessDefinition("open_service", "/v1/open-service", "account_session", ScopeOpen)
	missingLane := serviceAccessDefinition("no_lane", "/v1/no-lane", "account_session", ScopeSession)
	missingLane.Lane = ""
	missingScope := serviceAccessDefinition("no_scope", "/v1/no-scope", "account_session", "")
	for _, test := range []struct {
		name       string
		rules      []Access
		definition Definition
		want       string
	}{
		{"duplicate rule", []Access{"account_session", "account_session"}, open, "duplicate service access"},
		{"built-in collision", []Access{"device"}, open, "collides with a built-in access rule"},
		{"empty rule", []Access{""}, open, "service access label required"},
		{"undeclared label", []Access{"server_credential"}, open, "access rule required"},
		{"open scope", []Access{"account_session"}, open, "an authenticated route needs a scope"},
		{"missing scope", []Access{"account_session"}, missingScope, "credential scope required"},
		{"missing lane", []Access{"account_session"}, missingLane, "lane required"},
	} {
		_, err := GenerateWith([]Definition{test.definition}, Options{ServiceAccessRules: test.rules})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v, want error containing %q", test.name, err, test.want)
		}
	}
}

func TestServiceAccessGenerationDoesNotAdmitRuntime(t *testing.T) {
	definition := serviceAccessDefinition("account_get", "/v1/account", "account_session", ScopeSession)
	if _, err := GenerateWith([]Definition{definition}, Options{ServiceAccessRules: []Access{"account_session"}}); err != nil {
		t.Fatalf("generation approval: %v", err)
	}
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	served := false
	err := Register(r, Route[struct{}, struct{}]{Metadata: definition.Metadata, Handler: func(context.Context, *http.Request, struct{}) (struct{}, error) {
		served = true
		return struct{}{}, nil
	}})
	if err == nil || !strings.Contains(err.Error(), "access rule required") {
		t.Fatalf("generation approval reached Registry admission: %v", err)
	}
	if served {
		t.Fatal("an unadmitted service-labelled handler ran")
	}

	// Ordinary device admission still runs for a route apikit does admit.
	authorized := false
	r = New(func(context.Context, *http.Request, Metadata) error {
		if !authorized {
			return &apierror.Error{Code: "not_permitted"}
		}
		return nil
	}, nil)
	device := Metadata{Audience: Clients, ID: "device_get", Method: "GET", Path: "/v1/service-access-device", Summary: "Device route.", Access: Device, Requires: ScopeRead, Lane: Default, Cost: Constant, Status: 200}
	if err = Register(r, Route[struct{}, struct{}]{Metadata: device, Handler: func(context.Context, *http.Request, struct{}) (struct{}, error) {
		served = true
		return struct{}{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	served = false
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/service-access-device", nil))
	if w.Code != 403 || served {
		t.Fatalf("device authorization did not run: %d served=%v", w.Code, served)
	}
	authorized = true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/service-access-device", nil))
	if w.Code != 200 || !served {
		t.Fatalf("authorized device request did not reach its handler: %d served=%v", w.Code, served)
	}
}
