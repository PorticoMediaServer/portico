//go:build linux

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

func TestGraphicsDeviceAlertLastsUntilAWorkingRetest(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	messages, err := serverlog.Open(filepath.Join(t.TempDir(), "logs"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	previous := slog.Default()
	slog.SetDefault(slog.New(messages.Handler()))
	defer slog.SetDefault(previous)
	s.UseLog(messages)
	native, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, binary, 0700); err != nil {
		t.Fatal(err)
	}
	mode, calls := "failed", 0
	detector := &decoder.HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		calls++
		switch mode {
		case "failed":
			return errors.New("driver unavailable")
		case "busy":
			fmt.Fprintln(cmd.Stderr, "Device busy")
			return errors.New("busy")
		}
		return nil
	}}
	device := "alert-" + filepath.Base(t.TempDir())
	backend := decoder.BackendNVENC
	retest := func() { detector.Reprobe(t.Context(), path, backend, device) }
	defer func() { mode = "working"; retest() }()
	read := func(want bool) {
		t.Helper()
		before, found := calls, 0
		for _, alert := range get[Dashboard](t, s, owner, "/v1/admin/dashboard").Alerts {
			if alert.Code == "graphics_device_stopped" {
				found++
				if alert.Action.Kind != "streaming" || alert.Major || alert.Severity != "warning" || alert.Params["message"] == "" {
					t.Fatalf("alert: %+v", alert)
				}
			}
		}
		if (found == 1) != want || found > 1 || calls != before {
			t.Fatalf("alert count %d, want %v; probe calls %d -> %d", found, want, before, calls)
		}
	}
	retest()
	read(false) // Never had usable hardware.
	mode = "working"
	retest()
	read(false)
	mode = "busy"
	retest()
	read(false) // Capacity is not a stopped device.
	mode = "failed"
	retest()
	read(true)
	retest()
	read(true)
	entries, _ := messages.Since(0)
	notices := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Message, "Graphics device stopped working;") {
			notices++
			if entry.Level != "warn" || entry.Kind != "playback" || entry.Attrs["device"] != device {
				t.Fatalf("Messages: %+v", entry)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("stopped-device Messages: %d", notices)
	}
	mode = "working"
	retest()
	read(false)
	// The conversion's confirmed hardware failure uses the same alert and ordinary fallback circuit.
	decoder.RecordHardwareDeviceFailure(backend, device)
	read(false)
	decoder.RecordHardwareDeviceFailure(backend, device)
	read(false)
	decoder.RecordHardwareDeviceFailure(backend, device)
	read(true)
	mode = "working"
	retest()
	read(false)
}
