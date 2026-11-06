//go:build linux || darwin

package mediaexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestShimInstanceFencePolicy(t *testing.T) {
	resetForTest(t)
	state := t.TempDir()
	configureForTest(t, state)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fields := shimInstanceFields(t, self)
	fencePath := storagepaths.Path(state, "run", "media-processes") + ".instance"
	if runtime.GOOS == "darwin" {
		if fields["g"] != true {
			t.Fatal("macOS shim has no incarnation fence")
		}
		value, err := os.ReadFile(fencePath)
		if err != nil || string(value) != fields["i"] {
			t.Fatalf("incarnation was not published: %q %v", value, err)
		}
	} else {
		if _, ok := fields["g"]; ok {
			t.Fatal("Linux shim gained a macOS fence")
		}
		if _, err := os.Stat(fencePath); !os.IsNotExist(err) {
			t.Fatalf("Linux published a macOS fence: %v", err)
		}
	}
	// A process without the single-instance lock cannot supersede a live server's
	// fence or cause its subsequent shims to refuse. Test on both Unix platforms.
	active := []byte("another-live-server")
	if err = os.WriteFile(fencePath, active, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Configure(Options{StateDir: state, SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := shimInstanceFields(t, self)["g"]; ok {
		t.Fatal("unlocked process issued a fenced shim")
	}
	value, err := os.ReadFile(fencePath)
	if err != nil || string(value) != string(active) {
		t.Fatalf("unlocked process replaced live fence: %q %v", value, err)
	}
}

func shimInstanceFields(t *testing.T, self string) map[string]any {
	t.Helper()
	argv := shimArgv(Job{}, []string{self})
	raw, err := base64.RawURLEncoding.DecodeString(argv[2])
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestShimInstanceFenceRequiresIdentityAndLedger(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"g":true}`, `{"g":true,"i":"current"}`, `{"g":true,"l":"/owned/ledger"}`} {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, err := parseShim([]string{ShimFlag, encoded, "--", self}); err == nil {
			t.Fatalf("accepted incomplete fence: %s", raw)
		}
	}
}

func TestShimLaunchParentBinding(t *testing.T) {
	resetForTest(t)
	configureForTest(t, t.TempDir())
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := shimArgv(Job{}, []string{self})
	original := shimInstanceFields(t, self)
	if runtime.GOOS == "darwin" {
		if original["p"] != float64(os.Getpid()) {
			t.Fatalf("parent was not captured by argv issuer: %v", original)
		}
		for _, invalid := range []float64{0, 1, -1} {
			original["p"] = invalid
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := parseShim([]string{ShimFlag, base64.RawURLEncoding.EncodeToString(raw), "--", self}); err == nil {
				t.Fatalf("macOS accepted missing/invalid parent: %v", invalid)
			}
		}
	} else if _, ok := original["p"]; ok {
		t.Fatal("Linux argv gained a parent guard")
	}
	// A new launch inside a helper changes the immediate parent, but must keep
	// the original server incarnation and must not mutate the supplied argv.
	original["p"] = float64(42)
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	argv[2] = base64.RawURLEncoding.EncodeToString(raw)
	before := append([]string(nil), argv...)
	cmd, err := CommandArgv(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	launched, _, err := parseShim(cmd.Args[1:])
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(launched)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	want := float64(42)
	if runtime.GOOS == "darwin" {
		want = float64(os.Getpid())
	}
	if fields["p"] != want || fields["i"] != original["i"] || fields["g"] != original["g"] {
		t.Fatalf("launch binding drift: %v -> %v", original, fields)
	}
	if !reflect.DeepEqual(argv, before) {
		t.Fatal("launch mutated caller argv")
	}
}
