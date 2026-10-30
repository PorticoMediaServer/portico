package networking

import (
	"errors"
	"math/rand/v2"
	"time"
)

// A failed HTTP response is transport status, never signed terminal authority.
type controlHTTPError struct {
	retryAt    time.Time
	checkClaim bool
	// rejected: Hosted understood the request and refused it for good (a 4xx
	// that is not authentication, timeout or rate limiting). Retrying the same
	// body can never succeed.
	rejected bool
}

func (e *controlHTTPError) Error() string { return "hosted control temporarily unavailable" }
func (e *controlHTTPError) Unwrap() error { return ErrUnavailable }
func ControlRetryAt(err error) time.Time {
	var control *controlHTTPError
	if errors.As(err, &control) {
		return control.retryAt
	}
	var certificate *certificateHTTPError
	if errors.As(err, &certificate) {
		return certificate.RetryAt
	}
	return time.Time{}
}

// RetryAt is the one backoff rule for every call this server makes to Hosted: exponential
// from unit, capped at unit<<maxShift, with jitter proportional to the delay.
//
// Proportional matters. A fleet that failed together (a Hosted outage) backs off together, and
// a fixed few seconds of jitter leaves it arriving as one wave every cycle for ever. Up to half
// the delay again spreads that wave across minutes, so a recovering Hosted meets a trickle.
//
// A server-provided floor (Retry-After) is never undercut, and the same spread is added beyond
// it: everyone told "come back in an hour" must not come back in the same second.
func RetryAt(now time.Time, unit time.Duration, attempts, maxShift int, floor time.Time) time.Time {
	attempts = min(max(attempts, 0), maxShift)
	delay := unit << attempts
	spread := time.Duration(rand.Int64N(int64(delay)/2 + 1))
	next := now.Add(delay + spread)
	if floor.After(now.Add(delay)) {
		next = floor.Add(spread)
	}
	return next
}

// ControlRejected reports a permanent refusal of this exact request.
func ControlRejected(err error) bool {
	var e *controlHTTPError
	return errors.As(err, &e) && e.rejected
}

// An authentication denial requests original-key reconciliation, never direct revocation.
func NeedsClaimReconciliation(err error) bool {
	var e *controlHTTPError
	return errors.As(err, &e) && e.checkClaim
}
