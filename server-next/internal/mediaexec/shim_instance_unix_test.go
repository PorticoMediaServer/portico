//go:build linux || darwin

package mediaexec

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
)

func TestShimInstancePublicationReplacesWholeFile(t *testing.T) {
	parent := t.TempDir()
	ledger := filepath.Join(parent, "media-processes")
	if err := os.Mkdir(ledger, 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishMediaInstance(ledger, "previous"); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Open(ledger + ".instance")
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err = publishMediaInstance(ledger, "replacement"); err != nil {
		t.Fatal(err)
	}
	// A reader already holding the old file keeps a whole old instance;
	// opening the name after rename gets a whole new one. No in-place rewrite.
	old, err := io.ReadAll(previous)
	if err != nil || string(old) != "previous" {
		t.Fatalf("previous open file changed: %q %v", old, err)
	}
	current, err := os.ReadFile(ledger + ".instance")
	if err != nil || string(current) != "replacement" {
		t.Fatalf("replacement not published: %q %v", current, err)
	}
	info, err := os.Stat(ledger + ".instance")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("guard permissions: %v", info.Mode())
	}
	entries, err := os.ReadDir(ledger)
	if err != nil || len(entries) != 0 {
		t.Fatalf("guard entered PID namespace: %v %v", entries, err)
	}
	entries, err = os.ReadDir(parent)
	if err != nil || len(entries) != 2 {
		t.Fatalf("publication left temporary files: %v %v", entries, err)
	}
}

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
			t.Fatal("macOS shim has no instance guard")
		}
		value, err := os.ReadFile(fencePath)
		if err != nil || string(value) != fields["i"] {
			t.Fatalf("instance was not published: %q %v", value, err)
		}
	} else {
		if _, ok := fields["g"]; ok {
			t.Fatal("Linux shim gained a macOS guard")
		}
		if _, err := os.Stat(fencePath); !os.IsNotExist(err) {
			t.Fatalf("Linux published a macOS guard: %v", err)
		}
	}
	// A process without the single-instance lock cannot supersede a live server's
	// guard or cause its subsequent shims to refuse. Test on both Unix platforms.
	active := []byte("another-live-server")
	if err = os.WriteFile(fencePath, active, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Configure(Options{StateDir: state, SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := shimInstanceFields(t, self)["g"]; ok {
		t.Fatal("unlocked process issued a guarded shim")
	}
	value, err := os.ReadFile(fencePath)
	if err != nil || string(value) != string(active) {
		t.Fatalf("unlocked process replaced live guard: %q %v", value, err)
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
			t.Fatalf("accepted incomplete guard: %s", raw)
		}
	}
}
