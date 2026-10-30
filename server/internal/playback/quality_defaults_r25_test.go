package playback

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

func TestQualityDefaultsR25NetworkProvenanceAndCaps(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	if cfg.MaxVideoBitrateBPS != 0 || cfg.MaxAudioBitrateBPS != 0 || cfg.MaxVideoHeight != 0 {
		t.Fatal(cfg)
	}
	for _, class := range []NetworkClass{NetworkLocal, NetworkRemote, NetworkUnknown} {
		for _, transport := range []string{"wifi", "ethernet", "unknown", "cellular", "mobile", "wwan"} {
			values := WithPreferenceSources(stubPreferences{"quality.home": "original", "quality.remote": "original", "quality.mobile": "720p-high"}, map[string]string{"quality.home": "default", "quality.remote": "default", "quality.mobile": "default"})
			got := ResolveDeliveryPolicy(values, class, LocalityUnknown, transport, cfg)
			want := "original"
			if NormalizeTransportClass(transport) == "cellular" {
				want = "720p-high"
			}
			if got.QualityMode != want || got.NetworkClass != class || got.MaxVideoBitrateBPS != 0 {
				t.Fatalf("%s %s %+v", class, transport, got)
			}
			for _, chosen := range SavedQualities() {
				explicit := WithPreferenceSources(stubPreferences{"quality.home": chosen, "quality.remote": chosen, "quality.mobile": chosen}, map[string]string{"quality.home": "deviceClass", "quality.remote": "deviceClass", "quality.mobile": "deviceClass"})
				if got := ResolveDeliveryPolicy(explicit, class, LocalityUnknown, transport, cfg); got.QualityMode != chosen {
					t.Fatal(chosen, got)
				}
			}
		}
	}
	cfg.MaxVideoBitrateBPS = 2000000
	policy := ResolveDeliveryPolicy(nil, NetworkRemote, LocalityRemote, "cellular", cfg)
	choice, _ := namedQualityChoice(policy.QualityMode)
	target := choice.target(policy, cfg)
	if target.MaxVideoBitrateBPS != 2000000 || target.MaxVideoHeight != 720 {
		t.Fatal(target)
	}
	// A member's inherited zero leaves the server cap in force; explicit smaller caps narrow it.
	choice.AdminMaxVideoBitrateBPS = 1500000
	if got := choice.target(policy, cfg); got.MaxVideoBitrateBPS != 1500000 || got.MaxVideoHeight != 480 {
		t.Fatal(got)
	}
	r, ok := SelectedRung(QualityOffers(1080, true, true, openPolicy(), 1000000), "")
	if !ok || r.ID != "original" {
		t.Fatal(r, ok)
	}
	plan := planFor(t, DeliveryInput{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Policy: openPolicy(), Config: DefaultDeliveryConfiguration()})
	if plan.QualityID != "original" || plan.VideoAction != "original" {
		t.Fatal(plan)
	}
}
