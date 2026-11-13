package decoder

import (
	"log"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProbeObservation is the last completed hardware probe on one backend and
// device: whether it worked, the stable reason code, the tool's own words, and
// when it ran. The owner reads it as Last error and Last test, and the message
// log keeps the same text after the probe's child process has exited.
type ProbeObservation struct {
	Stopped   bool `json:"-"` // worked before; cleared only by a successful test
	worked    bool
	Backend   string    `json:"backend"`
	Device    string    `json:"device"`
	Available bool      `json:"available"`
	Reason    string    `json:"reason"`
	Encoder   string    `json:"encoder,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}

const (
	// probeDetailLimit bounds the tool output kept for one probe. FFmpeg writes
	// the useful line last, so the tail is what is retained.
	probeDetailLimit = 4 << 10
	// probeDetailShown bounds what is published or logged from that tail.
	probeDetailShown = 512
	// probeCellsKept bounds the remembered backend/device cells.
	probeCellsKept = 64
)

// hardwareProbeLog is process state, like the hardware circuit: one server has
// one set of graphics devices, and every detector instance in the process is
// probing those same devices.
var hardwareProbeLog = struct {
	sync.Mutex
	last  map[string]ProbeObservation
	order []string
}{last: map[string]ProbeObservation{}}

func probeCellKey(backend HardwareBackend, device string) string {
	return string(backend) + "\x00" + device
}

// probeTail keeps the last bytes a probe wrote. FFmpeg writes the useful line
// last, and a probe may write megabytes of decoder chatter before it refuses,
// so the tail is kept rather than the whole. It never fails a writer: the
// verdict must stay the tool's exit status, not this buffer's size.
type probeTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *probeTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > probeDetailLimit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-probeDetailLimit:]...)
	}
	return len(p), nil
}

func (t *probeTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// deviceLabel names the device in a log line without pretending a default
// render node was chosen for a backend that has no selector.
func deviceLabel(device string) string {
	if device == "" {
		return "the default device"
	}
	return device
}

// probeDetail bounds captured tool output to what a person can read in a log
// line and keeps only printable text, so a binary diagnostic cannot smuggle
// control characters into the message log.
func probeDetail(raw string) string {
	if len(raw) > probeDetailLimit {
		raw = raw[len(raw)-probeDetailLimit:]
	}
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, raw)
	clean = strings.TrimSpace(clean)
	if len(clean) > probeDetailShown {
		clean = clean[len(clean)-probeDetailShown:]
	}
	return clean
}

// probeDetailLine is the one line worth repeating in a log message: the last
// non-empty line, which is where FFmpeg states the actual refusal.
func probeDetailLine(detail string) string {
	lines := strings.Split(detail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			if len(line) > probeDetailShown {
				line = line[len(line)-probeDetailShown:]
			}
			return line
		}
	}
	return ""
}

// busyHardwareProbe recognizes resource refusals, never a generic driver failure.
func busyHardwareProbe(detail string) bool {
	detail = strings.ToLower(detail)
	for _, phrase := range []string{"device busy", "resource temporarily unavailable", "too many concurrent sessions",
		"all encoder sessions", "all encode sessions", "mfx_wrn_device_busy", "openencodesessionex failed: out of memory"} {
		if strings.Contains(detail, phrase) {
			return true
		}
	}
	return false
}

// noteProbeObservation keeps the verdict for the Owner-facing hardware facts and
// writes the same diagnosis to the message log. A failed probe is a warning with
// its reason and the tool's last line; a working one is a single info line.
func noteProbeObservation(out HardwareProbe, detail string) {
	// Only a probe that ran to its own verdict counts as observed. A cell that
	// was replaced, or that ran out of time, is not an answer about this device.
	if out.Reason == ProbeWarming {
		return
	}
	observation := ProbeObservation{
		Backend: string(out.Backend), Device: out.Device, Available: out.Available && out.Reason == ProbeAvailable,
		Reason: out.Reason, Encoder: out.Encoder, Detail: probeDetail(detail), At: time.Now().UTC(),
	}
	key := probeCellKey(out.Backend, out.Device)
	hardwareProbeLog.Lock()
	previous := hardwareProbeLog.last[key]
	observation.worked = previous.worked || observation.Available
	observation.Stopped = previous.Stopped
	switch out.Reason {
	case ProbeAvailable:
		observation.Stopped = false
	case ProbeEncodeFailed, ProbeConfinementMissing, ProbeUnsupportedPlatform:
		observation.Stopped = observation.worked
	}
	if _, seen := hardwareProbeLog.last[key]; !seen {
		hardwareProbeLog.order = append(hardwareProbeLog.order, key)
		for len(hardwareProbeLog.order) > probeCellsKept {
			delete(hardwareProbeLog.last, hardwareProbeLog.order[0])
			hardwareProbeLog.order = hardwareProbeLog.order[1:]
		}
	}
	hardwareProbeLog.last[key] = observation
	hardwareProbeLog.Unlock()
	if observation.Stopped && !previous.Stopped {
		logHardwareStopped(out.Backend, out.Device)
	}
	line := probeDetailLine(observation.Detail)
	fallback := "New conversions fall back to a working graphics device or the processor."
	if observation.Reason == ProbeBusy {
		if out.Available {
			fallback = "Conversions keep using the last working result."
		}
		log.Printf("warn: [playback] Could not test %s on %s now: %s. %s", observation.Backend, deviceLabel(observation.Device), line, fallback)
		return
	}
	if observation.Available {
		log.Printf("info: [playback] %s on %s works as %s.", observation.Backend, deviceLabel(observation.Device), observation.Encoder)
		return
	}
	if line == "" {
		log.Printf("warn: [playback] %s on %s failed its probe: %s. %s", observation.Backend, deviceLabel(observation.Device), observation.Reason, fallback)
		return
	}
	log.Printf("warn: [playback] %s on %s failed its probe: %s. %s %s", observation.Backend, deviceLabel(observation.Device), observation.Reason, line, fallback)
}

// NoteHardwareDeviceStopped consumes an open failure circuit or missing discovery.
// A device that never worked raises no alert.
func NoteHardwareDeviceStopped(backend HardwareBackend, device string) {
	key := probeCellKey(backend, device)
	hardwareProbeLog.Lock()
	previous := hardwareProbeLog.last[key]
	changed := previous.worked && !previous.Stopped
	if changed {
		previous.Stopped = true
		hardwareProbeLog.last[key] = previous
	}
	hardwareProbeLog.Unlock()
	if changed {
		logHardwareStopped(backend, device)
	}
}

// ForgetDevice retires the old selector when the same adapter moves to a new path.
// The old path must not keep its alert or lend a cached verdict to a future device.
func (d *HardwareDetector) ForgetDevice(backend HardwareBackend, device string) {
	d.mu.Lock()
	for key, p := range d.cache {
		if p.Backend == backend && p.Device == device {
			delete(d.cache, key)
		}
	}
	if d.probeGenerations != nil {
		d.probeGenerations[hardwareProbeCell{backend, device}]++
	}
	d.mu.Unlock()
	clearDeviceCircuit(backend, device)
	key := probeCellKey(backend, device)
	hardwareProbeLog.Lock()
	delete(hardwareProbeLog.last, key)
	for i, known := range hardwareProbeLog.order {
		if known == key {
			hardwareProbeLog.order = append(hardwareProbeLog.order[:i], hardwareProbeLog.order[i+1:]...)
			break
		}
	}
	hardwareProbeLog.Unlock()
}

func logHardwareStopped(backend HardwareBackend, device string) {
	slog.Warn("Graphics device stopped working; conversions fall back to a working graphics device or the processor.",
		"log_kind", "playback", "backend", backend, "device", deviceLabel(device))
}

// LastProbeObservation answers what the last probe of one backend and device
// said, including a failure. The bool is false before that cell has been probed.
func LastProbeObservation(backend HardwareBackend, device string) (ProbeObservation, bool) {
	hardwareProbeLog.Lock()
	defer hardwareProbeLog.Unlock()
	observation, ok := hardwareProbeLog.last[probeCellKey(backend, device)]
	return observation, ok
}

// LastProbeFailure answers only the failing cells, which is what Last error
// shows. A working probe clears the failure for its cell.
func LastProbeFailure(backend HardwareBackend, device string) (ProbeObservation, bool) {
	observation, ok := LastProbeObservation(backend, device)
	if !ok || observation.Available {
		return ProbeObservation{}, false
	}
	return observation, true
}

// ProbeObservations lists every remembered cell, newest probe last, so the
// hardware facts read one snapshot instead of one call per backend.
func ProbeObservations() []ProbeObservation {
	hardwareProbeLog.Lock()
	defer hardwareProbeLog.Unlock()
	out := make([]ProbeObservation, 0, len(hardwareProbeLog.last))
	for _, observation := range hardwareProbeLog.last {
		out = append(out, observation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Backend != out[j].Backend {
			return out[i].Backend < out[j].Backend
		}
		return out[i].Device < out[j].Device
	})
	return out
}

// forgetProbeObservations drops remembered verdicts. Tests use it to keep one
// case's probe from answering the next; production never forgets a running
// server's device verdicts.
func forgetProbeObservations() {
	hardwareProbeLog.Lock()
	defer hardwareProbeLog.Unlock()
	hardwareProbeLog.last = map[string]ProbeObservation{}
	hardwareProbeLog.order = nil
}
