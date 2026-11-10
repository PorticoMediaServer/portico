package auth

import "time"

// The guessing limits. Each makes whoever is guessing wait longer the more they get wrong;
// none locks an account. The owner chooses Relaxed, Standard or Strict for passwords and,
// separately, for PINs; each is a table of numbers below.

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

// PasswordRules is one strictness for passwords. SourceFailures is how many wrong passwords,
// PINs and codes of any kind one network away from home may send before it is blocked.
type PasswordRules struct {
	Limit          PasswordLimit
	SourceFailures int
}

// PINRules is one strictness for PINs. Device counts a profile's wrong PINs on one device.
// Profile counts them on every device together: a device's own count starts again on a
// device nobody has seen before, and a new device costs nothing to make, so without it a
// PIN could be guessed as fast as devices can be made. The shared count makes every device
// wait, the profile's own person included, so its waits are short and start late: on
// Standard, ten wrong PINs is more than a household mistypes in a day, and the longest wait
// is a quarter of an hour. Forgot PIN ends the wait at once.
type PINRules struct {
	Device, Profile PINLimit
}

var passwordStrictness = map[string]PasswordRules{
	"relaxed":  {PasswordLimit{FreeTries: 9, FirstWait: time.Second, MaxWait: time.Minute}, 60},
	"standard": {PasswordLimit{FreeTries: 4, FirstWait: time.Second, MaxWait: time.Minute}, 30},
	"strict":   {PasswordLimit{FreeTries: 2, FirstWait: 10 * time.Second, MaxWait: 15 * time.Minute}, 15},
}

var pinStrictness = map[string]PINRules{
	"relaxed": {
		PINLimit{{5, 30 * time.Second}, {10, 5 * time.Minute}, {15, 15 * time.Minute}, {20, time.Hour}},
		PINLimit{{20, time.Minute}, {40, 5 * time.Minute}, {80, 15 * time.Minute}},
	},
	"standard": {
		PINLimit{{1, 2 * time.Second}, {3, 30 * time.Second}, {5, 5 * time.Minute}, {10, 15 * time.Minute}, {15, time.Hour}},
		PINLimit{{10, time.Minute}, {20, 5 * time.Minute}, {40, 15 * time.Minute}},
	},
	"strict": {
		PINLimit{{1, 2 * time.Second}, {3, 5 * time.Minute}, {5, 15 * time.Minute}, {10, time.Hour}},
		PINLimit{{5, time.Minute}, {10, 5 * time.Minute}, {20, 15 * time.Minute}},
	},
}

// Passwords returns the numbers for the owner's choice of strictness for passwords.
func (p Policy) Passwords() PasswordRules { return passwordStrictness[p.PasswordAttempts] }

// PINs returns the numbers for the owner's choice of strictness for PINs.
func (p Policy) PINs() PINRules { return pinStrictness[p.PINAttempts] }

// SourceBlock is how long a network's first block lasts. Each one after is twice as long, up to a day.
const SourceBlock = 15 * time.Minute

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
func (p PasswordRules) SourceDelay(failures int) time.Duration {
	if failures < p.SourceFailures {
		return 0
	}
	return SourceBlock
}
