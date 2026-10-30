//go:build windows

package childprocess

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no process groups in the Unix sense and does not propagate
// termination, so the mechanism is a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: when the last handle to the job closes —
// which happens when this process dies, however it dies — every process in the
// job is terminated.
//
// The job is assigned to the SERVER process rather than to each child, because a
// child created by a process already in a job joins that job automatically. One
// call at startup therefore covers every child, every grandchild, every helper's
// rclone and every sandbox wrapper, with no per-call-site bookkeeping to forget.
//
// UNVERIFIED: this cannot be exercised on the machine it was written on. It is
// compile-checked for windows/amd64 and windows/arm64 and it fails soft — a
// server that cannot create or join a job runs exactly as it did before.
var processJob windows.Handle

func adoptTree() error {
	if processJob != 0 {
		return nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	if err = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		// Windows 8 and later support nested jobs, so this is rare; where it
		// fails, the server keeps running unguarded rather than refusing to start.
		windows.CloseHandle(job)
		return err
	}
	// The handle is held for the life of the process on purpose: closing it is
	// what kills the tree, so it must not be closed until the process ends.
	processJob = job
	return nil
}

// Children inherit the process job, so there is nothing to set per command,
// except one thing Windows can't do at all: inherit descriptors by number
// (exec.Cmd.ExtraFiles makes Start fail). The only ExtraFiles media jobs carry
// are lifetime fences (a tuner lock, a prepared-media custody lock, endpoint
// reservations) that keep a resource held if the server dies before the child;
// the job object ends the child with the server, which is the same guarantee,
// so they are dropped rather than failing Live TV and recording (BE-MEDIA-02).
// Inputs are never passed this way on Windows: the child opens the path.
func configure(cmd *exec.Cmd) { cmd.ExtraFiles = nil }

func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
