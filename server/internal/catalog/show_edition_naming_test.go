package catalog

import "testing"

func TestEpisodeShowEditionKeepsFileVersionEditionSeparate(t *testing.T) {
	plan := ParseEpisode("Babylon 5 (1994) {edition-BlurayRemaster}/Season 01/Babylon 5 S01E01 {edition-Extended}.mkv", "tv")
	if plan.Issue != "" || plan.ShowEdition != "BlurayRemaster" || plan.Edition != "Extended" || plan.ShowTitle != "Babylon 5" {
		t.Fatalf("folder namespace/version facts conflated: %+v", plan)
	}
}
