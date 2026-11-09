//go:build linux

package mediaexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
	"golang.org/x/sys/unix"
)

// The server side of probe workers (probe_worker_linux.go). Workers are keyed
// by everything that shapes their sandbox: the tool, its extra libraries and
// its priority. Concurrency is the caller's: storage admits each probe through
// its supervisor's scan-probe pool before it gets here, so there are never more
// busy workers than admitted probes.
const (
	// probeWorkerJobs recycles a worker after this many jobs, so nothing a job
	// managed to leave in the sandbox lives for long.
	probeWorkerJobs = 256
	// probeWorkerIdle closes a worker nobody has used for this long.
	probeWorkerIdle = 30 * time.Second
	// probeStderrBytes is how much of a tool's stderr is kept for its error.
	probeStderrBytes = 64 << 10
)

// ProbePoolEnabled turns the pool off when false; a seam for tests and a
// fallback switch. When off, or when a job doesn't qualify, RunPooledProbe
// reports handled=false and the caller starts a sandbox per job as before.
var ProbePoolEnabled atomic.Bool

func init() { ProbePoolEnabled.Store(true) }

// PooledProbe is one probe: the tool's arguments (its input named as
// descriptor 3), the input itself, and how its output is bounded.
type PooledProbe struct {
	Job   Job
	Input *os.File
	// OutputLimit bounds stdout; a tool that writes more fails.
	OutputLimit int64
	// LowPriority runs the worker at nice 19 with idle disk IO, as a background
	// helper process ran.
	LowPriority bool
}

// ProbeResult is a finished probe: its stdout, and its exit as a process would
// have reported it.
type ProbeResult struct {
	Stdout []byte
	Stderr string
	// Err is the tool's own failure ("exit status 1", "signal: killed"), nil
	// when it succeeded.
	Err error
}

// ProbePoolStats is what the pool has done, for diagnostics and the scan
// guard: a worker start is the only process the server itself starts per
// sandbox; each job is one tool process inside it.
type ProbePoolStats struct {
	WorkersStarted uint64 `json:"workersStarted"`
	Jobs           uint64 `json:"jobs"`
	Retries        uint64 `json:"retries"`
	Idle           int    `json:"idle"`
}

var probePool struct {
	mu      sync.Mutex
	idle    map[string][]*probeWorker
	reaping bool

	started atomic.Uint64
	jobs    atomic.Uint64
	retries atomic.Uint64
}

// ProbePoolSnapshot reports the pool's counters.
func ProbePoolSnapshot() ProbePoolStats {
	probePool.mu.Lock()
	idle := 0
	for _, list := range probePool.idle {
		idle += len(list)
	}
	probePool.mu.Unlock()
	return ProbePoolStats{WorkersStarted: probePool.started.Load(), Jobs: probePool.jobs.Load(), Retries: probePool.retries.Load(), Idle: idle}
}

// errWorkerLost is a worker that died or broke protocol mid-job; the job is
// retried once on a fresh worker.
var errWorkerLost = errors.New("probe worker lost")

// RunPooledProbe runs p on a long-lived worker. handled is false when p or this
// host doesn't qualify (no sandbox, a job that needs more than a plain input
// descriptor, the pool turned off); err is then nil and nothing has run.
func RunPooledProbe(ctx context.Context, p PooledProbe) (result ProbeResult, handled bool, err error) {
	key, spec, ok := probeWorkerSpec(p)
	if !ok {
		return ProbeResult{}, false, nil
	}
	for attempt := 0; ; attempt++ {
		w, e := takeProbeWorker(key, spec)
		if e != nil {
			// The sandbox could not start for this job at all: the caller's
			// ordinary path reports the same failure precisely.
			return ProbeResult{}, false, nil
		}
		result, err = w.run(ctx, p)
		if err == nil {
			probePool.jobs.Add(1)
			returnProbeWorker(key, w)
			return result, true, nil
		}
		w.close()
		if errors.Is(err, errWorkerLost) && attempt == 0 && ctx.Err() == nil {
			probePool.retries.Add(1)
			continue
		}
		if ctx.Err() != nil {
			return ProbeResult{}, true, ctx.Err()
		}
		return ProbeResult{}, true, err
	}
}

// ProbePoolAccepts reports whether job can run on a probe worker on this host,
// so a caller can choose the pool before admitting it.
func ProbePoolAccepts(job Job) bool {
	_, _, ok := probeWorkerSpec(PooledProbe{Job: job, Input: os.Stdin})
	return ok
}

type probeWorkerArgs struct {
	executable string
	helper     string
	job        Job
	posture    Posture
	low        bool
}

// probeWorkerSpec decides whether p can use the pool without rebuilding a
// sandbox an idle worker already owns. Library ownership is checked when a new
// namespace is created, never cached across namespace launches.
func probeWorkerSpec(p PooledProbe) (string, probeWorkerArgs, bool) {
	job := p.Job
	if !ProbePoolEnabled.Load() || p.Input == nil || job.PreConfined || job.Loopback != "" || job.Hardware || job.Fonts ||
		len(job.Files) != 0 || len(job.ReadPaths) != 0 || len(job.WriteDirs) != 0 || job.InputFD != 0 || job.InputMount != "" {
		return "", probeWorkerArgs{}, false
	}
	if err := validate(&job); err != nil || job.MaxFileBytes != NoOutputFiles {
		return "", probeWorkerArgs{}, false
	}
	exe, err := resolveExecutable(job.Executable)
	if err != nil {
		return "", probeWorkerArgs{}, false
	}
	posture := postureFor(exe)
	posture.Setting = setting()
	if posture.Setting == SettingOff || !posture.Sandboxed {
		return "", probeWorkerArgs{}, false
	}
	helper, err := probeWorkerHelper()
	if err != nil {
		return "", probeWorkerArgs{}, false
	}
	key := strings.Join(append([]string{exe, helper, fmt.Sprint(job.Background, p.LowPriority, job.MaxFileBytes)}, job.Libraries...), "\x00")
	job.Executable, job.Args = exe, nil
	return key, probeWorkerArgs{executable: exe, helper: helper, job: job, posture: posture, low: p.LowPriority}, true
}

// launchArgv runs only when taking an idle worker failed. Even a recycled or
// retried worker revalidates its complete library closure and read-only binds.
func (spec probeWorkerArgs) launchArgv() ([]string, error) {
	inner, err := sandboxArgv(spec.job, spec.posture)
	if err != nil {
		return nil, err
	}
	inner, err = asProbeWorker(inner, spec.executable, spec.helper)
	if err != nil {
		return nil, err
	}
	// The worker's own process gets the job's process policy (no files, low
	// priority); its CPU limit is set per tool process instead, since the
	// worker lives across jobs.
	launch := spec.job
	launch.MaxCPUSeconds = 0
	return shimArgv(launch, inner), nil
}

