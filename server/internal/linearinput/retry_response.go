package linearinput

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Only closed timing advice survives a temporary upstream response. Neither
// response text nor its URL/header values enter an error or public payload.
type retryableInput struct{ after time.Duration }

func (*retryableInput) Error() string { return ErrUnavailable.Error() }
func (*retryableInput) Unwrap() error { return ErrUnavailable }

func retryDelay(err error, fallback time.Duration) time.Duration {
	var advice *retryableInput
	if errors.As(err, &advice) {
		return max(fallback, advice.after)
	}
	return fallback
}

func retryAfter(raw string, budget time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 || budget <= 0 {
		return 0
	}
	digits := true
	for _, c := range raw {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		// Huge valid decimal hints still mean no resend within this budget.
		seconds, err := strconv.ParseUint(strings.TrimLeft(raw, "0"), 10, 64)
		if strings.Trim(raw, "0") == "" {
			return 0
		}
		if err != nil || seconds > uint64(budget/time.Second) {
			return budget
		}
		return time.Duration(seconds) * time.Second
	}
	at, err := http.ParseTime(raw)
	if err != nil {
		return 0
	}
	delay := time.Until(at)
	if delay <= 0 {
		return 0
	}
	return min(delay, budget)
}
