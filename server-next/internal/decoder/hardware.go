package decoder

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// HardwareBackend names an encoder family. Software is always available; every
// other backend must earn its place with a real bounded encode on this host.
type HardwareBackend string

const (
	BackendSoftware     HardwareBackend = "software"
	BackendVideoToolbox HardwareBackend = "videotoolbox"
	BackendVAAPI        HardwareBackend = "vaapi"
	BackendQSV          HardwareBackend = "qsv"
	BackendNVENC        HardwareBackend = "nvenc"
	BackendAMF          HardwareBackend = "amf"
)

// HardwareBackends is the probe order. The first backend that probes clean on a
// host wins automatic selection; an owner may still name one explicitly.
var HardwareBackends = []HardwareBackend{BackendVideoToolbox, BackendNVENC, BackendQSV, BackendVAAPI, BackendAMF}

// ValidBackend reports whether name is a backend this build understands. "auto"
// and "software" are configuration values, not probe targets.
func ValidBackend(name string) bool {
	if HardwareBackend(name) == BackendSoftware {
		return true
	}
	for _, b := range HardwareBackends {
		if HardwareBackend(name) == b {
			return true
		}
	}
	return false
}

// Probe outcome reason codes. They are published on diagnostics, so they are
// stable strings rather than free text.
const (
	ProbeAvailable           = "available"
	ProbeUnsupportedPlatform = "unsupported_platform"
	ProbeConfinementMissing  = "confinement_unavailable"
	ProbeEncodeFailed        = "probe_encode_failed"
	ProbeWarming             = "probe_warming"
	ProbeTimedOut            = "probe_timed_out"
	ProbeDisabled            = "probe_disabled"
	ProbeCircuitOpen         = "hardware_circuit_open"
)

// HardwareStage is one operation in the conversion graph and where it runs.
// Operations are decode, tone_map, scale and encode; execution is hardware or
// software. A hardware backend that cannot tone map in hardware still reports
// its encode stage as hardware.
type HardwareStage struct {
	Operation string `json:"operation"`
	Execution string `json:"execution"`
}

const (
	StageDecode  = "decode"
	StageToneMap = "tone_map"
	StageScale   = "scale"
	StageEncode  = "encode"
	// StageDeinterlace uses device frames only after its own filter probe.
	StageDeinterlace = "deinterlace"

	ExecutionHardware = "hardware"
	ExecutionSoftware = "software"
)

// HardwareProbe is the cached result of one bounded encode. NativeFilters says
// whether the backend's own tone-map/scale filters ran, not merely its encoder.
type HardwareProbe struct {
	BurnInFilters      bool            `json:"-"`
	DeinterlaceFilters bool            `json:"-"`
	DecodeCodecs       map[string]bool `json:"-"`
	SelectedDeviceID   string          `json:"-"`
	Backend            HardwareBackend `json:"backend"`
	Available          bool            `json:"available"`
	Reason             string          `json:"reason"`
	Encoder            string          `json:"encoder,omitempty"`
	NativeFilters      bool            `json:"nativeFilters"`
	ScaleFilters       bool            `json:"scaleFilters"`
	ToneMapFilters     bool            `json:"toneMapFilters"`
	OutputEncoders     map[string]bool `json:"outputEncoders,omitempty"`
	OutputQuality      map[string]bool `json:"outputQuality,omitempty"`
	Device             string          `json:"device,omitempty"`
	Identity           string          `json:"identity"`
	DurationMS         int64           `json:"durationMs"`
}

type backendSpec struct {
	encoder string
	// device builds the -init_hw_device argument pair, if the backend needs one.
	device func(device string) []string
	// upload is the filter prefix that moves frames into the backend's own
	// surface format. Empty when the encoder accepts ordinary system frames.
	upload string
	// toneMap is the backend's native tone-map filter, empty when it has none.
	toneMap func(algorithm string) string
	// scale is the backend's native scaler, empty when it has none.
	scale func(height int) string
	// platforms restricts a backend to the operating systems that can host it.
	platforms []string
}

func vaapiDevice(device string) []string {
	if device == "" {
		device = "/dev/dri/renderD128"
	}
	return []string{"-init_hw_device", "vaapi=hw:" + device, "-filter_hw_device", "hw"}
}

