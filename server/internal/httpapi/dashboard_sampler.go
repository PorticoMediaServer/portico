package httpapi

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"time"
)

type dashboardSampler struct {
	host telemetry.Sampler
	d    Dependencies
}

func (s dashboardSampler) Sample(ctx context.Context, at int64) telemetry.Sample {
	out := s.host.Sample(ctx, at)
	if s.d.streamTraffic != nil {
		out.Metrics["homeMbps"], out.Metrics["awayMbps"] = s.d.streamTraffic.Sample(time.UnixMilli(at))
	}
	if s.d.Playback != nil {
		counts, e := s.d.Playback.ConversionLoad(ctx)
		if e == nil && counts.Available {
			out.Metrics["hardwareConversions"], out.Metrics["softwareConversions"] = telemetry.Available(float64(counts.Hardware)), telemetry.Available(float64(counts.Software))
			if counts.Unknown > 0 {
				out.Metrics["hardwareConversions"] = telemetry.Limited(float64(counts.Hardware), "Some active converters have not published their encode execution yet.")
				out.Metrics["softwareConversions"] = telemetry.Limited(float64(counts.Software), "Some active converters have not published their encode execution yet.")
			}
		}
	}
	if audio, ok := s.d.AudioMedia.(interface{ ActiveAudioConversions() int64 }); ok {
		active := float64(audio.ActiveAudioConversions())
		software := out.Metrics["softwareConversions"]
		if software.Known() {
			software.Value += active
			out.Metrics["softwareConversions"] = software
		} else if active > 0 {
			out.Metrics["softwareConversions"] = telemetry.Limited(active, "Audio producers are observed; video producer counts are unavailable.")
		}
	} else if s.d.AudioMedia != nil {
		software := out.Metrics["softwareConversions"]
		if software.Known() {
			out.Metrics["softwareConversions"] = telemetry.Limited(software.Value, "The audio converter does not publish its active producer count.")
		}
	}

	return out
}
