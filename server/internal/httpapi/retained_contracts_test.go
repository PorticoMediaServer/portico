package httpapi

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

func TestAnalysisContractsKeepAcceptedQueriesAndReplies(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/items/{id}/analysis", "/v1/items/{id}/analysis/artifacts/{artifact}"} {
		definition, ok := registry.MetadataForPattern("GET " + path)
		if !ok {
			t.Fatal(path)
		}
		fields := []string{"sourceId", "sourceRevision", "mappingRevision", "sessionId", "generation", "positionUS"}
		for _, key := range fields {
			value := "test"
			if key == "generation" || key == "positionUS" {
				value = "0"
			}
			if _, _, err := analysisQuery(httptest.NewRequest("GET", "/v1/items/item/analysis?"+key+"="+value, nil)); err != nil {
				t.Fatalf("handler rejected %s: %v", key, err)
			}
			if !slices.Contains(definition.Query, key) {
				t.Errorf("%s accepts %s but omits it from the contract", path, key)
			}
		}
	}
	for _, pattern := range []string{"POST /v1/items/{id}/analysis/jobs", "POST /v1/libraries/{id}/lyrics/fetch"} {
		metadata, ok := registry.MetadataForPattern(pattern)
		if !ok {
			t.Fatal(pattern)
		}
		if metadata.Status != 202 {
			t.Errorf("%s replies 202; contract says %d", pattern, metadata.Status)
		}
	}
	control, ok := registry.MetadataForPattern("POST /v1/items/{id}/analysis/jobs/{job}/control")
	if !ok || !slices.Contains(control.SuccessStatuses, 202) {
		t.Error("analysis control omits its retry's 202 job reply")
	}
	for _, definition := range registry.Definitions() {
		if definition.ID == "post_items_id_analysis_jobs_job_control" {
			shape, err := apikit.Schema(definition.Response)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := shape["anyOf"]; !ok {
				t.Errorf("control must describe both status and job shapes: %v", shape)
			}
		}
		if definition.Path == "/v1/lyrics/provider" || definition.Path == "/v1/libraries/{id}/lyrics/fetch" {
			if definition.BodyLimit != 2<<20 {
				t.Errorf("%s body limit %d, actual decoder is 2 MiB", definition.Path, definition.BodyLimit)
			}
		}
		if definition.Method != "GET" && slices.Contains([]string{"/v1/items/{id}/lyrics", "/v1/items/{id}/lyrics/{resource}/offset", "/v1/items/{id}/lyrics/{resource}", "/v1/items/{id}/lyrics/search", "/v1/items/{id}/lyrics/selection"}, definition.Path) && len(definition.Query) != 0 {
			t.Errorf("%s rejects all queries but advertises %v", definition.ID, definition.Query)
		}
	}
}

func TestReadinessContractKeepsUnavailableDocument(t *testing.T) {
	d := Dependencies{}
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.registry = registry
	mux := http.NewServeMux()
	d.concurrencyRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/v1/readiness", nil))
	if response.Code != 503 || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("readiness: %d %s", response.Code, response.Body)
	}
	metadata, ok := registry.MetadataForPattern("GET /v1/readiness")
	if !ok || !slices.Contains(metadata.ResponseStatuses, 503) {
		t.Fatal("503 readiness document is missing from the contract")
	}
	// Its payload schema is the ordinary readiness document, not an error envelope.
	for _, definition := range registry.Definitions() {
		if definition.ID == "get_readiness" {
			shape, err := apikit.Schema(definition.Response)
			if err != nil {
				t.Fatal(err)
			}
			if got := shape["required"]; !reflect.DeepEqual(got, []string{"ready", "status"}) {
				t.Fatalf("readiness schema: %v", got)
			}
		}
	}
}
