package db

import (
	"errors"
	"time"
)

// What callers outside this package can tell apart. Anything else is a fault.
var (
	// ErrNotFound: there is no such thing, or the caller may not know that there is.
	ErrNotFound = errors.New("not found")
	// ErrConflict: the change cannot be made because of what already exists.
	ErrConflict = errors.New("conflict")
	// ErrKeyReused: this Idempotency-Key was already used for a different request.
	ErrKeyReused = errors.New("idempotency key reused with a different request")
)

// LimitedError means too many wrong guesses came from this source: wait, then try again.
type LimitedError struct{ Wait time.Duration }

func (e *LimitedError) Error() string { return "too many attempts" }
