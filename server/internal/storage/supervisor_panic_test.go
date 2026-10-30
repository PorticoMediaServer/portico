package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSupervisorPanicChild(t *testing.T) {
	if os.Args[len(os.Args)-1] != "supervisor-panic-child" {
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, "helper ready"); err != nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestConsumerPanicReapsHelperBeforeReleasingAdmission(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestSupervisorPanicChild$", "--", "supervisor-panic-child")
	defer func() {
		// A regression must fail the test without leaving its helper running.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	s := &Supervisor{Limit: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = s.Run(ctx, "panic-consumer", cmd, func(r io.Reader) error {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) != "helper ready" {
			return fmt.Errorf("unexpected helper output: %q", line)
		}
		panic("injected consumer panic")
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "consumer ended before process wait") {
		t.Fatalf("panic was not returned as a helper failure: %v", err)
	}
	if cmd.ProcessState == nil || cmd.ProcessState.Success() {
		t.Fatalf("helper was not killed and reaped: %v", cmd.ProcessState)
	}
	if active := s.Active(); active != 0 {
		t.Fatalf("reaped helper retained %d admission slots", active)
	}
}
