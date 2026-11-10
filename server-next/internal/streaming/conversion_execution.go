package streaming

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// ConversionExecutor describes a successfully started command, never a probe.
type ConversionExecutor struct {
	Name      string `json:"name"`
	Execution string `json:"execution"`
}
type ConversionLaunch struct {
	Sequence   int64              `json:"sequence"`
	StartedAt  string             `json:"startedAt"`
	Encoder    ConversionExecutor `json:"encoder"`
	Decoder    ConversionExecutor `json:"decoder"`
	RenderNode string             `json:"renderNode,omitempty"`
}

// ConversionExecution is host diagnostic data, published only to admins.
type ConversionExecution struct {
	LaunchCount         int64            `json:"launchCount"`
	SoftwareLaunchCount int64            `json:"softwareLaunchCount"`
	UnknownLaunchCount  int64            `json:"unknownLaunchCount"`
	FallbackCount       int64            `json:"fallbackCount"`
	LastLaunch          ConversionLaunch `json:"lastLaunch"`
}

var executionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var executionRenderNode = regexp.MustCompile(`/dev/dri/renderD[0-9]+`)

func observedConversionLaunch(plan *DeliveryPlan, args []string, stages []decoder.HardwareStage) ConversionLaunch {
	launch := ConversionLaunch{StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Encoder: ConversionExecutor{"unknown", "unknown"}, Decoder: ConversionExecutor{"unknown", "unknown"}}
	encoder, hwaccel := "", ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-c:v", "-codec:v", "-c:v:0":
			encoder = args[i+1]
		case "-hwaccel":
			hwaccel = args[i+1]
		case "-init_hw_device", "-hwaccel_device", "-vaapi_device", "-qsv_device":
			if node := executionRenderNode.FindString(args[i+1]); node != "" {
				launch.RenderNode = node
			}
		}
	}
	if executionName.MatchString(encoder) && encoder != "copy" {
		launch.Encoder.Name = encoder
		switch encoder {
		case "h264_vaapi", "hevc_vaapi", "av1_vaapi", "h264_qsv", "hevc_qsv", "av1_qsv", "h264_nvenc", "hevc_nvenc", "av1_nvenc", "h264_videotoolbox", "hevc_videotoolbox", "h264_amf", "hevc_amf", "av1_amf":
			launch.Encoder.Execution = "hardware"
		case "libx264", "libx265", "libsvtav1", "libaom-av1":
			launch.Encoder.Execution = "software"
		}
	}
	if plan != nil && executionName.MatchString(plan.SourceVideoCodec) {
		launch.Decoder.Name = plan.SourceVideoCodec
	}
	for _, stage := range stages {
		if stage.Operation == decoder.StageDecode {
			if stage.Execution == decoder.ExecutionSoftware && hwaccel == "" {
				launch.Decoder.Execution = "software"
			}
			if stage.Execution == decoder.ExecutionHardware && launch.Decoder.Name != "unknown" {
				switch hwaccel {
				case "vaapi", "qsv", "cuda", "videotoolbox", "d3d11va", "dxva2":
					launch.Decoder.Execution = "hardware"
				}
			}
		}
	}
	for _, stage := range stages {
		if stage.Operation == decoder.StageEncode && stage.Execution != launch.Encoder.Execution {
			launch.Encoder.Execution = "unknown"
		}
	}
	return launch
}
func (h *HLS) recordConversionStart(ctx context.Context, id string, plan *DeliveryPlan, args []string, stages []decoder.HardwareStage, fallback bool) {
	if plan == nil || plan.VideoAction != "convert" {
		return
	}
	launch := observedConversionLaunch(plan, args, stages)
	h.mu.Lock()
	recorded := false
	if s := h.streams[id]; s != nil {
		recorded = true
		s.execution.LaunchCount++
		if launch.Encoder.Execution == "software" || launch.Decoder.Execution == "software" {
			s.execution.SoftwareLaunchCount++
		} else if launch.Encoder.Execution == "unknown" || launch.Decoder.Execution == "unknown" {
			s.execution.UnknownLaunchCount++
		}
		if fallback {
			s.execution.FallbackCount++
		}
		launch.Sequence = s.execution.LaunchCount
		s.execution.LastLaunch = launch
	}
	h.mu.Unlock()
	// Exactly one redacted event per Start. The event time identifies the launch
	// without copying raw argv or paths.
	event := struct {
		Event      string             `json:"event"`
		Stream     string             `json:"stream"`
		StartedAt  string             `json:"startedAt"`
		Encoder    ConversionExecutor `json:"encoder"`
		Decoder    ConversionExecutor `json:"decoder"`
		RenderNode string             `json:"renderNode,omitempty"`
		Fallback   bool               `json:"fallback"`
		Recorded   bool               `json:"recorded"`
	}{"conversion_started", id, launch.StartedAt, launch.Encoder, launch.Decoder, launch.RenderNode, fallback, recorded}
	encoded, _ := json.Marshal(event)
	log.Print(string(encoded))
}
