package httpapi

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

func contractsAPILevel(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../packages/contracts/src/api-level.ts")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The web app and the server it opens must agree on the numbers they speak.
func TestAPILevelMatchesContracts(t *testing.T) {
	raw := contractsAPILevel(t)
	for name, want := range map[string]int{"API_LEVEL": APILevel, "API_MINOR": APIMinor} {
		match := regexp.MustCompile(`export const ` + name + ` = (\d+);`).FindSubmatch(raw)
		if match == nil {
			t.Fatalf("packages/contracts/src/api-level.ts has no %s", name)
		}
		if got, _ := strconv.Atoi(string(match[1])); got != want {
			t.Fatalf("contracts %s %d, server %d", name, got, want)
		}
	}
}

// O-159: one capability registry, mirrored exactly in contracts, sorted and never repeated.
func TestCapabilitiesRegistryMatchesContracts(t *testing.T) {
	match := regexp.MustCompile(`export const CAPABILITIES: readonly string\[\] = (\[[^\]]*\]);`).FindSubmatch(contractsAPILevel(t))
	if match == nil {
		t.Fatal("packages/contracts/src/api-level.ts has no CAPABILITIES")
	}
	var mirrored []string
	if err := json.Unmarshal(match[1], &mirrored); err != nil {
		t.Fatalf("CAPABILITIES must be a JSON-shaped string list: %v", err)
	}
	if !slices.Equal(mirrored, Capabilities) {
		t.Fatalf("contracts CAPABILITIES %v, server %v", mirrored, Capabilities)
	}
	if !slices.IsSorted(Capabilities) || len(slices.Compact(slices.Clone(Capabilities))) != len(Capabilities) {
		t.Fatalf("capabilities must be sorted and unique: %v", Capabilities)
	}
	ids := map[string]bool{}
	for _, entry := range CapabilityRegistry {
		if ids[entry.ID] || entry.ID == "" || entry.Since == "" {
			t.Fatalf("registry entries are unique, named and dated: %+v", entry)
		}
		ids[entry.ID] = true
		if entry.Since == "1.0" && slices.Contains(Capabilities, entry.ID) {
			t.Fatalf("baseline %s is never advertised", entry.ID)
		}
	}
	// The mechanism: a feature added after 1.0 is advertised, the baseline never is.
	if got := advertisedCapabilities(append(slices.Clone(CapabilityRegistry), CapabilityEntry{"future.feature", "1.1"})); !slices.Equal(got, append(slices.Clone(Capabilities), "future.feature")) {
		t.Fatalf("a post-1.0 feature must be advertised: %v", got)
	}
	if len(APIMajors) == 0 || APIMajors[len(APIMajors)-1] != APILevel || !slices.IsSorted(APIMajors) {
		t.Fatalf("APIMajors %v must be sorted and end at APILevel %d", APIMajors, APILevel)
	}
}

// O-159: GET /v1/system carries the negotiation facts, with an empty (never null) set at 1.0.
func TestSystemAdvertisesAPIVersionMajorsAndCapabilities(t *testing.T) {
	f := administrationFixtureNew(t)
	response := f.call("GET", "/v1/system", "", "")
	if response.Code != 200 {
		t.Fatalf("system %d %s", response.Code, response.Body)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"apiLevel": "1", "apiVersion": `"1.0"`, "apiMajors": "[1]", "capabilities": "[]"}
	for key, value := range want {
		if string(body[key]) != value {
			t.Fatalf("%s = %s, want %s", key, body[key], value)
		}
	}
}
