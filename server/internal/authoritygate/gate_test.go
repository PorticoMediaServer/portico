package authoritygate

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

type unlockedLogWriter struct {
	gate *AuthorityGate
	bytes.Buffer
	locked bool
}

func (w *unlockedLogWriter) Write(p []byte) (int, error) {
	if w.gate.mu.TryLock() {
		w.gate.mu.Unlock()
	} else {
		w.locked = true
	}
	return w.Buffer.Write(p)
}

func TestLeaseSlowLockWarningRunsAfterUnlock(t *testing.T) {
	gate, err := NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	output := &unlockedLogWriter{gate: gate}
	prior := log.Writer()
	log.SetOutput(output)
	defer log.SetOutput(prior)
	gate.mu.Lock()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() { close(started); done <- lease.Commit(func() error { return nil }) }()
	<-started
	// Exercise a real contended lock, rather than a fabricated elapsed value.
	timer := time.NewTimer(300 * time.Millisecond)
	<-timer.C
	gate.mu.Unlock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if output.locked || !strings.Contains(output.String(), "Authority lease lock waited for") {
		t.Fatalf("warning held authority or was missing: locked=%v %s", output.locked, output.String())
	}
}

func TestAuthorityQuiesceCancelsWithoutHoldingTheCommitCallback(t *testing.T) {
	gate, err := NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	defer once.Do(func() { close(release) })
	go func() { done <- lease.Commit(func() error { close(entered); <-release; return nil }) }()
	<-entered
	gate.BeginQuiesce()
	if lease.Context().Err() == nil || gate.Rotate(strings.Repeat("b", 64)) == nil {
		t.Fatal("live callback was not fenced")
	}
	lease.Close()
	once.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = gate.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = gate.Rotate(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err = gate.Activate(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
}
