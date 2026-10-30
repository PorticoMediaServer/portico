package apikit

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

// WireInner is exported so that embedding it promotes its members.
type WireInner struct {
	ID    string `json:"id"`
	Shade string `json:"shade"`
}
type wireOuter struct {
	WireInner
	Shade  string          `json:"shade,omitempty"`
	At     time.Time       `json:"at"`
	Maybe  *time.Time      `json:"maybe,omitempty"`
	Detail json.RawMessage `json:"detail"`
}

func TestSchemaWireTypes(t *testing.T) {
	s, err := Schema(reflect.TypeFor[wireOuter]())
	if err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if got := props["at"]; !reflect.DeepEqual(got, map[string]any{"type": "string", "format": "date-time"}) {
		t.Errorf("time.Time: %v", got)
	}
	if got := props["maybe"]; !reflect.DeepEqual(got, map[string]any{"type": "string", "format": "date-time"}) {
		t.Errorf("*time.Time: %v", got)
	}
	if got := props["detail"]; !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("json.RawMessage: %v", got)
	}
	// The promoted member is present; the outer, shallower one shadows the inner one.
	if got := props["id"]; !reflect.DeepEqual(got, map[string]any{"type": "string"}) {
		t.Errorf("promoted id: %v", got)
	}
	if !reflect.DeepEqual(s["required"], []string{"id", "at", "detail"}) {
		t.Errorf("required %v", s["required"])
	}
	if _, err = Schema(reflect.TypeFor[struct {
		Raw []byte `json:"raw"`
	}]()); err == nil {
		t.Error("[]byte accepted without an explicit wire type")
	}
}

type AmbiguousA struct {
	Name string `json:"name"`
}
type AmbiguousB struct {
	Name string `json:"name"`
}
type embedsTime struct {
	time.Time
}

func TestSchemaRefusesWhatEncodingJSONWouldDropOrReplace(t *testing.T) {
	// Built with reflect because vet rightly rejects the duplicate tag in source.
	ambiguous := reflect.StructOf([]reflect.StructField{
		{Name: "AmbiguousA", Type: reflect.TypeFor[AmbiguousA](), Anonymous: true},
		{Name: "AmbiguousB", Type: reflect.TypeFor[AmbiguousB](), Anonymous: true},
	})
	if _, err := Schema(ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("same-depth duplicate accepted: %v", err)
	}
	if _, err := Schema(reflect.TypeFor[embedsTime]()); err == nil {
		t.Error("embedded marshaler accepted")
	}
	if _, err := Schema(reflect.TypeFor[struct{ *AmbiguousA }]()); err == nil {
		t.Error("embedded pointer accepted")
	}
}

