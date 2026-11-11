// Package schedule is when a server may call Portico Services. It is the server's half of the
// call budget (hosted-services/CALL-BUDGET.md, rules 3 to 5) and uses only the standard
// library, so the server copies this folder as it is and both sides agree by construction.
package schedule

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

const day = 24 * time.Hour

// Slot is the second of the UTC day at which a server checks in. It depends only on who the
// server is, so the fleet is spread evenly over the day and is back on its own seconds after
// any outage: the time never depended on when the last check-in happened.
func Slot(serverID string) int {
	sum := sha256.Sum256(append([]byte("portico.check-in-slot\x00"), serverID...))
	return int(binary.BigEndian.Uint64(sum[:8]) % 86400)
}

// NextCheckIn is the first moment after the given one that falls on the server's slot.
func NextCheckIn(serverID string, after time.Time) time.Time {
	after = after.UTC()
	at := after.Truncate(day).Add(time.Duration(Slot(serverID)) * time.Second)
	if !at.After(after) {
		at = at.Add(day)
	}
	return at
}

// StartDelay is what a server does about its check-in when it starts. Starting is not a
// reason to call: it waits for its slot unless its last successful contact is more than a
// day old, and then it checks in at a random moment in the following hour. r is a random
// number in [0, 1).
func StartDelay(lastContact, now time.Time, r float64) (wait time.Duration, missed bool) {
	if now.Sub(lastContact) <= day {
		return 0, false
	}
	return time.Duration(r * float64(time.Hour)), true
}

// Retry is how an automatic call is repeated when it got no answer.
type Retry struct {
	First   time.Duration
	Ceiling time.Duration
}

var (
	// CheckInRetry repeats a failed daily check-in.
	CheckInRetry = Retry{First: 5 * time.Minute, Ceiling: 6 * time.Hour}
	// ChangeRetry repeats a document or certificate call that is waiting to be sent, and the
	// one "this server has left" message (which also stops for good after LeaveGiveUp).
	ChangeRetry = Retry{First: time.Minute, Ceiling: time.Hour}
	// AppRetry repeats an app's automatic call. An app never repeats one in the background.
	AppRetry = Retry{First: 30 * time.Second, Ceiling: time.Hour}
)

// LeaveGiveUp is how long a server that switched to local sign-in keeps trying to say so.
const LeaveGiveUp = 7 * day

// MinimumRetryAfter is the shortest wait a caller accepts from a Retry-After answer.
const MinimumRetryAfter = 30 * time.Second

// Wait is how long to wait before try number attempt (0 is the first repeat). The step
// doubles to the ceiling and the wait is drawn evenly between half the step and all of it,
// so callers that failed together do not return together. r is a random number in [0, 1).
func (p Retry) Wait(attempt int, r float64) time.Duration {
	step := p.Ceiling
	if attempt < 30 {
		if s := p.First << attempt; s > 0 && s < p.Ceiling {
			step = s
		}
	}
	return step/2 + time.Duration(r*float64(step/2))
}

// After is the wait when Services answered with Retry-After. Services is alive and is
// setting the pace, so its answer is obeyed in place of the backoff and the attempt count
// does not grow.
func After(retryAfter time.Duration) time.Duration {
	return max(retryAfter, MinimumRetryAfter)
}
