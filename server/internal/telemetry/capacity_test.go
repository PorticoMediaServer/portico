package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProbes struct {
	supported map[string]bool
	calls     int
	device    string
}

func (f *fakeProbes) Probe(_ context.Context, device string) []HardwareProbe {
	f.calls++
	f.device = device
	out := []HardwareProbe{{Backend: "software", Supported: true, Detail: "Software encoding is always available."}}
	for _, backend := range HardwareBackends {
		out = append(out, HardwareProbe{Backend: backend, Supported: f.supported[backend], Detail: "probed"})
	}
	return out
}

func settingsFor(t *testing.T) TranscodeSettings {
	return TranscodeSettings{Enabled: true, HardwareBackend: "auto", HDRToneMappingAlgorithm: "hable", X264Preset: "veryfast",
		DirectStreamRemux: true, PlanningPolicy: "maximum_fidelity", ThrottleBufferMs: 60000, PlayedRetentionMs: 180000,
		TemporaryDirectory: t.TempDir(), MaxConcurrentSessions: 3}
}

func workingReporter(ctx context.Context, name string, args ...string) (string, error) {
	return name + " version 7.1\nbuilt with clang", nil
}
func missingReporter(context.Context, string, ...string) (string, error) {
	return "", errors.New("not installed")
}

func TestCapacityProbesTheExactEffectiveProducerDirectory(t *testing.T) {
	root := t.TempDir()
	c := &Capacity{Directory: &root, State: root, Run: workingReporter}
	s := settingsFor(t)
	report := c.Report(context.Background(), s, SessionCounts{})
	if report.TemporaryDirectory.Path != root || !report.TemporaryDirectory.Ready {
		t.Fatal(report.TemporaryDirectory)
	}
	for _, unwanted := range []string{filepath.Join(root, "hls"), filepath.Join(s.TemporaryDirectory, "portico-hls")} {
		if _, err := os.Stat(unwanted); !os.IsNotExist(err) {
			t.Fatal("probed an unused directory", unwanted, err)
		}
	}
}

func TestCapacityResolvesAutoToTheFirstSupportedBackend(t *testing.T) {
	probes := &fakeProbes{supported: map[string]bool{"vaapi": true, "nvenc": true}}
	c := &Capacity{Probes: probes, Run: workingReporter}
	s := settingsFor(t)
	s.HardwareDevice = "/dev/dri/renderD128"
	report := c.Report(context.Background(), s, SessionCounts{})
	if report.Hardware.Effective != "vaapi" {
		t.Fatal("auto must resolve to the first probed backend in published order", report.Hardware)
	}
	if probes.device != "/dev/dri/renderD128" {
		t.Fatal("the configured device must reach the probe", probes.device)
	}
	if len(report.Hardware.Probes) != len(HardwareBackends)+1 {
		t.Fatal("every backend must be reported, supported or not", report.Hardware.Probes)
	}
	if report.Sessions.Limits.Concurrent != 3 {
		t.Fatal("limits come from the settings registry", report.Sessions.Limits)
	}
	if report.Sessions.Active != nil {
		t.Fatal("an unpublished session count must stay null, never zero")
	}
	if len(report.Presets) != 9 || report.Presets[0].ID != "original" || report.Presets[1].ID != "automatic" || report.Presets[2].Height != 2160 || report.Presets[2].VideoKbps != 40000 {
		t.Fatal("the ladder must be published by the server", report.Presets)
	}
	if !report.Dependencies["ffmpeg"].OK || !strings.Contains(report.Dependencies["ffmpeg"].Version, "version 7.1") {
		t.Fatal("dependency versions must be reported", report.Dependencies)
	}
	if len(report.Warnings) != 0 {
		t.Fatal("a healthy server warns about nothing", report.Warnings)
	}
}

func TestCapacityFallsBackToSoftwareAndWarns(t *testing.T) {
	c := &Capacity{Probes: &fakeProbes{}, Run: workingReporter}
	s := settingsFor(t)
	s.HardwareBackend = "qsv"
	report := c.Report(context.Background(), s, SessionCounts{})
	if report.Hardware.Configured != "qsv" || report.Hardware.Effective != "software" {
		t.Fatal("configured and effective must both be visible", report.Hardware)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "did not probe successfully") {
		t.Fatal("the fallback must be explained", report.Warnings)
	}
}

func TestCapacityWithTheDefaultProbeSourceClaimsSoftwareOnly(t *testing.T) {
	c := &Capacity{Run: workingReporter}
	report := c.Report(context.Background(), settingsFor(t), SessionCounts{})
	if report.Hardware.Effective != "software" {
		t.Fatal("no hardware may be claimed before the delivery service probes it", report.Hardware)
	}
	for _, probe := range report.Hardware.Probes {
		if probe.Backend != "software" && probe.Supported {
			t.Fatal("an unprobed backend must not report as supported", probe)
		}
	}
}

