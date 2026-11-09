//go:build !windows

package hostlimits

import "syscall"

// maximumSoftLimit is as high as this server will ask for. A hard limit of
// "unlimited" is common — macOS reports one, and so does a permissive systemd
// unit — and asking for it succeeds while meaning nothing; the kernel's own
// per-process ceiling applies anyway. A million descriptors is far past anything
// two hundred streams need, and a number a support bundle can be read.
const maximumSoftLimit = 1 << 20

func openFileLimits() (uint64, uint64) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return 0, 0
	}
	return uint64(limit.Cur), uint64(limit.Max)
}

// raiseOpenFiles lifts the soft limit towards the hard one. Raising the soft
// limit needs no privilege — it is the hard limit that does — so this works for
// an unprivileged service, and it does nothing when there is nothing to gain.
func raiseOpenFiles() (soft, hard uint64, raised bool) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return 0, 0, false
	}
	target := uint64(limit.Max)
	// macOS refuses any soft limit above kern.maxfilesperproc however generous
	// the hard limit claims to be, so ask for what the kernel will actually give.
	if ceiling, ok := platformFileCeiling(); ok && ceiling < target {
		target = ceiling
	}
	if target > maximumSoftLimit {
		target = maximumSoftLimit
	}
	if target <= uint64(limit.Cur) {
		return uint64(limit.Cur), uint64(limit.Max), false
	}
	raisedLimit := limit
	raisedLimit.Cur = rlimitValue(target)
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &raisedLimit); err != nil {
		return uint64(limit.Cur), uint64(limit.Max), false
	}
	return target, uint64(limit.Max), true
}
