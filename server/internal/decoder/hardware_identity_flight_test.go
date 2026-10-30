package decoder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type identityWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *identityWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestHardwareIdentityCoalescesOnlyOverlappingObservations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int64
	d := &HardwareDetector{RuntimeIdentity: func(check context.Context, _ string) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-check.Done():
				return "", check.Err()
			}
			return "first-exact-bytes", nil
		}
		return "changed-exact-bytes", nil
	}}
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	var first, second string
	var firstErr, secondErr error
	waiter := &identityWaitContext{Context: ctx, waiting: make(chan struct{})}
	startedSecond := false
	t.Cleanup(func() {
		cancel()
		once.Do(func() { close(release) })
		for _, done := range []<-chan struct{}{firstDone, secondDone} {
			if done == secondDone && !startedSecond {
				continue
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("identity goroutine did not retire")
			}
		}
	})
	go func() { defer close(firstDone); first, firstErr = d.runtimeIdentity(ctx, "/original/tool") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	startedSecond = true
	go func() { defer close(secondDone); second, secondErr = d.runtimeIdentity(waiter, "/original/tool") }()
	select {
	case <-waiter.waiting:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	once.Do(func() { close(release) })
	for _, done := range []<-chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if firstErr != nil || secondErr != nil || first != "first-exact-bytes" || second != first || calls.Load() != 1 {
		t.Fatal(first, second, firstErr, secondErr, calls.Load())
	}
	next, err := d.runtimeIdentity(ctx, "/original/tool")
	if err != nil || next != "changed-exact-bytes" || calls.Load() != 2 {
		t.Fatal("completed observation reused as file cache", next, err, calls.Load())
	}
}

func TestHardwareCachedSelectionHonorsOriginalCancellation(t *testing.T) {
	type originalKey struct{}
	parent := context.WithValue(context.Background(), originalKey{}, "original")
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	calls := 0
	d := &HardwareDetector{RuntimeIdentity: func(check context.Context, _ string) (string, error) {
		calls++
		if check.Value(originalKey{}) != "original" {
			t.Error("original values lost")
		}
		originalDeadline, _ := ctx.Deadline()
		checkedDeadline, ok := check.Deadline()
		if !ok || checkedDeadline.After(originalDeadline) {
			t.Error("original deadline extended")
		}
		cancel()
		if !errors.Is(check.Err(), context.Canceled) {
			t.Error("original cancellation detached", check.Err())
		}
		return "", check.Err()
	}}
	if _, err := d.SelectCachedContext(ctx, "/original/tool", "auto", ""); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, err := d.SelectCachedContext(ctx, "/original/tool", "auto", ""); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("retired caller did new work", err, calls)
	}
}
