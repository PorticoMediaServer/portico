package progressio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

type progressRoundTripper func(*http.Request) (*http.Response, error)

func (f progressRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlowResponseExtendsDeadlineAndStallEnds(t *testing.T) {
	for _, stall := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			// Keep the actual Transport/body wrappers, but use an in-process
			// producer so all byte arrivals and deadlines use the virtual clock.
			base := progressRoundTripper(func(r *http.Request) (*http.Response, error) {
				reader, writer := io.Pipe()
				stop := context.AfterFunc(r.Context(), func() { writer.CloseWithError(context.Cause(r.Context())) })
				go func() {
					defer writer.Close()
					defer stop()
					for range 8 {
						if _, err := writer.Write([]byte("x")); err != nil {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					if stall {
						<-r.Context().Done()
						writer.CloseWithError(context.Cause(r.Context()))
					}
				}()
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
			})
			client := &http.Client{Transport: Transport{Base: base, Idle: 50 * time.Millisecond, Overall: time.Second}}
			response, err := client.Get("http://fixture/progress")
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if len(data) != 8 {
				t.Fatalf("progressing body lost bytes: %q %v", data, err)
			}
			if stall && !errors.Is(err, context.DeadlineExceeded) || !stall && err != nil {
				t.Fatalf("stall=%v: %v", stall, err)
			}
		})
	}
}

func TestOverallSafetyCapAndParentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, watch := New(context.Background(), time.Second, 20*time.Millisecond)
		defer watch.Close()
		time.Sleep(19 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("overall cap fired early")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			t.Fatal("overall cap did not expire", context.Cause(ctx))
		}
		parent, cancel := context.WithCancel(context.Background())
		ctx, other := New(parent, time.Second, 0)
		defer other.Close()
		cancel()
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatal("lost parent cancellation")
		}
	})
}

func TestWaitingForAdmissionDoesNotStartIdleTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, watch := NewDormant(context.Background(), 20*time.Millisecond)
		defer watch.Close()
		time.Sleep(40 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("resource queue consumed the producer deadline")
		}
		watch.Ping()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			t.Fatal("started producer had no idle bound", context.Cause(ctx))
		}
	})
}

func TestQueuedExpiryDoesNotCancelNewProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, watch := New(context.Background(), 100*time.Millisecond, 0)
		defer watch.Close()
		// Hold back the old delivery, then invoke that same expiry callback
		// after new bytes arrive. No mutex fairness or scheduler order is assumed.
		watch.timer.Stop()
		time.Sleep(100 * time.Millisecond)
		watch.Ping()
		watch.expireIdle()
		if ctx.Err() != nil {
			t.Fatal("queued expiry cancelled newly progressing work")
		}
		time.Sleep(99 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("renewed idle deadline fired early")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			t.Fatal("real idle stall did not expire", context.Cause(ctx))
		}
	})
}
