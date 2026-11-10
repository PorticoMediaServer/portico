//go:build linux || darwin

package streaming

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These regressions exercise the actual quiesce step over one retained
// conditional object and one supervised attempt. They use a temporary OS pipe,
// a fake object and a canceled copy completion; no constructor, helper,
// provider, subprocess, media bytes or native/runtime claim is involved.

type quiesceOrder struct {
	mu     sync.Mutex
	events []string
}

func (o *quiesceOrder) add(event string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *quiesceOrder) index(event string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, e := range o.events {
		if e == event {
			return i
		}
	}
	return -1
}

type quiesceSource struct {
	order  *quiesceOrder
	closes int32
}

func (s *quiesceSource) Read([]byte) (int, error)    { return 0, io.EOF }
func (s *quiesceSource) Check(context.Context) error { return nil }
func (s *quiesceSource) Close() error {
	atomic.AddInt32(&s.closes, 1)
	s.order.add("source-close")
	return nil
}

type quiesceChecks struct {
	order     *quiesceOrder
	validate  func(int) error
	source    func(int) error
	validates int32
	sources   int32
}

func (c *quiesceChecks) validateFn(context.Context) error {
	n := int(atomic.AddInt32(&c.validates, 1))
	c.order.add("validate")
	if c.validate == nil {
		return nil
	}
	return c.validate(n)
}

func (c *quiesceChecks) sourceFn(context.Context) error {
	n := int(atomic.AddInt32(&c.sources, 1))
	c.order.add("check-source")
	if c.source == nil {
		return nil
	}
	return c.source(n)
}

func runQuiesce(t *testing.T, release func(), fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		release()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("quiesce invocation did not retire during cleanup")
		}
	})
	go func() { defer close(joined); done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("quiesce did not join its original attempt")
		return nil
	}
}
