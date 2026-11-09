package decoder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// errHardwareBurnUnsupported means the device cannot do this burn-in, so the
// processor path is the right one and nothing is lost but a copy.
var errHardwareBurnUnsupported = fmt.Errorf("hardware_subtitle_burn_in_unsupported")

// validateBurnIn is the one gate every burn-in passes, whether the processor or
// the device blends it: a sealed absolute file, a real position inside a day, a
// sane offset and origin, and a fonts directory when one is named. The device
// path used to build a graph without these checks, which would have accepted
// input the processor path refuses.
func validateBurnIn(b BurnIn) error {
	if !filepath.IsAbs(b.File) || filepath.Clean(b.File) != b.File || strings.ContainsAny(b.File, "\n\r\x00") ||
		b.PositionUS < 0 || b.PositionUS > 86400000000 || b.OffsetUS < -600000000 || b.OffsetUS > 600000000 ||
		b.InputStartUS < -86400000000 || b.InputStartUS > 86400000000 {
		return ErrInvalidConfiguration
	}
	if b.FontsDirectory != "" && (!filepath.IsAbs(b.FontsDirectory) || filepath.Clean(b.FontsDirectory) != b.FontsDirectory || strings.ContainsAny(b.FontsDirectory, "\n\r\x00")) {
		return ErrInvalidConfiguration
	}
	return nil
}

// burnInPlan is one backend's device composite. The pair of pixel formats is the
// one that backend's overlay filter documents: the picture is normalized on the
// device to picture, the subtitle surface carries alpha as subtitle, both are
// moved by upload, and post returns the blended frames to the encoder's surface
// after the picture is scaled.
type burnInPlan struct {
	// pre normalizes the picture into the format the composite expects.
	pre      string
	subtitle string
	upload   string
	overlay  string
	// post reaches the encoder's surface once the blend and the scale are done.
	post string
}

// hardwareBurnInFor names the composite for a backend that has one. CUDA takes a
// YUV420P picture with a YUVA420P subtitle, the only alpha pair that filter
// accepts; VAAPI and QSV take an NV12 picture with a BGRA subtitle. Apple's
// VideoToolbox and AMD's AMF have no composite, so they keep the processor
// burn-in and nothing is claimed for them.
func hardwareBurnInFor(backend HardwareBackend, surface string) (burnInPlan, bool) {
	switch backend {
	case BackendNVENC:
		return burnInPlan{pre: "scale_cuda=format=yuv420p", subtitle: "yuva420p", upload: "hwupload_cuda",
			overlay: "overlay_cuda=eof_action=pass:shortest=0", post: "scale_cuda=format=" + surface}, true
	case BackendVAAPI:
		return burnInPlan{pre: "scale_vaapi=format=nv12", subtitle: "bgra", upload: "hwupload",
			overlay: "overlay_vaapi=eof_action=pass:shortest=0", post: "scale_vaapi=format=" + surface}, true
	case BackendQSV:
		// The same frames pool the backend's own upload asks for.
		return burnInPlan{pre: "vpp_qsv=format=nv12", subtitle: "bgra", upload: "hwupload=extra_hw_frames=32",
			overlay: "overlay_qsv=eof_action=pass:shortest=0", post: "vpp_qsv=format=" + surface}, true
	}
	return burnInPlan{}, false
}

// nonEmpty keeps a chain's real steps and drops the ones a path does not have.
func nonEmpty(steps []string) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step) != "" {
			out = append(out, step)
		}
	}
	return out
}

// hasHardwareBurnIn reports whether this backend documents a composite at all.
func hasHardwareBurnIn(backend HardwareBackend) bool {
	_, ok := hardwareBurnInFor(backend, "nv12")
	return ok
}

// hardwareBurnInSupported answers whether this burn-in can run on the device at
// all: a bitmap subtitle, a standard-range 8-bit picture, and a backend whose own
// composite exists. Text subtitles are rendered by libass on system frames, and
// an HDR or 10-bit picture needs a surface the probe does not prove, so both stay
// on the processor.
func hardwareBurnInSupported(backend HardwareBackend, b BurnIn, target ConversionTarget) bool {
	if !hasHardwareBurnIn(backend) {
		return false
	}
	if validateBurnIn(b) != nil {
		return false
	}
	if target.DynamicRange != "sdr" || target.BitDepth != 8 || b.DynamicRange != "" && b.DynamicRange != "sdr" {
		return false
	}
	switch b.Format {
	case "pgs", "vobsub", "mks", "dvb":
		return b.Width >= 1 && b.Height >= 1 && b.Width <= 8192 && b.Height <= 4320
	}
	return false
}

