package mediaexec

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

// Setting is the owner's PORTICO_DECODER_SANDBOX choice.
type Setting string

const (
	// SettingAuto sandboxes where the platform can and otherwise runs with the
	// baseline, saying so in diagnostics. The default.
	SettingAuto Setting = "auto"
	// SettingRequired refuses to run a media job without the sandbox.
	SettingRequired Setting = "required"
	// SettingOff runs every job with the baseline only.
	SettingOff Setting = "off"
)

// Posture is how media processes run on this server, for diagnostics.
type Posture struct {
	Mode      Mode    `json:"mode"`
	Sandboxed bool    `json:"sandboxed"`
	Mechanism string  `json:"mechanism"`
	Setting   Setting `json:"setting"`
	// Reason is why the sandbox is not in use, in the owner's words.
	Reason string `json:"reason,omitempty"`
	// Limits are the baseline restrictions every job gets here.
	Limits []string `json:"limits"`
	// supportsDisableUserns records a bubblewrap feature for the argv builder.
	supportsDisableUserns bool
}

func setting() Setting {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PORTICO_DECODER_SANDBOX"))) {
	case "required":
		return SettingRequired
	case "off":
		return SettingOff
	}
	return SettingAuto
}

var postures = struct {
	sync.Mutex
	byTool map[string]*postureEntry
	last   *Posture
}{byTool: map[string]*postureEntry{}}

type postureEntry struct {
	once    sync.Once
	posture Posture
}

// postureFor decides, once per executable file version, whether jobs with it
// run sandboxed: the sandbox is tried for real with this tool, never assumed
// from the operating system.
func postureFor(executable string) Posture {
	key := executable
	if info, err := os.Stat(executable); err == nil {
		key += "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
	}
	postures.Lock()
	entry := postures.byTool[key]
	if entry == nil {
		entry = &postureEntry{}
		postures.byTool[key] = entry
	}
	postures.Unlock()
	entry.once.Do(func() {
		entry.posture = decidePosture(executable)
		postures.Lock()
		p := entry.posture
		postures.last = &p
		postures.Unlock()
		report(entry.posture)
	})
	return entry.posture
}

func decidePosture(executable string) Posture {
	s := setting()
	p := Posture{Setting: s, Mode: ModeBaseline, Mechanism: "none", Limits: baselineLimits()}
	if s == SettingOff {
		p.Reason = "the owner turned the decoder sandbox off (PORTICO_DECODER_SANDBOX=off)"
		return p
	}
	mechanism, disableUserns, err := sandboxProbe(executable)
	if err != nil {
		p.Reason = err.Error()
		return p
	}
	p.Mode, p.Sandboxed, p.Mechanism, p.supportsDisableUserns = ModeSandboxed, true, mechanism, disableUserns
	return p
}

// sandboxProbe is the platform probe; a variable so tests can stand in for a
// host without a sandbox.
var sandboxProbe = probeSandbox

func baselineLimits() []string {
	limits := []string{"input as an open file or private bridge", "protocol whitelist", "no server secrets in its environment", "own process group"}
	if runtime.GOOS == "windows" {
		return append(limits, "ends with the server (job object)")
	}
	return append(limits, "no core dumps", "bounded output files", "low priority in the background", "reaped after a crash")
}

// Current is the most recently decided posture, deciding it for ffmpeg on PATH
// if no job has run yet.
func Current() Posture {
	postures.Lock()
	last := postures.last
	postures.Unlock()
	if last != nil {
		return *last
	}
	exe, err := resolveExecutable("ffmpeg")
	if err != nil {
		return Posture{Setting: setting(), Mode: ModeBaseline, Mechanism: "none", Reason: "ffmpeg is not installed", Limits: baselineLimits()}
	}
	return postureFor(exe)
}

// Decide settles and reports the posture for a tool (the server calls it at
// startup, so the owner's diagnostics show it before the first job).
func Decide(executable string) Posture {
	exe, err := resolveExecutable(executable)
	if err != nil {
		return Posture{Setting: setting(), Mode: ModeBaseline, Mechanism: "none", Reason: executable + " is not installed", Limits: baselineLimits()}
	}
	return postureFor(exe)
}

// Capability is the name of the diagnostics entry.
const Capability = capabilityreport.DecoderSandbox

func report(p Posture) {
	if p.Sandboxed {
		capabilityreport.Report(Capability, true, "", describe(p))
		return
	}
	code := "decoder_sandbox_unavailable"
	if p.Setting == SettingOff {
		code = "decoder_sandbox_off"
	}
	capabilityreport.Report(Capability, false, code, describe(p))
}

type described string

func (d described) Error() string { return string(d) }

func describe(p Posture) error {
	if p.Sandboxed {
		return described("FFmpeg and ffprobe have a " + p.Mechanism + " filesystem sandbox: no access to the server's own files, output only to each job's folder. Network isolation is reported separately: Linux loopback fallback jobs retain the host network unless they use the decoder's isolated bridge, and required sandbox mode refuses that fallback.")
	}
	reason := p.Reason
	if len(reason) > 300 {
		reason = reason[:300] + "…"
	}
	text := "FFmpeg and ffprobe run without a sandbox because " + reason + ". Playback, Live TV, recording and prepared versions still work, with the baseline: " + strings.Join(p.Limits, "; ") + "."
	switch {
	case p.Setting == SettingOff:
	case runtime.GOOS == "linux":
		text += " To sandbox them, install bubblewrap (bwrap) and allow unprivileged user namespaces (in Docker, the container's seccomp and AppArmor profiles must permit them)."
	case runtime.GOOS == "windows":
		text += " Windows has no decoder sandbox yet."
	}
	if p.Setting == SettingRequired {
		text += " The owner requires the sandbox (PORTICO_DECODER_SANDBOX=required), so media jobs are refused."
	}
	return described(text)
}

// SandboxSetting is the owner's PORTICO_DECODER_SANDBOX choice.
func SandboxSetting() Setting { return setting() }
