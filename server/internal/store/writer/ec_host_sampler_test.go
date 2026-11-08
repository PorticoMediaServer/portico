package writer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host sampler for the EC2/EC3 screening cells: one cheap sample every 10 ms of
// the signals that separate device contention, CPU starvation/throttling, and a
// whole-process pause when a read or write is slow while a keeper PASSIVE runs.
//
//   - Linux PSI (/proc/pressure/{cpu,io}) cumulative stall microseconds;
//   - /proc/stat steal and iowait jiffies;
//   - the process cgroup's cpu.stat (periods, throttled periods, throttled µs);
//   - /proc/diskstats of the device holding the temp dir (the databases): in
//     flight IO, ms doing IO, weighted ms, completed reads/writes;
//   - /proc/vmstat nr_dirty/nr_writeback;
//   - the sampler's OWN wake-up lateness. A late tick means the whole process
//     (GC stop-the-world, CPU starvation, throttling) was not running; an on-time
//     tick during a slow call points at the device or a lock instead.
//
// Samples are batched (100 per record) so the measured chain gains no appends.
// The sampler never takes the stream's I/O path itself: records go through the
// asynchronous writer. Disable with PORTICO_EC_HOST_SAMPLER_MS=0.
const (
	hostSamplerDefaultInterval = 10 * time.Millisecond
	hostSamplerBatch           = 100
)

type hostSample struct {
	AtNS       int64  `json:"at_ns"`        // stream-origin clock, same as record at_ns
	LateNS     int64  `json:"tick_late_ns"` // wake-up lateness against the schedule
	ReadNS     int64  `json:"read_ns"`      // cost of reading all the sources
	CPUSomeUS  int64  `json:"psi_cpu_some_us"`
	IOSomeUS   int64  `json:"psi_io_some_us"`
	IOFullUS   int64  `json:"psi_io_full_us"`
	Steal      int64  `json:"steal_jiffies"`
	IOWait     int64  `json:"iowait_jiffies"`
	Periods    int64  `json:"cg_nr_periods"`
	Throttled  int64  `json:"cg_nr_throttled"`
	ThrottleUS int64  `json:"cg_throttled_usec"`
	CGIOSomeUS int64  `json:"cg_psi_io_some_us"`
	DiskReads  int64  `json:"disk_reads_completed"`
	DiskWrites int64  `json:"disk_writes_completed"`
	DiskInFly  int64  `json:"disk_in_flight"`
	DiskBusyMS int64  `json:"disk_io_ticks_ms"`
	DiskWeight int64  `json:"disk_weighted_ms"`
	Dirty      int64  `json:"nr_dirty"`
	Writeback  int64  `json:"nr_writeback"`
	Err        string `json:"error,omitempty"`
}

type hostSampler struct {
	stream   *n1Stream
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	cgroup   string
	diskKey  string // "major minor" as in /proc/diskstats
	header   map[string]any

	mu       sync.Mutex
	samples  int64
	maxLate  int64
	late25   int64 // ticks later than 25 ms
	late100  int64
	maxRead  int64
	failures int64
}

// startHostSampler returns nil when disabled. databaseDir picks the device whose
// diskstats are sampled (the temp dir holds every test database).
func startHostSampler(stream *n1Stream, databaseDir string) *hostSampler {
	interval := hostSamplerDefaultInterval
	if v := os.Getenv("PORTICO_EC_HOST_SAMPLER_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 1000 {
			panic("PORTICO_EC_HOST_SAMPLER_MS must be 0..1000")
		}
		if n == 0 {
			return nil
		}
		interval = time.Duration(n) * time.Millisecond
	}
	s := &hostSampler{stream: stream, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
	s.cgroup = processCgroupDir()
	major, minor, ok := deviceNumbers(databaseDir)
	if ok {
		s.diskKey = fmt.Sprintf("%d %d", major, minor)
	}
	s.header = map[string]any{
		"interval_ns": interval.Nanoseconds(), "cgroup_dir": s.cgroup, "database_dir": databaseDir,
		"disk_key": s.diskKey, "origin_utc_ns": stream.origin.UnixNano(),
		"cpu_max": readTrim(s.cgroup + "/cpu.max"), "scope": "10 ms host/cgroup/disk samples with the sampler's own tick lateness; PSI/cpu.stat/diskstats are cumulative counters, join by differencing around a call",
	}
	_ = stream.append("host_sampler_start", s.header)
	go s.run()
	return s
}

