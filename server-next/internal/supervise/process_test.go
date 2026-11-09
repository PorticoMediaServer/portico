package supervise

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartParentRelaunchesWorker(t *testing.T) {
	file := filepath.Join(t.TempDir(), "starts")
	t.Setenv("PORTICO_PROCESS_TEST_FILE", file)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = Process(ctx, executable, []string{"-test.run=^TestRestartWorkerFixture$"}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(file)
	if err != nil || string(bytes) != "started\nstarted\n" {
		t.Fatal(string(bytes), err)
	}
}

func TestRestartWorkerFixture(t *testing.T) {
	if os.Getenv(WorkerEnvironment) != "1" {
		return
	}
	path := os.Getenv("PORTICO_PROCESS_TEST_FILE")
	_, exists := os.Stat(path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(1)
	}
	_, _ = file.WriteString("started\n")
	file.Close()
	if os.IsNotExist(exists) {
		os.Exit(RestartExitCode)
	}
	os.Exit(0)
}

func TestParentStopDrainsWorker(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stopped")
	t.Setenv("PORTICO_PROCESS_STOP_FILE", file)
	executable, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if err := Process(ctx, executable, []string{"-test.run=^TestStopWorkerFixture$"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("worker did not drain", err)
	}
}

func TestStopWorkerFixture(t *testing.T) {
	if os.Getenv(WorkerEnvironment) != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := os.WriteFile(os.Getenv("PORTICO_PROCESS_STOP_FILE"), []byte("drained"), 0600); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
