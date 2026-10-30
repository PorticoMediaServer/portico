//go:build windows

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

const descriptorInput = "portico-retained-scan-input"
const scanCPUSeconds = 300

// A decoder that must open by name gets a read-data handle denying writes and
// deletion on the admitted file. Its physical path is checked before and after
// the probe, without locking ancestor directories. Protocol and format bounds
// stay identical to descriptor-fed Unix jobs.
func lockWindowsMediaInput(source *os.File) (*os.File, string, func(), error) {
	var buffer [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(source.Fd()), &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		return nil, "", nil, ErrObservedSourceLost
	}
	path := windows.UTF16ToString(buffer[:n])
	expected, token, err := windowsFileBinding(source)
	if err != nil {
		return nil, "", nil, err
	}
	var held []*os.File
	closeInput := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Close()
		}
	}
	open := func(name string) (*os.File, error) {
		wide, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(handle), name)
		held = append(held, file)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return nil, err
		}
		if err := rejectWindowsNameSurrogate(handle, info.FileAttributes); err != nil {
			return nil, err
		}
		return file, nil
	}
	locked, err := open(path)
	if err != nil {
		closeInput()
		return nil, "", nil, err
	}
	if err := checkWindowsMediaInput(locked, path, expected, token); err != nil {
		closeInput()
		return nil, "", nil, err
	}
	info, err := locked.Stat()
	if err != nil || !info.Mode().IsRegular() {
		closeInput()
		return nil, "", nil, ErrObservedSourceLost
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		closeInput()
		return nil, "", nil, ErrObservedSourceLost
	}
	return locked, path, closeInput, nil
}

// Check both the retained handle and the name the decoder uses. A failed final
// check makes the helper fail, so callers discard probe facts and artwork.
func checkWindowsMediaInput(locked *os.File, path, expected, token string) error {
	actual, revision, err := windowsFileBinding(locked)
	if err != nil || actual != expected || revision != token {
		return ErrObservedSourceLost
	}
	current, err := os.Open(path)
	if err != nil {
		return ErrObservedSourceLost
	}
	defer current.Close()
	actual, revision, err = windowsFileBinding(current)
	if err != nil || actual != expected || revision != token {
		return ErrObservedSourceLost
	}
	return nil
}

var probeJob struct {
	sync.Once
	handle windows.Handle
	err    error
}

// Windows cannot exec the tool in place. A mandatory nested job owned by this
// helper ends every descendant when it exits or is killed by its supervisor.
// Do not allow silent breakaway, and do not rely on the server's optional job.
func ensureWindowsProbeJob() error {
	probeJob.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			probeJob.err = err
			return
		}
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_JOB_TIME | windows.JOB_OBJECT_LIMIT_PRIORITY_CLASS
		limits.BasicLimitInformation.PriorityClass = windows.BELOW_NORMAL_PRIORITY_CLASS
		limits.BasicLimitInformation.PerJobUserTimeLimit = int64(scanCPUSeconds) * 10000000
		if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err == nil {
			err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
		}
		if err != nil {
			windows.CloseHandle(job)
			probeJob.err = err
			return
		}
		// Held to process exit; closing a job containing this helper kills it.
		probeJob.handle = job
	})
	return probeJob.err
}

// Bound wall time independently of the server, including acquisition and final
// identity checks. Terminating the entire mandatory job also ends descendants
// blocked on I/O, which a CPU budget or killing only the tool cannot guarantee.
func windowsProbeContext(timeout time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	stop := context.AfterFunc(ctx, func() {
		if err := windows.TerminateJobObject(probeJob.handle, uint32(windows.ERROR_TIMEOUT)); err != nil {
			// Process exit closes the kill-on-close job even if termination fails.
			os.Exit(1)
		}
	})
	return ctx, func() {
		// Stop before cancellation: normal cleanup must not terminate this job.
		stop()
		cancel()
	}
}

func inventoryDirectoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	return errors.New("descriptor-bound directory scan execution is unsupported on this server platform")
}

func inventoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	if err := ensureWindowsProbeJob(); err != nil {
		return err
	}
	// Match the Unix probe's five-minute CPU ceiling with a wall-clock ceiling.
	ctx, cancel := windowsProbeContext(time.Duration(scanCPUSeconds) * time.Second)
	defer cancel()
	source, err := openInventoryContent(*r.Inventory)
	if err != nil {
		return err
	}
	defer source.Close()
	locked, path, closeInput, err := lockWindowsMediaInput(source)
	if err != nil {
		return err
	}
	defer closeInput()
	expected, token, err := windowsFileBinding(locked)
	if err != nil {
		return err
	}
	argv, err := replaceScanInput(r.Argv, descriptorInput, path)
	if err != nil {
		return err
	}
	cmd, err := mediaexec.CommandArgv(ctx, argv)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return checkWindowsMediaInput(locked, path, expected, token)
}
