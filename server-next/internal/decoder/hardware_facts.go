package decoder

import "sort"

// HardwareFactDevice is one adapter this host exposes: the name its driver gives
// it, the method proven on it by an actual probe, and whether conversions are
// going there now. Nothing here is inferred from a vendor string alone.
type HardwareFactDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Method is the stable name of the backend proven on this adapter, which the
	// owner's screen turns into the frame's own wording (Quick Sync, NVENC, …).
	// Empty means no backend has earned this adapter yet.
	Method string `json:"method,omitempty"`
	// InUse marks the adapter the selected probe ran on: the witness decides, not
	// a shared backend name, so two cards of one vendor are never both marked.
	InUse bool `json:"inUse"`
}

// HardwareFactFormat is one row of the settings grid: whether the backend
// playback selected now is qualified to decode and encode that format in
// hardware. False means that backend's probe has no such cell, so a session
// would plan the processor for it. It describes selected capabilities, not one
// session's executed graph: burn-in, Dolby Vision profile 5 reshaping and a
// mid-session fallback can still differ.
type HardwareFactFormat struct {
	Format string `json:"format"`
	Decode bool   `json:"decode"`
	Encode bool   `json:"encode"`
}

// HardwareFactFault is the one failure worth telling the owner about, in the
// probe's own words. It is published only when no hardware backend works, so an
// absent NVIDIA card on an Intel host is not reported as a fault.
type HardwareFactFault struct {
	Backend string `json:"backend,omitempty"`
	Device  string `json:"device,omitempty"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// HardwareFactReasonNoDevice stands for "no adapter answered at all", which is
// what a server with no graphics card passed in has to say.
const HardwareFactReasonNoDevice = "no_graphics_device"

// HardwareFacts is the measured half of Server › Streaming › Hardware. The
// install's remedy and the frame's format order come from hardwarefacts; every
// value here comes from a probe that ran on this host.
type HardwareFacts struct {
	Devices []HardwareFactDevice
	Formats []HardwareFactFormat
	// Fault is nil while at least one hardware backend works.
	Fault *HardwareFactFault
	// LastTest is when the newest probe finished, in Unix seconds; zero means
	// this process has not probed anything yet.
	LastTest int64
}

// hardwareFactMethods names each backend as the frame's device list does. A
// backend without a name keeps its own token, so a new one never goes blank.
var hardwareFactMethods = map[HardwareBackend]string{
	BackendQSV:          "quick_sync",
	BackendNVENC:        "nvenc",
	BackendVAAPI:        "vaapi",
	BackendAMF:          "amf",
	BackendVideoToolbox: "videotoolbox",
	"rkmpp":             "rkmpp",
}

// hardwareFactFormatOrder is the grid's one order, shared with hardwarefacts.
var hardwareFactFormatOrder = []string{"h264", "hevc8", "hevc10", "av1", "vp9", "vc1", "mpeg2"}

// hardwareFactDecodeKeys are the decode cells that count for one grid row.
var hardwareFactDecodeKeys = map[string][]string{
	"h264":  {"h264:8"},
	"hevc8": {"hevc:8"},
	// A 12-bit success does not prove 10-bit: they are separate cells.
	"hevc10": {"hevc:10"},
	"av1":    {"av1:8", "av1:10"},
	"vp9":    {"vp9:8", "vp9:10"},
	// VC-1 needs a licensed fixture before it can be probed, so it stays false:
	// an unmeasured format is decoded by the processor, never assumed.
	"vc1":   {},
	"mpeg2": {"mpeg2:8"},
}

// hardwareFactEncodeKeys are the encode cells that count for one grid row. A
// format Portico never converts to has none, and says so by staying false.
var hardwareFactEncodeKeys = map[string][]string{
	"h264":   {"h264:8"},
	"hevc8":  {"hevc:8"},
	"hevc10": {"hevc:10"},
	"av1":    {"av1:8", "av1:10"},
}

// ComposeHardwareFacts answers the drawn Hardware rows from what this process
// measured. Devices come from discovery; the grid, each adapter's method and the
// adapter conversions run on come from the probe the delivery service selected,
// never from a second guess at the policy. That probe is the selected backend's
// own qualified capability: it is not proof of one session's executed graph,
// which plan stages, burn-in, Dolby Vision profile 5 reshaping and a mid-session
// software fallback can still change. configured is the owner's own choice;
// selected is what playback would use right now, nil when nothing is selected.
func ComposeHardwareFacts(devices []HardwareDevice, probes []HardwareProbe, configured string, selected *HardwareProbe) HardwareFacts {
	out := HardwareFacts{Devices: []HardwareFactDevice{}, Formats: []HardwareFactFormat{}}
	// proven maps a render node to the backend a real probe ran on it. A backend
	// probed through its own default selector (no node named) is the backend's
	// own choice of adapter, which is the first one discovery lists for it; the
	// claim is still a real probe, never discovery alone.
	proven := map[string]HardwareBackend{}
	defaulted := map[HardwareBackend]bool{}
	for _, probe := range probes {
		if !probe.Available || probe.Backend == BackendSoftware {
			continue
		}
		if probe.Device == "" {
			defaulted[probe.Backend] = true
			continue
		}
		if _, seen := proven[probe.Device]; !seen {
			proven[probe.Device] = probe.Backend
		}
	}
	assigned := map[HardwareBackend]bool{}
	for _, device := range devices {
		backend, ok := proven[device.Path]
		if !ok {
			for _, candidate := range device.Backends {
				if defaulted[candidate] && !assigned[candidate] {
					backend, ok = candidate, true
					break
				}
			}
		}
		if ok {
			assigned[backend] = true
		}
		fact := HardwareFactDevice{ID: device.ID, Name: hardwareFactDeviceName(device)}
		if ok {
			fact.Method = hardwareFactMethod(backend)
			fact.InUse = servedBy(device, backend, selected)
		}
		out.Devices = append(out.Devices, fact)
	}
	for _, format := range hardwareFactFormatOrder {
		out.Formats = append(out.Formats, HardwareFactFormat{Format: format,
			Decode: selected != nil && probeSays(selected.DecodeCodecs, hardwareFactDecodeKeys[format]),
			Encode: selected != nil && probeSays(selected.OutputQuality, hardwareFactEncodeKeys[format]),
		})
	}
	// A working backend the owner deliberately turned off is not a fault, and a
	// cold cache is not one either: only a selection that fell back because
	// every candidate failed is.
	if configured != string(BackendSoftware) {
		switch {
		case selected == nil:
			out.Fault = hardwareFactFault(nil)
		case selected.Backend == BackendSoftware && selected.Reason == ProbeEncodeFailed:
			out.Fault = hardwareFactFault(nil)
		case selected.Backend != BackendSoftware && !selected.Available && selected.Reason != ProbeWarming:
			out.Fault = hardwareFactFault(selected)
		}
	}
	out.LastTest = newestProbeTime()
	return out
}

// servedBy says whether this adapter is the one the selected probe ran on. The
// probe is the witness: its own adapter ID or render node decides, so two cards
// that share a backend are never both marked in use. A probe that names no
// adapter is the backend's own default, which is the first adapter discovery
// lists for that backend.
func servedBy(device HardwareDevice, backend HardwareBackend, selected *HardwareProbe) bool {
	if selected == nil || !selected.Available || selected.Backend != backend {
		return false
	}
	if selected.Device != "" {
		return selected.Device == device.Path
	}
	for _, candidate := range device.Backends {
		if candidate == backend {
			return true
		}
	}
	return false
}

// hardwareFactMethod turns a backend into the stable method token the owner's
// screen names. An unmapped backend keeps its own string.
func hardwareFactMethod(backend HardwareBackend) string {
	if method, ok := hardwareFactMethods[backend]; ok {
		return method
	}
	return string(backend)
}

func hardwareFactDeviceName(device HardwareDevice) string {
	if device.Name != "" {
		return device.Name
	}
	return device.ID
}

// probeSays reports whether one probe measured any of these cells as working.
func probeSays(cells map[string]bool, keys []string) bool {
	for _, key := range keys {
		if cells[key] {
			return true
		}
	}
	return false
}

// hardwareFactFault names the most recent failing probe. A timer-out or a
// confinement refusal is already in the observation's reason and detail.
func hardwareFactFault(selected *HardwareProbe) *HardwareFactFault {
	if selected != nil {
		if observation, ok := LastProbeObservation(selected.Backend, selected.Device); ok && !observation.Available {
			return &HardwareFactFault{Backend: observation.Backend, Device: observation.Device, Reason: observation.Reason, Detail: observation.Detail}
		}
	}
	observations := ProbeObservations()
	sort.SliceStable(observations, func(i, j int) bool { return observations[i].At.After(observations[j].At) })
	for _, observation := range observations {
		if observation.Available {
			continue
		}
		return &HardwareFactFault{Backend: observation.Backend, Device: observation.Device, Reason: observation.Reason, Detail: observation.Detail}
	}
	return &HardwareFactFault{Reason: HardwareFactReasonNoDevice}
}

// newestProbeTime is when this process last finished a probe.
func newestProbeTime() int64 {
	var newest int64
	for _, observation := range ProbeObservations() {
		if at := observation.At.Unix(); at > newest {
			newest = at
		}
	}
	return newest
}
