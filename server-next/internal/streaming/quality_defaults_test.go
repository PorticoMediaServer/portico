package streaming

import "testing"

func TestSavedQualityReadsTheNetworkLaneAndItsProvenance(t *testing.T) {
	values := stubPreferences{"quality.home": "original", "quality.remote": "480p"}
	sources := map[string]string{"quality.home": "default", "quality.remote": "profile-device-class"}
	for _, tc := range []struct {
		class NetworkClass
		want  string
	}{
		{NetworkLocal, "original"},
		{NetworkRemote, "480p"},
		{NetworkUnknown, "480p"},
	} {
		got := ResolveDeliveryPolicy(WithPreferenceSources(values, sources), tc.class, LocalityUnknown, "wifi", DefaultDeliveryConfiguration())
		if got.QualityMode != tc.want {
			t.Fatalf("%s selected %s, want %s", tc.class, got.QualityMode, tc.want)
		}
	}
	// A stored home choice is independent of the remote choice and its reset.
	sources["quality.home"] = "profile-device-class"
	if got := ResolveDeliveryPolicy(WithPreferenceSources(values, sources), NetworkLocal, LocalityLocal, "wifi", DefaultDeliveryConfiguration()); got.QualityMode != "original" {
		t.Fatal(got)
	}
	sources["quality.remote"] = "default"
	if got := ResolveDeliveryPolicy(WithPreferenceSources(values, sources), NetworkRemote, LocalityRemote, "wifi", DefaultDeliveryConfiguration()); got.QualityMode != "original" {
		t.Fatal(got)
	}
}

func TestSavedMobileQualityHasIndependentProvenance(t *testing.T) {
	values := stubPreferences{"quality.home": "original", "quality.remote": "480p", "quality.mobile": "original"}
	sources := map[string]string{"quality.home": "profile-device-class", "quality.remote": "profile-device-class", "quality.mobile": "default"}
	for _, class := range []NetworkClass{NetworkLocal, NetworkRemote, NetworkUnknown} {
		for _, transport := range []string{"cellular", "mobile", "wwan"} {
			reader := WithPreferenceSources(values, sources)
			if got := ResolveDeliveryPolicy(reader, class, LocalityUnknown, transport, DefaultDeliveryConfiguration()); got.QualityMode != "720p-high" {
				t.Fatal("default mobile provenance inherited a reach-lane choice", class, transport, got)
			}
			sources["quality.mobile"] = "profile-device-class"
			if got := ResolveDeliveryPolicy(reader, class, LocalityUnknown, transport, DefaultDeliveryConfiguration()); got.QualityMode != "original" {
				t.Fatal("explicit mobile Original became the cellular fallback", class, transport, got)
			}
			values["quality.mobile"] = "96p-invalid"
			if got := ResolveDeliveryPolicy(reader, class, LocalityUnknown, transport, DefaultDeliveryConfiguration()); got.QualityMode != "720p-high" {
				t.Fatal("unsupported mobile choice inherited another lane", class, transport, got)
			}
			values["quality.mobile"] = "original"
			sources["quality.mobile"] = "default"
		}
	}
	// The optional transport argument retains the existing non-transport API.
	reader := WithPreferenceSources(values, sources)
	if SavedQualityPreference(reader, NetworkLocal) != "original" || SavedQualityPreference(reader, NetworkRemote) != "480p" {
		t.Fatal("mobile preference replaced the reach-only reader")
	}
}