// qsvDevice differs by platform: Windows QSV binds through D3D11 and needs no
// selector, while Linux QSV is layered on a VAAPI render node.
func qsvDevice(device string) []string {
	if runtime.GOOS == "windows" {
		if device == "" {
			return []string{"-init_hw_device", "qsv=hw", "-filter_hw_device", "hw"}
		}
		return []string{"-init_hw_device", "qsv=hw:" + device, "-filter_hw_device", "hw"}
	}
	if device == "" {
		device = "/dev/dri/renderD128"
	}
	return []string{"-init_hw_device", "vaapi=qsvva:" + device, "-init_hw_device", "qsv=hw@qsvva", "-filter_hw_device", "hw"}
}
func cudaDevice(device string) []string {
	if device == "" {
		device = "0"
	}
	return []string{"-init_hw_device", "cuda=hw:" + device, "-filter_hw_device", "hw"}
}

var backendSpecs = map[HardwareBackend]backendSpec{
	BackendVideoToolbox: {
		encoder:   "h264_videotoolbox",
		platforms: []string{"darwin"},
	},
	BackendVAAPI: {
		encoder:   "h264_vaapi",
		device:    vaapiDevice,
		upload:    "format=nv12,hwupload",
		toneMap:   func(string) string { return "tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709" },
		scale:     func(h int) string { return "scale_vaapi=w=-2:h=" + strconv.Itoa(h) },
		platforms: []string{"linux"},
	},
	BackendQSV: {
		encoder:   "h264_qsv",
		device:    qsvDevice,
		upload:    "format=nv12,hwupload=extra_hw_frames=32",
		toneMap:   func(string) string { return "vpp_qsv=tonemap=1:format=nv12" },
		scale:     func(h int) string { return "vpp_qsv=w=-1:h=" + strconv.Itoa(h) },
		platforms: []string{"linux", "windows"},
	},
	BackendNVENC: {
		encoder:   "h264_nvenc",
		device:    cudaDevice,
		upload:    "format=yuv420p,hwupload_cuda",
		toneMap:   func(a string) string { return "tonemap_cuda=tonemap=" + a + ":desat=0:format=yuv420p" },
		scale:     func(h int) string { return "scale_cuda=-2:" + strconv.Itoa(h) },
		platforms: []string{"linux", "windows"},
	},
	BackendAMF: {
		encoder:   "h264_amf",
		platforms: []string{"windows"},
	},
}

func (s backendSpec) supported() bool {
	if len(s.platforms) == 0 {
		return true
	}
	for _, p := range s.platforms {
		if p == runtime.GOOS {
			return true
		}
	}
	return false
}

// ToneMapAlgorithms are the algorithms the software tone mapper accepts. The
// default is hable; an unknown value is refused rather than silently replaced.
var ToneMapAlgorithms = []string{"clip", "linear", "gamma", "reinhard", "hable", "mobius"}

const DefaultToneMapAlgorithm = "hable"

func ValidToneMapAlgorithm(a string) bool {
	for _, v := range ToneMapAlgorithms {
		if v == a {
			return true
		}
	}
	return false
}

// SoftwareToneMap is the software HDR to SDR graph. It is also the fallback for
// a hardware backend whose own tone mapper did not probe clean.
func SoftwareToneMap(algorithm string) string {
	if !ValidToneMapAlgorithm(algorithm) {
		algorithm = DefaultToneMapAlgorithm
	}
	return "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=" + algorithm + ":desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
}

type hardwareProbeCell struct {
	backend HardwareBackend
	device  string
}

// HardwareDetector caches measured backend/device results by FFmpeg runtime
// identity. Replacing that runtime or explicitly reproving a cell retires its
// prior observation without invalidating peer devices.
type HardwareDetector struct {
	// Devices supplies physical discovery only; candidates still require probes.
	Devices func(context.Context) []HardwareDevice
	// Timeout bounds one probe encode. Zero uses the default.
	Timeout time.Duration
	// Libraries are the confinement's readable dependency files, as resolved by
	// ResolveLibraries for the rest of this package.
	Libraries []string
	// Run is the process seam. Tests substitute it; production runs the confined
	// child directly, because a synthetic clip borrows no source ownership.
	Run func(ctx context.Context, cmd *exec.Cmd) error
	// CachedRuntimeIdentity is a trusted composition/test seam for a completed
	// generation only. Nil uses the platform metadata witness. It must not hash
	// or parse executable bytes and is never supplied by a playback request.
	CachedRuntimeIdentity func(context.Context, string) (string, bool)

	mu               sync.Mutex
	cache            map[string]HardwareProbe
	warming          map[string]bool
	probeGenerations map[hardwareProbeCell]uint64
}

