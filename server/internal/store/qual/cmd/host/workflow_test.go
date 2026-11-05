package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Execute the actual YAML step body under the previously unsafe bash -e mode.
// This proves the step's own pipefail, independent of the runner's shell defaults.
func workflowBody(t *testing.T, name string) string {
	t.Helper()
	repoRoot, e := filepath.Abs("../../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "store-qualification-host.yml"))
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	i := strings.Index(s, "      - name: "+name+"\n")
	if i < 0 {
		t.Fatal("step missing")
	}
	s = s[i:]
	if end := strings.Index(s[1:], "\n      - "); end >= 0 {
		s = s[:end+1]
	}
	if !strings.Contains(s, "        shell: bash\n") {
		t.Fatal("hashing step must select explicit bash")
	}
	parts := strings.SplitN(s, "        run: |\n", 2)
	if len(parts) != 2 {
		t.Fatal("step body missing")
	}
	var lines []string
	for _, line := range strings.Split(parts[1], "\n") {
		if strings.HasPrefix(line, "          ") {
			lines = append(lines, strings.TrimPrefix(line, "          "))
		}
	}
	return strings.Join(lines, "\n")
}
func TestHashProducerFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host workflow requires Linux bash")
	}
	for _, producer := range []string{"git", "find", "sort"} {
		t.Run(producer, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if e := os.MkdirAll(bin, 0700); e != nil {
				t.Fatal(e)
			}
			script := "#!/bin/bash\nexit 37\n"
			step := "Hash retained receipts, including partial failures"
			final := "SHA256SUMS"
			if producer == "git" {
				step = "Prepare task scratch and source reference"
				final = "scoped-archive.sha256"
				script = "#!/bin/bash\nif [[ $1 == archive ]]; then exit 37; fi\nprintf '%040d\\n' 0\n"
			}
			if e := os.WriteFile(filepath.Join(bin, producer), []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			out := filepath.Join(root, "store-host/out")
			if e := os.MkdirAll(out, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(out, "partial.json"), []byte("{}\n"), 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command("bash", "-e", "-c", workflowBody(t, step))
			cmd.Env = append(os.Environ(), "RUNNER_TEMP="+root, "PATH="+bin+":"+os.Getenv("PATH"))
			cmd.Dir = root
			b, e := cmd.CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 37 {
				t.Fatalf("producer failure swallowed: err=%v output=%s", e, b)
			}
			if _, e := os.Stat(filepath.Join(out, final)); !os.IsNotExist(e) {
				t.Fatal("published a final digest after producer failure")
			}
		})
	}
}
