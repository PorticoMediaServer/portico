package apikit

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPreparingResponseHeadersStayOnTheirStatus(t *testing.T) {
	metadata := Metadata{Audience: Clients, ID: "prepared_read", Method: "GET", Path: "/v1/prepared-read", Summary: "Prepared read", Access: Public, Requires: ScopeOpen, Lane: Browsing, Cost: PageSized, Status: 200, SuccessStatuses: []int{202},
		ResponseHeaders: []ResponseHeader{{Status: 202, Name: "Location", Description: "Same-server poll path", Required: true}, {Status: 202, Name: "Retry-After", Description: "Minimum delay before polling", Required: true}}}
	if err := validate(metadata); err != nil {
		t.Fatal(err)
	}
	generated, err := Generate([]Definition{{Metadata: metadata, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[statusResponse]()}})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []any `json:"parameters"`
			Responses  map[string]struct {
				Headers map[string]struct {
					Required bool           `json:"required"`
					Schema   map[string]any `json:"schema"`
				} `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(generated.OpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	operation := document.Paths[metadata.Path]["get"]
	if len(operation.Parameters) != 0 || len(operation.Responses["200"].Headers) != 0 {
		t.Fatal("response headers leaked into request parameters or ready status")
	}
	for _, name := range []string{"Location", "Retry-After"} {
		header, ok := operation.Responses["202"].Headers[name]
		if !ok || !header.Required || header.Schema["type"] != "string" {
			t.Fatalf("missing preparing header %s: %+v", name, header)
		}
	}
}

func TestResponseHeaderMetadataRejectsUnpublishedOrAmbiguousHeaders(t *testing.T) {
	base := Metadata{Audience: Clients, ID: "read", Method: "GET", Path: "/v1/read", Summary: "Read", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}
	for _, headers := range [][]ResponseHeader{
		{{Status: 202, Name: "Location", Description: "Poll"}},
		{{Status: 200, Name: "Bad\r\nName", Description: "Invalid"}},
		{{Status: 200, Name: "Location"}},
		{{Status: 200, Name: "Location", Description: "First"}, {Status: 200, Name: "location", Description: "Second"}},
	} {
		metadata := base
		metadata.ResponseHeaders = headers
		if err := validate(metadata); err == nil {
			t.Fatalf("accepted invalid headers: %+v", headers)
		}
	}
}
