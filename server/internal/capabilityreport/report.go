// Package capabilityreport records, once, why a server capability (Live TV,
// recording, prepared media, hardware encoding) is or isn't available on this
// host, with a reason code and the concrete cause. A capability that silently
// turns itself off is how a broken host stays broken: each change is logged
// once at the moment it is decided, and the owner's diagnostics list them all.
package capabilityreport

import (
	"log"
	"sort"
	"sync"
	"time"
)

// Status is one capability's current state.
type Status struct {
	Capability string `json:"capability"`
	Available  bool   `json:"available"`
	// Code is the machine reason when unavailable (decoder_confinement_unavailable…).
	Code string `json:"code,omitempty"`
	// Detail is the concrete cause for the owner ("bwrap is not on the server's PATH"),
	// or, for an available capability that reports one, how it works here.
	Detail    string `json:"detail,omitempty"`
	CheckedAt string `json:"checkedAt"`
}

// Names of the reported capabilities, with the words used in the log line.
const (
	LiveTV        = "live_tv"
	Recording     = "recording"
	PreparedMedia = "prepared_media"
	Hardware      = "hardware_encoding"
	// DecoderSandbox is how FFmpeg and ffprobe are confined on this host
	// (mediaexec): available means sandboxed; otherwise the detail says why not
	// and which baseline restrictions still apply.
	DecoderSandbox = "decoder_sandbox"
	// Filesystem confinement does not imply a separate network namespace.
	DecoderNetworkIsolation = "decoder_network_isolation"
	// DolbyVision is whether Dolby Vision Profile 5 files converted for screens
	// without Dolby Vision keep their colors (libplacebo on Vulkan) or are
	// approximate (COMPAT-04).
	DolbyVision = "dolby_vision_conversion"
)

var labels = map[string]string{LiveTV: "Live TV", Recording: "Recording", PreparedMedia: "Prepared media", Hardware: "Hardware encoding", DecoderSandbox: "Decoder sandbox", DecoderNetworkIsolation: "Decoder network isolation", DolbyVision: "Dolby Vision conversion"}

var (
	mu       sync.Mutex
	statuses = map[string]Status{}
	logf     = log.Printf
	now      = time.Now
)

// Report records a capability's state. It logs one line when the state first
// becomes known and whenever it changes, never per request. cause is the reason
// when unavailable; an available capability may pass a description instead.
func Report(capability string, available bool, code string, cause error) {
	detail := ""
	if cause != nil {
		detail = cause.Error()
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
	}
	if available {
		code = ""
	}
	mu.Lock()
	prev, seen := statuses[capability]
	next := Status{Capability: capability, Available: available, Code: code, Detail: detail, CheckedAt: now().UTC().Format(time.RFC3339)}
	statuses[capability] = next
	mu.Unlock()
	if seen && prev.Available == next.Available && prev.Code == next.Code && prev.Detail == next.Detail {
		return
	}
	label := labels[capability]
	if label == "" {
		label = capability
	}
	switch {
	case available && detail != "":
		logf("%s available: %s", label, detail)
	case available:
		logf("%s available", label)
	case detail != "":
		logf("%s unavailable (%s): %s", label, code, detail)
	default:
		logf("%s unavailable (%s)", label, code)
	}
}

// All is every reported capability, in a stable order.
func All() []Status {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Status, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Capability < out[j].Capability })
	return out
}
