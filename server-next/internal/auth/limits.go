package auth

import (
	"net/netip"
	"time"
)

// SessionIdle is how long a sign-in lasts without being used. Each renewal starts it again.
const SessionIdle = 90 * 24 * time.Hour

// The guessing limits. Each makes a source wait longer the more it gets wrong; none locks
// an account, and old failures fade (the count halves every 15 minutes).

// PasswordDelay: four wrong passwords are free, then the wait doubles from 1 second to a minute.
func PasswordDelay(failures int) time.Duration {
	if failures < 5 {
		return 0
	}
	return min(time.Second<<min(failures-5, 6), time.Minute)
}

// PINDelay is steeper, because a PIN has only 10,000 values.
func PINDelay(failures int) time.Duration {
	switch {
	case failures <= 0:
		return 0
	case failures <= 2:
		return 2 * time.Second
	case failures <= 4:
		return 30 * time.Second
	case failures <= 9:
		return 5 * time.Minute
	case failures <= 14:
		return 15 * time.Minute
	}
	return time.Hour
}

// SourceDelay stops one network address that keeps failing at anything for a quarter of an hour.
func SourceDelay(failures int) time.Duration {
	if failures < 30 {
		return 0
	}
	return 15 * time.Minute
}

// Source turns a connection's remote address into what failures are counted against: the
// IPv4 address, or the /64 of an IPv6 address. blockable is false for addresses on the
// home network or this machine, which are counted but never made to wait as a source:
// one mistyping household must not shut out its own TV.
func Source(remoteAddr string) (source string, blockable bool) {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return "", false
	}
	addr := ap.Addr().Unmap()
	blockable = !(addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast())
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String(), blockable
	}
	return addr.String(), blockable
}
