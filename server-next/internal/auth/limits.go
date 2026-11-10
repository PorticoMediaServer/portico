package auth

import "time"

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
	// StandardProfilePINs counts a profile's wrong PINs on every device together. A device's
	// own count starts again on a device nobody has seen before, and a new device costs
	// nothing to make, so without this a PIN could be guessed as fast as devices can be
	// made. It makes every device wait, the profile's own person included, so its waits are
	// short and start late: ten wrong PINs is more than a household mistypes in a day, and
	// the longest wait is a quarter of an hour. Forgot PIN ends the wait at once.
	StandardProfilePINs = PINLimit{{10, time.Minute}, {20, 5 * time.Minute}, {40, 15 * time.Minute}}
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
