package hardwarefacts

import "strings"

// Target is one platform's published V1 target: what Portico aims to decode,
// encode and tone map there (HARDWARE §4, Jellyfin's level). The matrix is the
// goal a host is measured against, never a claim about what this host did: the
// decoder's own probes answer that, and an unprobed cell stays unsupported.
type Target struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Decode      []string `json:"decode"`
	Encode      []string `json:"encode"`
	ToneMapping []string `json:"toneMapping"`
	Notes       []string `json:"notes"`
}

// The row identifiers, so a consumer can name a target without matching labels.
const (
	TargetIntel     = "intel"
	TargetNVIDIA    = "nvidia"
	TargetAMD       = "amd"
	TargetApple     = "apple"
	TargetRockchip  = "rockchip"
	TargetRaspberry = "raspberry_pi_5"
)

// Decode codecs common to the three desktop GPU vendors. Their generation
// decides which of these cells a particular card can actually fill.
var desktopDecode = []string{"H.264", "HEVC 8-bit", "HEVC 10-bit", "HEVC 12-bit", "AV1", "VP9", "VC-1", "MPEG-2"}

// Targets is the V1 matrix, one row per platform family, in the frame's order.
func Targets() []Target {
	return []Target{
		{
			ID:          TargetIntel,
			Label:       "Intel (Linux, Windows)",
			Decode:      desktopDecode,
			Encode:      []string{"H.264", "HEVC 8-bit", "HEVC 10-bit", "AV1 (Arc and newer)"},
			ToneMapping: []string{"VPP", "OpenCL"},
			Notes:       []string{"Decode depends on the generation.", "Tone mapping includes Dolby Vision profile 5."},
		},
		{
			ID:          TargetNVIDIA,
			Label:       "NVIDIA (Linux, Windows)",
			Decode:      desktopDecode,
			Encode:      []string{"H.264", "HEVC 8-bit", "HEVC 10-bit", "AV1 (Ada and newer)"},
			ToneMapping: []string{"CUDA"},
			Notes:       []string{"Decode depends on the generation.", "Tone mapping includes Dolby Vision profile 5."},
		},
		{
			ID:          TargetAMD,
			Label:       "AMD (Linux VAAPI, Windows AMF)",
			Decode:      desktopDecode,
			Encode:      []string{"H.264", "HEVC", "AV1 (RX 7000 and newer)"},
			ToneMapping: []string{"OpenCL", "Vulkan"},
			Notes:       []string{"Decode depends on the generation.", "Tone mapping includes Dolby Vision profile 5."},
		},
		{
			ID:          TargetApple,
			Label:       "Apple",
			Decode:      []string{"H.264", "HEVC", "AV1 (M3 and newer)", "VP9"},
			Encode:      []string{"H.264", "HEVC 8-bit", "HEVC 10-bit"},
			ToneMapping: []string{"Metal", "VideoToolbox"},
			Notes:       []string{"Tone mapping includes Dolby Vision profile 5."},
		},
		{
			ID:          TargetRockchip,
			Label:       "Rockchip RK3588",
			Decode:      []string{"H.264", "HEVC", "AV1", "VP9"},
			Encode:      []string{"H.264", "HEVC"},
			ToneMapping: []string{"RKMPP", "OpenCL"},
			Notes:       []string{"Needs the vendor kernel."},
		},
		{
			ID:          TargetRaspberry,
			Label:       "Raspberry Pi 5",
			Decode:      []string{"HEVC"},
			Encode:      []string{},
			ToneMapping: []string{"CPU"},
			Notes:       []string{"This board has no video encoder, so the processor converts video."},
		},
	}
}

// Format is one row of the Settings formats grid, in the one order the frame
// uses everywhere. A format is a label, not a claim: the decoder's probe says
// who does the job on this server.
type Format struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Formats is the grid's row order (HARDWARE HW-5a).
func Formats() []Format {
	return []Format{
		{ID: "h264", Label: "H.264"},
		{ID: "hevc8", Label: "HEVC 8-bit"},
		{ID: "hevc10", Label: "HEVC 10-bit"},
		{ID: "av1", Label: "AV1"},
		{ID: "vp9", Label: "VP9"},
		{ID: "vc1", Label: "VC-1"},
		{ID: "mpeg2", Label: "MPEG-2"},
	}
}

// Deferred is a capability the frame places after V1. It is published so a
// settings screen can say "not yet" instead of leaving an unclear blank.
type Deferred struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// Later is the frame's "Later" list (HARDWARE §6).
func Later() []Deferred {
	return []Deferred{
		{ID: "vulkan_video", Label: "Vulkan Video encoding", Detail: "Vulkan Video encoding comes after V1."},
		{ID: "multi_machine", Label: "Transcoding across several machines", Detail: "Transcoding spread across several machines comes after V1."},
	}
}

// Document is what the settings surface reads: the platform and its one
// remedy, the V1 target matrix, the formats grid's order, and what V1 defers.
type Document struct {
	Platform Platform   `json:"platform"`
	Targets  []Target   `json:"targets"`
	Formats  []Format   `json:"formats"`
	Later    []Deferred `json:"later"`
}

// Publish is the live document for this server. Only the platform is a fact
// about this host; the matrix and the deferred list are the same everywhere.
func Publish() Document {
	return Document{Platform: Detect(), Targets: Targets(), Formats: Formats(), Later: Later()}
}

// TargetFor is the matrix row that matches a detected platform, and false for
// a platform the matrix does not cover. Desktop and container installs are
// covered through their graphics vendor with TargetForVendor.
func TargetFor(p Platform) (Target, bool) {
	switch p.Kind {
	case KindRaspberryPi5:
		return TargetByID(TargetRaspberry)
	case KindRockchip:
		return TargetByID(TargetRockchip)
	}
	return Target{}, false
}

// TargetByID returns one matrix row by its published identifier.
func TargetByID(id string) (Target, bool) {
	for _, target := range Targets() {
		if target.ID == id {
			return target, true
		}
	}
	return Target{}, false
}

// TargetForVendor returns the matrix row for a graphics vendor. The name may be
// a vendor word ("NVIDIA", "Intel"), a matrix identifier, or a PCI vendor ID,
// because a caller may have either from device enumeration.
func TargetForVendor(vendor string) (Target, bool) {
	value := strings.ToLower(strings.TrimSpace(vendor))
	value = strings.NewReplacer("-", " ", "_", " ").Replace(value)
	switch value {
	case "intel", "0x8086":
		return TargetByID(TargetIntel)
	case "nvidia", "0x10de":
		return TargetByID(TargetNVIDIA)
	case "amd", "advanced micro devices", "0x1002":
		return TargetByID(TargetAMD)
	case "apple", "metal":
		return TargetByID(TargetApple)
	case "rockchip", "rk3588":
		return TargetByID(TargetRockchip)
	case "raspberry pi 5", "raspberry", "raspberry pi":
		return TargetByID(TargetRaspberry)
	}
	return Target{}, false
}
