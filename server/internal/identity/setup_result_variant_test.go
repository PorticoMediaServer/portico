package identity

import (
	"encoding/json"
	"testing"
)

// R-09b: the setup result is told apart by a required const kind.
func TestSetupResultCarriesKind(t *testing.T) {
	for _, c := range []struct {
		result SetupResult
		want   string
	}{
		{SetupResult{Envelope: Envelope{AccessToken: "a"}}, "session"},
		{SetupResult{Pending: &PendingAccountSetup{ServerID: "s", AuthMode: "hosted", ClaimRequired: true}}, "claim-required"},
	} {
		raw, err := json.Marshal(c.result)
		var tag struct{ Variant string }
		if err != nil || json.Unmarshal(raw, &tag) != nil || tag.Variant != c.want {
			t.Fatalf("%s: %s %v", c.want, raw, err)
		}
	}
	alternatives := (SetupResult{}).JSONSchema()["oneOf"].([]any)
	seen := map[string]bool{}
	for _, a := range alternatives {
		shape := a.(map[string]any)
		seen[shape["properties"].(map[string]any)["variant"].(map[string]any)["const"].(string)] = true
		found := false
		for _, name := range shape["required"].([]string) {
			found = found || name == "variant"
		}
		if !found {
			t.Fatal("variant not required")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("kinds: %v", seen)
	}
}
