package decoder

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// Controlled results establish admission/lifecycle behavior, not host GPU proof.
// The production runner still executes the same bounded mediaexec jobs.
func startupInventory(t *testing.T, runtime func(context.Context, mediaexec.Job) (string, error)) toolchainProbeRunner {
	t.Helper()
	return func(ctx context.Context, job mediaexec.Job) (string, error) {
		t.Helper()
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("unbounded toolchain command")
		}
		if job.Hardware || job.Loopback != "" || len(job.ReadPaths) != 0 || len(job.WriteDirs) != 0 {
			t.Fatal("toolchain probe gained source, network, write or hardware permissions")
		}
		if slices.Contains(job.Args, "-init_hw_device") {
			return runtime(ctx, job)
		}
		if len(job.Args) != 2 || job.Args[0] != "-hide_banner" {
			t.Fatalf("unexpected inventory command: %v", job.Args)
		}
		switch job.Args[1] {
		case "-version":
			return "ffmpeg version controlled-startup-fixture\nconfiguration: --enable-gpl --enable-version3\n", nil
		case "-encoders":
			return " V..... libx264\n A..... aac\n", nil
		case "-decoders":
			return " V..... h264\n", nil
		case "-filters":
			return " ... libplacebo\n ... tonemap_opencl\n", nil
		case "-muxers":
			return " E mp4\n", nil
		case "-hwaccels":
			return "vulkan\nopencl\n", nil
		case "-bsfs":
			return "dovi_rpu\n", nil
		default:
			t.Fatalf("unexpected inventory: %v", job.Args)
			return "", nil
		}
	}
}

func TestToolchainStartupInventoryDoesNotRunGPU(t *testing.T) {
	path := fakeFFmpeg(t)
	runner := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) {
		t.Fatal("GPU media probe blocked inventory qualification")
		return "", nil
	})
	facts, err := probeToolchainInventory(context.Background(), path, runner)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Identity == "" || !facts.Encoders["aac"] || !facts.Filters["libplacebo"] || facts.VulkanAvailable || facts.OpenCLAvailable {
		t.Fatalf("unqualified inventory facts: %+v", facts)
	}
	if CurrentToolchain() != nil && CurrentToolchain().Identity == facts.Identity {
		t.Fatal("probe installed facts before caller's admission decision")
	}
}

func TestToolchainRuntimeQualification(t *testing.T) {
	for _, tc := range []struct {
		name               string
		vulkanOK, openclOK bool
		calls              int
	}{{"vulkan", true, false, 1}, {"opencl fallback", false, true, 2}, {"CPU fallback", false, false, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			path := fakeFFmpeg(t)
			calls := 0
			runner := startupInventory(t, func(_ context.Context, job mediaexec.Job) (string, error) {
				calls++
				ok := tc.openclOK
				if slices.Contains(job.Args, "vulkan=tone") {
					ok = tc.vulkanOK
				}
				if !ok {
					return "", errors.New("controlled unavailable device")
				}
				return "", nil
			})
			inventory, err := probeToolchainInventory(context.Background(), path, runner)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := probeToolchainRuntime(context.Background(), path, runner)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.calls || facts.Identity != inventory.Identity || facts.VulkanAvailable != tc.vulkanOK || facts.OpenCLAvailable != tc.openclOK {
				t.Fatalf("calls=%d facts=%+v", calls, facts)
			}
			cold, err := probeToolchainInventory(context.Background(), path, runner)
			if err != nil || cold.VulkanAvailable || cold.OpenCLAvailable || calls != tc.calls {
				t.Fatal("optional runtime qualification contaminated trusted inventory", cold, err, calls)
			}
			// Same FFmpeg identity, but the external ICD/device is now unavailable.
			// Its previous positive result must not survive an explicit reprobe.
			refusedCalls := 0
			refused := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) {
				refusedCalls++
				return "", errors.New("controlled removed driver or device")
			})
			reprobed, err := probeToolchainRuntime(context.Background(), path, refused)
			if err != nil || reprobed.Identity != facts.Identity || reprobed.VulkanAvailable || reprobed.OpenCLAvailable || refusedCalls != 2 {
				t.Fatal("reused stale driver qualification", reprobed, err, refusedCalls)
			}
		})
	}
}

func TestToolchainRuntimeRejectsChangedExecutable(t *testing.T) {
	path := fakeFFmpeg(t)
	runner := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) {
		if err := os.WriteFile(path, []byte("changed executable during runtime probe"), 0700); err != nil {
			t.Fatal(err)
		}
		return "", nil
	})
	inventory, err := probeToolchainInventory(context.Background(), path, runner)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := probeToolchainRuntime(context.Background(), path, runner)
	if err == nil || facts.Identity != "" {
		t.Fatal("published runtime facts for replaced executable", facts, err)
	}
	if cached, ok := toolchainCache.Load(inventory.Identity); ok && (cached.(ToolchainFacts).VulkanAvailable || cached.(ToolchainFacts).OpenCLAvailable) {
		t.Fatal("cached changed-executable runtime grants")
	}
}

func TestToolchainRuntimeCancellationDoesNotPublish(t *testing.T) {
	path := fakeFFmpeg(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) { cancel(); return "", nil })
	inventory, err := probeToolchainInventory(ctx, path, runner)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := probeToolchainRuntime(ctx, path, runner)
	if !errors.Is(err, context.Canceled) || facts.Identity != "" {
		t.Fatal("published cancelled qualification", facts, err)
	}
	if cached, ok := toolchainCache.Load(inventory.Identity); ok && (cached.(ToolchainFacts).VulkanAvailable || cached.(ToolchainFacts).OpenCLAvailable) {
		t.Fatal("cached cancelled qualification grants")
	}
}

func TestToolchainInventoryRejectsChangedExecutable(t *testing.T) {
	path := fakeFFmpeg(t)
	runner := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) {
		t.Fatal("unexpected GPU command")
		return "", nil
	})
	facts, err := probeToolchainInventory(context.Background(), path, func(ctx context.Context, job mediaexec.Job) (string, error) {
		raw, err := runner(ctx, job)
		if slices.Contains(job.Args, "-bsfs") {
			if e := os.WriteFile(path, []byte("changed during introspection"), 0700); e != nil {
				t.Fatal(e)
			}
		}
		return raw, err
	})
	if err == nil || facts.Identity != "" {
		t.Fatal("published changed-executable inventory", facts, err)
	}
}

func TestToolchainInventoryCancellationAfterLastCommand(t *testing.T) {
	path := fakeFFmpeg(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := startupInventory(t, func(context.Context, mediaexec.Job) (string, error) { t.Fatal("unexpected GPU probe"); return "", nil })
	facts, err := probeToolchainInventory(ctx, path, func(ctx context.Context, job mediaexec.Job) (string, error) {
		raw, err := runner(ctx, job)
		if slices.Contains(job.Args, "-bsfs") {
			cancel()
		}
		return raw, err
	})
	if !errors.Is(err, context.Canceled) || facts.Identity != "" {
		t.Fatal("cancelled final inventory was admitted", facts, err)
	}
}
