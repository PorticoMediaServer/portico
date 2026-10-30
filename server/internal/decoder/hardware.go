package decoder

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
	// child directly, because a synthetic clip borrows no source custody.
	Run func(ctx context.Context, cmd *exec.Cmd) error
	// RuntimeIdentity is a trusted composition/test seam. Nil uses the full executable and
	// resolved-library identity; it is never supplied by a playback request.
	RuntimeIdentity func(context.Context, string) (string, error)
	identityMu      sync.Mutex
	identityFlight  *hardwareIdentityFlight

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

// SelectCachedContext performs the exact runtime observation under the original caller's
// cancellation/deadline. Playback calls this BEFORE acquiring any SQL transaction.
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
	identity, err := d.runtimeIdentity(ctx, executable)
	if err != nil {
		if ctx.Err() != nil {
			return HardwareProbe{}, ctx.Err()
		}
		return HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeEncodeFailed}, nil
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

// ConversionRequest describes one video output rung. It is deliberately free of
// session identity: the same request always produces the same argument graph.
type ConversionRequest struct {
	// Source facts are never inferred from the requested output codec/depth.
	SourceVideoCodec string
	SourceBitDepth   int
	// HDR10+ carry is explicit intent over measured source facts. Empty intent
	// keeps the encoder's default-off behavior, regardless of bundle capability.
	HDR10PlusIntent HDR10PlusEncoding
	HDR10PlusSource HDR10PlusEncodeSource
	// SoftwareDecode disables only hardware decoding. Qualified hardware
	// filters/encoding may still upload the software-decoded frames.
	SoftwareDecode bool
	// DisableHardwareDecode also forces system-frame filters for legacy source
	// geometry corrections (rotation and non-square pixels). Keep that meaning.
	DisableHardwareDecode bool
	Target                *ConversionTarget
	BurnIn                *BurnIn
	Toolchain             *ToolchainFacts
	AudioCodec            string
	Probe                 HardwareProbe
	ConvertVideo          bool
	CopyVideo             bool
	ToneMap               bool
	ToneMapAlgorithm      string
	MaxHeight             int
	MaxWidth              int
	VideoBitrateBPS       int
	AudioBitrateBPS       int
	AudioChannels         int
	ConvertAudio          bool
	CopyAudio             bool
	SoftwarePreset        string
	KeyframeSeconds       int
	// Deinterlace inserts a deinterlacer ahead of every other picture filter.
	Deinterlace bool
	// MaxFrameRate caps the output rate; zero leaves the source rate alone.
	MaxFrameRate float64
	// Downmix replaces ffmpeg's implicit channel reduction with a fixed matrix,
	// make-up gain and a limiter, so the result does not depend on which sample
	// format the source decoder happened to produce.
	Downmix bool
	// AudioSampleRate is explicit when known; zero selects 48 kHz. Lossless
	// targets may select higher rates through ConversionTarget.
	AudioSampleRate int
}

// ConversionGraph is the argument contribution of one conversion. Input arguments
// precede -i; Video and Audio follow it. Stages document what actually ran where.
type ConversionGraph struct {
	ExtraInput []string        `json:"-"`
	VideoMap   string          `json:"-"`
	Input      []string        `json:"-"`
	Video      []string        `json:"-"`
	Audio      []string        `json:"-"`
	Stages     []HardwareStage `json:"stages"`
	Backend    HardwareBackend `json:"backend"`
}

// HardwareStagesRequireDevice carries the graph's device requirement to a
// bridge executor, including GPU filters paired with a software encoder.
func HardwareStagesRequireDevice(stages []HardwareStage) bool {
	for _, stage := range stages {
		if stage.Execution == ExecutionHardware {
			return true
		}
	}
	return false
}

// SoftwarePresets are the x264 presets an owner may choose.
var SoftwarePresets = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower"}

const DefaultSoftwarePreset = "veryfast"

func ValidSoftwarePreset(p string) bool {
	for _, v := range SoftwarePresets {
		if v == p {
			return true
		}
	}
	return false
}