const defaultProbeTimeout = 20 * time.Second

// ffmpegIdentity retains the legacy non-Linux inventory identity. Linux probes
// use the native executable and resolved-library binding below.
func ffmpegIdentity(executable string) (string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", ErrInvalidConfiguration
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(executable + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	return hex.EncodeToString(sum[:16]), nil
}

func hardwareRuntimeIdentity(ctx context.Context, executable string) (string, error) {
	identity, complete, err := inventoryIdentity(ctx, executable)
	if err != nil {
		return "", err
	}
	if !complete || identity == "" {
		return "", ErrInvalidConfiguration
	}
	return identity, nil
}

func invalidHardwareObservation(out HardwareProbe, reason string) HardwareProbe {
	out.Available, out.Reason, out.Identity = false, reason, ""
	out.NativeFilters, out.ScaleFilters, out.ToneMapFilters, out.DeinterlaceFilters = false, false, false, false
	out.OutputEncoders, out.OutputQuality, out.DecodeCodecs = nil, nil, nil
	return out
}

// probeCommand runs a hardware probe exactly as a hardware conversion runs:
// through mediaexec with the GPU devices and driver libraries a hardware job
// gets, sandboxed where the platform can. A sandbox that breaks a backend makes
// the probe fail, so the backend is reported unavailable and conversions use
// software, instead of every hardware session failing. The probe's input is a
// synthetic lavfi clip and its output the null muxer.
func probeCommand(ctx context.Context, executable string, args []string, libraries, readPaths []string) (*exec.Cmd, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, ErrInvalidConfiguration
	}
	return mediaexec.CommandContext(ctx, mediaexec.Job{Executable: executable, Args: args, Hardware: true, Libraries: libraries, ReadPaths: readPaths})
}

// ReshapesDolbyVision says whether converting this request tone maps with
// libplacebo, the one FFmpeg tone mapper that applies a Dolby Vision stream's
// reshaping metadata (its apply_dolbyvision option is on by default). Profile 5
// files have no HDR10 or SDR base layer, so every other tone mapper treats
// their IPT-PQ-C2 picture as HDR10 and shows the well-known purple and green
// cast (COMPAT-04). It mirrors BuildConversion's choice of tone mapper; a test
// holds the two together.
func ReshapesDolbyVision(r ConversionRequest) bool {
	if !r.ConvertVideo {
		return false
	}
	reshapes := r.ToneMap || r.Target != nil && r.Target.ToneMap
	if !reshapes && r.Target != nil && r.Target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(r.Target.DolbyVisionProfile, r.Target.DolbyVisionCompatibility) == "" && r.Target.DynamicRange != "sdr" {
		// An HDR target keeps the picture's range; only the reshaping remains.
		reshapes = true
	}
	if !reshapes {
		return false
	}
	graph, err := BuildConversion(r)
	return err == nil && strings.Contains(strings.Join(graph.Video, " "), "libplacebo=")
}

//go:embed probeassets/hdr10.hevc
var hdrProbeClip []byte

func probeArgs(spec backendSpec, device string, filters string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if spec.device != nil {
		args = append(args, spec.device(device)...)
	}
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=10:duration=0.5", "-frames:v", "5")
	chain := spec.upload
	if filters != "" {
		chain = strings.NewReplacer("format=nv12", "format=p010", "format=yuv420p", "format=p010").Replace(chain)
		if chain != "" {
			chain += ","
		}
		chain += filters
	}
	if chain != "" {
		args = append(args, "-vf", chain)
	}
	args = append(args, "-c:v", spec.encoder)
	switch spec.encoder {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		args = append(args, "-forced-idr", "1")
	case "h264_qsv", "h264_amf", "hevc_qsv", "hevc_amf":
		args = append(args, "-forced_idr", "1")
	}
	args = append(args, "-b:v", "200k", "-an", "-sn", "-dn", "-f", "null", "-")
	return args
}

func (d *HardwareDetector) run(ctx context.Context, executable string, args []string) error {
	return d.runInput(ctx, executable, args, nil)
}

