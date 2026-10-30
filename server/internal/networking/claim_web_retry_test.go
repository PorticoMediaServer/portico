package networking

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A slow or failing Portico Services response after the person approved must not leave
// setup stuck: transient failures are retried until they recover (the rig's d2).
func TestWebApprovalRetriesTransientFailuresUntilTheyRecover(t *testing.T) {
	calls := 0
	failures := []error{&controlHTTPError{}, timeoutError{}, ErrUnavailable}
	started := time.Now()
	err := retryWebApproval(context.Background(), func() error {
		calls++
		if calls <= len(failures) {
			return failures[calls-1]
		}
		return nil
	})
	if err != nil || calls != 4 {
		t.Fatalf("transient failures were not retried to success: calls=%d err=%v", calls, err)
	}
	// 1 s, 2 s, 4 s base delays (plus up to half again of spread).
	if elapsed := time.Since(started); elapsed < 7*time.Second || elapsed > 12*time.Second {
		t.Fatalf("backoff was not 1, 2, 4 s: %s", elapsed)
	}
}

func TestWebApprovalHonoursRetryAfter(t *testing.T) {
	calls := 0
	started := time.Now()
	err := retryWebApproval(context.Background(), func() error {
		calls++
		if calls == 1 {
			return &controlHTTPError{retryAt: time.Now().Add(3 * time.Second)}
		}
		return nil
	})
	if err != nil || calls != 2 || time.Since(started) < 3*time.Second {
		t.Fatalf("Retry-After was undercut: calls=%d after %s err=%v", calls, time.Since(started), err)
	}
}

func TestWebApprovalStopsAtOnceOnARefusal(t *testing.T) {
	for _, refusal := range []error{ErrStale, ErrApprovalRequired, ErrClaimOwnerRequired, ErrCancelled, &controlHTTPError{rejected: true}} {
		calls := 0
		started := time.Now()
		if err := retryWebApproval(context.Background(), func() error { calls++; return refusal }); !errors.Is(err, refusal) && err != refusal || calls != 1 || time.Since(started) > time.Second {
			t.Fatalf("a refusal was retried: %v calls=%d", refusal, calls)
		}
	}
}

func TestWebApprovalRetryEndsWithTheClaimRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	calls := 0
	if err := retryWebApproval(ctx, func() error { calls++; return ErrUnavailable }); !errors.Is(err, ErrUnavailable) || calls < 1 || calls > 3 {
		t.Fatalf("retries outlived the claim request: calls=%d err=%v", calls, err)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }
