package read

import (
	"context"
	"errors"
	"testing"
)

func TestUOEventResumeBindingRestartAndForgery(t *testing.T) {
	c, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	b := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
	token, err := c.encode(b, 42)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := c.decode(token, b); err != nil || n != 42 {
		t.Fatal(n, err)
	}
	other := b
	other.viewer[0]++
	if _, err := c.decode(token, other); !errors.Is(err, errUOEventResync) {
		t.Fatal("foreign viewer", err)
	}
	restarted, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.decode(token, b); !errors.Is(err, errUOEventResync) {
		t.Fatal("restart reused token", err)
	}
	mutated := []byte(token)
	if mutated[20] == 'A' {
		mutated[20] = 'B'
	} else {
		mutated[20] = 'A'
	}
	if _, err := c.decode(string(mutated), b); !errors.Is(err, errUOEventResync) {
		t.Fatal("forged token", err)
	}
}
func TestUOEventWakeClosesReadParkRace(t *testing.T) {
	h := &uoEventWakeHub{}
	a, err := h.subscribe("profile:a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.release()
	b, err := h.subscribe("profile:a")
	if err != nil {
		t.Fatal(err)
	}
	defer b.release()
	before, err := a.observe()
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.subscribe("profile:b")
	if err != nil {
		t.Fatal(err)
	}
	defer other.release()
	otherBefore, _ := other.observe()
	h.publish("profile:a")
	if err := a.wait(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	select {
	case <-before.changed:
	default:
		t.Fatal("same audience did not wake")
	}
	select {
	case <-otherBefore.changed:
		t.Fatal("unrelated audience woke")
	default:
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := other.wait(ctx, otherBefore); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestUOEventRetentionGapAndEmptyHighWater(t *testing.T) {
	if err := uoEventResumeWindow(42, 40, 100); err != nil {
		t.Fatal(err)
	}
	if err := uoEventResumeWindow(38, 40, 100); !errors.Is(err, errUOEventResync) {
		t.Fatal("gap", err)
	}
	if err := uoEventResumeWindow(99, 0, 100); !errors.Is(err, errUOEventResync) {
		t.Fatal("empty retained ring lost high-water", err)
	}
	if err := uoEventResumeWindow(100, 0, 100); err != nil {
		t.Fatal(err)
	}
}