// BuildConversion turns a rung into ffmpeg arguments. A hardware backend that
// probed without its own filters keeps its encoder and tone maps in software,
// downloading frames only where the graph actually needs it.
func BuildConversion(r ConversionRequest) (ConversionGraph, error) {
	out := ConversionGraph{Backend: BackendSoftware, Stages: []HardwareStage{}}
	target := ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", ToneMap: r.ToneMap, Width: r.MaxWidth, Height: r.MaxHeight, FrameRate: r.MaxFrameRate, VideoBitrateBPS: r.VideoBitrateBPS, AudioCodec: r.AudioCodec, AudioChannels: r.AudioChannels, AudioBitrateBPS: r.AudioBitrateBPS, AudioSampleRate: r.AudioSampleRate}
	var err error
	if r.Target != nil {
		target = *r.Target
	}
	target, err = target.Normalized()
	if err != nil {
		return out, err
	}
	if r.Target != nil {
		r.ToneMap, r.MaxWidth, r.MaxHeight, r.MaxFrameRate, r.VideoBitrateBPS = target.ToneMap, target.Width, target.Height, target.FrameRate, target.VideoBitrateBPS
		r.AudioCodec, r.AudioChannels, r.AudioBitrateBPS, r.AudioSampleRate = target.AudioCodec, target.AudioChannels, target.AudioBitrateBPS, target.AudioSampleRate
	}
	pixel, surface := "yuv420p", "nv12"
	if target.BitDepth == 10 {
		pixel, surface = "yuv420p10le", "p010le"
	}
	// A Dolby Vision profile without a base layer is only converted correctly
	// through libplacebo's reshaping, which runs on system frames. Force the
	// software pipeline for that picture so a GPU host does not hold device
	// frames the filter cannot take (VS-1e; hardware tone mapping is HW-3).
	if r.ConvertVideo && target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility) == "" {
		r.Probe = HardwareProbe{Backend: BackendSoftware, Identity: r.Probe.Identity}
	}

	if r.CopyVideo && r.ConvertVideo || r.CopyAudio && r.ConvertAudio || target.CopyDolbyVision && !r.CopyVideo {
		return out, ErrInvalidConfiguration
	}
	if r.HDR10PlusIntent != "" && (!r.ConvertVideo || r.SourceVideoCodec != r.HDR10PlusSource.Codec || r.SourceBitDepth != r.HDR10PlusSource.BitDepth) {
		return out, ErrInvalidConfiguration
	}
	if r.ConvertVideo && (target.BitDepth != 8 && target.BitDepth != 10 || target.VideoCodec == "h264" && (target.BitDepth != 8 || target.DynamicRange != "sdr")) {
		return out, ErrInvalidConfiguration
	}
	if math.IsNaN(r.MaxFrameRate) || math.IsInf(r.MaxFrameRate, 0) || r.MaxFrameRate < 0 || r.MaxFrameRate > 240 {
		return out, ErrInvalidConfiguration
	}
	if r.MaxWidth < 0 || r.MaxWidth > 32768 || r.MaxHeight < 0 || r.MaxHeight > 4320 || r.VideoBitrateBPS < 0 || r.VideoBitrateBPS > 200_000_000 || r.AudioBitrateBPS < 0 || r.AudioBitrateBPS > 4_096_000 || r.AudioChannels < 0 || r.AudioChannels > 64 {
		return out, ErrInvalidConfiguration
	}
	if (r.ToneMap || r.BurnIn != nil) && !r.ConvertVideo {
		return out, ErrInvalidConfiguration
	}
	algorithm := r.ToneMapAlgorithm
	if algorithm == "" {
		algorithm = DefaultToneMapAlgorithm
	}
	if !ValidToneMapAlgorithm(algorithm) {
		return out, ErrInvalidConfiguration
	}
	preset := r.SoftwarePreset
	if preset == "" {
		preset = DefaultSoftwarePreset
	}
	if !ValidSoftwarePreset(preset) {
		return out, ErrInvalidConfiguration
	}
	keyframes := r.KeyframeSeconds
	if keyframes <= 0 {
		keyframes = 6
	}
	backend := BackendSoftware
	spec := backendSpec{}
	// Persisted hardware results from another selected runtime cannot execute
	// its graph. Legacy argument-only callers may omit toolchain facts.
	mismatchedRuntime := r.Toolchain != nil && r.Toolchain.Identity != "" && r.Probe.Identity != "" && r.Toolchain.Identity != r.Probe.Identity
	if !mismatchedRuntime && r.Probe.Available && r.Probe.Backend != BackendSoftware {
		if s, ok := backendSpecs[r.Probe.Backend]; ok && s.supported() {
			backend, spec = r.Probe.Backend, s
		}
	}
	// The pinned VideoToolbox encoder/upload path writes colour tags but has no
	// AVFrame mastering-display/content-light metadata writer. Preserve known
	// static HDR10 metadata with the software encoder instead of silently losing
	// it on a hardware route. Copied video, SDR and HLG keep their existing path.
	if r.ConvertVideo && backend == BackendVideoToolbox && target.DynamicRange == "hdr10" && (target.MasterDisplay != "" || target.MaxCLL != "") {
		backend, spec = BackendSoftware, backendSpec{}
	}
	encoder := ""
	if r.ConvertVideo {
		encoder = encoderFor(backend, spec)
		if target.VideoCodec != "h264" {
			hardwareEncoder := target.VideoCodec + "_" + string(backend)
			if backend != BackendSoftware && r.Probe.OutputEncoders[target.VideoCodec+":"+strconv.Itoa(target.BitDepth)] {
				encoder = hardwareEncoder
			} else {
				backend, spec = BackendSoftware, backendSpec{}
			}
		}
		if backend == BackendSoftware {
			encoderFacts := r.Toolchain
			if r.Target == nil {
				encoderFacts = nil
			}
			encoder, err = softwareVideoEncoder(target.VideoCodec, encoderFacts)
			if err != nil {
				return out, err
			}
		}
		// Every hardware quality mode must pass its own encode probe. Capped
		// quality uses software rather than ignoring a requested bitrate ceiling.
		if target.RateControl == "quality" && backend != BackendSoftware && (target.VideoBitrateBPS > 0 || !r.Probe.OutputQuality[target.VideoCodec+":"+strconv.Itoa(target.BitDepth)]) {
			backend, spec = BackendSoftware, backendSpec{}
			encoder, err = softwareVideoEncoder(target.VideoCodec, r.Toolchain)
			if err != nil {
				return out, err
			}
		}
	}
	out.Backend = backend
	if r.CopyVideo {
		if target.DolbyVisionProfile > 0 && r.Target != nil && !target.CopyDolbyVision {
			base := DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility)
			if base == "" || base != target.DynamicRange {
				return out, errors.New("dolby_vision_base_layer_unavailable")
			}
			if r.Toolchain == nil || !r.Toolchain.BitstreamFilters["dovi_rpu"] || target.DolbyVisionProfile == 7 && !r.Toolchain.BitstreamFilters["filter_units"] {
				return out, errors.New("dolby_vision_strip_filter_unavailable")
			}
			filter := "dovi_rpu=strip=1"
			if target.DolbyVisionProfile == 7 {
				filter += ",filter_units=remove_types=63"
			}
			out.Video = append(out.Video, "-bsf:v", filter)
		}
		copyFilter, err := DolbyVisionCopyBitstreamFilter(target, r.Toolchain)
		if err != nil {
			return out, err
		}
		if copyFilter != "" {
			out.Video = append(out.Video, "-bsf:v", copyFilter)
		}
		out.Video = append(out.Video, "-c:v", "copy")
		out.Stages = append(out.Stages, HardwareStage{StageDecode, ExecutionSoftware}, HardwareStage{StageEncode, ExecutionSoftware})
	}
	if r.ConvertVideo {
		hdr10PlusOptions, err := HDR10PlusLibX265Options(HDR10PlusEncodeRequest{
			Intent: r.HDR10PlusIntent, Target: target, Source: r.HDR10PlusSource,
			Encoder: encoder, Deinterlace: r.Deinterlace, BurnIn: r.BurnIn != nil,
		}, r.Toolchain)
		if err != nil {
			return out, err
		}
		native := backend != BackendSoftware && spec.scale != nil && (r.ToneMap && r.Probe.NativeFilters && spec.toneMap != nil || !r.ToneMap && (r.Probe.ScaleFilters || r.Probe.NativeFilters))
		// A subtitle burn-in used to download the picture on purpose. When the
		// device's own composite passed its probe, the frames stay where they
		// are and the device blends the subtitle surface in instead.
		deviceBurnIn := r.BurnIn != nil && native && r.Probe.BurnInFilters && hardwareBurnInSupported(backend, *r.BurnIn, target)
		if r.BurnIn != nil && !deviceBurnIn {
			native = false
		}
		if r.DisableHardwareDecode {
			native = false
		}
		if r.ToneMap && target.SourceRange == "hlg" && backend == BackendVAAPI && target.MasterDisplay == "" {
			native = false
		}
		if r.Toolchain != nil && r.ToneMap {
			filter := map[HardwareBackend]string{BackendNVENC: "tonemap_cuda", BackendVAAPI: "tonemap_vaapi", BackendQSV: "vpp_qsv"}[backend]
			native = native && r.Toolchain.Filters[filter]
		}
		if r.ToneMap && !native && r.Toolchain != nil && (!r.Toolchain.Filters["zscale"] || !r.Toolchain.Filters["tonemap"]) && ((!r.Toolchain.VulkanAvailable && !r.Toolchain.OpenCLAvailable) || spec.upload != "") {
			return out, errors.New("hdr_tone_mapping_filter_unavailable")
		}
		if target.DolbyVisionProfile > 0 {
			compatible := DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility) != ""
			if !compatible {
				// A profile without a base layer needs libplacebo's reshaping.
				// The HDR-to-HDR reshape runs that filter without any tone
				// mapping, so it needs the Vulkan device and the filter itself;
				// every other conversion is judged by its tone-map route.
				reshapeOnly := !r.ToneMap && target.DynamicRange != "sdr"
				if reshapeOnly {
					if r.Toolchain == nil || !r.Toolchain.VulkanAvailable || !r.Toolchain.Filters["libplacebo"] {
						return out, errors.New("dolby_vision_reshaping_unavailable")
					}
				} else if !r.ToneMap || r.Toolchain == nil || !r.Toolchain.VulkanAvailable {
					return out, errors.New("dolby_vision_reshaping_unavailable")
				}
				native = false
			}
		}
		useNativeDeinterlace := native && r.Deinterlace && r.Probe.DeinterlaceFilters && nativeDeinterlace(backend) != "" && r.MaxFrameRate == 0
		decode := ExecutionSoftware
		qualifiedDecode := !r.SoftwareDecode && !r.DisableHardwareDecode && r.Probe.DecodeCodecs[hardwareDecodeKey(r.SourceVideoCodec, r.SourceBitDepth)]
		if backend != BackendSoftware && spec.device != nil {
			out.Input = append(out.Input, spec.device(r.Probe.Device)...)
			// Device initialization alone is software decode.
		}
		if qualifiedDecode && backend != BackendSoftware {
			if args := hardwareDecodeArgs(backend); len(args) != 0 {
				out.Input = append(out.Input, args...)
				decode = ExecutionHardware
			}
		}
		filters := []string{}
		composite := ""
		hardwareFrames := decode == ExecutionHardware
		if hardwareFrames && (!native || r.Deinterlace && !useNativeDeinterlace || r.MaxFrameRate > 0) {
			filters = append(filters, "hwdownload", "format="+hardwareDownloadFormat(r.SourceBitDepth))
			hardwareFrames = false
		}
		// Deinterlacing and the frame-rate ceiling run on system frames, ahead of
		// any upload, so they are the same graph on every backend. Only frames the
		// decoder flags as interlaced are touched: mixed content stays sharp.
		if r.Deinterlace && !useNativeDeinterlace {
			filter := "yadif"
			if r.Toolchain != nil {
				if r.Toolchain.Filters["bwdif"] {
					filter = "bwdif"
				} else if !r.Toolchain.Filters["yadif"] {
					return out, errors.New("deinterlace_filter_unavailable")
				}
			}
			filters = append(filters, filter+"=mode=send_frame:parity=auto:deint=interlaced")
		}
		if r.MaxFrameRate > 0 {
			filters = append(filters, "fps=fps='min(source_fps,"+strconv.FormatFloat(r.MaxFrameRate, 'f', -1, 64)+")'")
		}
		pre := len(filters)
		toneExecution, scaleExecution := ExecutionSoftware, ExecutionSoftware
		if native {
			if useNativeDeinterlace {
				filters = append(filters, nativeDeinterlace(backend))
			}
			if r.ToneMap {
				filters = append(filters, spec.toneMap(algorithm))
				toneExecution = ExecutionHardware
			}
			if spec.upload != "" && !hardwareFrames {
				// The upload sits between the system-frame filters and the
				// backend's own, wherever either list happens to be empty.
				upload := spec.upload
				if r.ToneMap || target.BitDepth == 10 {
					upload = strings.NewReplacer("format=nv12", "format=p010", "format=yuv420p", "format=p010le").Replace(upload)
				}
				hardware := append([]string{upload}, filters[pre:]...)
				filters = append(filters[:pre:pre], hardware...)
			}
			scale := boundedScaleFilter(backend, r.MaxWidth, r.MaxHeight)
			encoderSurface := map[HardwareBackend]string{BackendNVENC: "scale_cuda=format=", BackendVAAPI: "scale_vaapi=format=", BackendQSV: "vpp_qsv=format="}[backend]
			if deviceBurnIn && encoderSurface != "" {
				// Blend on the source canvas, exactly where the processor burn
				// does, then scale: a subtitle fitted to the source would land
				// in the wrong place on an already-scaled picture.
				burnInput := *r.BurnIn
				burnInput.DynamicRange = target.DynamicRange
				graph, extra, err := hardwareBurnInGraph(backend, burnInput, strings.Join(filters, ","), scale+","+encoderSurface+surface)
				if err != nil {
					return out, err
				}
				composite, out.ExtraInput = graph, extra
				out.VideoMap = "[burn_output]"
				out.Stages = append(out.Stages, HardwareStage{"subtitle_burn_in", ExecutionHardware})
				scaleExecution = ExecutionHardware
				filters = nil
			} else {
				filters = append(filters, scale)
				scaleExecution = ExecutionHardware
				if encoderSurface != "" {
					filters = append(filters, encoderSurface+surface)
				}
			}
		} else {
			if r.ToneMap {
				if r.Toolchain != nil && r.Toolchain.VulkanAvailable && spec.upload == "" && (target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility) == "" || !r.Toolchain.Filters["zscale"] || !r.Toolchain.Filters["tonemap"]) {
					out.Input = append(out.Input, "-init_hw_device", "vulkan=tone", "-filter_hw_device", "tone")
					filters = append(filters, "format=yuv420p10le,hwupload,libplacebo=tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:format=yuv420p,hwdownload,format=yuv420p")
					toneExecution = ExecutionHardware
				} else if r.Toolchain != nil && r.Toolchain.OpenCLAvailable && spec.upload == "" && (!r.Toolchain.Filters["zscale"] || !r.Toolchain.Filters["tonemap"]) {
					out.Input = append(out.Input, "-init_hw_device", "opencl=tone", "-filter_hw_device", "tone")
					filters = append(filters, "format=p010,hwupload,tonemap_opencl=tonemap=hable:desat=0:format=nv12,hwdownload,format=nv12")
					toneExecution = ExecutionHardware
				} else {
					filters = append(filters, SoftwareToneMap(algorithm))
				}
			}
			// A Dolby Vision profile without a base layer keeps its HDR range
			// through libplacebo's reshaping alone; there is no tone mapping
			// to apply when the target range is HDR10 or HLG.
			if !r.ToneMap && target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility) == "" && target.DynamicRange != "sdr" && r.Toolchain != nil && r.Toolchain.VulkanAvailable && r.Toolchain.Filters["libplacebo"] {
				transfer := "smpte2084"
				if target.DynamicRange == "hlg" {
					transfer = "arib-std-b67"
				}
				out.Input = append(out.Input, "-init_hw_device", "vulkan=tone", "-filter_hw_device", "tone")
				filters = append(filters, "format=yuv420p10le,hwupload,libplacebo=colorspace=bt2020nc:color_primaries=bt2020:color_trc="+transfer+":format=yuv420p10le,hwdownload,format=yuv420p10le")
				toneExecution = ExecutionHardware
			}
			if r.BurnIn != nil {
				burnInput := *r.BurnIn
				burnInput.DynamicRange = target.DynamicRange
				burn, extra, err := burnFilter(burnInput, r.Toolchain)
				if err != nil {
					return out, err
				}
				filters = append(filters, burn)
				out.ExtraInput = extra
				if len(extra) > 0 || target.DynamicRange != "sdr" {
					out.VideoMap = "[burn_output]"
				}
				out.Stages = append(out.Stages, HardwareStage{"subtitle_burn_in", ExecutionSoftware})
			}
			if len(hdr10PlusOptions) == 0 {
				filters = append(filters, boundedScaleFilter(BackendSoftware, r.MaxWidth, r.MaxHeight))
				if target.DynamicRange != "sdr" {
					filters = append(filters, "format="+pixel)
				}
			}
			if spec.upload != "" {
				filters = append(filters, "format="+surface, strings.NewReplacer("format=nv12", "format="+surface, "format=yuv420p", "format="+pixel).Replace(spec.upload))
			}
		}
		if len(hdr10PlusOptions) > 0 && (backend != BackendSoftware || decode != ExecutionSoftware || native || len(filters) > 0 || out.VideoMap != "" || len(out.ExtraInput) > 0) {
			return out, ErrInvalidConfiguration
		}
		if composite != "" {
			out.Video = append(out.Video, "-filter_complex", composite)
		} else if len(filters) > 0 {
			if out.VideoMap != "" {
				out.Video = append(out.Video, "-filter_complex", "[0:v:0]"+strings.Join(filters, ",")+"[burn_output]")
			} else {
				out.Video = append(out.Video, "-vf", strings.Join(filters, ","))
			}
		}
		out.Video = append(out.Video, "-c:v", encoder)
		out.Video = append(out.Video, hdr10PlusOptions...)
		if backend == BackendSoftware {
			out.Video = append(out.Video, "-threads", "2")
		}
		if backend == BackendSoftware || backend == BackendAMF || backend == BackendVideoToolbox {
			out.Video = append(out.Video, "-pix_fmt", pixel)
		}
		out.Video = append(out.Video, videoRateArgs(target, encoder, preset)...)
		out.Video = append(out.Video, videoColorArgs(target)...)
		if target.VideoCodec == "h264" {
			level := target.VideoLevel
			if level == 0 {
				level = H264OutputLevel(target.Width, target.Height, target.FrameRate)
			}
			if encoder == "libx264" {
				out.Video = append(out.Video, "-x264-params", func() string {
					if target.VideoProfile == "baseline" {
						return "cabac=0:8x8dct=0:open-gop=0"
					}
					if target.VideoProfile == "main" {
						return "cabac=1:8x8dct=0:open-gop=0"
					}
					return "cabac=1:8x8dct=1:open-gop=0"
				}())
			}
			profile := target.VideoProfile
			if profile == "" {
				profile = "high"
			}
			out.Video = append(out.Video, "-profile:v", profile, "-level:v", strconv.FormatFloat(float64(level)/10, 'f', 1, 64))
		}
		if target.VideoCodec == "hevc" {
			profile := "main"
			if target.BitDepth == 10 {
				profile = "main10"
			}
			level := target.VideoLevel
			if level == 0 {
				level = HEVCOutputLevel(target.Width, target.Height, target.FrameRate)
			}
			out.Video = append(out.Video, "-profile:v", profile, "-tag:v", "hvc1", "-level:v", strconv.FormatFloat(float64(level)/30, 'f', 1, 64))
			if encoder == "libx265" {
				params := "pools=2:frame-threads=2:open-gop=0:repeat-headers=1"
				if target.DynamicRange == "hdr10" {
					params += ":hdr10=1"
					if target.MasterDisplay != "" {
						params += ":master-display=" + target.MasterDisplay
					}
					if target.MaxCLL != "" {
						params += ":max-cll=" + target.MaxCLL
					}
				}
				out.Video = append(out.Video, "-x265-params", params)
			}
		}
		if target.VideoCodec == "hevc" && target.DolbyVisionProfile > 0 {
			out.Video = append(out.Video, "-bsf:v", "filter_units=remove_types=62|63")
		}
		out.Video = append(out.Video, "-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(keyframes)+")")
		switch backend {
		case BackendNVENC:
			out.Video = append(out.Video, "-forced-idr", "1")
		case BackendQSV, BackendAMF:
			out.Video = append(out.Video, "-forced_idr", "1")
		}
		encode := ExecutionSoftware
		if backend != BackendSoftware {
			encode = ExecutionHardware
		}
		out.Stages = append(out.Stages, HardwareStage{StageDecode, decode})
		if r.Deinterlace {
			execution := ExecutionSoftware
			if useNativeDeinterlace {
				execution = ExecutionHardware
			}
			out.Stages = append(out.Stages, HardwareStage{StageDeinterlace, execution})
		}
		if r.ToneMap || toneExecution == ExecutionHardware {
			// A tone map or a Dolby Vision reshape that ran on the Vulkan
			// device is a hardware stage, whatever the decoder and encoder do.
			out.Stages = append(out.Stages, HardwareStage{StageToneMap, toneExecution})
		}
		if (r.MaxHeight > 0 || r.MaxWidth > 0) && len(hdr10PlusOptions) == 0 {
			out.Stages = append(out.Stages, HardwareStage{StageScale, scaleExecution})
		}
		out.Stages = append(out.Stages, HardwareStage{StageEncode, encode})
	}
	if r.CopyAudio {
		out.Audio = append(out.Audio, "-c:a", "copy")
	}
	if r.ConvertAudio {
		audio, err := buildAudioTarget(target, r.Downmix, r.Toolchain)
		if err != nil {
			return out, err
		}
		out.Audio = append(out.Audio, audio...)
	}

	return out, nil
}

