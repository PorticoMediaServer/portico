package mediaengine

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// Capabilities is what the engine knows now of this server's media tools, for the owner's
// "What this server can do" (12-console rule 74). Nothing is run to answer it: it is what was
// found out beside the start, or beside the owner's last change of program or folder.
type Capabilities struct {
	// Asking is true while FFmpeg is still being asked what it can do; the rest of the FFmpeg
	// and graphics facts are not known yet.
	Asking bool
	// Converter is false on a server without a working FFmpeg: only original files play.
	// FFmpeg is the program in force and Version what it says it is; Problem is why it could
	// not be used ("" with no Converter: none was found).
	Converter               bool
	FFmpeg, Version, Problem string
	// ConversionFolder is the folder conversions are written to now (the server keeps its own
	// folder inside it).
	ConversionFolder string
	// SoftwareEncoding is the owner's choice of software encoding: no graphics device is used.
	SoftwareEncoding bool
	// Graphics is each hardware backend tried on this machine, in the order they are tried,
	// with whether it works and why not (decoder.HardwareDetector.Known).
	Graphics []decoder.HardwareProbe
	// Vulkan is whether FFmpeg found a working Vulkan graphics device, for the Dolby Vision
	// and tone-map filters; VulkanProblem is what FFmpeg said when it did not.
	Vulkan        bool
	VulkanProblem string
}

// Capabilities answers what the engine knows now. It waits for nothing.
func (e *Engine) Capabilities(ctx context.Context) Capabilities {
	t := e.tools()
	e.mu.Lock()
	out := Capabilities{ConversionFolder: e.current.folder}
	e.mu.Unlock()
	out.SoftwareEncoding = softwareEncoding(e.owner())
	select {
	case <-t.asked:
	default:
		out.Asking = true
		return out
	}
	out.Converter, out.FFmpeg, out.Problem = t.converter, t.ffmpeg, t.problem
	if !t.converter {
		return out
	}
	if facts := decoder.InstalledToolchain(); facts != nil {
		out.Version, out.Vulkan, out.VulkanProblem = facts.Version, facts.VulkanAvailable, facts.VulkanReason
	}
	if t.hardware != nil {
		out.Graphics = t.hardware.Known(ctx, t.ffmpeg)
	}
	return out
}