func TestCapacityReportsMissingDependenciesAndAnUnwritableDirectory(t *testing.T) {
	c := &Capacity{Probes: &fakeProbes{}, Run: missingReporter}
	s := settingsFor(t)
	s.Enabled = false
	// A regular file where the directory should be cannot be turned into one,
	// which is deterministic on every platform and for every user.
	blocked := filepath.Join(t.TempDir(), "occupied")
	if e := os.WriteFile(blocked, []byte("not a directory"), 0o600); e != nil {
		t.Fatal(e)
	}
	s.TemporaryDirectory = filepath.Join(blocked, "conversions")
	report := c.Report(context.Background(), s, SessionCounts{})
	if report.TemporaryDirectory.Ready {
		t.Fatal("a directory the server cannot create must not report ready")
	}
	joined := strings.Join(report.Warnings, " | ")
	for _, expected := range []string{"Transcoding is turned off", "ffmpeg executable", "ffprobe executable", "working directory is not writable"} {
		if !strings.Contains(joined, expected) {
			t.Fatal("missing warning", expected, joined)
		}
	}
}

func TestToneMappingStatusFollowsTheSettingAndTheBackend(t *testing.T) {
	c := &Capacity{Probes: &fakeProbes{supported: map[string]bool{"nvenc": true}}, Run: workingReporter}
	s := settingsFor(t)
	if status := c.Report(context.Background(), s, SessionCounts{}).HDRToneMapping; status.Status != "disabled" {
		t.Fatal("tone mapping off must read as disabled", status)
	}
	s.HDRToneMapping = true
	s.HardwareBackend = "nvenc"
	if status := c.Report(context.Background(), s, SessionCounts{}).HDRToneMapping; status.Status != "available" || !strings.Contains(status.Detail, "nvenc") {
		t.Fatal("tone mapping on a probed backend is available", status)
	}
	s.HardwareBackend = "software"
	if status := c.Report(context.Background(), s, SessionCounts{}).HDRToneMapping; status.Status != "limited" {
		t.Fatal("software tone mapping is limited, and the cost must be named", status)
	}
	missing := &Capacity{Probes: &fakeProbes{}, Run: missingReporter}
	if status := missing.Report(context.Background(), s, SessionCounts{}).HDRToneMapping; status.Status != "unavailable" {
		t.Fatal("tone mapping without ffmpeg is unavailable", status)
	}
}

func TestCapacityCachesReportersButNotSettings(t *testing.T) {
	probes := &fakeProbes{}
	clock := time.Now()
	c := &Capacity{Probes: probes, Run: workingReporter, Now: func() time.Time { return clock }}
	s := settingsFor(t)
	c.Report(context.Background(), s, SessionCounts{})
	s.X264Preset = "slow"
	report := c.Report(context.Background(), s, SessionCounts{})
	if probes.calls != 1 {
		t.Fatal("reporters must not run again within the cache period", probes.calls)
	}
	if report.X264Preset != "slow" {
		t.Fatal("a settings change must be visible on the next read", report.X264Preset)
	}
	clock = clock.Add(2 * time.Minute)
	c.Report(context.Background(), s, SessionCounts{})
	if probes.calls != 2 {
		t.Fatal("reporters must run again once the cache expires", probes.calls)
	}
}

func TestCapacityMemoryCapabilityIsNotAWarning(t *testing.T) {
	disk := t.TempDir()
	c := &Capacity{Directory: &disk, Run: workingReporter}
	report := c.Report(context.Background(), settingsFor(t), SessionCounts{})
	if report.TemporaryDirectory.Path != disk || report.TemporaryDirectory.Memory != memoryTranscodeDirectory() {
		t.Fatal(report.TemporaryDirectory)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "Conversions use memory") {
			t.Fatal("working memory configuration reported as a warning", report.Warnings)
		}
	}
}

func TestCapacityPublishesMemoryAdmissionForTheCurrentCap(t *testing.T) {
	c := &Capacity{Run: workingReporter}
	s := settingsFor(t)
	s.MemoryCap = "quarter"
	if c.Report(context.Background(), s, SessionCounts{}).TemporaryDirectory.MemoryAvailable {
		t.Fatal("missing admission callback must mean unavailable")
	}
	for _, available := range []bool{false, true, false} {
		calls := 0
		c.MemoryAvailable = func(path, cap string) bool {
			calls++
			if path != memoryTranscodeDirectory() || cap != s.MemoryCap {
				t.Fatalf("wrong admission inputs: %q %q", path, cap)
			}
			return available
		}
		report := c.Report(context.Background(), s, SessionCounts{})
		if calls != 1 || report.TemporaryDirectory.MemoryAvailable != available || report.TemporaryDirectory.Memory != memoryTranscodeDirectory() {
			t.Fatalf("admission must be fresh and preserve the candidate path: %+v (calls %d)", report.TemporaryDirectory, calls)
		}
		s.MemoryCap = "half"
	}
}
