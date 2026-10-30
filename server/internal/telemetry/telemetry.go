// Package telemetry samples host, storage, network and GPU facts for the owner
// Server overview, keeps a bounded rolling history of them, and composes the
// transcode capacity and needs-attention reports.
//
// Two rules shape the package. Sampling never runs on a request: handlers read
// the last sample the background collector produced. And no metric is invented:
// every metric carries its own status, so a platform that cannot expose GPU
// encoder utilisation says so instead of publishing a zero.
package telemetry

import "github.com/PorticoMediaServer/portico/server/internal/reportwindow"

// Metric statuses. "available" carries a measured value; "limited" carries a
// value the platform only partly exposes, or a value measured under a
// restriction worth naming; "unavailable" carries no value at all.
const (
	StatusAvailable   = "available"
	StatusLimited     = "limited"
	StatusUnavailable = "unavailable"
)

// Metric is one observation with its own provenance. Value is meaningless
// unless Status is available or limited.
type Metric struct {
	Status string  `json:"status"`
	Value  float64 `json:"value"`
	Detail string  `json:"detail,omitempty"`
}

func Available(v float64) Metric { return Metric{Status: StatusAvailable, Value: v} }
func Limited(v float64, detail string) Metric {
	return Metric{Status: StatusLimited, Value: v, Detail: bounded(detail)}
}
func Unavailable(detail string) Metric {
	return Metric{Status: StatusUnavailable, Detail: bounded(detail)}
}

func (m Metric) Known() bool { return m.Status == StatusAvailable || m.Status == StatusLimited }

func bounded(detail string) string {
	const limit = 240
	out := make([]rune, 0, limit)
	for _, r := range detail {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if len(out) == limit {
			break
		}
		out = append(out, r)
	}
	return string(out)
}

func percent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// MetricNames is the published order of the series a client can chart. It is
// also the key set of Sample.Metrics and of the status map on a reading.
var MetricNames = []string{"cpu", "memory", "diskRead", "diskWrite", "netIn", "netOut", "gpuUsage", "gpuMemory", "gpuEncoder", "processCPU", "processMemory", "homeMbps", "awayMbps", "hardwareConversions", "softwareConversions"}

// Sample is one observation of every published metric, plus the absolute memory
// figures a percentage alone cannot convey.
type Sample struct {
	counts           map[string]int
	totals           map[string]float64
	firstAt, lastAt  map[string]int64
	At               int64             `json:"at"`
	Metrics          map[string]Metric `json:"metrics"`
	MemoryUsedBytes  int64             `json:"memoryUsedBytes"`
	MemoryTotalBytes int64             `json:"memoryTotalBytes"`
	GPUDevice        string            `json:"gpuDevice,omitempty"`
	GPUProvider      string            `json:"gpuProvider,omitempty"`
}

func newSample(at int64) Sample {
	s := Sample{At: at, Metrics: map[string]Metric{}}
	for _, name := range MetricNames {
		s.Metrics[name] = Unavailable("Not yet sampled.")
	}
	return s
}

// PointStatus is the provenance of an emitted measurement. Unavailable
// observations have no point; available zero remains a measured zero.
type PointStatus string

func (PointStatus) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{StatusAvailable, StatusLimited}}
}

// Point is one charted value with its own provenance. No series-level status
// can establish whether a different, historical observation was measured.
type Point struct {
	T      int64       `json:"t"`
	V      float64     `json:"v"`
	Status PointStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// Reading is the wire shape of GET /v1/admin/telemetry.
type Reading struct {
	reportwindow.Interval
	Window     string             `json:"window"`
	Series     map[string][]Point `json:"series"`
	Status     map[string]Metric  `json:"status"`
	ObservedAt int64              `json:"observedAt"`
}
