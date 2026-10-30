package identity

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestCurrentErasureRegistryResolvesBaselineKeysAndFKChildren(t *testing.T) {
	s, _, _ := directFixture(t)
	entries, err := InspectErasureRegistry(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(CurrentErasureRegistry()) {
		t.Fatal("erasure entries disappeared")
	}
	var profileFence, history, accountChildren, preferences bool
	for _, e := range entries {
		if e.Rule.Handler == "" || e.Rule.Owner == "" || e.Rule.Effect == "" || len(e.PrimaryKey) == 0 {
			t.Fatalf("incomplete rule %+v", e)
		}
		switch e.Rule.Table {
		case "playback_personal_profile_fences":
			profileFence = e.Rule.Blocker != ""
		case "play_history":
			history = e.Rule.Blocker != ""
		case "accounts":
			accountChildren = len(e.Children) > 0
		case "preference_scopes":
			preferences = len(e.Children) > 0
		}
	}
	if !profileFence || !history || !accountChildren || !preferences {
		t.Fatal("retention exceptions or reverse ownership edges missing")
	}
	if path := os.Getenv("PORTICO_ERASURE_REGISTRY_REPORT"); path != "" {
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}

}