// runObserved is the same bounded probe as run, plus the tail of what the tool
// wrote. The verdict still comes from the exit status alone; the words are only
// kept for the message log and the owner's hardware facts (HW-5b), because
// FFmpeg states a refusal on its last line and then the process is gone.
func (d *HardwareDetector) runObserved(ctx context.Context, executable string, args, readPaths []string) (string, error) {
	cmd, err := probeCommand(ctx, executable, args, d.Libraries, readPaths)
	if err != nil {
		return "", err
	}
	cmd.WaitDelay = 2 * time.Second
	tail := &probeTail{}
	cmd.Stdout, cmd.Stderr = tail, tail
	if d.Run != nil {
		err = d.Run(ctx, cmd)
	} else {
		err = cmd.Run()
	}
	return tail.String(), err
}

func (d *HardwareDetector) runInput(ctx context.Context, executable string, args, readPaths []string) error {
	cmd, err := probeCommand(ctx, executable, args, d.Libraries, readPaths)
	if err != nil {
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if d.Run != nil {
		return d.Run(ctx, cmd)
	}
	return cmd.Run()
}

// VA-API requires mastering-display side data, which a lavfi pattern cannot
// provide. Mount only this private fixture, with no source or output authority.
func (d *HardwareDetector) probeVAAPIFilters(ctx context.Context, executable string, args []string) error {
	file, err := os.CreateTemp(os.TempDir(), "portico-hdr-probe-*.hevc")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(hdrProbeClip); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	for i := 0; i+3 < len(args); i++ {
		if args[i] == "-f" && args[i+1] == "lavfi" && args[i+2] == "-i" {
			args[i+1], args[i+3] = "hevc", file.Name()
			break
		}
	}
	return d.runInput(ctx, executable, args, []string{file.Name()})
}

// Probe returns this host's verdict for one backend, running at most one bounded
// encode per native runtime identity. A failed probe is cached too: a host without the
// hardware must not pay for a subprocess on every session.
func (d *HardwareDetector) Probe(ctx context.Context, executable string, backend HardwareBackend, device string) HardwareProbe {
	return d.probe(ctx, executable, backend, device, false, 0)
}

// Reprobe refreshes exactly one existing backend/device cell. It bypasses that
// cell's cached result and cooldown, while retiring observations already in
// flight for it. Peer devices and backend families keep their own verdicts.
func (d *HardwareDetector) Reprobe(ctx context.Context, executable string, backend HardwareBackend, device string) HardwareProbe {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return HardwareProbe{Backend: backend, Device: device, Reason: ProbeTimedOut}
	}
	d.mu.Lock()
	if d.probeGenerations == nil {
		d.probeGenerations = map[hardwareProbeCell]uint64{}
	}
	cell := hardwareProbeCell{backend, device}
	d.probeGenerations[cell]++
	generation := d.probeGenerations[cell]
	for key, cached := range d.cache {
		if cached.Backend == backend && cached.Device == device {
			delete(d.cache, key)
		}
	}
	d.mu.Unlock()
	return d.probe(ctx, executable, backend, device, true, generation)
}

