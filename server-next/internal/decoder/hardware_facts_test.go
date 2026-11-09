package decoder

import "testing"

// The grid answers for the probe playback selected, not for what any card on the
// host might manage: a second backend's verdict cannot claim hardware the server
// will not use now.
func TestHardwareFactsGridFollowsTheSelectedProbe(t *testing.T) {
	devices := []HardwareDevice{{ID: "card-0", Name: "Intel UHD Graphics 770", Path: "/dev/dri/renderD128", Backends: []HardwareBackend{BackendVAAPI}}}
	probes := []HardwareProbe{
		{Backend: BackendVAAPI, Available: true, Device: "/dev/dri/renderD128", DecodeCodecs: map[string]bool{"h264:8": true}, OutputQuality: map[string]bool{"h264:8": true}},
		{Backend: BackendQSV, Available: true, Device: "/dev/dri/renderD128", DecodeCodecs: map[string]bool{"av1:8": true}, OutputQuality: map[string]bool{"av1:8": true}},
	}
	selected := probes[0]
	facts := ComposeHardwareFacts(devices, probes, "auto", &selected)
	cells := map[string]HardwareFactFormat{}
	for _, format := range facts.Formats {
		cells[format.Format] = format
	}
	if !cells["h264"].Decode || !cells["h264"].Encode {
		t.Fatal("the selected backend's own verdict must fill the grid")
	}
	if cells["av1"].Decode || cells["av1"].Encode {
		t.Fatal("another backend's verdict must not claim hardware the selected backend lacks")
	}
}

// A 12-bit success is not evidence for the 10-bit row.
func TestHardwareFactsHEVC10NeedsTheExactCell(t *testing.T) {
	probes := []HardwareProbe{{Backend: BackendNVENC, Available: true, Device: "0", DecodeCodecs: map[string]bool{"hevc:12": true}}}
	selected := probes[0]
	facts := ComposeHardwareFacts(nil, probes, "nvenc", &selected)
	for _, format := range facts.Formats {
		if format.Format == "hevc10" && format.Decode {
			t.Fatal("HEVC 12-bit decode is not HEVC 10-bit evidence")
		}
	}
}

// Discovery alone proves nothing about an adapter's method.
func TestHardwareFactsMethodNeedsAProbe(t *testing.T) {
	devices := []HardwareDevice{{ID: "card-0", Name: "Intel UHD Graphics 770", Path: "/dev/dri/renderD128", Backends: []HardwareBackend{BackendQSV}}}
	facts := ComposeHardwareFacts(devices, nil, "auto", nil)
	if len(facts.Devices) != 1 || facts.Devices[0].Method != "" || facts.Devices[0].InUse {
		t.Fatalf("an unprobed adapter must claim no method and no use: %+v", facts.Devices)
	}
}

// Two cards that share a backend stay distinct: only the selected adapter is in
// use, and only when the probe names it.
func TestHardwareFactsInUseBindsTheSelectedAdapter(t *testing.T) {
	devices := []HardwareDevice{
		{ID: "card-0", Name: "NVIDIA GeForce RTX 4070", Path: "/dev/dri/renderD128", Backends: []HardwareBackend{BackendNVENC}},
		{ID: "card-1", Name: "NVIDIA GeForce RTX 3060", Path: "/dev/dri/renderD129", Backends: []HardwareBackend{BackendNVENC}},
	}
	probes := []HardwareProbe{
		{Backend: BackendNVENC, Available: true, Device: "/dev/dri/renderD128"},
		{Backend: BackendNVENC, Available: true, Device: "/dev/dri/renderD129"},
	}
	selected := probes[1]
	facts := ComposeHardwareFacts(devices, probes, "nvenc", &selected)
	if facts.Devices[0].InUse {
		t.Fatal("the card the probe did not run on cannot be in use")
	}
	if !facts.Devices[1].InUse || facts.Devices[1].Method != "nvenc" {
		t.Fatalf("the selected card must be the one in use: %+v", facts.Devices[1])
	}
}

// Falling back to software because every candidate failed is worth telling the
// owner; choosing software is not.
func TestHardwareFactsFaultOnlyWhenSelectionFellBack(t *testing.T) {
	forgetProbeObservations()
	t.Cleanup(forgetProbeObservations)
	software := HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeEncodeFailed}
	if facts := ComposeHardwareFacts(nil, nil, "auto", &software); facts.Fault == nil {
		t.Fatal("a fallback with no working candidate is a fault")
	}
	chosen := HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}
	if facts := ComposeHardwareFacts(nil, nil, string(BackendSoftware), &chosen); facts.Fault != nil {
		t.Fatal("the owner choosing software is not a fault")
	}
	warming := HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeWarming}
	if facts := ComposeHardwareFacts(nil, nil, "auto", &warming); facts.Fault != nil {
		t.Fatal("a cold cache is not a fault")
	}
}

// A backend probed through its own default selector is in use on the first
// adapter discovery lists for it, and on no other.
func TestHardwareFactsDefaultSelectorClaimsOneAdapter(t *testing.T) {
	devices := []HardwareDevice{
		{ID: "card-0", Name: "Intel UHD Graphics 770", Path: "/dev/dri/renderD128", Backends: []HardwareBackend{BackendVAAPI}},
		{ID: "card-1", Name: "Intel Arc A380", Path: "/dev/dri/renderD129", Backends: []HardwareBackend{BackendVAAPI}},
	}
	selected := HardwareProbe{Backend: BackendVAAPI, Available: true}
	facts := ComposeHardwareFacts(devices, []HardwareProbe{selected}, "auto", &selected)
	if !facts.Devices[0].InUse || facts.Devices[0].Method != "vaapi" {
		t.Fatalf("the backend's default is the first adapter: %+v", facts.Devices[0])
	}
	if facts.Devices[1].InUse || facts.Devices[1].Method != "" {
		t.Fatalf("a second card of the same family stays unclaimed: %+v", facts.Devices[1])
	}
}
