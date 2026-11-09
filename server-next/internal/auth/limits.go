package auth

import (
	"net/netip"
	"time"
)

// SessionIdle is how long a sign-in lasts without being used. Each renewal starts it again.
const SessionIdle = 90 * 24 * time.Hour

// The guessing limits. Each makes whoever is guessing wait longer the more they get wrong;
// none locks an account. A strictness is a table of numbers: Standard is the only one so
// far, and Relaxed and Strict will be two more values of the same types.

// PasswordLimit: FreeTries wrong passwords cost nothing, then the wait doubles from
// FirstWait up to MaxWait.
type PasswordLimit struct {
	FreeTries          int
	FirstWait, MaxWait time.Duration
}

// PINStep is the wait from the From-th wrong PIN in a row onwards.
type PINStep struct {
	From int
	Wait time.Duration
}

// PINLimit is steeper than a password's, because a PIN has only 10,000 values.
type PINLimit []PINStep

var (
	StandardPasswords = PasswordLimit{FreeTries: 4, FirstWait: time.Second, MaxWait: time.Minute}
	StandardPINs      = PINLimit{{1, 2 * time.Second}, {3, 30 * time.Second}, {5, 5 * time.Minute}, {10, 15 * time.Minute}, {15, time.Hour}}
)

// SourceFailures is how many wrong passwords, PINs and codes of any kind one network away
// from home may send before it is blocked, and SourceBlock how long the first block lasts.
const (
	SourceFailures = 30
	SourceBlock    = 15 * time.Minute
)

// Delay is the wait after this many recent wrong passwords.
func (l PasswordLimit) Delay(failures int) time.Duration {
	if failures <= l.FreeTries {
		return 0
	}
	return min(l.FirstWait<<min(failures-l.FreeTries-1, 20), l.MaxWait)
}

// Delay is the wait after this many wrong PINs in a row.
func (l PINLimit) Delay(failures int) time.Duration {
	var wait time.Duration
	for _, step := range l {
		if failures >= step.From {
			wait = step.Wait
		}
	}
	return wait
}

// SourceDelay blocks a network that keeps failing at anything.
func SourceDelay(failures int) time.Duration {
	if failures < SourceFailures {
		return 0
	}
	return SourceBlock
}

// Source turns a connection's remote address into the network failures are counted against:
// the IPv4 address, or the /64 of an IPv6 address. home is true for this machine and for
// private addresses, which stand for the home network until the owner can say what theirs
// is. The home network is never blocked: one mistyping household must not shut out its own TV.
func Source(remoteAddr string) (source string, home bool) {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return "", true
	}
	addr := ap.Addr().Unmap()
	home = addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String(), home
	}
	return addr.String(), home
}

// AtHome reports whether a stored address (no port) is on the home network.
func AtHome(addr string) bool {
	a, err := netip.ParseAddr(addr)
	return err == nil && (a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast())
}
