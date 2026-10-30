package dbwork

import (
	"context"
	"testing"
	"time"
)

func TestBackgroundPauseResumesAndCancelsAtBatchBoundary(t *testing.T) {
	defer SetBackgroundPolicy(SpeedNormal, 0)
	SetBackgroundPolicy(SpeedGentle, time.Now().Add(time.Hour).UnixMilli())
	if BackgroundSpeed(context.Background()) != SpeedGentle {
		t.Fatal("unconfigured workers missed speed")
	}
	done := make(chan bool, 1)
	go func() { done <- Yield(context.Background()) }()
	select {
	case <-done:
		t.Fatal("paused batch started")
	case <-time.After(20 * time.Millisecond):
	}
	SetBackgroundPolicy(SpeedFast, 0)
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("resume failed")
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not wake worker")
	}
	SetBackgroundPolicy(SpeedNormal, time.Now().Add(time.Hour).UnixMilli())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Yield(ctx) {
		t.Fatal("cancelled pause continued")
	}
	SetBackgroundPolicy(SpeedNormal, time.Now().Add(-time.Second).UnixMilli())
	if !Yield(context.Background()) {
		t.Fatal("expired pause blocked")
	}
}
