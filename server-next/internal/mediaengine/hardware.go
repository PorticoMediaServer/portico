package mediaengine

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/hardwarefacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// StreamingHardwareDevice is a discovered adapter and only its measured codec cells.
type StreamingHardwareDevice struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Method      string                       `json:"method,omitempty"`
	InUse       bool                         `json:"inUse"`
	Formats     []decoder.HardwareFactFormat `json:"formats"`
	Problem     string                       `json:"problem,omitempty"`
	ToneMapping bool                         `json:"toneMapping"`
}

type StreamingHardwareFault struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	Remedy string `json:"remedy,omitempty"`
}

// StreamingHardwareState is the owner's Hardware read. Reading never launches a child.
type StreamingHardwareState struct {
	Checking    bool                         `json:"checking"`
	Devices     []StreamingHardwareDevice    `json:"devices"`
	Formats     []decoder.HardwareFactFormat `json:"formats"`
	ToneMapping bool                         `json:"toneMapping"`
	LastTest    *time.Time                   `json:"lastTest,omitempty"`
	LastError   *StreamingHardwareFault      `json:"lastError,omitempty"`
}

// TestHardware starts one bounded re-test beside serving. Repeated taps share that test.
func (e *Engine) TestHardware() {
	t := e.tools()
	select {
	case <-t.asked:
		e.testHardware(t, true)
	default:
	}
}

func (e *Engine) testHardware(t *toolset, force bool) {
	if t.hardware == nil {
		return
	}
	e.mu.Lock()
	if t.probing {
		e.mu.Unlock()
		return
	}
	t.probing = true
	e.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
		defer cancel()
		defer func() { e.mu.Lock(); t.probing = false; e.mu.Unlock() }()
		discover := t.hardware.Devices
		if discover == nil {
			discover = decoder.DiscoverHardwareDevices
		}
		devices := discover(ctx)
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		t.devices = devices
		e.mu.Unlock()
		if !t.converter {
			return
		}
		if force {
			decoder.ForgetPermitIdentity(t.ffmpeg)
			decoder.HardwarePermits().Retire("", "", "")
		}
		probe := t.hardware.Probe
		if force {
			probe = t.hardware.Reprobe
		}
		// Default selectors also cover platforms whose driver cannot name an adapter.
		for _, b := range decoder.HardwareBackends {
			if ctx.Err() != nil {
				return
			}
			probe(ctx, t.ffmpeg, b, "")
		}
		for _, d := range devices {
			if d.Path == "" {
				continue
			} // no observed selector: never guess a device number
			for _, b := range d.Backends {
				if ctx.Err() != nil {
					return
				}
				probe(ctx, t.ffmpeg, b, d.Path)
			}
		}
	}()
}

// hardwareCandidates is also what the owner sees selected. A physical choice goes first,
// then all other qualified devices in the detector's backend order, then the processor.
func (e *Engine) hardwareCandidates(ctx context.Context, t *toolset, refresh bool) []decoder.HardwareProbe {
	software := decoder.HardwareProbe{Backend: decoder.BackendSoftware, Available: true, Reason: decoder.ProbeDisabled}
	owner := e.owner()
	if owner.GraphicsDevice == "processor" || t.hardware == nil {
		return []decoder.HardwareProbe{software}
	}
	e.mu.Lock()
	devices := append([]decoder.HardwareDevice(nil), t.devices...)
	e.mu.Unlock()
	var out []decoder.HardwareProbe
	needsRefresh := false
	for _, b := range decoder.HardwareBackends {
		for _, d := range devices {
			if d.Path == "" {
				continue
			}
			for _, p := range t.hardware.KnownDevice(ctx, t.ffmpeg, d.Path) {
				needsRefresh = needsRefresh || p.Reason == decoder.ProbeWarming
				if p.Backend == b && p.Available {
					p.SelectedDeviceID = d.ID
					out = append(out, p)
				}
			}
		}
	}
	defaults := t.hardware.Known(ctx, t.ffmpeg)
	for _, p := range defaults {
		needsRefresh = needsRefresh || p.Reason == decoder.ProbeWarming
		if p.Available {
			out = append(out, p)
		}
	}
	if refresh && (needsRefresh || len(out) == 0 && len(defaults) == 0) {
		e.testHardware(t, false)
	}
	// Stable partition keeps the detector's order within the selected and fallback groups.
	var preferred, others []decoder.HardwareProbe
	for _, p := range out {
		if p.SelectedDeviceID == owner.GraphicsDevice || string(p.Backend) == owner.GraphicsDevice {
			preferred = append(preferred, p)
		} else {
			others = append(others, p)
		}
	}
	out = append(preferred, others...)
	software.Reason = decoder.ProbeEncodeFailed
	return append(out, software)
}

