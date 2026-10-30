//go:build darwin

package mediaexec

import (
	"strconv"

	"golang.org/x/sys/unix"
)

type processIdentity struct{ start, boot string }

// identityOf is the process's start time and the boot time, from the kernel's
// process table.
func identityOf(pid int) (processIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return processIdentity{}, err
	}
	if int(info.Proc.P_pid) != pid {
		return processIdentity{}, ErrInvalidJob
	}
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return processIdentity{}, err
	}
	start := info.Proc.P_starttime
	return processIdentity{
		start: strconv.FormatInt(int64(start.Sec), 10) + "." + strconv.FormatInt(int64(start.Usec), 10),
		boot:  strconv.FormatInt(int64(boot.Sec), 10) + "." + strconv.FormatInt(int64(boot.Usec), 10),
	}, nil
}