func (d *HardwareDetector) probe(ctx context.Context, executable string, backend HardwareBackend, device string, force bool, generation uint64) HardwareProbe {
	out := HardwareProbe{Backend: backend, Reason: ProbeUnsupportedPlatform, Device: device}
	spec, ok := backendSpecs[backend]
	if !ok || backend == BackendSoftware {
		out.Reason = ProbeDisabled
		return out
	}
	identity, err := hardwareRuntimeIdentity(ctx, executable)
	if err != nil {
		out.Reason = ProbeEncodeFailed
		return out
	}
	out.Identity, out.Encoder = identity, spec.encoder
	if !spec.supported() {
		return out
	}
	if !force && deviceCircuitOpen(backend, device) {
		out.Reason = ProbeCircuitOpen
		return out
	}
	recovery := deviceNeedsProbe(backend, device)
	key := identity + "/" + string(backend) + "/" + device
	cell := hardwareProbeCell{backend, device}
	d.mu.Lock()
	if force && d.probeGenerations[cell] != generation {
		d.mu.Unlock()
		return invalidHardwareObservation(out, ProbeWarming)
	}
	generation = d.probeGenerations[cell]
	if cached, ok := d.cache[key]; ok && !recovery && !force {
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	started := time.Now()
	bounded, cancel := context.WithTimeout(ctx, timeout)
	detail, err := d.runObserved(bounded, executable, probeArgs(spec, device, ""), nil)
	cancel()
	out.DurationMS = time.Since(started).Milliseconds()
	switch {
	case err == nil:
		out.Available, out.Reason = true, ProbeAvailable
	case errors.Is(err, ErrInvalidConfiguration), errors.Is(err, mediaexec.ErrSandboxRequired):
		out.Reason = ProbeConfinementMissing
	case errors.Is(err, context.DeadlineExceeded):
		out.Reason = ProbeTimedOut
	default:
		out.Reason = ProbeEncodeFailed
	}
	// One observed answer per cell, whatever later filter or codec probing
	// decides: HW-5b keeps what the tool said for the log and Server › Streaming.
	defer func() { noteProbeObservation(out, detail) }()
	// A backend whose encoder works may still lack its own filters in this
	// build. Confirm them with the real production chain rather than assume.
	if out.Available && spec.toneMap != nil && spec.scale != nil {
		bounded, cancel = context.WithTimeout(ctx, timeout)
		scaleArgs := probeArgs(spec, device, "")
		for i := 0; i+1 < len(scaleArgs); i++ {
			if scaleArgs[i] == "-vf" {
				scaleArgs[i+1] += "," + spec.scale(120)
			}
		}
		out.ScaleFilters = d.run(bounded, executable, scaleArgs) == nil
		cancel()
		chain := spec.toneMap(DefaultToneMapAlgorithm) + "," + spec.scale(120)
		bounded, cancel = context.WithTimeout(ctx, timeout)
		args := probeArgs(spec, device, chain)
		out.NativeFilters = d.probeVAAPIFilters(bounded, executable, args) == nil
		out.ToneMapFilters = out.NativeFilters
		cancel()
	}
	if out.Available {
		out.OutputEncoders = map[string]bool{"h264:8": true}
		out.OutputQuality = map[string]bool{}
		qualityArgs := probeQualityArgs(spec, device, 8)
		bounded, cancel = context.WithTimeout(ctx, timeout)
		out.OutputQuality["h264:8"] = d.run(bounded, executable, qualityArgs) == nil
		cancel()
		for _, cell := range []struct {
			codec string
			depth int
		}{{"hevc", 8}, {"hevc", 10}, {"av1", 8}, {"av1", 10}} {
			depth := cell.depth
			codecSpec := spec
			codecSpec.encoder = cell.codec + "_" + string(backend)
			args := probeArgs(codecSpec, device, "")
			if depth == 10 {
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "-vf" {
						args[i+1] = strings.NewReplacer("format=nv12", "format=p010le", "format=yuv420p", "format=p010le").Replace(args[i+1])
					}
				}
				if spec.upload == "" {
					args = append(args[:len(args)-4], append([]string{"-pix_fmt", "yuv420p10le"}, args[len(args)-4:]...)...)
				}
			}
			bounded, cancel = context.WithTimeout(ctx, timeout)
			key := cell.codec + ":" + strconv.Itoa(depth)
			out.OutputEncoders[key] = d.run(bounded, executable, args) == nil
			cancel()
			if out.OutputEncoders[key] {
				bounded, cancel = context.WithTimeout(ctx, timeout)
				out.OutputQuality[key] = d.run(bounded, executable, probeQualityArgs(codecSpec, device, depth)) == nil
				cancel()
			}
		}
		out.DecodeCodecs = d.probeDecodeCodecs(ctx, executable, backend, device, timeout)
		out.DeinterlaceFilters = d.probeDeinterlace(ctx, executable, spec, backend, device, timeout)
		out.BurnInFilters = d.probeBurnIn(ctx, executable, spec, backend, device, timeout)
	}
	if ctx.Err() != nil {
		return invalidHardwareObservation(out, ProbeTimedOut)
	}
	// A replacement during any codec/filter probe cannot publish or memoize a
	// result for the old executable or library generation.
	current, err := hardwareRuntimeIdentity(ctx, executable)
	if err != nil || current != identity {
		return invalidHardwareObservation(out, ProbeEncodeFailed)
	}
	d.mu.Lock()
	if d.probeGenerations[cell] != generation {
		d.mu.Unlock()
		return invalidHardwareObservation(out, ProbeWarming)
	}
	if d.cache == nil {
		d.cache = map[string]HardwareProbe{}
	}
	d.cache[key] = out
	if out.Available {
		clearDeviceCircuit(backend, device)
	} else if recovery {
		// Retry after the same cooldown, not on every new playback request.
		for range 3 {
			RecordHardwareDeviceFailure(backend, device)
		}
	}
	d.mu.Unlock()
	return out
}

