//go:build linux

package mediaexec

import (
	"os"
	"slices"
	"testing"
)

func TestHardwareSandboxKeepsDriverAccessOutOfSoftwareJobs(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, hardware := range []bool{false, true} {
		args, err := linuxArgv("/usr/bin/bwrap", Job{Executable: executable, Hardware: hardware}, false)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(args, "--unshare-net") || !slices.Contains(args, "--cap-drop") {
			t.Fatal("hardware driver access must preserve network and capability isolation")
		}
		for _, grant := range [][3]string{
			{"--ro-bind-try", "/etc/vulkan", "/etc/vulkan"},
			{"--ro-bind-try", "/usr/lib/wsl", "/usr/lib/wsl"},
			{"--dev-bind-try", "/dev/dri", "/dev/dri"},
			{"--dev-bind-try", "/dev/dxg", "/dev/dxg"},
			{"--dev-bind-try", "/dev/mpp_service", "/dev/mpp_service"},
			{"--dev-bind-try", "/dev/rga", "/dev/rga"},
			{"--dev-bind-try", "/dev/dma_heap", "/dev/dma_heap"},
		} {
			present := false
			for i := 0; i+2 < len(args); i++ {
				present = present || slices.Equal(args[i:i+3], grant[:])
			}
			if present != hardware {
				t.Fatalf("hardware=%v, grant=%v, present=%v", hardware, grant, present)
			}
		}
	}
}
