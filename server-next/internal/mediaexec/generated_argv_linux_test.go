//go:build linux

package mediaexec

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// A distribution FFprobe can need hundreds of exact dependency mounts. Tool
// arguments and generated mount flags have distinct limits: the latter must
// fit a full dependency set without permitting an unbounded helper request.
func TestGeneratedArgvBoundsExactDependencyMounts(t *testing.T) {
	resetForTest(t)
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	sandboxProbe = func(string) (string, bool, error) { return "bubblewrap", false, nil }
	job := Job{Executable: "/bin/true", Args: make([]string, maxJobArguments)}
	for i := 0; i < maxLibraryFiles; i++ {
		job.Libraries = append(job.Libraries, fmt.Sprintf("/portico-test/dependency-%03d.so", i))
	}
	argv, err := Argv(job)
	if err != nil {
		t.Fatalf("a full exact dependency set and permitted tool arguments must fit: %v", err)
	}
	if len(argv) <= 1024 || len(argv) > MaxGeneratedArguments {
		t.Fatalf("unexpected complete argument count: %d", len(argv))
	}
	mounts := map[string]bool{}
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == "--ro-bind" && argv[i+1] == argv[i+2] {
			mounts[argv[i+1]] = true
		}
	}
	for _, library := range job.Libraries {
		if !mounts[library] {
			t.Fatalf("exact dependency mount omitted: %s", library)
		}
	}
	// Declared paths also generate flags. They cannot grow a complete command
	// beyond the shared bound even when the tool argument count remains valid.
	for i := 0; i < MaxGeneratedArguments; i++ {
		job.ReadPaths = append(job.ReadPaths, "/portico-test/input")
	}
	if _, err = Argv(job); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("oversized generated argv was accepted: %v", err)
	}
	job = Job{Executable: "/bin/true", PreConfined: true, Args: make([]string, maxJobArguments+1)}
	if _, err = Argv(job); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("original tool argument bound was widened: %v", err)
	}
}