// hardwareSubtitleChain builds the subtitle half of the composite: the same
// clock shift and the same fit the processor path uses, in the pixel format the
// device's composite takes, then the upload.
func hardwareSubtitleChain(plan burnInPlan, b BurnIn) (string, []string, error) {
	if err := validateBurnIn(b); err != nil {
		return "", nil, err
	}
	inputs, err := bitmapSubtitleInput(b)
	if err != nil {
		return "", nil, err
	}
	chain := "[1:s:0]settb=AVTB,setpts=PTS+(" + seconds(b.OffsetUS-b.PositionUS) + ")/TB,format=" + plan.subtitle + "," + subtitleFit(b) + "," + plan.upload + "[burn_cues]"
	return chain, inputs, nil
}

// hardwareBurnInGraph is the whole device path: the picture normalized into the
// composite's own format, the subtitle surface uploaded beside it, the device
// blend at source size — the same canvas the processor path blends on — and only
// then the scale and the encoder surface. The probe builds this same graph, so a
// passing probe is a probe of the filter set a session will run.
func hardwareBurnInGraph(backend HardwareBackend, b BurnIn, picture, after string) (string, []string, error) {
	plan, ok := hardwareBurnInFor(backend, "")
	if !ok {
		return "", nil, errHardwareBurnUnsupported
	}
	cues, inputs, err := hardwareSubtitleChain(plan, b)
	if err != nil {
		return "", nil, err
	}
	// Hardware-decoded frames with no preprocessing arrive in no named chain, so
	// the picture half may be empty; a leading comma there is an invalid graph.
	pictureChain := []string{picture, plan.pre}
	head := "[0:v:0]"
	if joined := strings.Join(nonEmpty(pictureChain), ","); joined != "" {
		head += joined
	}
	return head + "[burn_picture];" + cues + ";[burn_picture][burn_cues]" + plan.overlay + "[burn_mixed];[burn_mixed]" + after + "[burn_output]", inputs, nil
}

// probeBurnIn proves the composite on this device with the graph a session runs,
// including a source canvas that differs from the target one: the picture is
// uploaded and normalized, a subtitle that carries alpha from the source is
// fitted and uploaded, the device blends, and the result is scaled and converted
// to the encoder's surface and read back. A device or driver that cannot hold the
// pair fails here, so a session keeps the processor burn-in instead of
// discovering it mid-stream.
func (d *HardwareDetector) probeBurnIn(ctx context.Context, executable string, spec backendSpec, backend HardwareBackend, device string, timeout time.Duration) bool {
	plan, ok := hardwareBurnInFor(backend, "nv12")
	if !ok || spec.upload == "" {
		return false
	}
	// The synthetic subtitle carries alpha from its own source; a late format
	// conversion cannot put back alpha a filter never had.
	graph, _, err := hardwareBurnInGraph(backend, BurnIn{Format: "pgs", File: probeSubtitleStandIn(), Width: 160, Height: 120},
		spec.upload, boundedScaleFilter(backend, 0, 100)+","+plan.post)
	if err != nil {
		return false
	}
	graph = strings.Replace(graph, "[1:s:0]", "[1:v]", 1)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if spec.device != nil {
		args = append(args, spec.device(device)...)
	}
	args = append(args,
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=10:duration=0.5",
		"-f", "lavfi", "-i", "color=c=black@0.0:s=160x120:rate=10:duration=0.5,format=yuva420p",
		"-filter_complex", graph, "-map", "[burn_output]", "-frames:v", "3", "-an", "-sn", "-dn", "-f", "null", "-")
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.run(bounded, executable, args) == nil
}

// probeSubtitleStandIn is a sealed-subtitle path the shared builder accepts in
// the probe: the probe supplies its own synthetic frames, so only the path has
// to pass the same gate a real subtitle passes, on every platform.
func probeSubtitleStandIn() string { return filepath.Join(os.TempDir(), "portico-probe.sup") }
