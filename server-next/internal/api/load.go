package api

import (
	"context"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/console"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// Server load, for the Dashboard's charts: live (the last two minutes, a point every two
// seconds) or the last 24 hours (a point an hour, the hour's average). Kept in memory only, so
// the charts start again after a restart.

// Load is the server's load over a range. Series are by name: cpu and memory (the whole
// machine, percent), processCPU and processMemory (Portico's own process, percent), homeMbps
// and awayMbps (the bitrates of the streams playing), conversions (running encoders),
// hardwareConversions and processorConversions (those encoders split by execution).
// A series this machine cannot measure is absent.
type Load struct {
	// Period is live or 24h.
	Period string    `json:"period"`
	From   time.Time `json:"from"`
	Until  time.Time `json:"until"`
	// Buckets are the 24 hours of 24h, the current one last and partial; empty for live.
	Buckets    []LoadBucket           `json:"buckets"`
	ObservedAt time.Time              `json:"observedAt"`
	Series     map[string][]LoadPoint `json:"series"`
}

// LoadBucket is one hour of the 24-hour chart.
type LoadBucket struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Partial bool      `json:"partial"`
}

// LoadPoint is one value: a sample, or an hour's average at the hour's start.
type LoadPoint struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// liveWindow is how far back the live chart goes.
const liveWindow = 2 * time.Minute

// loadRoutes starts sampling the server's load and registers its read.
func (s *Server) loadRoutes(ctx context.Context) {
	load := &console.Load{Now: func() time.Time { return s.now() }, Streams: s.streamLoad, ConversionKinds: s.conversionLoad}
	go load.Run(ctx)
	Handle(s, "GET /v1/admin/load", Owner, func(r *http.Request, _ None) (Load, error) {
		now := s.now().UTC()
		out := Load{Period: r.URL.Query().Get("range"), Until: now, ObservedAt: now, Buckets: []LoadBucket{}, Series: map[string][]LoadPoint{}}
		var series map[string][]console.Point
		switch out.Period {
		case "", "live":
			out.Period, out.From, series = "live", now.Add(-liveWindow), load.Live(now)
		case "24h":
			out.From, series = load.Day(now)
			out.From = out.From.UTC()
			for start := out.From; start.Before(now); start = start.Add(time.Hour) {
				b := LoadBucket{Start: start, End: start.Add(time.Hour)}
				if b.End.After(now) {
					b.End, b.Partial = now, true
				}
				out.Buckets = append(out.Buckets, b)
			}
		default:
			return Load{}, ErrField("range", "Choose live or 24h.")
		}
		for name, points := range series {
			for _, p := range points {
				out.Series[name] = append(out.Series[name], LoadPoint{T: p.At.UTC(), V: p.Value})
			}
		}
		return out, nil
	})
}

// streamLoad is what the streams are doing now. A paused stream sends nothing; a converted one
// may still be converting ahead, so it counts while paused.
func (s *Server) streamLoad() (homeKbps, awayKbps, conversions int) {
	for _, v := range s.playback.All() {
		if v.Plan.Route == playback.RouteConverted {
			conversions++
		}
		if v.State != playback.StatePlaying {
			continue
		}
		if v.Network == playback.NetworkLocal {
			homeKbps += v.Plan.BitrateKbps
		} else {
			awayKbps += v.Plan.BitrateKbps
		}
	}
	return homeKbps, awayKbps, conversions
}

func (s *Server) conversionLoad() (hardware, processor int) {
	if engine, ok := s.playback.Engine().(interface{ ConversionLoad() (int, int) }); ok {
		return engine.ConversionLoad()
	}
	return 0, 0
}
