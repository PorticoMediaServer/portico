package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// The owner registry is the actual producer of the declared Field schema.
// Record it so the generated canonical decoder is checked against a real
// response, including the unlimited caps' explicit null defaults.
func TestConsoleRegistryProducerRecordsNullableDefaults(t *testing.T) {
	d, session, _ := hostedAdministrationFixture(t, "owner")
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	read := supportRequest(mux, session.AccessToken, "/v1/console/registry")
	if read.Code != 200 {
		t.Fatal(read.Code, read.Body.String())
	}
	var reply struct {
		Data struct {
			Fields []operations.Field `json:"fields"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	var nonNull int
	for _, field := range reply.Data.Fields {
		switch field.ID {
		case "perAccountCap", "serverCap":
			if field.Default != nil {
				t.Fatalf("%s default is no longer null: %#v", field.ID, field.Default)
			}
			found[field.ID] = true
		default:
			if field.Default != nil {
				nonNull++
			}
		}
	}
	if !found["perAccountCap"] || !found["serverCap"] || nonNull == 0 {
		t.Fatalf("producer defaults are incomplete: %v, %d non-null defaults", found, nonNull)
	}
	var listDefaults int
	for _, field := range reply.Data.Fields {
		if field.Type == "string-list" && field.Default != nil {
			listDefaults++
		}
	}
	if listDefaults == 0 {
		t.Fatal("producer lost its real list defaults")
	}
	if directory := os.Getenv("PORTICO_CONSOLE_REGISTRY_FIXTURE_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record(filepath.Join(directory, "get-console-registry.json"), "GetConsoleRegistryResponse", "GET", "/v1/console/registry", nil, read, "owner"); err != nil {
			t.Fatal(err)
		}
	}
}
