package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

func TestEarlyStartupRecoveryStopsWhenItsParentDisappears(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.Command(executable, "-test.run=^TestEarlyRecoveryWorkerFixture$")
	cmd.Env = append(os.Environ(), supervise.WorkerEnvironment+"=1", "PORTICO_EARLY_RECOVERY_FIXTURE=1", "PORTICO_BIND=127.0.0.1:0")
	cmd.Stdin = reader
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	writer.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("early recovery worker outlived its parent's pipe")
	}
}

func TestEarlyRecoveryWorkerFixture(t *testing.T) {
	if os.Getenv("PORTICO_EARLY_RECOVERY_FIXTURE") != "1" {
		return
	}
	serveEarlyStartupFailure(errors.New("state directory is unavailable"))
}
