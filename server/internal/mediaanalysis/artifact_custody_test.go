//go:build !windows

package mediaanalysis

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestAnalysisArtifactCustodySharesReadersUntilPhysicalRelease(t *testing.T) {
	root := filepath.Join(canonicalFixtureDir(t), "locks")
	first, err := livechannels.NewPhysicalLocks(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := livechannels.NewPhysicalLocks(root)
	if err != nil {
		t.Fatal(err)
	}
	a, b := &artifactCustody{locks: first}, &artifactCustody{locks: second}
	digest := token("fixture")
	release1, err := a.read(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	release2, err := a.read(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	assertBusy := func() {
		t.Helper()
		gate, err := b.exclusive(digest)
		if gate != nil {
			gate.Close()
		}
		if !errors.Is(err, livechannels.ErrPhysicalBusy) {
			t.Fatal("live reader admitted deletion/publication", err)
		}
	}
	assertBusy()
	release1()
	release1() // idempotent close does not release another reader
	assertBusy()
	release2()
	gate, err := b.exclusive(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if release, err := a.read(digest); !errors.Is(err, livechannels.ErrPhysicalBusy) {
		if release != nil {
			release()
		}
		t.Fatal("read admitted while publication/deletion holds gate", err)
	}
}

func TestAnalysisArtifactCustodyFencesAnotherProcess(t *testing.T) {
	const rootEnv = "PORTICO_ANALYSIS_CUSTODY_TEST_ROOT"
	const busyEnv = "PORTICO_ANALYSIS_CUSTODY_TEST_BUSY"
	digest := token("subprocess fixture")
	if root := os.Getenv(rootEnv); root != "" {
		locks, err := livechannels.NewPhysicalLocks(root)
		if err != nil {
			t.Fatal(err)
		}
		gate, err := (&artifactCustody{locks: locks}).exclusive(digest)
		if gate != nil {
			defer gate.Close()
		}
		if os.Getenv(busyEnv) == "1" {
			if !errors.Is(err, livechannels.ErrPhysicalBusy) {
				t.Fatal("cross-process reader gate lost", err)
			}
		} else if err != nil {
			t.Fatal("retired reader kept cross-process gate", err)
		}
		return
	}
	root := filepath.Join(canonicalFixtureDir(t), "locks")
	locks, err := livechannels.NewPhysicalLocks(root)
	if err != nil {
		t.Fatal(err)
	}
	release, err := (&artifactCustody{locks: locks}).read(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	probe := func(busy string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestAnalysisArtifactCustodyFencesAnotherProcess$")
		cmd.Env = append(os.Environ(), rootEnv+"="+root, busyEnv+"="+busy)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("custody probe: %v\n%s", err, output)
		}
	}
	probe("1")
	release()
	probe("0")
}