// DownmixFilter is the one channel reduction this server performs. The matrix is
// ffmpeg's ITU-R BS.775 default with the LFE left out, forced to normalise so it
// cannot clip whether the decoder produced floats or integers; a stereo result
// gets three decibels of make-up gain, because a normalised fold-down of a film
// mix is otherwise markedly quieter than the same film's own stereo track, and a
// limiter takes the rare full-scale passage that gain would push over.
func DownmixFilter(channels int) string {
	if channels >= 6 {
		return "aresample=rematrix_maxval=1.0,aformat=channel_layouts=5.1"
	}
	return "aresample=rematrix_maxval=1.0,aformat=channel_layouts=stereo,volume=3dB,alimiter=limit=0.95:level=false"
}

func encoderFor(backend HardwareBackend, spec backendSpec) string {
	if backend == BackendSoftware || spec.encoder == "" {
		return "libx264"
	}
	return spec.encoder
}

// HDRTransfers are the probe transfer characteristics that mean HDR: PQ and HLG.
// bt2020-10 and bt2020-12 are the BT.2020 *SDR* transfer (the BT.709 curve at a
// higher precision) and smpte428 is the DCI cinema curve; tone mapping any of
// them as PQ crushes a picture that was never HDR.
var HDRTransfers = []string{"smpte2084", "arib-std-b67"}

