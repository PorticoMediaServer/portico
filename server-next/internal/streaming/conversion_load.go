package streaming

import "github.com/PorticoMediaServer/portico/server-next/internal/decoder"

// ConversionLoad counts playback encoders that started and have not retired.
// Planned, queued and completed conversions do not use an encoder now.
func (s *Streams) ConversionLoad() (hardware, processor int) {
	s.hls.mu.Lock()
	defer s.hls.mu.Unlock()
	return s.hls.hardwareRunning, s.hls.processorRunning
}

// conversionCount follows one existing converter run through actual Start and
// physical retirement, including a refused start and a hardware fallback run.
func (h *HLS) conversionCount() (started func(string), finished func()) {
	kind := ""
	ended := false
	return func(execution string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if kind != "" || ended {
				return
			}
			switch execution {
			case "hardware":
				h.hardwareRunning++
			case "software":
				h.processorRunning++
			default:
				return
			}
			kind = execution
		}, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			switch kind {
			case "hardware":
				h.hardwareRunning--
			case "software":
				h.processorRunning--
			}
			kind = ""
			ended = true
		}
}

func conversionKind(plan *DeliveryPlan, args []string, stages []decoder.HardwareStage) string {
	if plan == nil {
		return ""
	}
	if plan.VideoAction == "convert" {
		return observedConversionLaunch(plan, args, stages).Encoder.Execution
	}
	if plan.AudioAction == "convert" {
		return "software"
	}
	return ""
}
