package httpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

// The published API document must describe every route this package registers.
// Standing principle 2a.1 of the Feature Depth Program requires third parties to
// be able to build players and automations against a documented endpoint for
// everything a first-party client can do, so an undocumented route is a defect
// rather than an omission. This test reads the generated JSON artifact and
// fails when the live registry, source bindings and published document drift.

const openAPIPath = "../../api/portico.openapi.json"

var (
	// "METHOD /path" as registered with mux.HandleFunc / mux.Handle.
	routeLiteral = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (/[^"]*)"`)
	// A bare path literal that some handlers concatenate a method onto, or
	// compare r.URL.Path against.
	pathLiteral = regexp.MustCompile(`"(/(?:v[0-9]+|health)/[^"]*)"`)
)

// composedRoutes are registered from fragments joined at run time, so a source
// scan cannot reconstruct them. They are listed here so the document still has
// to carry them; keep this list in step with the loops that build them.
var composedRoutes = []string{
	// onboarding.go: "POST /v1/setup/" + action, "POST /v1/quick-connect/" + action.
	"POST /v1/setup/finish",
	"POST /v1/quick-connect/token",
	"POST /v1/quick-connect/cancel",
	// admin.go: "GET /v1/admin/" + kind.
	"GET /v1/admin/libraries",
	"GET /v1/admin/jobs",
	// discovery.go: method + " /v1/collections/{id}/items/{itemId}".
	"PUT /v1/collections/{id}/items/{itemId}",
	"DELETE /v1/collections/{id}/items/{itemId}",
	// metadata_repair.go and manual_metadata.go: method + " /path".
	"GET /v1/metadata/{kind}/{id}",
	"POST /v1/metadata/{kind}/{id}",
	"GET /v1/items/{id}/metadata/manual",
	"PATCH /v1/items/{id}/metadata/manual",
	// screen_metadata.go: "/v1/" + {items,shows} + "/{id}/metadata/screen" [+ suffix].
	"GET /v1/items/{id}/metadata/screen",
	"PUT /v1/items/{id}/metadata/screen",
	"POST /v1/items/{id}/metadata/screen/search",
	"POST /v1/items/{id}/metadata/screen/retry",
	"GET /v1/shows/{id}/metadata/screen",
	"PUT /v1/shows/{id}/metadata/screen",
	"POST /v1/shows/{id}/metadata/screen/search",
	"POST /v1/shows/{id}/metadata/screen/retry",
	// musicbrainz.go: method + " " + route.path + operation.suffix.
	"GET /v1/albums/{id}/metadata/musicbrainz",
	"PUT /v1/albums/{id}/metadata/musicbrainz",
	"POST /v1/albums/{id}/metadata/musicbrainz/retry",
	"POST /v1/albums/{id}/metadata/musicbrainz/search",
	"POST /v1/albums/{id}/metadata/musicbrainz/policy",
	"GET /v1/items/{id}/metadata/musicbrainz",
	"PUT /v1/items/{id}/metadata/musicbrainz",
	"POST /v1/items/{id}/metadata/musicbrainz/retry",
	"POST /v1/items/{id}/metadata/musicbrainz/search",
	"POST /v1/items/{id}/metadata/musicbrainz/policy",
	// prepared_media.go: "POST /v1/optimization-jobs/{id}/" + action.
	"POST /v1/optimization-jobs/{id}/cancel",
	"POST /v1/optimization-jobs/{id}/retry",
}

type specIndex struct {
	operations map[string]bool
	paths      []string
}

func readSpec(t *testing.T) specIndex {
	t.Helper()
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	index := specIndex{operations: map[string]bool{}}
	for path, methods := range doc.Paths {
		index.paths = append(index.paths, path)
		for method := range methods {
			index.operations[strings.ToUpper(method)+" "+path] = true
		}
	}
	if len(index.operations) == 0 {
		t.Fatal("generated OpenAPI has no operations")
	}
	return index
}

func packageSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	sources := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if developmentOnlySource(raw) {
			// Development-only routes are never in a release build, so they are not part of the API.
			continue
		}
		sources[name] = string(raw)
	}
	if len(sources) == 0 {
		t.Fatal("no package sources found")
	}
	return sources
}

func (s specIndex) hasPrefix(prefix string) bool {
	for _, path := range s.paths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func TestOpenAPICoversRegisteredRoutes(t *testing.T) {
	spec := readSpec(t)
	// The published artifact must cover the live registry; do not add its
	// declarations to the parsed document, which would hide stale generation.
	foundation, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range foundation.Definitions() {
		if !spec.operations[route.Method+" "+route.Path] {
			t.Errorf("generated document is missing %s %s", route.Method, route.Path)
		}
	}

	missing := map[string]string{}
	for name, source := range packageSources(t) {
		for _, m := range routeLiteral.FindAllStringSubmatch(source, -1) {
			route, path := m[1]+" "+m[2], m[2]
			if path == "/receiver/cast/" {
				// The credential-scope table also names the static receiver app.
				// Its HTML/assets are not API operations.
				continue
			}
			if strings.HasSuffix(path, "/") {
				// A concatenation prefix such as "POST /v1/setup/"; the document
				// must describe at least one operation underneath it.
				if !spec.hasPrefix(path) {
					missing[route] = name + " (route prefix)"
				}
				continue
			}
			if !spec.operations[route] {
				missing[route] = name
			}
		}
	}
	for _, route := range composedRoutes {
		if !spec.operations[route] {
			missing[route] = "composed at run time"
		}
	}
	report(t, missing, "registered route is not documented in "+openAPIPath)
}

func TestOpenAPICoversPathLiterals(t *testing.T) {
	spec := readSpec(t)
	// The published artifact must cover the live registry; do not add its
	// declarations to the parsed document, which would hide stale generation.
	foundation, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range foundation.Definitions() {
		if !spec.operations[route.Method+" "+route.Path] {
			t.Errorf("generated document is missing %s %s", route.Method, route.Path)
		}
	}

	missing := map[string]string{}
	for name, source := range packageSources(t) {
		for _, m := range pathLiteral.FindAllStringSubmatch(source, -1) {
			if !spec.hasPrefix(m[1]) {
				missing[m[1]] = name
			}
		}
	}
	report(t, missing, "API path literal has no documented path in "+openAPIPath)
}

func report(t *testing.T, missing map[string]string, message string) {
	t.Helper()
	if len(missing) == 0 {
		return
	}
	keys := make([]string, 0, len(missing))
	for key := range missing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Errorf("%s: %q (%s)", message, key, missing[key])
	}
}

// Published schemas and policy metadata must change together with the router.
func TestPublishedOpenAPIMatchesRegistry(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	components, wire, err := PublishedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := apikit.GenerateWith(registry.Definitions(), apikit.Options{Errors: registry.ErrorDefinitions(), Components: components, WireSchemas: wire, GrandfatheredUnionTags: GrandfatheredUnionTags})
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated.OpenAPI, published) {
		t.Fatal("published API differs from the registry; regenerate with go run ./cmd/apigen")
	}
}