func TestGenerateWithOptions(t *testing.T) {
	plain := []Definition{{Metadata: Metadata{Audience: Clients, ID: "plain", Method: "GET", Path: "/v1/plain", Summary: "Plain.", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[testResponse]()}}
	server, err := Generate(plain)
	if err != nil {
		t.Fatal(err)
	}
	same, err := GenerateWith(plain, Options{})
	if err != nil || !reflect.DeepEqual(server, same) {
		t.Fatal("zero Options differ from Generate", err)
	}
	if !bytes.Contains(server.OpenAPI, []byte(`"title": "Portico API"`)) || !bytes.Equal(server.Runtime, runtimeTemplate) {
		t.Error("server contract changed")
	}
	withAny := append(plain, Definition{Metadata: Metadata{Audience: Clients, ID: "free", Method: "GET", Path: "/v1/free", Summary: "Free.", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[wireOuter]()})
	hosted, err := GenerateWith(withAny, Options{Title: "Portico Hosted API", Errors: []apierror.Definition{}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(hosted.OpenAPI, []byte(`"title": "Portico Hosted API"`)) {
		t.Error("title ignored")
	}
	if !bytes.Contains(hosted.Errors, []byte("export const errors = [] as const;")) {
		t.Errorf("errors: %s", hosted.Errors)
	}
	if !bytes.Contains(hosted.Runtime, []byte("if (schema.type === undefined) return input;")) || !bytes.Contains(hosted.Runtime, []byte("type?: string")) {
		t.Error("runtime cannot pass through any-JSON members")
	}
	if !bytes.Contains(hosted.Typescript, []byte(`"detail": unknown`)) || !bytes.Contains(hosted.Typescript, []byte(`"maybe"?: string`)) {
		t.Errorf("types: %s", hosted.Typescript)
	}
}

// A pointer member without omitempty is always sent and null when unset: it is
// required and nullable, typed `T | null`, and the runtime accepts null for it.
// Contracts without one keep the original runtime.
func TestNullablePointerMembers(t *testing.T) {
	type presence struct {
		Online bool       `json:"online"`
		Seen   *time.Time `json:"seen"`
		Maybe  *string    `json:"maybe,omitempty"`
	}
	s, err := Schema(reflect.TypeFor[presence]())
	if err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if props["seen"].(map[string]any)["nullable"] != true || props["maybe"].(map[string]any)["nullable"] != nil {
		t.Fatalf("nullable flags wrong: %v", props)
	}
	if got := tsType(s); got != `{ "maybe"?: string; "online": boolean; "seen": string | null }` {
		t.Fatal(got)
	}
	with, err := GenerateWith([]Definition{{Metadata: Metadata{Audience: Clients, ID: "presence_get", Method: "GET", Path: "/v1/p", Summary: "Schema presence fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[presence]()}}, Options{Errors: []apierror.Definition{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with.Runtime), "if (input === null && schema.nullable) return null;") {
		t.Fatal("nullable runtime not applied")
	}
	type plain struct {
		Online bool `json:"online"`
	}
	without, err := GenerateWith([]Definition{{Metadata: Metadata{Audience: Clients, ID: "plain_get", Method: "GET", Path: "/v1/q", Summary: "Schema presence fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[plain]()}}, Options{Errors: []apierror.Definition{}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(without.Runtime, runtimeTemplate) {
		t.Fatal("runtime changed for a contract without nullable members")
	}
}

func TestGeneratedBinaryResponseUsesItsContentType(t *testing.T) {
	definitions := []Definition{{Metadata: Metadata{Audience: Clients, ID: "subtitle_original", Method: "GET", Path: "/v1/media/{grant}/original", Summary: "Read original subtitle bytes", Access: Public, Requires: ScopeOpen, Lane: Media, Cost: Constant, Status: 200, ResponseContentType: "application/octet-stream"}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[string]()}}
	out, e := Generate(definitions)
	if e != nil {
		t.Fatal(e)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Scope     Scope `json:"x-scope"`
			Responses map[string]struct {
				Content map[string]any `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if e = json.Unmarshal(out.OpenAPI, &spec); e != nil {
		t.Fatal(e)
	}
	content := spec.Paths["/v1/media/{grant}/original"]["get"].Responses["200"].Content
	if spec.Paths["/v1/media/{grant}/original"]["get"].Scope != ScopeOpen {
		t.Fatal("binary response lost its credential scope")
	}
	if _, ok := content["application/octet-stream"]; !ok {
		t.Fatal(content)
	}
	if _, ok := content["application/json"]; ok {
		t.Fatal("binary response labelled JSON")
	}
}

func TestHTTPDocumentationKeepsMultipartHeadersAndEmptyResponses(t *testing.T) {
	definitions := []Definition{{Metadata: Metadata{Audience: Clients, ID: "upload", Method: "POST", Path: "/v1/files/{id}", Summary: "Upload a file", Access: Owner, Requires: ScopeLibrary, Lane: Default, Cost: Constant, Status: 202, BodyLimit: 64, RequestContentTypes: []string{"multipart/form-data"}, Headers: []string{"X-Installation"}, NoResponseBody: true}, Request: reflect.TypeFor[struct {
		File testBinary `json:"file"`
	}](), Response: reflect.TypeFor[struct{}]()}}
	generated, err := Generate(definitions)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(generated.OpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	operation := document["paths"].(map[string]any)["/v1/files/{id}"].(map[string]any)["post"].(map[string]any)
	reply := operation["responses"].(map[string]any)["202"].(map[string]any)
	if _, ok := reply["content"]; ok {
		t.Fatal("empty success advertises a JSON body")
	}
	params := operation["parameters"].([]any)
	if len(params) != 2 || params[0].(map[string]any)["in"] != "path" || params[1].(map[string]any)["in"] != "header" {
		t.Fatalf("parameters: %v", params)
	}
	request := operation["requestBody"].(map[string]any)["content"].(map[string]any)["multipart/form-data"].(map[string]any)["schema"].(map[string]any)
	if request["properties"].(map[string]any)["file"].(map[string]any)["format"] != "binary" {
		t.Fatal("multipart file must be binary")
	}
}

type testBinary string

func (testBinary) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "format": "binary"}
}

func TestTSTypeDecodedRequiredProperties(t *testing.T) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"},"note":{"type":"string"}}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := tsType(decoded); got != `{ "note"?: string; "value": string }` {
		t.Fatalf("decoded required properties changed: %s", got)
	}
}
