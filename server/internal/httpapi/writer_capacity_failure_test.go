package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var writerRefusals = []error{
	dbwork.ErrWriterCapacity,
	fmt.Errorf("save playlist: %w", dbwork.ErrWriterCapacity),
	errors.New("database is locked (5) (SQLITE_BUSY)"),
}

// A write the writer turned away, or one that met a lock after admission,
// saved nothing. It answers with the writer's own typed, retryable refusal and
// a Retry-After from 1 to 30 seconds, never "the request could not be saved".
func TestWriterRefusalAnswersWriterCapacity(t *testing.T) {
	for _, err := range writerRefusals {
		w := httptest.NewRecorder()
		failure(w, err)
		body := w.Body.String()
		seconds, _ := strconv.Atoi(w.Header().Get("Retry-After"))
		if w.Code != 503 || !strings.Contains(body, `"code":"writer_capacity"`) || !strings.Contains(body, `"retryable":true`) || seconds < 1 || seconds > 30 {
			t.Fatalf("%v answered %d %q Retry-After=%q", err, w.Code, body, w.Header().Get("Retry-After"))
		}
	}
}

// A typed route that returns the storage error unchanged gets the same answer
// from the catalogue, and the code is part of every write route's contract.
func TestTypedRouteWriterRefusalAnswersWriterCapacity(t *testing.T) {
	registry := apikit.New(func(context.Context, *http.Request, apikit.Metadata) error { return nil }, policyCatalogue())
	registry.WriteErrors("writer_capacity")
	for index, err := range writerRefusals {
		refusal := err
		if registerErr := apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients,
			ID: "write_" + strconv.Itoa(index), Method: "POST", Path: "/v1/write/" + strconv.Itoa(index), Summary: "Write", Access: apikit.Public, Requires: apikit.ScopeOpen,
			Lane: apikit.Default, Cost: apikit.Constant, Status: 204, Errors: []string{"internal"}, BodyLimit: 64,
		}, Handler: func(context.Context, *http.Request, struct{}) (struct{}, error) { return struct{}{}, refusal }}); registerErr != nil {
			t.Fatal(registerErr)
		}
	}
	for _, route := range registry.Definitions() {
		if !strings.Contains(strings.Join(route.Errors, ","), "writer_capacity") {
			t.Fatalf("%s does not declare writer_capacity: %v", route.ID, route.Errors)
		}
	}
	for index := range writerRefusals {
		w := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/v1/write/"+strconv.Itoa(index), strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		registry.ServeHTTP(w, request)
		var body struct {
			Error struct {
				Code         string `json:"code"`
				Retry        string `json:"retry"`
				RetryAfterMs int64  `json:"retryAfterMs"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		seconds, _ := strconv.Atoi(w.Header().Get("Retry-After"))
		if w.Code != 503 || body.Error.Code != "writer_capacity" || body.Error.Retry != "same_request" || seconds < 1 || seconds > 30 || body.Error.RetryAfterMs != int64(seconds)*1000 {
			t.Fatalf("route %d answered %d %s Retry-After=%q", index, w.Code, w.Body.String(), w.Header().Get("Retry-After"))
		}
	}
}

// A read refused behind a WAL reset answers database_busy on both paths, and
// every route, reads included, declares it.
func TestReadPressureAnswersDatabaseBusy(t *testing.T) {
	refusal := fmt.Errorf("read library: %w", dbwork.ErrReadPressure)
	w := httptest.NewRecorder()
	failure(w, refusal)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"database_busy"`) || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("legacy path answered %d %s", w.Code, w.Body.String())
	}
	registry := apikit.New(func(context.Context, *http.Request, apikit.Metadata) error { return nil }, policyCatalogue())
	registry.RouteErrors("database_busy")
	if err := apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		ID: "read", Method: "GET", Path: "/v1/read", Summary: "Read", Access: apikit.Public, Requires: apikit.ScopeOpen,
		Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"internal"},
	}, Handler: func(context.Context, *http.Request, struct{}) (struct{}, error) { return struct{}{}, refusal }}); err != nil {
		t.Fatal(err)
	}
	if errs := registry.Definitions()[0].Errors; !strings.Contains(strings.Join(errs, ","), "database_busy") {
		t.Fatalf("read route does not declare database_busy: %v", errs)
	}
	w = httptest.NewRecorder()
	registry.ServeHTTP(w, httptest.NewRequest("GET", "/v1/read", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"database_busy"`) || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("typed path answered %d %s", w.Code, w.Body.String())
	}
}
