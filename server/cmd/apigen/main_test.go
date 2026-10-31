package main

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

func TestGrandfatheredTagsFilterOnlyDevelopment(t *testing.T) {
	tags := map[string]string{"get_live_route $.seeAll": "type", "no_such_route $.seeAll": "type", "get_dev_route $.state": "kind"}
	defs := []apikit.Definition{{Metadata: apikit.Metadata{ID: "get_dev_route"}}}
	if got := grandfatheredFor(false, defs, tags); len(got) != len(tags) {
		t.Fatal("production generation dropped grandfathered entries", got)
	}
	if got := grandfatheredFor(true, defs, tags); len(got) != 1 || got["get_dev_route $.state"] != "kind" {
		t.Fatal("development generation kept other routes' entries", got)
	}
	// What production keeps it for: a stale entry still fails generation.
	_, err := apikit.GenerateWith(nil, apikit.Options{GrandfatheredUnionTags: grandfatheredFor(false, nil, map[string]string{"no_such_route $.seeAll": "type"}), RequireGrandfatheredUse: true})
	if err == nil || !strings.Contains(err.Error(), "matches no response union") {
		t.Fatal("a stale production grandfathered entry did not fail generation", err)
	}
}
