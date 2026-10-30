package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sharedRangeResult struct {
	data []byte
	err  error
}

func sharedRangeFixture(t *testing.T) (*ServeEndpoint, <-chan struct{}, func(), *atomic.Int32) {
	t.Helper()
	life, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	endpoint, err := NewServeEndpoint(12345, "fixture", "private", NewOperationBudget(life, 2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)
	started, release := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	calls := new(atomic.Int32)
	endpoint.client.Transport = serveRoundTrip(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-release:
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Etag": {request.Header.Get("If-Match")}, "Content-Range": {"bytes 0-3/16"}}, ContentLength: 4, Body: io.NopCloser(strings.NewReader("same"))}, nil
	})
	return endpoint, started, unblock, calls
}

func waitRangeUsers(t *testing.T, endpoint *ServeEndpoint, users int) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		endpoint.rangesMu.Lock()
		actual := 0
		for _, shared := range endpoint.ranges {
			actual += shared.users
		}
		endpoint.rangesMu.Unlock()
		if actual == users {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("concurrent range users did not arrive", actual, users)
		case <-tick.C:
		}
	}
}

func runSharedRange(endpoint *ServeEndpoint, ctx context.Context, tag string, check func(context.Context) error) <-chan sharedRangeResult {
	done := make(chan sharedRangeResult, 1)
	go func() {
		data, err := endpoint.ReadRange(ctx, "film", tag, 16, 0, 4, check)
		done <- sharedRangeResult{data, err}
	}()
	return done
}

func TestSharedRangeUsesOneOperationButEachViewersOwnAuthority(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-bytes", true: "revoked-joiner"}[revoked], func(t *testing.T) {
			endpoint, started, release, calls := sharedRangeFixture(t)
			check := func(context.Context) error { return nil }
			leader := runSharedRange(endpoint, context.Background(), `"captured"`, check)
			<-started
			var checks atomic.Int32
			joiner := runSharedRange(endpoint, context.Background(), `"captured"`, func(context.Context) error {
				if checks.Add(1) > 1 && revoked {
					return ErrDenied
				}
				return nil
			})
			waitRangeUsers(t, endpoint, 2)
			if counts := endpoint.budget.Counts(); counts.Active != 1 || counts.Peak != 1 || calls.Load() != 1 {
				t.Fatal("same-file viewers kept separate upstream reads", counts, calls.Load())
			}
			release()
			first, second := <-leader, <-joiner
			if first.err != nil || string(first.data) != "same" {
				t.Fatal(first)
			}
			if revoked {
				if !errors.Is(second.err, ErrDenied) || len(second.data) != 0 {
					t.Fatal("leader permission granted bytes to revoked joiner", second)
				}
			} else {
				if second.err != nil || string(second.data) != "same" {
					t.Fatal(second)
				}
				clear(first.data)
				if string(second.data) != "same" {
					t.Fatal("one caller cleared another caller's bytes")
				}
			}
			// Completed bytes are not a cache and cannot substitute for another
			// captured request's real read/fences.
			data, err := endpoint.ReadRange(context.Background(), "film", `"captured"`, 16, 0, 4, check)
			if err != nil || string(data) != "same" || calls.Load() != 2 {
				t.Fatal("completed range was retained as provider evidence", data, err, calls.Load())
			}
		})
	}
}

func TestSharedRangeCancellationDoesNotDetachOrRetryTransfer(t *testing.T) {
	for _, cancelLeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "joiner", true: "leader"}[cancelLeader], func(t *testing.T) {
			endpoint, started, release, calls := sharedRangeFixture(t)
			leaderContext, stopLeader := context.WithCancel(context.Background())
			defer stopLeader()
			joinerContext, stopJoiner := context.WithCancel(context.Background())
			defer stopJoiner()
			check := func(context.Context) error { return nil }
			leader := runSharedRange(endpoint, leaderContext, `"captured"`, check)
			<-started
			joiner := runSharedRange(endpoint, joinerContext, `"captured"`, check)
			waitRangeUsers(t, endpoint, 2)
			if cancelLeader {
				stopLeader()
			} else {
				stopJoiner()
			}
			second := <-joiner
			if second.err == nil || len(second.data) != 0 {
				t.Fatal("canceled shared read exposed bytes", second)
			}
			release()
			first := <-leader
			if cancelLeader && (first.err == nil || len(first.data) != 0) || !cancelLeader && (first.err != nil || string(first.data) != "same") {
				t.Fatal("request cancellation crossed caller ownership", first, cancelLeader)
			}
			if calls.Load() != 1 || endpoint.budget.Counts().Active != 0 {
				t.Fatal("shared transfer detached or retried", calls.Load(), endpoint.budget.Counts())
			}
		})
	}
}

func TestSharedRangesNeverCombineDifferentValidators(t *testing.T) {
	endpoint, started, release, calls := sharedRangeFixture(t)
	check := func(context.Context) error { return nil }
	first := runSharedRange(endpoint, context.Background(), `"one"`, check)
	<-started
	second := runSharedRange(endpoint, context.Background(), `"two"`, check)
	<-started
	if calls.Load() != 2 || endpoint.budget.Counts().Active != 2 {
		t.Fatal("different captures shared an upstream read", calls.Load(), endpoint.budget.Counts())
	}
	release()
	if a, b := <-first, <-second; a.err != nil || b.err != nil {
		t.Fatal(a.err, b.err)
	}
}
