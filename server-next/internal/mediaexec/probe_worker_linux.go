//go:build linux

package mediaexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Scanner M4 (planning/v3/rebuilds/SCANNER-REVIEW.md): a scan probe used to
// set up a fresh bubblewrap sandbox per file, and on Linux that setup — one
// read-only bind mount for each file of ffprobe's library closure, 379 on
// Ubuntu — took ~200 ms against ffprobe's own ~35 ms. A probe worker is that
// same sandbox, set up once: the server binary runs inside it as
// ProbeWorkerFlag and starts a fresh ffprobe process for each request. The
// sandbox's mounts, namespaces and limits are exactly a probe's; what is shared
// between jobs is only the sandbox, and the worker cleans it between them:
//   - every process a job left behind is killed (the worker is the PID
//     namespace's subreaper), and its private temporary folder is removed;
//   - the worker is not dumpable, so a compromised tool cannot ptrace it to
//     reach a later job's descriptors;
//   - the tool only ever receives descriptors for its own job's input and
//     output, passed over the control socket.
//
// ProbeWorkerFlag is the server binary's entry point for the worker.
const ProbeWorkerFlag = "--portico-probe-worker"

// probeRequest is one job sent to a worker, with its input, stdout and stderr
// descriptors attached (SCM_RIGHTS, in that order).
type probeRequest struct {
	Args       []string `json:"args"`
	CPUSeconds int      `json:"cpuSeconds"`
}

// probeReply is the job's outcome.
type probeReply struct {
	Exit   int    `json:"exit"`
	Signal string `json:"signal,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	probeControlFD    = 3
	maxProbeRequest   = 256 << 10
	probeRequestFiles = 3
)

// runProbeWorker serves requests on descriptor 3 until the server closes it.
// executable is the one tool it may run, fixed when the sandbox was built.
func runProbeWorker(args []string) error {
	if len(args) != 2 || args[0] != ProbeWorkerFlag || !filepath.IsAbs(args[1]) {
		return ErrInvalidJob
	}
	executable := args[1]
	// The worker kills every other process it can see after each job, which
	// is safe only inside the sandbox's own PID namespace: bubblewrap's
	// --unshare-pid runs an init as PID 1 and the command as PID 2. Anywhere
	// else, refuse before touching anything.
	if !inOwnPIDNamespace() {
		return errProbeWorkerOutsideSandbox
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("probe worker: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("probe worker: %w", err)
	}
	// The tool must never inherit the control socket.
	unix.CloseOnExec(probeControlFD)
	control := os.NewFile(probeControlFD, "probe-control")
	if control == nil {
		return ErrInvalidJob
	}
	defer control.Close()
	buffer := make([]byte, maxProbeRequest)
	oob := make([]byte, unix.CmsgSpace(4*probeRequestFiles))
	for job := 0; ; job++ {
		n, oobn, _, _, err := unix.Recvmsg(probeControlFD, buffer, oob, unix.MSG_CMSG_CLOEXEC)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		if n == 0 && oobn == 0 {
			return nil // the server closed the socket
		}
		files, ferr := receivedFiles(oob[:oobn])
		reply := probeReply{}
		var request probeRequest
		switch {
		case ferr != nil:
			reply.Error = ferr.Error()
		case json.Unmarshal(buffer[:n], &request) != nil || len(request.Args) > maxJobArguments || request.CPUSeconds < 0:
			reply.Error = ErrInvalidJob.Error()
		default:
			reply = runProbeJob(executable, request, files, job)
		}
		for _, f := range files {
			f.Close()
		}
		raw, _ := json.Marshal(reply)
		if _, err = unix.Write(probeControlFD, raw); err != nil {
			return err
		}
	}
}

// runProbeJob runs one tool process and leaves the sandbox as it found it.
func runProbeJob(executable string, request probeRequest, files []*os.File, job int) probeReply {
	// The sandbox's private temporary folder (a tmpfs), one subfolder per job.
	temp := filepath.Join(os.TempDir(), "job-"+strconv.Itoa(job))
	if err := os.Mkdir(temp, 0700); err != nil {
		return probeReply{Error: err.Error()}
	}
	defer cleanProbeSandbox(temp)
	cmd := &exec.Cmd{
		Path: executable,
		Args: append([]string{executable}, request.Args...),
		// The sandbox's own environment (cleared by bubblewrap: PATH and the
		// closure's LD_LIBRARY_PATH), with this job's temporary folder.
		Env:         append(os.Environ(), "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp, "HOME="+temp),
		Dir:         temp,
		Stdout:      files[1],
		Stderr:      files[2],
		ExtraFiles:  []*os.File{files[0]},
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err := cmd.Start(); err != nil {
		return probeReply{Error: err.Error()}
	}
	if request.CPUSeconds > 0 {
		limit := uint64(request.CPUSeconds)
		if err := unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_CPU, &unix.Rlimit{Cur: limit, Max: limit + 5}, nil); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return probeReply{Error: err.Error()}
		}
	}
	err := cmd.Wait()
	reply := probeReply{}
	if state := cmd.ProcessState; state != nil {
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			reply.Signal = status.Signal().String()
			reply.Exit = -1
		} else {
			reply.Exit = state.ExitCode()
		}
	} else if err != nil {
		reply.Error = err.Error()
	}
	return reply
}

// cleanProbeSandbox kills every process but the worker and the sandbox's init,
// reaps them, and removes the job's temporary folder.
func cleanProbeSandbox(temp string) {
	self := os.Getpid()
	for pass := 0; pass < 3; pass++ {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			break
		}
		killed := false
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid <= 1 || pid == self {
				continue
			}
			if unix.Kill(pid, unix.SIGKILL) == nil {
				killed = true
			}
		}
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
		}
		if !killed {
			break
		}
	}
	_ = os.RemoveAll(temp)
}

var errProbeWorkerOutsideSandbox = errors.New("probe worker: not inside its sandbox's PID namespace")

// inOwnPIDNamespace reports whether the worker runs as bubblewrap's command:
// PID 2, whose parent is PID 1, and PID 1 is bubblewrap itself. The PID shape
// alone could match an unrelated init's second process (a container's, say),
// where the cleanup would reach every process that init can see.
func inOwnPIDNamespace() bool {
	if os.Getpid() != 2 || os.Getppid() != 1 {
		return false
	}
	comm, err := os.ReadFile("/proc/1/comm")
	return err == nil && strings.TrimSpace(string(comm)) == "bwrap"
}

func receivedFiles(oob []byte) ([]*os.File, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range messages {
		rights, err := unix.ParseUnixRights(&m)
		if err != nil {
			return nil, err
		}
		fds = append(fds, rights...)
	}
	if len(fds) != probeRequestFiles {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return nil, errors.New("a probe request carries exactly its input, stdout and stderr")
	}
	files := make([]*os.File, len(fds))
	for i, fd := range fds {
		files[i] = os.NewFile(uintptr(fd), "probe-job")
	}
	return files, nil
}
