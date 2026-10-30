package decoder

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ToolchainFacts struct {
	inventoryCacheable       bool
	DolbyVisionDualLayerJoin bool `json:"-"`
	// Only exact bundle-qualified libx265 opt-in carry may set this. It does
	// not qualify rendering transforms or activate any conversion recipe.
	HDR10PlusLibX265Encode bool `json:"-"`
	// Only a bundle's qualified packet conversion may enable this fact. A BSF
	// name, stock dovi_rpu options or a generic device probe is insufficient.
	// It remains false until the libdovi-backed mode-2 fixture gate is wired.
	DolbyVisionProfile7To81 bool `json:"dolbyVisionProfile7To81"`

	BitstreamFilters map[string]bool `json:"bitstreamFilters"`
	VulkanAvailable  bool            `json:"vulkanAvailable"`
	OpenCLAvailable  bool            `json:"openclAvailable"`
	Version          string          `json:"version"`
	Configuration    string          `json:"configuration"`
	Encoders         map[string]bool `json:"encoders"`
	Decoders         map[string]bool `json:"decoders"`
	Filters          map[string]bool `json:"filters"`
	Muxers           map[string]bool `json:"muxers"`
	HWAccels         map[string]bool `json:"hwaccels"`
	Identity         string          `json:"identity"`
}

var installedToolchain atomic.Pointer[ToolchainFacts]
var toolchainCache sync.Map

// ConfigureToolchain installs the probed toolchain and reports, in the owner's
// diagnostics, whether it converts Dolby Vision Profile 5 with its own colors.
func ConfigureToolchain(f ToolchainFacts) {
	installedToolchain.Store(&f)
	switch {
	case f.Filters["libplacebo"] && f.VulkanAvailable:
		capabilityreport.Report(capabilityreport.DolbyVision, true, "", errors.New("Dolby Vision files without a compatible base layer are converted only with a qualified color reshaping filter."))
	case !f.Filters["libplacebo"]:
		capabilityreport.Report(capabilityreport.DolbyVision, false, "dolby_vision_reshaping_unavailable", errors.New("Dolby Vision files without a compatible base layer need the libplacebo filter for conversion. Use compatible direct playback or another FFmpeg build."))
	default:
		capabilityreport.Report(capabilityreport.DolbyVision, false, "dolby_vision_reshaping_unavailable", errors.New("Dolby Vision files without a compatible base layer need a qualified Vulkan color reshaping filter for conversion. Use compatible direct playback when that filter is unavailable."))
	}
}
func CurrentToolchain() *ToolchainFacts { return installedToolchain.Load() }

// RestoreToolchain puts back what CurrentToolchain returned earlier (nil
// included), for a test that configured a probed toolchain for itself.
func RestoreToolchain(f *ToolchainFacts) { installedToolchain.Store(f) }

// ResolveTool uses the shared verified tool resolver.
func ResolveTool(tool string) string { return mediatools.Resolve(tool) }
func ResolveTools() (string, string) { return mediatools.ResolvePair() }

type toolOutput struct{ bytes.Buffer }

func (w *toolOutput) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 1<<20 {
		return 0, errors.New("toolchain probe output exceeds bound")
	}
	return w.Buffer.Write(p)
}
func parseToolList(raw, kind string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if kind == "hwaccels" || kind == "bsfs" {
			if len(f) == 1 && !strings.ContainsAny(f[0], ":=-") {
				out[f[0]] = true
			}
			continue
		}
		if len(f) < 2 || f[1] == "=" || strings.Contains(f[0], "-") {
			continue
		}
		flags := f[0]
		valid := len(flags) <= 8
		for _, r := range flags {
			if !strings.ContainsRune(".VASDFTIXBESC|N", r) {
				valid = false
			}
		}
		if valid && len(flags) > 0 {
			out[f[1]] = true
		}
	}
	return out
}

