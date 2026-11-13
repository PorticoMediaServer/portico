package decoder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetestKeepsWorkingHardwareUntilAConclusiveResult(t *testing.T) {
	backend := supportedTestBackend(t)
	device, path := "occupied-card", fakeFFmpeg(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var mode atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var messages bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&messages)
	defer log.SetOutput(previous)
	d := &HardwareDetector{Run: func(ctx context.Context, cmd *exec.Cmd) error {
		switch mode.Load() {
		case 1:
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			fmt.Fprintln(cmd.Stderr, "Device busy: all encoder sessions are in use")
			return errors.New("busy")
		case 2:
			if strings.Contains(strings.Join(cmd.Args, " "), "hevc_"+string(backend)) {
				fmt.Fprintln(cmd.Stderr, "Resource temporarily unavailable")
				return errors.New("busy during HEVC trial")
			}
		case 3:
			fmt.Fprintln(cmd.Stderr, "The graphics device was removed")
			return errors.New("device removed")
		}
		return nil
	}}
	working := d.Probe(ctx, path, backend, device)
	if !working.Available {
		t.Fatal(working)
	}
	d.CachedRuntimeIdentity = func(context.Context, string) (string, bool) { return working.Identity, true }
	mode.Store(1)
	finished := make(chan HardwareProbe, 1)
	go func() { finished <- d.Reprobe(ctx, path, backend, device) }()
	<-entered
	if known := d.KnownDevice(ctx, path, device); len(known) != 1 || !known[0].Available {
		t.Fatalf("the in-progress test took working hardware away from a new play: %+v", known)
	}
	close(release)
	if result := <-finished; !result.Available || result.Reason != ProbeBusy {
		t.Fatalf("a busy test should keep the last good result: %+v", result)
	}
	mode.Store(2)
	if result := d.Reprobe(ctx, path, backend, device); !result.Available || result.Reason != ProbeBusy {
		t.Fatalf("a busy codec trial should keep the last good result: %+v", result)
	}
	if known := d.KnownDevice(ctx, path, device); len(known) != 1 || !reflect.DeepEqual(known[0], working) {
		t.Fatalf("a busy test overwrote working formats: %+v", known)
	}
	if strings.Count(messages.String(), "Conversions keep using the last working result") != 2 ||
		!strings.Contains(messages.String(), device) || !strings.Contains(messages.String(), "Resource temporarily unavailable") {
		t.Fatal("the busy tests did not explain their device, reason and unchanged conversion route", messages.String())
	}
	mode.Store(3)
	if result := d.Reprobe(ctx, path, backend, device); result.Available {
		t.Fatal("a conclusive failure kept the broken device", result)
	}
	if !strings.Contains(messages.String(), "The graphics device was removed") ||
		!strings.Contains(messages.String(), "New conversions fall back to a working graphics device or the processor") {
		t.Fatal("the failure did not explain why conversions fall back", messages.String())
	}
}