// IsHDR reports whether probe facts describe an HDR source.
func IsHDR(transfer, primaries string) bool {
	transfer, primaries = strings.ToLower(strings.TrimSpace(transfer)), strings.ToLower(strings.TrimSpace(primaries))
	for _, v := range HDRTransfers {
		if transfer == v {
			return true
		}
	}
	_ = primaries
	// BT.2020 primaries with no stated transfer are wide-gamut SDR far more often
	// than they are untagged PQ, and the two mistakes are not equal: an SDR
	// picture tone mapped as PQ is ruined, an untagged PQ picture left alone is
	// merely flat. Primaries alone therefore never mean HDR.
	return false
}

// All backends use the same fit expression; rounding down keeps codec-aligned
// dimensions within both client bounds and never enlarges the source.
func boundedScaleFilter(backend HardwareBackend, width, height int) string {
	ratio := "1"
	if width > 0 {
		ratio = "min(" + ratio + "," + strconv.Itoa(width) + "/iw)"
	}
	if height > 0 {
		ratio = "min(" + ratio + "," + strconv.Itoa(height) + "/ih)"
	}
	filter := "scale"
	switch backend {
	case BackendNVENC:
		filter = "scale_cuda"
	case BackendVAAPI:
		filter = "scale_vaapi"
	case BackendQSV:
		filter = "vpp_qsv"
	}
	if backend == BackendSoftware {
		display := "min(iw*sar,ih*dar)"
		ratio = "1"
		if width > 0 {
			ratio = "min(" + ratio + "," + strconv.Itoa(width) + "/(iw*sar))"
		}
		if height > 0 {
			ratio = "min(" + ratio + "," + strconv.Itoa(height) + "/ih)"
		}
		return "scale=w='max(2,trunc(" + display + "*" + ratio + "/2)*2)':h='max(2,trunc(ih*" + ratio + "/2)*2)',setsar=1"
	}
	return filter + "=w='max(2,trunc(iw*" + ratio + "/2)*2)':h='max(2,trunc(ih*" + ratio + "/2)*2)'"
}
