package hostlimits

import (
	"math"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// The descriptor limit is the one resource whose exhaustion does not crash: accept
// returns EMFILE, net/http backs off a second, and the server degrades into
// "everything is mysteriously broken". Raising it must actually work on the
// platform it runs on, and must never make it worse.
func TestApplyRaisesTheDescriptorLimitWithoutLoweringIt(t *testing.T) {
	before, hard := OpenFiles()
	if runtime.GOOS == "windows" {
		if before != 0 || hard != 0 {
			t.Fatalf("Windows reported a descriptor rlimit: %d/%d", before, hard)
		}
		return
	}
	if before == 0 {
		t.Skip("this platform reports no descriptor limit")
	}
	previousLimit := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previousLimit) })
	Apply()
	after, _ := OpenFiles()
	if after < before {
		t.Fatalf("the soft descriptor limit fell from %d to %d", before, after)
	}
	t.Logf("descriptor limit %d -> %d (hard %d)", before, after, hard)
	// Applying twice is not a mistake an owner should be able to make matter.
	Apply()
	again, _ := OpenFiles()
	if again != after {
		t.Fatalf("a second Apply changed the limit from %d to %d", after, again)
	}
}

func TestAutomaticMemoryLimit(t *testing.T) {
	for _, tc := range []struct {
		name         string
		memory, want int64
	}{
		{"unknown", 0, 0},
		{"invalid", -1, 0},
		{"too small", 191 << 20, 0},
		{"minimum", 192 << 20, 64 << 20},
		{"256 MiB", 256 << 20, 128 << 20},
		{"512 MiB", 512 << 20, 384 << 20},
		{"1 GiB", 1 << 30, 768 << 20},
		{"maximum integer", math.MaxInt64, math.MaxInt64 - math.MaxInt64/4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := automaticMemoryLimit(tc.memory); got != tc.want {
				t.Fatalf("automatic limit=%d want %d", got, tc.want)
			}
		})
	}
}

// GOMEMLIMIT is read by Go before main or tests run. Separate processes prove
// that Apply preserves the runtime's real startup parsing, including "off".
func TestApplyMemoryLimitStartup(t *testing.T) {
	for _, tc := range []struct{ name, override string }{
		{"default", ""},
		{"numeric", "96MiB"},
		{"high explicit", "8TiB"},
		{"off", "off"},
		{"programmatic", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestApplyMemoryLimitStartupHelper$")
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "GOMEMLIMIT=") && !strings.HasPrefix(env, "PORTICO_LIMIT_TEST=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "PORTICO_LIMIT_TEST="+tc.name)
			if tc.override != "" {
				cmd.Env = append(cmd.Env, "GOMEMLIMIT="+tc.override)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("startup test: %v\n%s", err, out)
			}
		})
	}
}

func TestApplyMemoryLimitStartupHelper(t *testing.T) {
	mode := os.Getenv("PORTICO_LIMIT_TEST")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	before := debug.SetMemoryLimit(-1)
	want := before
	switch mode {
	case "default":
		if limit := automaticMemoryLimit(EffectiveMemoryBytes()); limit > 0 {
			want = min(before, limit)
		}
	case "numeric":
		if before != 96<<20 {
			t.Fatalf("runtime parsed numeric limit=%d", before)
		}
	case "high explicit":
		if before != 8<<40 {
			t.Fatalf("runtime parsed explicit limit=%d", before)
		}
	case "off":
		if before != math.MaxInt64 {
			t.Fatalf("runtime parsed off=%d", before)
		}
	case "programmatic":
		debug.SetMemoryLimit(32 << 20)
		want = 32 << 20
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	Apply()
	if got := debug.SetMemoryLimit(-1); got != want {
		t.Fatalf("Apply limit=%d want %d", got, want)
	}
	Apply()
	if got := debug.SetMemoryLimit(-1); got != want {
		t.Fatalf("second Apply limit=%d want %d", got, want)
	}
}
