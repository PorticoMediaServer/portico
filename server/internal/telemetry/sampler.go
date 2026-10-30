package telemetry

import (
	"context"
	"sync"
	"time"
)

// counter is a cumulative platform counter read at a moment. Rates come from
// the difference between two reads, never from one.
type counter struct {
	at    int64
	read  uint64
	write uint64
	known bool
}

// cpuCounter holds the busy and total CPU time a platform reports. A percentage
// needs two reads, so the first sample after startup is unavailable rather than
// pretending to a measured value.
type cpuCounter struct {
	busy  uint64
	total uint64
	known bool
}

type gpuReading struct {
	usage, memory, encoder Metric
	device, provider       string
}

func unknownGPU(detail string) gpuReading {
	return gpuReading{usage: Unavailable(detail), memory: Unavailable(detail), encoder: Unavailable(detail)}
}

type gpuCache struct {
	at      int64
	reading gpuReading
	loaded  bool
}

// gpuInterval is how often the GPU reporters may run. They are the most
// expensive thing this package touches, so their result is cached well above
// the sampling interval.
const gpuInterval = 15 * time.Second

const warmingUp = "Waiting for a second reading before a rate can be reported."

type hostSampler struct {
	state   string
	mu      sync.Mutex
	cpu     cpuCounter
	disk    counter
	net     counter
	gpu     gpuCache
	process processCounter
}

// HostSampler samples this host. state is the server state directory, which
// selects the volume whose read and write rates are reported.
func HostSampler(state ...string) Sampler {
	s := &hostSampler{}
	if len(state) > 0 {
		s.state = state[0]
	}
	return s
}

func (s *hostSampler) Sample(ctx context.Context, at int64) Sample {
	out := newSample(at)
	s.mu.Lock()
	defer s.mu.Unlock()

	out.Metrics["cpu"] = sampleCPU(ctx, &s.cpu)
	memory, used, total := sampleMemory(ctx)
	out.Metrics["memory"], out.MemoryUsedBytes, out.MemoryTotalBytes = memory, used, total
	out.Metrics["processCPU"], out.Metrics["processMemory"] = sampleProcess(ctx, &s.process, at, total)

	disk, diskDetail := readDiskCounter(ctx, s.state)
	s.disk = rate(out, "diskRead", "diskWrite", s.disk, at, disk, diskDetail)
	network, networkDetail := readNetCounter(ctx)
	s.net = rate(out, "netIn", "netOut", s.net, at, network, networkDetail)

	if !s.gpu.loaded || at-s.gpu.at >= gpuInterval.Milliseconds() {
		s.gpu = gpuCache{at: at, reading: sampleGPU(ctx), loaded: true}
	}
	out.Metrics["gpuUsage"] = s.gpu.reading.usage
	out.Metrics["gpuMemory"] = s.gpu.reading.memory
	out.Metrics["gpuEncoder"] = s.gpu.reading.encoder
	out.GPUDevice, out.GPUProvider = s.gpu.reading.device, s.gpu.reading.provider
	return out
}

// rate converts two cumulative counters into units per second. A counter that
// went backwards (a reboot, a renamed interface) starts over rather than
// reporting a negative or enormous rate.
func rate(out Sample, readName, writeName string, previous counter, at int64, current counter, detail string) counter {
	if !current.known {
		out.Metrics[readName], out.Metrics[writeName] = Unavailable(detail), Unavailable(detail)
		return previous
	}
	current.at = at
	if !previous.known || current.at <= previous.at || current.read < previous.read || current.write < previous.write {
		out.Metrics[readName], out.Metrics[writeName] = Unavailable(warmingUp), Unavailable(warmingUp)
		return current
	}
	seconds := float64(current.at-previous.at) / 1000
	out.Metrics[readName] = Available(float64(current.read-previous.read) / seconds)
	out.Metrics[writeName] = Available(float64(current.write-previous.write) / seconds)
	return current
}

// deltaCPU turns two cumulative CPU time reads into a busy percentage.
func deltaCPU(previous *cpuCounter, busy, total uint64) Metric {
	defer func() { *previous = cpuCounter{busy: busy, total: total, known: true} }()
	if !previous.known || total <= previous.total || busy < previous.busy {
		return Unavailable(warmingUp)
	}
	return Available(percent(float64(busy-previous.busy) / float64(total-previous.total) * 100))
}