// asProbeWorker turns a probe's sandbox invocation, which ends with
// "--remount-ro / -- tool args...", into the worker's: the server binary bound
// read-only at /portico-helper (with its own libraries when it is dynamic) and
// run as the worker for that tool.
func asProbeWorker(argv []string, exe, helper string) ([]string, error) {
	end := -1
	for i := len(argv) - 4; i >= 0; i-- {
		if argv[i] == "--remount-ro" && argv[i+1] == "/" && argv[i+2] == "--" && argv[i+3] == exe {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, ErrInvalidJob
	}
	out := append([]string{}, argv[:end]...)
	libraries, err := librariesFor(helper, nil)
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for i := 0; i+2 < len(out); i++ {
		if out[i] == "--ro-bind" {
			bound[out[i+2]] = true
		}
	}
	for _, lib := range libraries {
		if bound[lib] {
			continue
		}
		mount, err := LinuxReadOnlyFileArgs(lib, false)
		if err != nil {
			return nil, err
		}
		out = append(out, mount...)
	}
	out = append(out, "--ro-bind", helper, probeWorkerPath, "--remount-ro", "/", "--", probeWorkerPath, ProbeWorkerFlag, exe)
	return out, nil
}

// probeWorkerPath is where the worker binary is mounted inside its sandbox.
const probeWorkerPath = "/portico-helper"

// probeWorkerHelper is the configured helper binary (Configure). Only a binary
// that routes its arguments through RunHelper first can be the worker; an
// unconfigured process (a test binary that never called Configure) keeps the
// per-probe sandbox rather than starting itself inside one.
func probeWorkerHelper() (string, error) {
	configured.RLock()
	helper := configured.helper
	configured.RUnlock()
	if helper == "" {
		return "", ErrInvalidJob
	}
	return filepath.EvalSymlinks(helper)
}

type probeWorker struct {
	cmd      *exec.Cmd
	control  *os.File
	jobs     int
	lastUsed time.Time
	exited   chan struct{}
	stderr   *bytes.Buffer
}

func takeProbeWorker(key string, spec probeWorkerArgs) (*probeWorker, error) {
	probePool.mu.Lock()
	list := probePool.idle[key]
	for len(list) > 0 {
		w := list[len(list)-1]
		list = list[:len(list)-1]
		probePool.idle[key] = list
		select {
		case <-w.exited:
			w.close()
			continue
		default:
		}
		probePool.mu.Unlock()
		return w, nil
	}
	probePool.mu.Unlock()
	return startProbeWorker(spec)
}

func returnProbeWorker(key string, w *probeWorker) {
	w.jobs++
	w.lastUsed = time.Now()
	if w.jobs >= probeWorkerJobs {
		w.close()
		return
	}
	probePool.mu.Lock()
	defer probePool.mu.Unlock()
	if probePool.idle == nil {
		probePool.idle = map[string][]*probeWorker{}
	}
	probePool.idle[key] = append(probePool.idle[key], w)
	if !probePool.reaping {
		probePool.reaping = true
		supervise.Go("mediaexec.probe-pool.reap", reapIdleProbeWorkers)
	}
}

// reapIdleProbeWorkers closes workers idle past probeWorkerIdle, and stops
// once none are left.
func reapIdleProbeWorkers() {
	ticker := time.NewTicker(probeWorkerIdle / 3)
	defer ticker.Stop()
	for range ticker.C {
		var stale []*probeWorker
		probePool.mu.Lock()
		left := 0
		for key, list := range probePool.idle {
			kept := list[:0]
			for _, w := range list {
				if time.Since(w.lastUsed) > probeWorkerIdle {
					stale = append(stale, w)
				} else {
					kept = append(kept, w)
				}
			}
			probePool.idle[key] = kept
			left += len(kept)
		}
		if left == 0 {
			probePool.reaping = false
		}
		probePool.mu.Unlock()
		for _, w := range stale {
			w.close()
		}
		if left == 0 {
			return
		}
	}
}

func startProbeWorker(spec probeWorkerArgs) (*probeWorker, error) {
	argv, err := spec.launchArgv()
	if err != nil {
		return nil, err
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	control, child := os.NewFile(uintptr(fds[0]), "probe-control"), os.NewFile(uintptr(fds[1]), "probe-worker")
	defer child.Close()
	w := &probeWorker{control: control, exited: make(chan struct{}), stderr: &bytes.Buffer{}}
	cmd := &exec.Cmd{
		Path:       argv[0],
		Args:       argv,
		Env:        passedEnvironmentList(),
		ExtraFiles: []*os.File{child},
		Stderr:     &limited{buffer: w.stderr, limit: probeStderrBytes},
		// No Pdeathsig: Go ties it to the starting OS thread, which may exit
		// while the worker is still wanted. bubblewrap's --die-with-parent
		// ends the sandbox when the server does.
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err = cmd.Start(); err != nil {
		control.Close()
		return nil, err
	}
	if spec.low {
		lowerProcessPriority(cmd.Process.Pid)
	}
	w.cmd = cmd
	probePool.started.Add(1)
	supervise.Go("mediaexec.probe-worker.wait", func() {
		defer close(w.exited)
		_ = cmd.Wait()
	})
	return w, nil
}

// lowerProcessPriority gives a freshly started worker the background policy a
// background helper had: nice 19 and idle disk IO. Threads and processes it
// creates afterwards inherit it.
func lowerProcessPriority(pid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 19)
	const ioprioWhoProcess, ioprioClassIdle, ioprioClassShift = 1, 3, 13
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(pid), ioprioClassIdle<<ioprioClassShift)
}

func passedEnvironmentList() []string {
	env := []string{}
	for _, name := range passedEnvironment {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// close kills the worker's whole sandbox and waits for it.
func (w *probeWorker) close() {
	if w.cmd != nil && w.cmd.Process != nil {
		_ = unix.Kill(-w.cmd.Process.Pid, unix.SIGKILL)
		_ = w.cmd.Process.Kill()
	}
	_ = w.control.Close()
	if w.exited != nil {
		<-w.exited
	}
}

// run sends one job and collects its output. Any failure of the worker itself
// (it died, a protocol error, cancellation) leaves it unusable: the caller
// closes it.
func (w *probeWorker) run(ctx context.Context, p PooledProbe) (ProbeResult, error) {
	request, err := json.Marshal(probeRequest{Args: p.Job.Args, CPUSeconds: p.Job.MaxCPUSeconds})
	if err != nil || len(request) > maxProbeRequest {
		return ProbeResult{}, ErrInvalidJob
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return ProbeResult{}, err
	}
	defer outR.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		return ProbeResult{}, err
	}
	defer errR.Close()
	rights := unix.UnixRights(int(p.Input.Fd()), int(outW.Fd()), int(errW.Fd()))
	sendErr := unix.Sendmsg(int(w.control.Fd()), request, rights, nil, 0)
	outW.Close()
	errW.Close()
	if sendErr != nil {
		return ProbeResult{}, errors.Join(errWorkerLost, sendErr)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var over atomic.Bool
	copied := make(chan struct{}, 2)
	supervise.Go("mediaexec.probe-job.stdout", func() {
		defer func() { copied <- struct{}{} }()
		n, _ := io.Copy(&stdout, io.LimitReader(outR, p.OutputLimit+1))
		if n > p.OutputLimit {
			over.Store(true)
			_, _ = io.Copy(io.Discard, outR)
		}
	})
	supervise.Go("mediaexec.probe-job.stderr", func() {
		defer func() { copied <- struct{}{} }()
		_, _ = io.Copy(&limited{buffer: &stderr, limit: probeStderrBytes}, errR)
	})
	replies := make(chan []byte, 1)
	// Read through the *os.File, never its raw descriptor number: close (on
	// cancellation) may run before this read starts, and a raw read would
	// then hit whatever file reused the number.
	supervise.Go("mediaexec.probe-job.reply", func() {
		buffer := make([]byte, 4096)
		n, err := w.control.Read(buffer)
		if err != nil || n <= 0 {
			replies <- nil
			return
		}
		replies <- buffer[:n]
	})
	var raw []byte
	select {
	case <-ctx.Done():
		w.close()
		<-copied
		<-copied
		return ProbeResult{}, ctx.Err()
	case raw = <-replies:
	}
	if raw == nil {
		w.close()
		<-copied
		<-copied
		return ProbeResult{}, errWorkerLost
	}
	<-copied
	<-copied
	var reply probeReply
	if json.Unmarshal(raw, &reply) != nil {
		return ProbeResult{}, errWorkerLost
	}
	if reply.Error != "" {
		return ProbeResult{}, errors.Join(errWorkerLost, errors.New(reply.Error))
	}
	result := ProbeResult{Stdout: stdout.Bytes(), Stderr: stderr.String()}
	switch {
	case over.Load():
		result.Err = errors.New("scan output exceeds limit")
	case reply.Signal != "":
		result.Err = errors.New("signal: " + reply.Signal)
	case reply.Exit != 0:
		result.Err = fmt.Errorf("exit status %d", reply.Exit)
	}
	return result, nil
}

// closeIdleProbeWorkers closes every idle worker (tests, and shutdown).
func closeIdleProbeWorkers() {
	probePool.mu.Lock()
	var all []*probeWorker
	for key, list := range probePool.idle {
		all = append(all, list...)
		delete(probePool.idle, key)
	}
	probePool.mu.Unlock()
	for _, w := range all {
		w.close()
	}
}
