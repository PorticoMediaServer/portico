package catalog

import "testing"

func TestEpisodeThumbnailModeVocabularyAndDefaults(t *testing.T) {
	for _, kind := range []string{"tv", "anime"} {
		if DefaultEpisodeThumbnailMode(kind) != "provider_then_made" {
			t.Fatalf("wrong %s default", kind)
		}
	}
	for _, kind := range []string{"movie", "music", "audiobook", "book", "comic", "", "unknown"} {
		if DefaultEpisodeThumbnailMode(kind) != "off" {
			t.Fatalf("wrong %s default", kind)
		}
	}
	for _, mode := range []string{"provider_then_made", "provider", "off"} {
		if !ValidEpisodeThumbnailMode(mode) {
			t.Fatalf("rejected %s", mode)
		}
	}
	for _, mode := range []string{"", "provider_then_generated", "provider_only", "Provider", " made", "generated"} {
		if ValidEpisodeThumbnailMode(mode) {
			t.Fatalf("accepted unknown/alias %s", mode)
		}
	}
}

func TestEpisodeThumbnailScanOperationUsesNamedPolicyAndProbe(t *testing.T) {
	for _, tier := range []string{"file_list_only", "basic", "complete"} {
		policy, err := ValidateScanPolicy(ScanPolicy{Tier: tier})
		if err != nil || policy.Allows(EpisodeThumbnailOperation) != (tier == "complete") {
			t.Fatal(tier, policy, err)
		}
	}
	if _, err := ValidateScanPolicy(ScanPolicy{Tier: "custom", Operations: []string{EpisodeThumbnailOperation}}); err == nil {
		t.Fatal("decode stage admitted without actual probe permission")
	}
	policy, err := ValidateScanPolicy(ScanPolicy{Tier: "custom", Operations: []string{"probe", EpisodeThumbnailOperation}})
	if err != nil || !policy.Allows(EpisodeThumbnailOperation) {
		t.Fatal(policy, err)
	}
}