func (e *Engine) planWithHardware(ctx context.Context, t *toolset, r streaming.PlanRequest) (streaming.DeliveryPlan, error) {
	var first streaming.DeliveryPlan
	var firstErr error
	for n, p := range e.hardwareCandidates(ctx, t, true) {
		r.Hardware = p
		if r.Settings.SoftwareDecode {
			r.Hardware.DecodeCodecs = nil
		}
		plan, err := streaming.Plan(r)
		if n == 0 || err == nil {
			first, firstErr = plan, err
		}
		if err == nil && (plan.VideoAction != "convert" || plan.HardwareBackend != "" && plan.HardwareBackend != "software") {
			return plan, nil
		}
	}
	return first, firstErr
}

func (e *Engine) HardwareState(ctx context.Context) StreamingHardwareState {
	out := StreamingHardwareState{Devices: []StreamingHardwareDevice{}, Formats: []decoder.HardwareFactFormat{}}
	t := e.tools()
	select {
	case <-t.asked:
	default:
		out.Checking = true
		return out
	}
	e.mu.Lock()
	devices, checking := append([]decoder.HardwareDevice(nil), t.devices...), t.probing
	e.mu.Unlock()
	out.Checking = checking
	if !t.converter {
		out.LastError = &StreamingHardwareFault{Reason: "ffmpeg_unavailable", Detail: t.problem, Remedy: hardwarefacts.Detect().Remedy}
		for _, d := range devices {
			out.Devices = append(out.Devices, StreamingHardwareDevice{ID: d.ID, Name: d.Name,
				Formats: []decoder.HardwareFactFormat{}, Problem: "FFmpeg is not available to test this device."})
		}
		return out
	}
	selected := e.hardwareCandidates(ctx, t, false)[0]
	var probes []decoder.HardwareProbe
	for _, d := range devices {
		var known []decoder.HardwareProbe
		if d.Path != "" {
			known = t.hardware.KnownDevice(ctx, t.ffmpeg, d.Path)
		}
		probes = append(probes, known...)
		row := StreamingHardwareDevice{ID: d.ID, Name: d.Name, Formats: []decoder.HardwareFactFormat{}}
		if d.Path == "" || len(d.Backends) == 0 {
			row.Problem = "The graphics driver does not expose a selector for this physical device; Automatic uses the driver's default."
		}

		for _, p := range known {
			if !p.Available {
				continue
			}
			if row.Method != "" && !(selected.SelectedDeviceID == d.ID && selected.Backend == p.Backend) {
				continue
			}
			row.Method = string(p.Backend)
			row.InUse = selected.SelectedDeviceID == d.ID && selected.Backend == p.Backend
			row.Formats = decoder.ComposeHardwareFacts(nil, known, "auto", &p).Formats
			row.ToneMapping = p.ToneMapFilters
			if row.InUse {
				break
			}
		}
		if row.Method == "" && row.Problem == "" && !out.Checking {
			row.Problem = "This graphics device has not completed a working hardware probe."
			if len(known) != 0 {
				p := known[0]
				if failure, ok := decoder.LastProbeFailure(p.Backend, p.Device); ok && failure.Detail != "" {
					row.Problem = failure.Detail
				}
			}
		}
		out.Devices = append(out.Devices, row)
	}
	probes = append(probes, t.hardware.Known(ctx, t.ffmpeg)...)
	configured := "auto"
	if e.owner().GraphicsDevice == "processor" {
		configured = "software"
	}
	facts := decoder.ComposeHardwareFacts(devices, probes, configured, &selected)
	out.Formats, out.ToneMapping = facts.Formats, selected.ToneMapFilters
	if facts.LastTest != 0 {
		at := time.Unix(facts.LastTest, 0).UTC()
		out.LastTest = &at
	}
	if !out.Checking && facts.Fault != nil {
		out.LastError = &StreamingHardwareFault{Reason: facts.Fault.Reason, Detail: facts.Fault.Detail, Remedy: hardwarefacts.Detect().Remedy}
	}
	return out
}
