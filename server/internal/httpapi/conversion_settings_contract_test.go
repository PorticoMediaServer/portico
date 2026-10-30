package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strings"
	"testing"
)

func TestConversionSettingsTypedContractAndOwnerAccess(t *testing.T) {
	d, session, _ := hostedAdministrationFixture(t, "owner")
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	registryRead := supportRequest(mux, session.AccessToken, "/v1/console/registry")
	if registryRead.Code != 200 || !strings.Contains(registryRead.Body.String(), `"ffmpegPath"`) || !strings.Contains(registryRead.Body.String(), `"library.missingTitles"`) {
		t.Fatal(registryRead.Code, registryRead.Body.String())
	}
	read := supportRequest(mux, session.AccessToken, "/v1/admin/console/settings")
	if read.Code != 200 {
		t.Fatal(read.Code, read.Body.String())
	}
	var envelope ConsoleReply[operations.SettingsDocument]
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	values := envelope.Data.Requested
	values.SessionStorageMiB = 8192
	body, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: envelope.Data.Revision, IdempotencyKey: "conversion-settings-1", Values: values})
	request := httptest.NewRequest("PATCH", "/v1/admin/console/settings", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+session.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	written := httptest.NewRecorder()
	mux.ServeHTTP(written, request)
	if written.Code != 200 || !strings.Contains(written.Body.String(), `"sessionStorageMiB":8192`) {
		t.Fatal(written.Code, written.Body.String())
	}
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, definition := range registry.Definitions() {
		key := definition.Method + " " + definition.Path
		routes[key] = true
		if strings.Contains(key, "/console/settings") || key == "GET /v1/console/registry" {
			if definition.Requires != apikit.ScopeAdmin {
				t.Fatal(key, definition.Requires)
			}
		}
	}
	for _, route := range []string{"GET /v1/console/registry", "GET /v1/admin/console/settings", "PATCH /v1/admin/console/settings"} {
		if !routes[route] {
			t.Fatal("missing typed contract", route)
		}
	}
}
func TestConversionSettingsCompleteDocumentRejectsOmissionAndNull(t *testing.T) {
	values := operations.DefaultSettings()
	raw, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: 1, IdempotencyKey: "conversion-fields-1", Values: values})
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	fields := body["values"].(map[string]any)
	delete(fields, "hdrToneMapping")
	incomplete, _ := json.Marshal(body)
	var request ConsoleSettingsChange
	if json.Unmarshal(incomplete, &request) == nil {
		t.Fatal("omitted boolean silently defaulted")
	}
	fields["hdrToneMapping"] = nil
	null, _ := json.Marshal(body)
	if json.Unmarshal(null, &request) == nil {
		t.Fatal("null boolean accepted")
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal("complete default settings refused", err)
	}
	fields["hdrToneMapping"] = values.HDRToneMapping
	fields["transcodingEnabled"] = true
	retired, _ := json.Marshal(body)
	if json.Unmarshal(retired, &request) == nil {
		t.Fatal("retired independent conversion gate accepted")
	}
}

func TestSegmentConfidenceExplicitValuesAreClosedChoices(t *testing.T) {
	raw, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: 1, IdempotencyKey: "confidence", Values: operations.DefaultSettings()})
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(body["values"], &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"segments.viewerConfidencePercent", "segments.automaticConfidencePercent"} {
		original := values[key]
		for _, invalid := range []string{"0", "-0", "null", "49", "100", "50.0", "\"50\""} {
			values[key] = json.RawMessage(invalid)
			body["values"], _ = json.Marshal(values)
			encoded, _ := json.Marshal(body)
			var request ConsoleSettingsChange
			if json.Unmarshal(encoded, &request) == nil {
				t.Fatalf("accepted %s=%s", key, invalid)
			}
		}
		delete(values, key)
		body["values"], _ = json.Marshal(values)
		encoded, _ := json.Marshal(body)
		var request ConsoleSettingsChange
		if err := json.Unmarshal(encoded, &request); err != nil {
			t.Fatal("omission rejected", key, err)
		}
		values[key] = original
	}
}

func TestChapterSettingExplicitBooleanOrOmission(t *testing.T) {
	raw, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: 1, IdempotencyKey: "chapter-policy", Values: operations.DefaultSettings()})
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(body["values"], &values); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		value string
		valid bool
	}{{"true", true}, {"false", true}, {"null", false}, {"0", false}, {"1", false}, {"\"false\"", false}} {
		values["segments.useChapterNames"] = json.RawMessage(candidate.value)
		body["values"], _ = json.Marshal(values)
		encoded, _ := json.Marshal(body)
		var request ConsoleSettingsChange
		err := json.Unmarshal(encoded, &request)
		if (err == nil) != candidate.valid {
			t.Fatalf("value=%s error=%v", candidate.value, err)
		}
	}
	delete(values, "segments.useChapterNames")
	body["values"], _ = json.Marshal(values)
	encoded, _ := json.Marshal(body)
	var request ConsoleSettingsChange
	if err := json.Unmarshal(encoded, &request); err != nil || request.Values.SegmentUseChapterNames != nil {
		t.Fatal("omission", request, err)
	}
}