func (s *hostSampler) run() {
	defer close(s.done)
	batch := make([]hostSample, 0, hostSamplerBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		_ = s.stream.append("host_samples", map[string]any{"samples": append([]hostSample(nil), batch...)})
		batch = batch[:0]
	}
	start := time.Now()
	for n := 1; ; n++ {
		due := start.Add(time.Duration(n) * s.interval)
		if d := time.Until(due); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-s.stop:
				timer.Stop()
				flush()
				return
			case <-timer.C:
			}
		} else {
			select {
			case <-s.stop:
				flush()
				return
			default:
			}
		}
		woke := time.Now()
		sample := s.read()
		sample.AtNS = s.stream.since()
		sample.LateNS = woke.Sub(due).Nanoseconds()
		sample.ReadNS = time.Since(woke).Nanoseconds()
		s.mu.Lock()
		s.samples++
		s.maxLate = max(s.maxLate, sample.LateNS)
		s.maxRead = max(s.maxRead, sample.ReadNS)
		if sample.LateNS > 25e6 {
			s.late25++
		}
		if sample.LateNS > 100e6 {
			s.late100++
		}
		if sample.Err != "" {
			s.failures++
		}
		s.mu.Unlock()
		batch = append(batch, sample)
		if len(batch) == hostSamplerBatch {
			flush()
		}
	}
}

// close stops the sampler and flushes its last batch into the stream. Call it
// before the final record.
func (s *hostSampler) close() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func (s *hostSampler) read() hostSample {
	var out hostSample
	var errs []string
	note := func(err error) {
		if err != nil && len(errs) < 3 {
			errs = append(errs, err.Error())
		}
	}
	if v, err := psiTotals("/proc/pressure/cpu"); err == nil {
		out.CPUSomeUS = v["some"]
	} else {
		note(err)
	}
	if v, err := psiTotals("/proc/pressure/io"); err == nil {
		out.IOSomeUS, out.IOFullUS = v["some"], v["full"]
	} else {
		note(err)
	}
	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		if line, _, _ := strings.Cut(string(raw), "\n"); strings.HasPrefix(line, "cpu ") {
			f := strings.Fields(line)
			if len(f) >= 9 {
				out.IOWait, out.Steal = atoi64(f[5]), atoi64(f[8])
			}
		}
	} else {
		note(err)
	}
	if s.cgroup != "" {
		if raw, err := os.ReadFile(s.cgroup + "/cpu.stat"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				f := strings.Fields(line)
				if len(f) != 2 {
					continue
				}
				switch f[0] {
				case "nr_periods":
					out.Periods = atoi64(f[1])
				case "nr_throttled":
					out.Throttled = atoi64(f[1])
				case "throttled_usec":
					out.ThrottleUS = atoi64(f[1])
				}
			}
		} else {
			note(err)
		}
		if v, err := psiTotals(s.cgroup + "/io.pressure"); err == nil {
			out.CGIOSomeUS = v["some"]
		}
	}
	if s.diskKey != "" {
		if raw, err := os.ReadFile("/proc/diskstats"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				f := strings.Fields(line)
				if len(f) >= 14 && f[0]+" "+f[1] == s.diskKey {
					out.DiskReads, out.DiskWrites = atoi64(f[3]), atoi64(f[7])
					out.DiskInFly, out.DiskBusyMS, out.DiskWeight = atoi64(f[11]), atoi64(f[12]), atoi64(f[13])
					break
				}
			}
		} else {
			note(err)
		}
	}
	if raw, err := os.ReadFile("/proc/vmstat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			switch {
			case strings.HasPrefix(line, "nr_dirty "):
				out.Dirty = atoi64(strings.TrimPrefix(line, "nr_dirty "))
			case strings.HasPrefix(line, "nr_writeback "):
				out.Writeback = atoi64(strings.TrimPrefix(line, "nr_writeback "))
			}
		}
	} else {
		note(err)
	}
	if len(errs) > 0 {
		out.Err = strings.Join(errs, "; ")
	}
	return out
}

func atoi64(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

func readTrim(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// psiTotals parses "some avg10=.. total=N" / "full ..." lines into total µs.
func psiTotals(path string) (map[string]int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			if v, ok := strings.CutPrefix(kv, "total="); ok {
				out[f[0]] = atoi64(v)
			}
		}
	}
	return out, nil
}

// processCgroupDir resolves this process's cgroup v2 directory ("" if none).
func processCgroupDir() string {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			dir := "/sys/fs/cgroup" + strings.TrimSpace(rest)
			if _, err := os.Stat(dir + "/cpu.stat"); err == nil {
				return dir
			}
		}
	}
	return ""
}

// summary is attached to the final record: a late tick anywhere means the whole
// process was not running at that moment.
func (s *hostSampler) summary() any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"samples": s.samples, "max_tick_late_ns": s.maxLate, "ticks_late_over_25ms": s.late25, "ticks_late_over_100ms": s.late100, "max_read_ns": s.maxRead, "read_error_samples": s.failures, "interval_ns": s.interval.Nanoseconds()}
}
