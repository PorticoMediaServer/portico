package httpapi

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

// Restriction coverage follows the actual production registry installed in the
// fixture. Compiled artifact generation is checkpoint-only, so its last API
// projection must not make a newly registered guarded route look absent (or
// keep a removed API looking present). Infrastructure has no API definitions
// and retains its existing inventory and separate admission-completeness test.
func restrictionRouteInventory(t *testing.T, registry *apikit.Registry, infrastructure map[string]string) map[string]string {
	t.Helper()
	if registry == nil {
		t.Fatal("fixture has no production API registry")
	}
	inventory := map[string]string{}
	for pattern, lane := range infrastructure {
		_, path, _ := strings.Cut(pattern, " ")
		if !strings.HasPrefix(path, "/v1/") {
			inventory[pattern] = lane
		}
	}
	for _, route := range registry.Definitions() {
		if route.Admission == "" {
			t.Fatalf("registered API %s has no admission class", route.ID)
		}
		inventory[route.Method+" "+route.Path] = route.Admission
	}
	return inventory
}

func TestRestrictionInventoryUsesLiveAPIsAndRetainsInfrastructure(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	inventory := restrictionRouteInventory(t, registry, map[string]string{"GET /v1/stale-checkpoint-only": "default", "GET ": "default"})
	if _, stale := inventory["GET /v1/stale-checkpoint-only"]; stale {
		t.Fatal("stale generated API widened the live inventory")
	}
	if inventory["GET "] != "default" {
		t.Fatal("non-API infrastructure disappeared")
	}
	for _, route := range registry.Definitions() {
		if inventory[route.Method+" "+route.Path] != route.Admission {
			t.Fatal("live API omitted from restriction inventory", route.ID)
		}
	}
	for _, pattern := range []string{"GET /v1/storage/sources/rclone/providers", "GET /v1/storage/sources/rclone/operations/{id}", "POST /v1/storage/sources/rclone/test", "POST /v1/storage/sources/rclone", "PUT /v1/storage/sources/rclone/{id}"} {
		if _, registered := inventory[pattern]; !registered {
			t.Fatal("actual rclone route absent", pattern)
		}
	}
}