// SelectCached answers from cached verdicts only. A playback request must never
// wait on a probe encode, so a cold cache yields software for this session and
// starts the real probe in the background; the next session sees the verdict.
func (d *HardwareDetector) SelectCached(executable, configured, device string) HardwareProbe {
	probe, _ := d.SelectCachedContext(context.Background(), executable, configured, device)
	return probe
}

// SelectCachedContext consumes a completed runtime generation without reading
// executable or library bytes. A missing or changed generation uses software
// immediately while the existing coalesced background probe refreshes it.
func (d *HardwareDetector) SelectCachedContext(ctx context.Context, executable, configured, device string) (HardwareProbe, error) {
	if ctx == nil {
		return HardwareProbe{}, ErrInvalidConfiguration
	}
	if err := ctx.Err(); err != nil {
		return HardwareProbe{}, err
	}
	if configured == "" {
		configured = "auto"
	}
	if configured == string(BackendSoftware) {
		return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}, nil
	}
	lookup := d.CachedRuntimeIdentity
	if lookup == nil {
		lookup = cachedHardwareIdentity
	}
	identity, ready := lookup(ctx, executable)
	if ctx.Err() != nil {
		return HardwareProbe{}, ctx.Err()
	}
	if !ready {
		d.warm(executable, configured, device)
		return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeWarming}, nil
	}
	candidates := HardwareBackends
	if configured != "auto" {
		if !ValidBackend(configured) {
			return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}, nil
		}
		candidates = []HardwareBackend{HardwareBackend(configured)}
	}
	cold := false
	d.mu.Lock()
	for _, backend := range candidates {
		spec, ok := backendSpecs[backend]
		if !ok || !spec.supported() {
			continue
		}
		if deviceCircuitOpen(backend, device) {
			continue
		}
		cached, ok := d.cache[identity+"/"+string(backend)+"/"+device]
		if !ok || deviceNeedsProbe(backend, device) {
			cold = true
			break
		}
		if cached.Available && !deviceCircuitOpen(backend, device) {
			d.mu.Unlock()
			return cached, ctx.Err()
		}
	}
	d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return HardwareProbe{}, err
	}
	if cold {
		d.warm(executable, configured, device)
		return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeWarming, Identity: identity}, nil
	}
	return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeEncodeFailed, Identity: identity}, nil
}

// warm runs one background Select per (identity, configuration, device) so a
// cold cache is filled exactly once, off every request path.
func (d *HardwareDetector) warm(executable, configured, device string) {
	key := executable + "/" + configured + "/" + device
	d.mu.Lock()
	if d.warming == nil {
		d.warming = map[string]bool{}
	}
	if d.warming[key] {
		d.mu.Unlock()
		return
	}
	d.warming[key] = true
	d.mu.Unlock()
	supervise.Go("decoder.hardware.warm", func() {
		defer func() { d.mu.Lock(); delete(d.warming, key); d.mu.Unlock() }()
		d.Select(context.Background(), executable, configured, device)
	})
}

// Select resolves the owner's configured backend. "auto" probes in order and
// takes the first that works; a named backend that fails its probe falls back to
// software rather than failing the session.
func (d *HardwareDetector) Select(ctx context.Context, executable, configured, device string) HardwareProbe {
	if configured == "" {
		configured = "auto"
	}
	if configured == string(BackendSoftware) {
		return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}
	}
	if configured != "auto" {
		if !ValidBackend(configured) {
			return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}
		}
		probe := d.Probe(ctx, executable, HardwareBackend(configured), device)
		if probe.Available && !deviceCircuitOpen(probe.Backend, probe.Device) {
			return probe
		}
		fallback := HardwareProbe{Backend: BackendSoftware, Available: true, Reason: probe.Reason, Identity: probe.Identity}
		return fallback
	}
	for _, backend := range HardwareBackends {
		if probe := d.Probe(ctx, executable, backend, device); probe.Available {
			return probe
		}
	}
	identity, _ := hardwareRuntimeIdentity(ctx, executable)
	return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeEncodeFailed, Identity: identity}
}

// Report lists every backend's verdict for the owner capacity read. Results are
// ordered by backend name so the published report is stable.
func (d *HardwareDetector) Report(ctx context.Context, executable, device string) []HardwareProbe {
	out := []HardwareProbe{{Backend: BackendSoftware, Available: true, Reason: ProbeAvailable, Encoder: "libx264"}}
	for _, backend := range HardwareBackends {
		out = append(out, d.Probe(ctx, executable, backend, device))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Backend < out[j].Backend })
	return out
}
