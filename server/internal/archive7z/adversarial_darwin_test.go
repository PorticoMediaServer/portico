package archive7z

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"testing"
)

func TestDiagnosticNativeASRefusesOverBudget(t *testing.T) {
	if os.Getenv("ARCHIVE7Z_AS_CHILD") == "1" {
		if err := limitProcess(); err != nil {
			t.Fatal(err)
		}
		small, err := unix.Mmap(-1, 0, 64<<20, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANON)
		if err != nil {
			t.Fatalf("within budget: %v", err)
		}
		if err = unix.Munmap(small); err != nil {
			t.Fatal(err)
		}
		large, err := unix.Mmap(-1, 0, 2<<30, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANON)
		if err == nil {
			unix.Munmap(large)
			t.Fatal("over-budget mapping succeeded")
		}
		if !errors.Is(err, unix.ENOMEM) {
			t.Fatalf("wrong refusal: %v", err)
		}
		t.Log("over-budget mapping refused with ENOMEM")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDiagnosticNativeASRefusesOverBudget$", "-test.v")
	cmd.Env = append(os.Environ(), "ARCHIVE7Z_AS_CHILD=1")
	out, err := cmd.CombinedOutput()
	t.Logf("child: %s", out)
	if err != nil {
		t.Fatal(err)
	}
}
