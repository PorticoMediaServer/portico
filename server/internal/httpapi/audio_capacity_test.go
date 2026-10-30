package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
)

func TestConversionAdmissionIsTypedRetryable(t *testing.T) {
	w := httptest.NewRecorder()
	failure(w, playback.ErrConversionCapacity)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), `"code":"conversion_capacity"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPlaybackV1ConversionCapacityUsesRetryableEnvelope(t *testing.T) {
	r := apikit.New(nil, v1Catalogue())
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, ID: "capacity", Method: "POST", Path: "/v1/capacity", Summary: "Capacity", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Media, Admission: lanePlayback, Cost: apikit.Constant, BodyLimit: 1024, Status: 201, Errors: []string{"conversion_capacity"}}, func(context.Context, *http.Request, noBody) (noBody, error) {
		return noBody{}, fmt.Errorf("conversion admission: %w", playback.ErrConversionCapacity)
	})
	request := httptest.NewRequest("POST", "/v1/capacity", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), `"code":"conversion_capacity"`) || !strings.Contains(w.Body.String(), `"retry":"same_request"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}

func TestEveryConversionCreatingOperationDeclaresCapacityRefusal(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	needed := map[string]bool{
		"start_playback_session":  false,
		"change_playback_session": false,
		"create_queue":            false,
		"advance_queue":           false,
		"prepare_queue_next":      false,
	}
	for _, operation := range registry.Definitions() {
		if _, ok := needed[operation.ID]; !ok {
			continue
		}
		for _, code := range operation.Errors {
			if code == "conversion_capacity" {
				needed[operation.ID] = true
			}
		}
	}
	for operation, declared := range needed {
		if !declared {
			t.Errorf("%s cannot publish its ordinary capacity refusal", operation)
		}
	}
}
