//go:build linux

package decoder

import (
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestHardwareBridgeMountsResolvedLoaderFile(t *testing.T) {
	executable, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	loader := "/lib64/ld-linux-x86-64.so.2"
	real, err := filepath.EvalSymlinks(loader)
	if err != nil || real == loader {
		t.Skip("host does not have the usr-merged x86-64 loader alias")
	}
	args, err := sandboxArgs(linuxSpec{Executable: executable, Hardware: true}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--ro-bind" {
			if args[i+2] == loader {
				t.Fatal("bridge attempts to mount over the loader alias")
			}
			found = found || args[i+1] == real && args[i+2] == real
		}
	}
	if !found || !slices.Contains(args, "--unshare-net") || !slices.Contains(args, "--remount-ro") {
		t.Fatal("bridge lost the resolved loader or its confinement")
	}
}