// Software encoders may select these filters, so qualify them without the
// device and driver grants reserved for hardware jobs. A host-only GPU filter
// must not displace the CPU tone mapper inside the software sandbox.
func toneMapProbeJob(path, device, graph string) mediaexec.Job {
	return mediaexec.Job{Executable: path, Args: []string{"-v", "error", "-nostdin", "-init_hw_device", device, "-filter_hw_device", "tone", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=1", "-vf", graph, "-frames:v", "1", "-f", "null", "-"}}
}

// ProbeToolchain qualifies trusted executable inventories. Optional GPU media
// probes run separately through ProbeToolchainRuntime after startup qualification.
// Cold inventory facts do not grant Vulkan/OpenCL runtime availability.
func ProbeToolchain(ctx context.Context, ffmpeg string) (ToolchainFacts, error) {
	return probeToolchainInventory(ctx, ffmpeg, runToolchainProbe)
}

type toolchainProbeRunner func(context.Context, mediaexec.Job) (string, error)

func runToolchainProbe(ctx context.Context, job mediaexec.Job) (string, error) {
	cmd, err := mediaexec.CommandContext(ctx, job)
	if err != nil {
		return "", err
	}
	cmd.WaitDelay = time.Second
	buffer := &toolOutput{}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err = cmd.Run()
	return buffer.String(), err
}

func probeToolchainInventory(ctx context.Context, ffmpeg string, runner toolchainProbeRunner) (ToolchainFacts, error) {
	if err := ctx.Err(); err != nil {
		return ToolchainFacts{}, err
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return ToolchainFacts{}, err
	}
	identity, cacheable, err := inventoryIdentity(ctx, path)
	if err != nil {
		return ToolchainFacts{}, err
	}
	if cached, ok := toolchainCache.Load(identity); cacheable && ok {
		return cached.(ToolchainFacts), nil
	}
	out := ToolchainFacts{Identity: identity, inventoryCacheable: cacheable}
	for _, kind := range []string{"version", "encoders", "decoders", "filters", "muxers", "hwaccels", "bsfs"} {
		run, cancel := context.WithTimeout(ctx, 5*time.Second)
		raw, err := runner(run, mediaexec.Job{Executable: path, Args: []string{"-hide_banner", "-" + kind}})
		cancel()
		if err != nil {
			return ToolchainFacts{}, err
		}
		switch kind {
		case "version":
			for _, line := range strings.Split(raw, "\n") {
				if strings.HasPrefix(line, "ffmpeg version ") {
					out.Version = strings.TrimPrefix(line, "ffmpeg version ")
				}
				if strings.HasPrefix(line, "configuration:") {
					out.Configuration = strings.TrimSpace(strings.TrimPrefix(line, "configuration:"))
				}
			}
		case "encoders":
			out.Encoders = parseToolList(raw, kind)
		case "decoders":
			out.Decoders = parseToolList(raw, kind)
		case "filters":
			out.Filters = parseToolList(raw, kind)
		case "muxers":
			out.Muxers = parseToolList(raw, kind)
		case "hwaccels":
			out.HWAccels = parseToolList(raw, kind)
		case "bsfs":
			out.BitstreamFilters = parseToolList(raw, kind)
		}
	}
	if out.Version == "" || len(out.Encoders) == 0 || len(out.Muxers) == 0 {
		return ToolchainFacts{}, errors.New("incomplete toolchain probe")
	}
	if err := ctx.Err(); err != nil {
		return ToolchainFacts{}, err
	}
	if current, complete, err := inventoryIdentity(ctx, path); err != nil || current != identity || complete != cacheable {
		return ToolchainFacts{}, errors.New("FFmpeg changed during inventory qualification")
	}
	if err := ctx.Err(); err != nil {
		return ToolchainFacts{}, err
	}
	if cacheable {
		toolchainCache.Store(identity, out)
	}
	return out, nil
}

// ProbeToolchainRuntime qualifies optional software-sandbox GPU filters for the
// already qualified executable identity. Call it on the server lifecycle after
// choosing the final configured or bundled tool; it is not a startup barrier.
// Failure leaves CPU conversion available and grants no unmeasured GPU support.
func ProbeToolchainRuntime(ctx context.Context, ffmpeg string) (ToolchainFacts, error) {
	return probeToolchainRuntime(ctx, ffmpeg, runToolchainProbe)
}

func probeToolchainRuntime(ctx context.Context, ffmpeg string, runner toolchainProbeRunner) (ToolchainFacts, error) {
	out, err := probeToolchainInventory(ctx, ffmpeg, runner)
	if err != nil {
		return ToolchainFacts{}, err
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return ToolchainFacts{}, err
	}
	identity := out.Identity

	probeDevice := func(device, graph string) bool {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		// Use the same permissions as a software conversion, including fallback.
		_, err := runner(bounded, toneMapProbeJob(path, device, graph))
		return err == nil
	}
	if out.Filters["libplacebo"] && out.HWAccels["vulkan"] {
		out.VulkanAvailable = probeDevice("vulkan=tone", "format=yuv420p,hwupload,libplacebo=tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:format=yuv420p,hwdownload,format=yuv420p")
	}
	if !out.VulkanAvailable && out.Filters["tonemap_opencl"] && out.HWAccels["opencl"] {
		out.OpenCLAvailable = probeDevice("opencl=tone", "format=p010,hwupload,tonemap_opencl=tonemap=hable:desat=0:format=nv12,hwdownload,format=nv12")
	}
	if err := ctx.Err(); err != nil {
		return ToolchainFacts{}, err
	}
	if current, complete, err := inventoryIdentity(ctx, path); err != nil || current != identity || complete != out.inventoryCacheable {
		return ToolchainFacts{}, errors.New("FFmpeg changed during runtime qualification")
	}
	// ICDs, dynamically loaded driver plugins and device permissions can change
	// without changing FFmpeg's ELF closure. An explicit runtime/reprobe call
	// must run these bounded jobs again; never put their grants in the trusted
	// inventory cache, which promises no GPU qualification.
	return out, nil
}
func (f *ToolchainFacts) MissingRequired() []string {
	if f == nil {
		return []string{"probe_pending"}
	}
	missing := []string{}
	for kind, names := range map[string][]string{"encoder": {"libx264", "aac", "ac3", "eac3"}, "decoder": {"truehd", "dca", "eac3", "hevc", "av1", "vc1", "mpeg2video"}, "filter": {"zscale", "tonemap", "subtitles"}, "muxer": {"hls", "mp4", "mpegts", "segment"}} {
		set := f.Encoders
		switch kind {
		case "decoder":
			set = f.Decoders
		case "filter":
			set = f.Filters
		case "muxer":
			set = f.Muxers
		}
		for _, name := range names {
			if !set[name] {
				missing = append(missing, kind+":"+name)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

var hardwareRuntimeFailures = struct {
	sync.Mutex
	failures   map[HardwareBackend][]time.Time
	needsProbe map[HardwareBackend]bool
}{failures: map[HardwareBackend][]time.Time{}, needsProbe: map[HardwareBackend]bool{}}

func RecordHardwareFailure(backend HardwareBackend) {
	hardwareRuntimeFailures.Lock()
	defer hardwareRuntimeFailures.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := []time.Time{}
	for _, at := range hardwareRuntimeFailures.failures[backend] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	hardwareRuntimeFailures.failures[backend] = append(recent, time.Now())
	if len(hardwareRuntimeFailures.failures[backend]) >= 3 {
		hardwareRuntimeFailures.needsProbe[backend] = true
	}
}
func hardwareCircuitOpen(backend HardwareBackend) bool {
	hardwareRuntimeFailures.Lock()
	defer hardwareRuntimeFailures.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := hardwareRuntimeFailures.failures[backend][:0]
	for _, at := range hardwareRuntimeFailures.failures[backend] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	hardwareRuntimeFailures.failures[backend] = recent
	return len(recent) >= 3
}
func clearHardwareCircuit(backend HardwareBackend) {
	hardwareRuntimeFailures.Lock()
	defer hardwareRuntimeFailures.Unlock()
	delete(hardwareRuntimeFailures.failures, backend)
	delete(hardwareRuntimeFailures.needsProbe, backend)
}

// A cooled-down circuit must prove recovery before a cached success is reused.
func hardwareNeedsProbe(backend HardwareBackend) bool {
	hardwareRuntimeFailures.Lock()
	defer hardwareRuntimeFailures.Unlock()
	return hardwareRuntimeFailures.needsProbe[backend]
}
