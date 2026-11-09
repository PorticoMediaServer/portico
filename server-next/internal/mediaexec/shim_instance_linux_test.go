//go:build linux

package mediaexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"syscall"
	"testing"
)

func TestLinuxShimRetainsParentDeathPolicy(t *testing.T) {
	resetForTest(t)
	configureForTest(t, t.TempDir())
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := shimArgv(Job{}, []string{self})
	raw, err := base64.RawURLEncoding.DecodeString(argv[2])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["g"]; ok {
		t.Fatal("Linux shim gained a macOS instance guard")
	}
	cmd, err := CommandArgv(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Linux Pdeathsig changed: %+v", cmd.SysProcAttr)
	}
}
