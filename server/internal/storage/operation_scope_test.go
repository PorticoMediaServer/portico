package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOperationScopeWaitsForCleanupAndPermitRetirement(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	supervisor := &Supervisor{Limit: 4}
	scope := NewOperationScope()
	complete, err := scope.Begin()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(request{Operation: "root", Path: t.TempDir()})
	cmd := exec.Command(binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(body)
	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCleanup) }) }
	defer release()
	runDone := make(chan error, 1)
	go func() {
		runDone <- supervisor.RunOwnedCompletion(context.Background(), "playback:scope-cleanup", cmd, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e }, func() { close(cleanupEntered); <-releaseCleanup }, complete)
	}()
	select {
	case <-cleanupEntered:
	// Race-instrumented helpers include a one-second runtime exit delay.
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not reach post-Wait cleanup")
	}
	scope.Close()
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err = scope.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("scope acknowledged unfinished cleanup", err)
	}
	if supervisor.Active() != 1 {
		t.Fatal("physical permit retired before owner cleanup")
	}
	release()
	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = scope.Wait(deadline); err != nil {
		t.Fatal(err)
	}
	if supervisor.Active() != 0 || scope.Active() != 0 {
		t.Fatal("scope finished before permit retirement")
	}
	if err = <-runDone; err != nil {
		t.Fatal(err)
	}
}
func TestOperationScopeRegistrationNoStartDebt(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		scope := NewOperationScope()
		client := New("/portico-no-such-root-helper")
		client.SourceOperations = scope
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		if registration, err := client.RegisterRoot(ctx, t.TempDir(), "", "", context.Background()); err == nil || registration != nil {
			t.Fatal("invalid registration accepted")
		}
		cancel()
		scope.Close()
		deadline, stop := context.WithTimeout(context.Background(), time.Second)
		if err := scope.Wait(deadline); err != nil {
			t.Fatal("no-start left physical debt", err)
		}
		stop()
		if scope.Active() != 0 || client.Supervisor.Active() != 0 {
			t.Fatal("no-start accounting leaked")
		}
	}
}
func TestOperationScopeTracksReturnedRegistrationAndObservedChild(t *testing.T) {
	client, root := registrationFixture(t)
	scope := NewOperationScope()
	client.SourceOperations = scope
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	registration, err := client.RegisterRoot(context.Background(), root, "", "", life)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registration.Borrow(filepath.Base(filepath.Join(root, "tiny")))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := client.OpenObservedPlayback(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer observed.Close()
	defer registration.Close()
	// Scope shutdown alone owns cancellation; root registration is still live.
	scope.Close()
	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = scope.Wait(deadline); err != nil {
		t.Fatal(err)
	}
	if life.Err() != nil {
		t.Fatal("test canceled separate root owner")
	}
	if client.Supervisor.Active() != 0 {
		t.Fatal("scope ignored returned-registration or observed helper debt")
	}
}
