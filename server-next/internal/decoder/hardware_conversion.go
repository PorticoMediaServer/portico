package decoder

import (
	_ "embed"
	"errors"
	"math"
	"strconv"
	"strings"
)

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
	target := ConversionTarget{
		VideoCodec:      "h264",
		BitDepth:        8,
		DynamicRange:    "sdr",
		ToneMap:         r.ToneMap,
		Width:           r.MaxWidth,
		Height:          r.MaxHeight,
		FrameRate:       r.MaxFrameRate,
		VideoBitrateBPS: r.VideoBitrateBPS,
		AudioCodec:      r.AudioCodec,
		AudioChannels:   r.AudioChannels,
		AudioBitrateBPS: r.AudioBitrateBPS,
		AudioSampleRate: r.AudioSampleRate,
	}
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
	if r.MaxWidth < 0 || r.MaxWidth > 32768 || r.MaxHeight < 0 || r.MaxHeight > 4320 || r.VideoBitrateBPS < 0 || r.VideoBitrateBPS > 200_000_000 || r.AudioBitrateBPS < 0 ||
		r.AudioBitrateBPS > 4_096_000 ||
		r.AudioChannels < 0 ||
		r.AudioChannels > 64 {
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
		if r.ToneMap && !native && r.Toolchain != nil && (!r.Toolchain.Filters["zscale"] || !r.Toolchain.Filters["tonemap"]) &&
			((!r.Toolchain.VulkanAvailable && !r.Toolchain.OpenCLAvailable) || spec.upload != "") {
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
				if r.Toolchain != nil && r.Toolchain.VulkanAvailable && spec.upload == "" && (target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(target.DolbyVisionProfile,
					target.DolbyVisionCompatibility) == "" || !r.Toolchain.Filters["zscale"] || !r.Toolchain.Filters["tonemap"]) {
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
			if !r.ToneMap && target.DolbyVisionProfile > 0 && DolbyVisionBaseRange(target.DolbyVisionProfile, target.DolbyVisionCompatibility) == "" && target.DynamicRange != "sdr" &&
				r.Toolchain != nil && r.Toolchain.VulkanAvailable && r.Toolchain.Filters["libplacebo"] {
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
